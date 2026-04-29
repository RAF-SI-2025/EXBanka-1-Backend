package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"strconv"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kafkamsg "github.com/exbanka/contract/kafka"
	pb "github.com/exbanka/contract/transactionpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

// paymentFacade is the subset of *service.PaymentService used by the gRPC handler.
type paymentFacade interface {
	CreatePayment(ctx context.Context, payment *model.Payment) error
	ExecutePayment(ctx context.Context, paymentID uint64) error
	GetPayment(id uint64) (*model.Payment, error)
	ListPaymentsByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error)
	ListPaymentsByClient(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error)
}

// transferFacade is the subset of *service.TransferService used by the gRPC handler.
type transferFacade interface {
	CreateTransfer(ctx context.Context, transfer *model.Transfer) error
	ExecuteTransfer(ctx context.Context, transferID uint64) error
	GetTransfer(id uint64) (*model.Transfer, error)
	ListTransfersByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error)
}

// recipientFacade is the subset of *service.PaymentRecipientService used by the gRPC handler.
type recipientFacade interface {
	Create(pr *model.PaymentRecipient) error
	GetByID(id uint64) (*model.PaymentRecipient, error)
	ListByClient(clientID uint64) ([]model.PaymentRecipient, error)
	Update(id uint64, recipientName, accountNumber *string) (*model.PaymentRecipient, error)
	Delete(id uint64) error
}

// txProducer is the subset of *kafka.Producer used by the gRPC handler.
type txProducer interface {
	PublishPaymentCreated(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error
	PublishPaymentCompleted(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error
	PublishTransferCreated(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error
	PublishTransferCompleted(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error
}

func generateIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type TransactionGRPCHandler struct {
	pb.UnimplementedTransactionServiceServer
	paymentSvc         paymentFacade
	transferSvc        transferFacade
	recipientSvc       recipientFacade
	verificationClient verificationpb.VerificationGRPCServiceClient
	producer           txProducer
}

func NewTransactionGRPCHandler(
	paymentSvc *service.PaymentService,
	transferSvc *service.TransferService,
	recipientSvc *service.PaymentRecipientService,
	verificationClient verificationpb.VerificationGRPCServiceClient,
	producer txProducer,
) *TransactionGRPCHandler {
	return &TransactionGRPCHandler{
		paymentSvc:         paymentSvc,
		transferSvc:        transferSvc,
		recipientSvc:       recipientSvc,
		verificationClient: verificationClient,
		producer:           producer,
	}
}

// newTransactionGRPCHandlerForTest constructs a TransactionGRPCHandler with interface values
// for use in unit tests (avoids dependency on concrete service types).
func newTransactionGRPCHandlerForTest(
	payment paymentFacade,
	transfer transferFacade,
	recipient recipientFacade,
	verif verificationpb.VerificationGRPCServiceClient,
	prod txProducer,
) *TransactionGRPCHandler {
	return &TransactionGRPCHandler{
		paymentSvc:         payment,
		transferSvc:        transfer,
		recipientSvc:       recipient,
		verificationClient: verif,
		producer:           prod,
	}
}

// ---- Payment RPCs ----

func (h *TransactionGRPCHandler) CreatePayment(ctx context.Context, req *pb.CreatePaymentRequest) (*pb.PaymentResponse, error) {
	idempotencyKey := generateIdempotencyKey()
	paymentAmount, _ := decimal.NewFromString(req.GetAmount())
	payment := &model.Payment{
		IdempotencyKey:    idempotencyKey,
		ClientID:          req.GetClientId(),
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     paymentAmount,
		RecipientName:     req.GetRecipientName(),
		PaymentCode:       req.GetPaymentCode(),
		ReferenceNumber:   req.GetReferenceNumber(),
		PaymentPurpose:    req.GetPaymentPurpose(),
	}

	if err := h.paymentSvc.CreatePayment(ctx, payment); err != nil {
		return nil, err
	}

	// Publish payment-created Kafka event
	msg := kafkamsg.PaymentCompletedMessage{
		PaymentID:         payment.ID,
		FromAccountNumber: payment.FromAccountNumber,
		ToAccountNumber:   payment.ToAccountNumber,
		Amount:            payment.InitialAmount.StringFixed(4),
		Status:            payment.Status,
	}
	if err := h.producer.PublishPaymentCreated(ctx, msg); err != nil {
		log.Printf("warn: failed to publish payment-created event: %v", err)
	}

	// Payment created in pending_verification status.
	// The gateway or browser will call verification-service to create the challenge.
	return paymentToProto(payment), nil
}

func (h *TransactionGRPCHandler) ExecutePayment(ctx context.Context, req *pb.ExecutePaymentRequest) (*pb.PaymentResponse, error) {
	// 1. Verify via verification-service
	if req.GetChallengeId() > 0 {
		verifyResp, err := h.verificationClient.GetChallengeStatus(ctx, &verificationpb.GetChallengeStatusRequest{
			ChallengeId: req.GetChallengeId(),
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "verification check failed: %v", err)
		}
		if verifyResp.Status != "verified" {
			return nil, status.Errorf(codes.FailedPrecondition, "verification not completed")
		}
	}

	// 2. Execute the payment (balance changes)
	if err := h.paymentSvc.ExecutePayment(ctx, req.GetPaymentId()); err != nil {
		return nil, err
	}

	// 3. Get updated payment and return
	payment, err := h.paymentSvc.GetPayment(req.GetPaymentId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch executed payment: %v", err)
	}

	// 4. Publish Kafka events for completed payment
	if payment.Status == "completed" {
		msg := kafkamsg.PaymentCompletedMessage{
			PaymentID:         payment.ID,
			FromAccountNumber: payment.FromAccountNumber,
			ToAccountNumber:   payment.ToAccountNumber,
			Amount:            payment.InitialAmount.StringFixed(4),
			Status:            payment.Status,
		}
		if err := h.producer.PublishPaymentCompleted(ctx, msg); err != nil {
			log.Printf("warn: failed to publish payment-completed event: %v", err)
		}

	}

	return paymentToProto(payment), nil
}

func (h *TransactionGRPCHandler) GetPayment(ctx context.Context, req *pb.GetPaymentRequest) (*pb.PaymentResponse, error) {
	payment, err := h.paymentSvc.GetPayment(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "payment not found: %v", err)
	}
	return paymentToProto(payment), nil
}

func (h *TransactionGRPCHandler) ListPaymentsByAccount(ctx context.Context, req *pb.ListPaymentsByAccountRequest) (*pb.ListPaymentsResponse, error) {
	amountMin, _ := strconv.ParseFloat(req.GetAmountMin(), 64)
	amountMax, _ := strconv.ParseFloat(req.GetAmountMax(), 64)
	payments, total, err := h.paymentSvc.ListPaymentsByAccount(
		req.GetAccountNumber(),
		req.GetDateFrom(),
		req.GetDateTo(),
		req.GetStatusFilter(),
		amountMin,
		amountMax,
		int(req.GetPage()),
		int(req.GetPageSize()),
	)
	if err != nil {
		return nil, err
	}

	pbPayments := make([]*pb.PaymentResponse, 0, len(payments))
	for i := range payments {
		pbPayments = append(pbPayments, paymentToProto(&payments[i]))
	}
	return &pb.ListPaymentsResponse{Payments: pbPayments, Total: total}, nil
}

func (h *TransactionGRPCHandler) ListPaymentsByClient(ctx context.Context, req *pb.ListPaymentsByClientRequest) (*pb.ListPaymentsResponse, error) {
	payments, total, err := h.paymentSvc.ListPaymentsByClient(
		req.GetAccountNumbers(),
		int(req.GetPage()),
		int(req.GetPageSize()),
	)
	if err != nil {
		return nil, err
	}

	pbPayments := make([]*pb.PaymentResponse, 0, len(payments))
	for i := range payments {
		pbPayments = append(pbPayments, paymentToProto(&payments[i]))
	}
	return &pb.ListPaymentsResponse{Payments: pbPayments, Total: total}, nil
}

// ---- Transfer RPCs ----

func (h *TransactionGRPCHandler) CreateTransfer(ctx context.Context, req *pb.CreateTransferRequest) (*pb.TransferResponse, error) {
	idempotencyKey := generateIdempotencyKey()
	transferAmount, _ := decimal.NewFromString(req.GetAmount())
	transfer := &model.Transfer{
		IdempotencyKey:    idempotencyKey,
		ClientID:          req.GetClientId(),
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     transferAmount,
		ExchangeRate:      decimal.NewFromInt(1),
		FromCurrency:      req.GetFromCurrency(),
		ToCurrency:        req.GetToCurrency(),
	}

	if err := h.transferSvc.CreateTransfer(ctx, transfer); err != nil {
		return nil, err
	}

	// Publish transfer-created Kafka event
	msg := kafkamsg.TransferCompletedMessage{
		TransferID:        transfer.ID,
		FromAccountNumber: transfer.FromAccountNumber,
		ToAccountNumber:   transfer.ToAccountNumber,
		InitialAmount:     transfer.InitialAmount.StringFixed(4),
		FinalAmount:       transfer.FinalAmount.StringFixed(4),
		ExchangeRate:      transfer.ExchangeRate.StringFixed(4),
	}
	if err := h.producer.PublishTransferCreated(ctx, msg); err != nil {
		log.Printf("warn: failed to publish transfer-created event: %v", err)
	}

	// Transfer created in pending_verification status.
	// The gateway or browser will call verification-service to create the challenge.
	return transferToProto(transfer), nil
}

func (h *TransactionGRPCHandler) ExecuteTransfer(ctx context.Context, req *pb.ExecuteTransferRequest) (*pb.TransferResponse, error) {
	// 1. Verify via verification-service
	if req.GetChallengeId() > 0 {
		verifyResp, err := h.verificationClient.GetChallengeStatus(ctx, &verificationpb.GetChallengeStatusRequest{
			ChallengeId: req.GetChallengeId(),
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "verification check failed: %v", err)
		}
		if verifyResp.Status != "verified" {
			return nil, status.Errorf(codes.FailedPrecondition, "verification not completed")
		}
	}

	// 2. Execute the transfer (balance changes)
	if err := h.transferSvc.ExecuteTransfer(ctx, req.GetTransferId()); err != nil {
		return nil, err
	}

	// 3. Get updated transfer and return
	transfer, err := h.transferSvc.GetTransfer(req.GetTransferId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch executed transfer: %v", err)
	}

	// 4. Publish Kafka events for completed transfer
	if transfer.Status == "completed" {
		msg := kafkamsg.TransferCompletedMessage{
			TransferID:        transfer.ID,
			FromAccountNumber: transfer.FromAccountNumber,
			ToAccountNumber:   transfer.ToAccountNumber,
			InitialAmount:     transfer.InitialAmount.StringFixed(4),
			FinalAmount:       transfer.FinalAmount.StringFixed(4),
			ExchangeRate:      transfer.ExchangeRate.StringFixed(4),
		}
		if err := h.producer.PublishTransferCompleted(ctx, msg); err != nil {
			log.Printf("warn: failed to publish transfer-completed event: %v", err)
		}

	}

	return transferToProto(transfer), nil
}

func (h *TransactionGRPCHandler) GetTransfer(ctx context.Context, req *pb.GetTransferRequest) (*pb.TransferResponse, error) {
	transfer, err := h.transferSvc.GetTransfer(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "transfer not found: %v", err)
	}
	return transferToProto(transfer), nil
}

func (h *TransactionGRPCHandler) ListTransfersByClient(ctx context.Context, req *pb.ListTransfersByClientRequest) (*pb.ListTransfersResponse, error) {
	transfers, total, err := h.transferSvc.ListTransfersByAccountNumbers(
		req.GetAccountNumbers(),
		int(req.GetPage()),
		int(req.GetPageSize()),
	)
	if err != nil {
		return nil, err
	}

	pbTransfers := make([]*pb.TransferResponse, 0, len(transfers))
	for i := range transfers {
		pbTransfers = append(pbTransfers, transferToProto(&transfers[i]))
	}
	return &pb.ListTransfersResponse{Transfers: pbTransfers, Total: total}, nil
}

// ---- Payment Recipient RPCs ----

func (h *TransactionGRPCHandler) CreatePaymentRecipient(ctx context.Context, req *pb.CreatePaymentRecipientRequest) (*pb.PaymentRecipientResponse, error) {
	pr := &model.PaymentRecipient{
		ClientID:      req.GetClientId(),
		RecipientName: req.GetRecipientName(),
		AccountNumber: req.GetAccountNumber(),
	}
	if err := h.recipientSvc.Create(pr); err != nil {
		return nil, err
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) GetPaymentRecipient(ctx context.Context, req *pb.GetPaymentRecipientRequest) (*pb.PaymentRecipientResponse, error) {
	pr, err := h.recipientSvc.GetByID(req.GetId())
	if err != nil {
		return nil, err
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) ListPaymentRecipients(ctx context.Context, req *pb.ListPaymentRecipientsRequest) (*pb.ListPaymentRecipientsResponse, error) {
	recipients, err := h.recipientSvc.ListByClient(req.GetClientId())
	if err != nil {
		return nil, err
	}

	pbRecipients := make([]*pb.PaymentRecipientResponse, 0, len(recipients))
	for i := range recipients {
		pbRecipients = append(pbRecipients, recipientToProto(&recipients[i]))
	}
	return &pb.ListPaymentRecipientsResponse{Recipients: pbRecipients}, nil
}

func (h *TransactionGRPCHandler) UpdatePaymentRecipient(ctx context.Context, req *pb.UpdatePaymentRecipientRequest) (*pb.PaymentRecipientResponse, error) {
	pr, err := h.recipientSvc.Update(req.GetId(), req.RecipientName, req.AccountNumber)
	if err != nil {
		return nil, err
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) DeletePaymentRecipient(ctx context.Context, req *pb.DeletePaymentRecipientRequest) (*pb.DeletePaymentRecipientResponse, error) {
	if err := h.recipientSvc.Delete(req.GetId()); err != nil {
		return nil, err
	}
	return &pb.DeletePaymentRecipientResponse{Success: true}, nil
}

// ---- Helpers ----

func paymentToProto(p *model.Payment) *pb.PaymentResponse {
	return &pb.PaymentResponse{
		Id:                p.ID,
		ClientId:          p.ClientID,
		FromAccountNumber: p.FromAccountNumber,
		ToAccountNumber:   p.ToAccountNumber,
		InitialAmount:     p.InitialAmount.StringFixed(4),
		FinalAmount:       p.FinalAmount.StringFixed(4),
		Commission:        p.Commission.StringFixed(4),
		RecipientName:     p.RecipientName,
		PaymentCode:       p.PaymentCode,
		ReferenceNumber:   p.ReferenceNumber,
		PaymentPurpose:    p.PaymentPurpose,
		Status:            p.Status,
		Timestamp:         p.Timestamp.String(),
	}
}

func transferToProto(t *model.Transfer) *pb.TransferResponse {
	return &pb.TransferResponse{
		Id:                t.ID,
		ClientId:          t.ClientID,
		FromAccountNumber: t.FromAccountNumber,
		ToAccountNumber:   t.ToAccountNumber,
		InitialAmount:     t.InitialAmount.StringFixed(4),
		FinalAmount:       t.FinalAmount.StringFixed(4),
		ExchangeRate:      t.ExchangeRate.StringFixed(4),
		Commission:        t.Commission.StringFixed(4),
		Timestamp:         t.Timestamp.String(),
		Status:            t.Status,
	}
}

func recipientToProto(r *model.PaymentRecipient) *pb.PaymentRecipientResponse {
	return &pb.PaymentRecipientResponse{
		Id:            r.ID,
		ClientId:      r.ClientID,
		RecipientName: r.RecipientName,
		AccountNumber: r.AccountNumber,
		CreatedAt:     r.CreatedAt.String(),
	}
}

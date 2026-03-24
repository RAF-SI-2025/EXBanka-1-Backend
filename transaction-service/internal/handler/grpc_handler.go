package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kafkamsg "github.com/exbanka/contract/kafka"
	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "insufficient funds"), strings.Contains(msg, "limit exceeded"),
		strings.Contains(msg, "spending limit"), strings.Contains(msg, "already used"),
		strings.Contains(msg, "already blocked"), strings.Contains(msg, "already deactivated"),
		strings.Contains(msg, "already "):
		return codes.FailedPrecondition
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	case strings.Contains(msg, "expired"):
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}

func generateIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type TransactionGRPCHandler struct {
	pb.UnimplementedTransactionServiceServer
	paymentSvc      *service.PaymentService
	transferSvc     *service.TransferService
	recipientSvc    *service.PaymentRecipientService
	exchangeSvc     *service.ExchangeService
	verificationSvc *service.VerificationService
	producer        *kafka.Producer
}

func NewTransactionGRPCHandler(
	paymentSvc *service.PaymentService,
	transferSvc *service.TransferService,
	recipientSvc *service.PaymentRecipientService,
	exchangeSvc *service.ExchangeService,
	verificationSvc *service.VerificationService,
	producer *kafka.Producer,
) *TransactionGRPCHandler {
	return &TransactionGRPCHandler{
		paymentSvc:      paymentSvc,
		transferSvc:     transferSvc,
		recipientSvc:    recipientSvc,
		exchangeSvc:     exchangeSvc,
		verificationSvc: verificationSvc,
		producer:        producer,
	}
}

// ---- Payment RPCs ----

func (h *TransactionGRPCHandler) CreatePayment(ctx context.Context, req *pb.CreatePaymentRequest) (*pb.PaymentResponse, error) {
	idempotencyKey := generateIdempotencyKey()
	paymentAmount, _ := decimal.NewFromString(req.GetAmount())
	payment := &model.Payment{
		IdempotencyKey:    idempotencyKey,
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     paymentAmount,
		RecipientName:     req.GetRecipientName(),
		PaymentCode:       req.GetPaymentCode(),
		ReferenceNumber:   req.GetReferenceNumber(),
		PaymentPurpose:    req.GetPaymentPurpose(),
	}

	if err := h.paymentSvc.CreatePayment(ctx, payment); err != nil {
		return nil, status.Errorf(mapServiceError(err), "create payment: %v", err)
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

	return paymentToProto(payment), nil
}

func (h *TransactionGRPCHandler) ExecutePayment(ctx context.Context, req *pb.ExecutePaymentRequest) (*pb.PaymentResponse, error) {
	// 1. Validate verification code
	valid, _, err := h.verificationSvc.ValidateVerificationCode(req.GetClientId(), req.GetPaymentId(), "payment", req.GetVerificationCode())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "verify: %v", err)
	}
	if !valid {
		return nil, status.Errorf(codes.FailedPrecondition, "invalid verification code")
	}

	// 2. Execute the payment (balance changes)
	if err := h.paymentSvc.ExecutePayment(ctx, req.GetPaymentId()); err != nil {
		return nil, status.Errorf(mapServiceError(err), "execute payment: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "list payments: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "list payments by client: %v", err)
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
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     transferAmount,
		ExchangeRate:      decimal.NewFromInt(1),
		FromCurrency:      req.GetFromCurrency(),
		ToCurrency:        req.GetToCurrency(),
	}

	if err := h.transferSvc.CreateTransfer(ctx, transfer); err != nil {
		return nil, status.Errorf(mapServiceError(err), "create transfer: %v", err)
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

	return transferToProto(transfer), nil
}

func (h *TransactionGRPCHandler) ExecuteTransfer(ctx context.Context, req *pb.ExecuteTransferRequest) (*pb.TransferResponse, error) {
	// 1. Validate verification code
	valid, _, err := h.verificationSvc.ValidateVerificationCode(req.GetClientId(), req.GetTransferId(), "transfer", req.GetVerificationCode())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "verify: %v", err)
	}
	if !valid {
		return nil, status.Errorf(codes.FailedPrecondition, "invalid verification code")
	}

	// 2. Execute the transfer (balance changes)
	if err := h.transferSvc.ExecuteTransfer(ctx, req.GetTransferId()); err != nil {
		return nil, status.Errorf(mapServiceError(err), "execute transfer: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "list transfers: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "create recipient: %v", err)
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) ListPaymentRecipients(ctx context.Context, req *pb.ListPaymentRecipientsRequest) (*pb.ListPaymentRecipientsResponse, error) {
	recipients, err := h.recipientSvc.ListByClient(req.GetClientId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "list recipients: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "update recipient: %v", err)
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) DeletePaymentRecipient(ctx context.Context, req *pb.DeletePaymentRecipientRequest) (*pb.DeletePaymentRecipientResponse, error) {
	if err := h.recipientSvc.Delete(req.GetId()); err != nil {
		return nil, status.Errorf(mapServiceError(err), "delete recipient: %v", err)
	}
	return &pb.DeletePaymentRecipientResponse{Success: true}, nil
}

// ---- Exchange Rate RPCs ----

func (h *TransactionGRPCHandler) GetExchangeRate(ctx context.Context, req *pb.GetExchangeRateRequest) (*pb.ExchangeRateResponse, error) {
	rate, err := h.exchangeSvc.GetExchangeRate(req.GetFromCurrency(), req.GetToCurrency())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "exchange rate not found: %v", err)
	}
	return exchangeRateToProto(rate), nil
}

func (h *TransactionGRPCHandler) ListExchangeRates(ctx context.Context, req *pb.ListExchangeRatesRequest) (*pb.ListExchangeRatesResponse, error) {
	rates, err := h.exchangeSvc.ListExchangeRates()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "list exchange rates: %v", err)
	}

	pbRates := make([]*pb.ExchangeRateResponse, 0, len(rates))
	for i := range rates {
		pbRates = append(pbRates, exchangeRateToProto(&rates[i]))
	}
	return &pb.ListExchangeRatesResponse{Rates: pbRates}, nil
}

// ---- Verification Code RPCs ----

func (h *TransactionGRPCHandler) CreateVerificationCode(ctx context.Context, req *pb.CreateVerificationCodeRequest) (*pb.CreateVerificationCodeResponse, error) {
	vc, code, err := h.verificationSvc.CreateVerificationCode(ctx, req.GetClientId(), req.GetTransactionId(), req.GetTransactionType())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "create verification code: %v", err)
	}

	// Send verification code via email
	if email := req.GetClientEmail(); email != "" {
		_ = h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
			To:        email,
			EmailType: kafkamsg.EmailTypeTransactionVerify,
			Data: map[string]string{
				"verification_code": code,
				"expires_in":        "5 minutes",
			},
		})
	}

	return &pb.CreateVerificationCodeResponse{
		Code:      vc.Code,
		ExpiresAt: vc.ExpiresAt.Unix(),
	}, nil
}

func (h *TransactionGRPCHandler) ValidateVerificationCode(ctx context.Context, req *pb.ValidateVerificationCodeRequest) (*pb.ValidateVerificationCodeResponse, error) {
	valid, remaining, err := h.verificationSvc.ValidateVerificationCode(req.GetClientId(), req.GetTransactionId(), req.GetTransactionType(), req.GetCode())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "validate verification code: %v", err)
	}
	return &pb.ValidateVerificationCodeResponse{
		Valid:             valid,
		RemainingAttempts: int32(remaining),
	}, nil
}

// ---- Helpers ----

func paymentToProto(p *model.Payment) *pb.PaymentResponse {
	return &pb.PaymentResponse{
		Id:                p.ID,
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

func exchangeRateToProto(r *model.ExchangeRate) *pb.ExchangeRateResponse {
	return &pb.ExchangeRateResponse{
		FromCurrency: r.FromCurrency,
		ToCurrency:   r.ToCurrency,
		BuyRate:      r.BuyRate.StringFixed(4),
		SellRate:     r.SellRate.StringFixed(4),
		UpdatedAt:    r.UpdatedAt.String(),
	}
}

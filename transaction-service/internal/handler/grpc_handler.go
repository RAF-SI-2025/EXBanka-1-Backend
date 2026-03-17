package handler

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/transactionpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

type TransactionGRPCHandler struct {
	pb.UnimplementedTransactionServiceServer
	paymentSvc     *service.PaymentService
	transferSvc    *service.TransferService
	recipientSvc   *service.PaymentRecipientService
	exchangeSvc    *service.ExchangeService
	verificationSvc *service.VerificationService
	producer       *kafka.Producer
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
	payment := &model.Payment{
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     req.GetAmount(),
		RecipientName:     req.GetRecipientName(),
		PaymentCode:       req.GetPaymentCode(),
		ReferenceNumber:   req.GetReferenceNumber(),
		PaymentPurpose:    req.GetPaymentPurpose(),
	}

	if err := h.paymentSvc.CreatePayment(ctx, payment); err != nil {
		return nil, status.Errorf(codes.Internal, "create payment: %v", err)
	}

	// Publish Kafka events
	msg := kafkamsg.PaymentCompletedMessage{
		PaymentID:         payment.ID,
		FromAccountNumber: payment.FromAccountNumber,
		ToAccountNumber:   payment.ToAccountNumber,
		Amount:            payment.InitialAmount,
		Status:            payment.Status,
	}
	if err := h.producer.PublishPaymentCreated(ctx, msg); err != nil {
		log.Printf("warn: failed to publish payment-created event: %v", err)
	}
	if err := h.producer.PublishPaymentCompleted(ctx, msg); err != nil {
		log.Printf("warn: failed to publish payment-completed event: %v", err)
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
	payments, total, err := h.paymentSvc.ListPaymentsByAccount(
		req.GetAccountNumber(),
		req.GetDateFrom(),
		req.GetDateTo(),
		req.GetStatusFilter(),
		req.GetAmountMin(),
		req.GetAmountMax(),
		int(req.GetPage()),
		int(req.GetPageSize()),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list payments: %v", err)
	}

	pbPayments := make([]*pb.PaymentResponse, 0, len(payments))
	for i := range payments {
		pbPayments = append(pbPayments, paymentToProto(&payments[i]))
	}
	return &pb.ListPaymentsResponse{Payments: pbPayments, Total: total}, nil
}

// ---- Transfer RPCs ----

func (h *TransactionGRPCHandler) CreateTransfer(ctx context.Context, req *pb.CreateTransferRequest) (*pb.TransferResponse, error) {
	transfer := &model.Transfer{
		FromAccountNumber: req.GetFromAccountNumber(),
		ToAccountNumber:   req.GetToAccountNumber(),
		InitialAmount:     req.GetAmount(),
		ExchangeRate:      1,
	}

	if err := h.transferSvc.CreateTransfer(ctx, transfer); err != nil {
		return nil, status.Errorf(codes.Internal, "create transfer: %v", err)
	}

	// Publish Kafka events
	msg := kafkamsg.TransferCompletedMessage{
		TransferID:        transfer.ID,
		FromAccountNumber: transfer.FromAccountNumber,
		ToAccountNumber:   transfer.ToAccountNumber,
		InitialAmount:     transfer.InitialAmount,
		FinalAmount:       transfer.FinalAmount,
		ExchangeRate:      transfer.ExchangeRate,
	}
	if err := h.producer.PublishTransferCreated(ctx, msg); err != nil {
		log.Printf("warn: failed to publish transfer-created event: %v", err)
	}
	if err := h.producer.PublishTransferCompleted(ctx, msg); err != nil {
		log.Printf("warn: failed to publish transfer-completed event: %v", err)
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
	transfers, total, err := h.transferSvc.ListTransfersByClient(
		req.GetClientId(),
		int(req.GetPage()),
		int(req.GetPageSize()),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list transfers: %v", err)
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
		return nil, status.Errorf(codes.Internal, "create recipient: %v", err)
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) ListPaymentRecipients(ctx context.Context, req *pb.ListPaymentRecipientsRequest) (*pb.ListPaymentRecipientsResponse, error) {
	recipients, err := h.recipientSvc.ListByClient(req.GetClientId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list recipients: %v", err)
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
		return nil, status.Errorf(codes.Internal, "update recipient: %v", err)
	}
	return recipientToProto(pr), nil
}

func (h *TransactionGRPCHandler) DeletePaymentRecipient(ctx context.Context, req *pb.DeletePaymentRecipientRequest) (*pb.DeletePaymentRecipientResponse, error) {
	if err := h.recipientSvc.Delete(req.GetId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete recipient: %v", err)
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
		return nil, status.Errorf(codes.Internal, "list exchange rates: %v", err)
	}

	pbRates := make([]*pb.ExchangeRateResponse, 0, len(rates))
	for i := range rates {
		pbRates = append(pbRates, exchangeRateToProto(&rates[i]))
	}
	return &pb.ListExchangeRatesResponse{Rates: pbRates}, nil
}

// ---- Verification Code RPCs ----

func (h *TransactionGRPCHandler) CreateVerificationCode(ctx context.Context, req *pb.CreateVerificationCodeRequest) (*pb.CreateVerificationCodeResponse, error) {
	vc, _, err := h.verificationSvc.CreateVerificationCode(ctx, req.GetClientId(), req.GetTransactionId(), req.GetTransactionType())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create verification code: %v", err)
	}
	return &pb.CreateVerificationCodeResponse{
		Code:      vc.Code,
		ExpiresAt: vc.ExpiresAt.Unix(),
	}, nil
}

func (h *TransactionGRPCHandler) ValidateVerificationCode(ctx context.Context, req *pb.ValidateVerificationCodeRequest) (*pb.ValidateVerificationCodeResponse, error) {
	valid, remaining, err := h.verificationSvc.ValidateVerificationCode(req.GetClientId(), req.GetTransactionId(), req.GetCode())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "validate verification code: %v", err)
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
		InitialAmount:     p.InitialAmount,
		FinalAmount:       p.FinalAmount,
		Commission:        p.Commission,
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
		InitialAmount:     t.InitialAmount,
		FinalAmount:       t.FinalAmount,
		ExchangeRate:      t.ExchangeRate,
		Commission:        t.Commission,
		Timestamp:         t.Timestamp.String(),
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
		BuyRate:      r.BuyRate,
		SellRate:     r.SellRate,
		UpdatedAt:    r.UpdatedAt.String(),
	}
}

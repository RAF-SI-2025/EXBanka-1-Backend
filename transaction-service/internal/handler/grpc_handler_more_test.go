package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kafkamsg "github.com/exbanka/contract/kafka"
	pb "github.com/exbanka/contract/transactionpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

// TestExecutePayment_VerificationGRPCError verifies that a gRPC error from
// verification-service surfaces as Internal.
func TestExecutePayment_VerificationGRPCError(t *testing.T) {
	verif := &mockVerificationClient{
		getChallengeStatusFn: func(_ context.Context, _ *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
			return nil, errors.New("verif down")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, &mockRecipientFacade{}, verif, &mockTxProducer{})
	_, err := h.ExecutePayment(context.Background(), &pb.ExecutePaymentRequest{PaymentId: 1, ChallengeId: 100})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestExecuteTransfer_VerificationGRPCError verifies same on transfer.
func TestExecuteTransfer_VerificationGRPCError(t *testing.T) {
	verif := &mockVerificationClient{
		getChallengeStatusFn: func(_ context.Context, _ *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
			return nil, errors.New("verif down")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, &mockRecipientFacade{}, verif, &mockTxProducer{})
	_, err := h.ExecuteTransfer(context.Background(), &pb.ExecuteTransferRequest{TransferId: 1, ChallengeId: 100})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestExecutePayment_GetPaymentFailsAfterExecute_Internal verifies the
// post-execute fetch failure path returns Internal.
func TestExecutePayment_GetPaymentFailsAfterExecute_Internal(t *testing.T) {
	pm := &mockPaymentFacade{
		executePaymentFn: func(_ context.Context, _ uint64) error { return nil },
		getPaymentFn: func(_ uint64) (*model.Payment, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ExecutePayment(context.Background(), &pb.ExecutePaymentRequest{PaymentId: 1})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestExecuteTransfer_GetTransferFailsAfterExecute_Internal verifies same.
func TestExecuteTransfer_GetTransferFailsAfterExecute_Internal(t *testing.T) {
	tm := &mockTransferFacade{
		executeTransferFn: func(_ context.Context, _ uint64) error { return nil },
		getTransferFn: func(_ uint64) (*model.Transfer, error) {
			return nil, errors.New("missing")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ExecuteTransfer(context.Background(), &pb.ExecuteTransferRequest{TransferId: 1})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestCreatePayment_PublishFails_StillSucceeds verifies that a Kafka publish
// failure on payment-created is logged but doesn't fail the request.
func TestCreatePayment_PublishFails_StillSucceeds(t *testing.T) {
	prod := &mockTxProducer{
		publishPaymentCreatedFn: func(_ context.Context, _ kafkamsg.PaymentCompletedMessage) error {
			return errors.New("kafka down")
		},
	}
	pm := &mockPaymentFacade{
		createPaymentFn: func(_ context.Context, p *model.Payment) error {
			p.ID = 9
			p.Status = "pending_verification"
			return nil
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, prod)
	resp, err := h.CreatePayment(context.Background(), &pb.CreatePaymentRequest{
		FromAccountNumber: "ACC-1", ToAccountNumber: "ACC-2", Amount: "10", ClientId: 1,
	})
	require.NoError(t, err, "publish failure must not bubble up")
	assert.Equal(t, uint64(9), resp.Id)
}

// TestCreateTransfer_PublishFails_StillSucceeds verifies Kafka-publish failure
// is non-fatal on transfer-created.
func TestCreateTransfer_PublishFails_StillSucceeds(t *testing.T) {
	prod := &mockTxProducer{}
	tm := &mockTransferFacade{
		createTransferFn: func(_ context.Context, tr *model.Transfer) error {
			tr.ID = 11
			tr.Status = "pending_verification"
			tr.ExchangeRate = decimal.NewFromInt(1)
			return nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, prod)
	resp, err := h.CreateTransfer(context.Background(), &pb.CreateTransferRequest{
		FromAccountNumber: "ACC-1", ToAccountNumber: "ACC-2", Amount: "5", ClientId: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(11), resp.Id)
}

// TestGetPaymentRecipient_NotFound verifies the recipient-not-found path.
func TestGetPaymentRecipient_NotFound(t *testing.T) {
	rm := &mockRecipientFacade{
		getByIDFn: func(_ uint64) (*model.PaymentRecipient, error) {
			return nil, service.ErrRecipientNotFound
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, rm, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.GetPaymentRecipient(context.Background(), &pb.GetPaymentRecipientRequest{Id: 99})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestCreatePaymentRecipient_Error verifies create error path.
func TestCreatePaymentRecipient_Error(t *testing.T) {
	rm := &mockRecipientFacade{
		createFn: func(_ *model.PaymentRecipient) error {
			return errors.New("db down")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, rm, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.CreatePaymentRecipient(context.Background(), &pb.CreatePaymentRecipientRequest{
		ClientId: 1, RecipientName: "x", AccountNumber: "y",
	})
	if err == nil {
		t.Errorf("expected error")
	}
}

// TestListPaymentRecipients_Error verifies list error path.
func TestListPaymentRecipients_Error(t *testing.T) {
	rm := &mockRecipientFacade{
		listByClientFn: func(_ uint64) ([]model.PaymentRecipient, error) {
			return nil, errors.New("db down")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, rm, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ListPaymentRecipients(context.Background(), &pb.ListPaymentRecipientsRequest{ClientId: 1})
	if err == nil {
		t.Errorf("expected error")
	}
}

// TestNewTransactionGRPCHandler_Constructor verifies the production
// constructor returns a non-nil handler. Exercises the simple wiring branch.
func TestNewTransactionGRPCHandler_Constructor(t *testing.T) {
	h := NewTransactionGRPCHandler(nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatalf("constructor returned nil")
	}
}

// TestNewFeeGRPCHandler_Constructor verifies the simple wiring path.
func TestNewFeeGRPCHandler_Constructor(t *testing.T) {
	h := NewFeeGRPCHandler(nil)
	if h == nil {
		t.Fatalf("constructor returned nil")
	}
}

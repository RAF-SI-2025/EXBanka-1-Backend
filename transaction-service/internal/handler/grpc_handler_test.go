package handler

import (
	"context"
	"errors"
	"fmt"
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

// ---------------------------------------------------------------------------
// Mock stubs (function-field pattern)
// ---------------------------------------------------------------------------

type mockPaymentFacade struct {
	createPaymentFn         func(ctx context.Context, payment *model.Payment) error
	executePaymentFn        func(ctx context.Context, paymentID uint64) error
	getPaymentFn            func(id uint64) (*model.Payment, error)
	listPaymentsByAccountFn func(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error)
	listPaymentsByClientFn  func(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error)
}

func (m *mockPaymentFacade) CreatePayment(ctx context.Context, payment *model.Payment) error {
	if m.createPaymentFn != nil {
		return m.createPaymentFn(ctx, payment)
	}
	return nil
}

func (m *mockPaymentFacade) ExecutePayment(ctx context.Context, paymentID uint64) error {
	if m.executePaymentFn != nil {
		return m.executePaymentFn(ctx, paymentID)
	}
	return nil
}

func (m *mockPaymentFacade) GetPayment(id uint64) (*model.Payment, error) {
	if m.getPaymentFn != nil {
		return m.getPaymentFn(id)
	}
	return nil, nil
}

func (m *mockPaymentFacade) ListPaymentsByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	if m.listPaymentsByAccountFn != nil {
		return m.listPaymentsByAccountFn(accountNumber, dateFrom, dateTo, statusFilter, amountMin, amountMax, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockPaymentFacade) ListPaymentsByClient(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error) {
	if m.listPaymentsByClientFn != nil {
		return m.listPaymentsByClientFn(accountNumbers, page, pageSize)
	}
	return nil, 0, nil
}

// --

type mockTransferFacade struct {
	createTransferFn                 func(ctx context.Context, transfer *model.Transfer) error
	executeTransferFn                func(ctx context.Context, transferID uint64) error
	getTransferFn                    func(id uint64) (*model.Transfer, error)
	listTransfersByAccountNumbersFn  func(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error)
}

func (m *mockTransferFacade) CreateTransfer(ctx context.Context, transfer *model.Transfer) error {
	if m.createTransferFn != nil {
		return m.createTransferFn(ctx, transfer)
	}
	return nil
}

func (m *mockTransferFacade) ExecuteTransfer(ctx context.Context, transferID uint64) error {
	if m.executeTransferFn != nil {
		return m.executeTransferFn(ctx, transferID)
	}
	return nil
}

func (m *mockTransferFacade) GetTransfer(id uint64) (*model.Transfer, error) {
	if m.getTransferFn != nil {
		return m.getTransferFn(id)
	}
	return nil, nil
}

func (m *mockTransferFacade) ListTransfersByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error) {
	if m.listTransfersByAccountNumbersFn != nil {
		return m.listTransfersByAccountNumbersFn(accountNumbers, page, pageSize)
	}
	return nil, 0, nil
}

// --

type mockRecipientFacade struct {
	createFn      func(pr *model.PaymentRecipient) error
	getByIDFn     func(id uint64) (*model.PaymentRecipient, error)
	listByClientFn func(clientID uint64) ([]model.PaymentRecipient, error)
	updateFn      func(id uint64, recipientName, accountNumber *string) (*model.PaymentRecipient, error)
	deleteFn      func(id uint64) error
}

func (m *mockRecipientFacade) Create(pr *model.PaymentRecipient) error {
	if m.createFn != nil {
		return m.createFn(pr)
	}
	return nil
}

func (m *mockRecipientFacade) GetByID(id uint64) (*model.PaymentRecipient, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(id)
	}
	return nil, nil
}

func (m *mockRecipientFacade) ListByClient(clientID uint64) ([]model.PaymentRecipient, error) {
	if m.listByClientFn != nil {
		return m.listByClientFn(clientID)
	}
	return nil, nil
}

func (m *mockRecipientFacade) Update(id uint64, recipientName, accountNumber *string) (*model.PaymentRecipient, error) {
	if m.updateFn != nil {
		return m.updateFn(id, recipientName, accountNumber)
	}
	return nil, nil
}

func (m *mockRecipientFacade) Delete(id uint64) error {
	if m.deleteFn != nil {
		return m.deleteFn(id)
	}
	return nil
}

// --

type mockTxProducer struct {
	publishPaymentCreatedFn   func(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error
	publishPaymentCompletedFn func(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error
	publishTransferCreatedFn  func(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error
	publishTransferCompletedFn func(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error
}

func (m *mockTxProducer) PublishPaymentCreated(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	if m.publishPaymentCreatedFn != nil {
		return m.publishPaymentCreatedFn(ctx, msg)
	}
	return nil
}

func (m *mockTxProducer) PublishPaymentCompleted(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	if m.publishPaymentCompletedFn != nil {
		return m.publishPaymentCompletedFn(ctx, msg)
	}
	return nil
}

func (m *mockTxProducer) PublishTransferCreated(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	if m.publishTransferCreatedFn != nil {
		return m.publishTransferCreatedFn(ctx, msg)
	}
	return nil
}

func (m *mockTxProducer) PublishTransferCompleted(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	if m.publishTransferCompletedFn != nil {
		return m.publishTransferCompletedFn(ctx, msg)
	}
	return nil
}

// --

// mockVerificationClient implements verificationpb.VerificationGRPCServiceClient.
type mockVerificationClient struct {
	getChallengeStatusFn func(ctx context.Context, in *verificationpb.GetChallengeStatusRequest, opts ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error)
}

func (m *mockVerificationClient) CreateChallenge(ctx context.Context, in *verificationpb.CreateChallengeRequest, opts ...grpc.CallOption) (*verificationpb.CreateChallengeResponse, error) {
	return nil, nil
}

func (m *mockVerificationClient) GetChallengeStatus(ctx context.Context, in *verificationpb.GetChallengeStatusRequest, opts ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
	if m.getChallengeStatusFn != nil {
		return m.getChallengeStatusFn(ctx, in, opts...)
	}
	return &verificationpb.GetChallengeStatusResponse{Status: "verified"}, nil
}

func (m *mockVerificationClient) GetPendingChallenge(ctx context.Context, in *verificationpb.GetPendingChallengeRequest, opts ...grpc.CallOption) (*verificationpb.GetPendingChallengeResponse, error) {
	return nil, nil
}

func (m *mockVerificationClient) SubmitVerification(ctx context.Context, in *verificationpb.SubmitVerificationRequest, opts ...grpc.CallOption) (*verificationpb.SubmitVerificationResponse, error) {
	return nil, nil
}

func (m *mockVerificationClient) SubmitCode(ctx context.Context, in *verificationpb.SubmitCodeRequest, opts ...grpc.CallOption) (*verificationpb.SubmitCodeResponse, error) {
	return nil, nil
}

func (m *mockVerificationClient) VerifyByBiometric(ctx context.Context, in *verificationpb.VerifyByBiometricRequest, opts ...grpc.CallOption) (*verificationpb.VerifyByBiometricResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestHandler(paymentFn func(*mockPaymentFacade), transferFn func(*mockTransferFacade), recipientFn func(*mockRecipientFacade)) *TransactionGRPCHandler {
	pm := &mockPaymentFacade{}
	if paymentFn != nil {
		paymentFn(pm)
	}
	tm := &mockTransferFacade{}
	if transferFn != nil {
		transferFn(tm)
	}
	rm := &mockRecipientFacade{}
	if recipientFn != nil {
		recipientFn(rm)
	}
	return newTransactionGRPCHandlerForTest(pm, tm, rm, &mockVerificationClient{}, &mockTxProducer{})
}

// ---------------------------------------------------------------------------
// Payment tests
// ---------------------------------------------------------------------------

func TestCreatePayment_Success(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.createPaymentFn = func(ctx context.Context, payment *model.Payment) error {
			payment.ID = 1
			payment.Status = "pending_verification"
			return nil
		}
	}, nil, nil)

	resp, err := h.CreatePayment(context.Background(), &pb.CreatePaymentRequest{
		FromAccountNumber: "ACC-1",
		ToAccountNumber:   "ACC-2",
		Amount:            "100.00",
		ClientId:          5,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, "pending_verification", resp.Status)
	assert.Equal(t, "ACC-1", resp.FromAccountNumber)
	assert.Equal(t, "ACC-2", resp.ToAccountNumber)
	assert.Equal(t, uint64(5), resp.ClientId)
}

func TestCreatePayment_ServiceError(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.createPaymentFn = func(ctx context.Context, payment *model.Payment) error {
			return service.ErrInsufficientBalance
		}
	}, nil, nil)

	_, err := h.CreatePayment(context.Background(), &pb.CreatePaymentRequest{
		FromAccountNumber: "ACC-1",
		ToAccountNumber:   "ACC-2",
		Amount:            "100.00",
		ClientId:          5,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestGetPayment_Success(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.getPaymentFn = func(id uint64) (*model.Payment, error) {
			return &model.Payment{
				ID:                1,
				ClientID:          5,
				FromAccountNumber: "ACC-1",
				ToAccountNumber:   "ACC-2",
				InitialAmount:     decimal.NewFromInt(100),
				Status:            "pending_verification",
			}, nil
		}
	}, nil, nil)

	resp, err := h.GetPayment(context.Background(), &pb.GetPaymentRequest{Id: 1})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, uint64(5), resp.ClientId)
	assert.Equal(t, "pending_verification", resp.Status)
}

func TestGetPayment_NotFound(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.getPaymentFn = func(id uint64) (*model.Payment, error) {
			return nil, errors.New("payment not found")
		}
	}, nil, nil)

	_, err := h.GetPayment(context.Background(), &pb.GetPaymentRequest{Id: 999})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------------------------------------------------------------------
// Transfer tests
// ---------------------------------------------------------------------------

func TestGetTransfer_Success(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.getTransferFn = func(id uint64) (*model.Transfer, error) {
			return &model.Transfer{
				ID:                1,
				ClientID:          5,
				FromAccountNumber: "ACC-1",
				ToAccountNumber:   "ACC-2",
				InitialAmount:     decimal.NewFromInt(100),
				ExchangeRate:      decimal.NewFromInt(1),
				Status:            "pending_verification",
			}, nil
		}
	}, nil)

	resp, err := h.GetTransfer(context.Background(), &pb.GetTransferRequest{Id: 1})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, "pending_verification", resp.Status)
}

func TestCreateTransfer_Success(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.createTransferFn = func(ctx context.Context, transfer *model.Transfer) error {
			transfer.ID = 1
			transfer.Status = "pending_verification"
			transfer.ExchangeRate = decimal.NewFromInt(1)
			return nil
		}
	}, nil)

	resp, err := h.CreateTransfer(context.Background(), &pb.CreateTransferRequest{
		FromAccountNumber: "ACC-1",
		ToAccountNumber:   "ACC-2",
		Amount:            "100.00",
		ClientId:          5,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, "pending_verification", resp.Status)
	assert.Equal(t, "ACC-1", resp.FromAccountNumber)
	assert.Equal(t, "ACC-2", resp.ToAccountNumber)
}

func TestCreateTransfer_ServiceError(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.createTransferFn = func(ctx context.Context, transfer *model.Transfer) error {
			return service.ErrTransferNotFound
		}
	}, nil)

	_, err := h.CreateTransfer(context.Background(), &pb.CreateTransferRequest{
		FromAccountNumber: "ACC-1",
		ToAccountNumber:   "ACC-2",
		Amount:            "100.00",
		ClientId:          5,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------------------------------------------------------------------
// Payment Recipient tests
// ---------------------------------------------------------------------------

func TestCreatePaymentRecipient_Success(t *testing.T) {
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.createFn = func(pr *model.PaymentRecipient) error {
			pr.ID = 1
			return nil
		}
	})

	resp, err := h.CreatePaymentRecipient(context.Background(), &pb.CreatePaymentRecipientRequest{
		ClientId:      5,
		RecipientName: "Alice",
		AccountNumber: "ACC-2",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, uint64(5), resp.ClientId)
	assert.Equal(t, "Alice", resp.RecipientName)
	assert.Equal(t, "ACC-2", resp.AccountNumber)
}

func TestGetPaymentRecipient_Success(t *testing.T) {
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.getByIDFn = func(id uint64) (*model.PaymentRecipient, error) {
			return &model.PaymentRecipient{
				ID:            1,
				ClientID:      5,
				RecipientName: "Alice",
				AccountNumber: "ACC-2",
			}, nil
		}
	})

	resp, err := h.GetPaymentRecipient(context.Background(), &pb.GetPaymentRecipientRequest{Id: 1})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, "Alice", resp.RecipientName)
}

func TestListPaymentRecipients_Success(t *testing.T) {
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.listByClientFn = func(clientID uint64) ([]model.PaymentRecipient, error) {
			return []model.PaymentRecipient{
				{ID: 1, ClientID: clientID, RecipientName: "Alice", AccountNumber: "ACC-2"},
				{ID: 2, ClientID: clientID, RecipientName: "Bob", AccountNumber: "ACC-3"},
			}, nil
		}
	})

	resp, err := h.ListPaymentRecipients(context.Background(), &pb.ListPaymentRecipientsRequest{ClientId: 5})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Recipients, 2)
	assert.Equal(t, "Alice", resp.Recipients[0].RecipientName)
	assert.Equal(t, "Bob", resp.Recipients[1].RecipientName)
}

func TestDeletePaymentRecipient_Success(t *testing.T) {
	deleted := false
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.deleteFn = func(id uint64) error {
			deleted = true
			return nil
		}
	})

	resp, err := h.DeletePaymentRecipient(context.Background(), &pb.DeletePaymentRecipientRequest{Id: 1})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.True(t, deleted)
}

func TestDeletePaymentRecipient_Error(t *testing.T) {
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.deleteFn = func(id uint64) error {
			return service.ErrRecipientNotFound
		}
	})

	_, err := h.DeletePaymentRecipient(context.Background(), &pb.DeletePaymentRecipientRequest{Id: 999})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------------------------------------------------------------------
// ExecutePayment
// ---------------------------------------------------------------------------

func TestExecutePayment_Success(t *testing.T) {
	pm := &mockPaymentFacade{
		executePaymentFn: func(_ context.Context, _ uint64) error { return nil },
		getPaymentFn: func(id uint64) (*model.Payment, error) {
			return &model.Payment{ID: id, Status: "completed", InitialAmount: decimal.NewFromInt(100)}, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	resp, err := h.ExecutePayment(context.Background(), &pb.ExecutePaymentRequest{PaymentId: 5})
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
}

func TestExecutePayment_VerificationFailed(t *testing.T) {
	verif := &mockVerificationClient{
		getChallengeStatusFn: func(_ context.Context, _ *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
			return &verificationpb.GetChallengeStatusResponse{Status: "failed"}, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, &mockRecipientFacade{}, verif, &mockTxProducer{})
	_, err := h.ExecutePayment(context.Background(), &pb.ExecutePaymentRequest{PaymentId: 5, ChallengeId: 100})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestExecutePayment_ServiceError(t *testing.T) {
	pm := &mockPaymentFacade{
		executePaymentFn: func(_ context.Context, _ uint64) error { return service.ErrPaymentNotFound },
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ExecutePayment(context.Background(), &pb.ExecutePaymentRequest{PaymentId: 99})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListPaymentsByAccount / ListPaymentsByClient
// ---------------------------------------------------------------------------

func TestListPaymentsByAccount_Success(t *testing.T) {
	pm := &mockPaymentFacade{
		listPaymentsByAccountFn: func(_ string, _, _, _ string, _, _ float64, _, _ int) ([]model.Payment, int64, error) {
			return []model.Payment{{ID: 1, Status: "completed", InitialAmount: decimal.NewFromInt(50)}}, 1, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	resp, err := h.ListPaymentsByAccount(context.Background(), &pb.ListPaymentsByAccountRequest{AccountNumber: "ACC-1", Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Len(t, resp.Payments, 1)
}

func TestListPaymentsByAccount_Error(t *testing.T) {
	pm := &mockPaymentFacade{
		listPaymentsByAccountFn: func(_ string, _, _, _ string, _, _ float64, _, _ int) ([]model.Payment, int64, error) {
			return nil, 0, service.ErrInvalidPayment
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ListPaymentsByAccount(context.Background(), &pb.ListPaymentsByAccountRequest{AccountNumber: "ACC-1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListPaymentsByClient_Success(t *testing.T) {
	pm := &mockPaymentFacade{
		listPaymentsByClientFn: func(_ []string, _, _ int) ([]model.Payment, int64, error) {
			return []model.Payment{{ID: 1, InitialAmount: decimal.NewFromInt(75)}}, 1, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	resp, err := h.ListPaymentsByClient(context.Background(), &pb.ListPaymentsByClientRequest{
		AccountNumbers: []string{"ACC-1", "ACC-2"}, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
}

func TestListPaymentsByClient_Error(t *testing.T) {
	pm := &mockPaymentFacade{
		listPaymentsByClientFn: func(_ []string, _, _ int) ([]model.Payment, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newTransactionGRPCHandlerForTest(pm, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ListPaymentsByClient(context.Background(), &pb.ListPaymentsByClientRequest{AccountNumbers: []string{"ACC-1"}})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ExecuteTransfer
// ---------------------------------------------------------------------------

func TestExecuteTransfer_Success(t *testing.T) {
	tm := &mockTransferFacade{
		executeTransferFn: func(_ context.Context, _ uint64) error { return nil },
		getTransferFn: func(id uint64) (*model.Transfer, error) {
			return &model.Transfer{ID: id, Status: "completed", InitialAmount: decimal.NewFromInt(200)}, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	resp, err := h.ExecuteTransfer(context.Background(), &pb.ExecuteTransferRequest{TransferId: 7})
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
}

func TestExecuteTransfer_VerificationFailed(t *testing.T) {
	verif := &mockVerificationClient{
		getChallengeStatusFn: func(_ context.Context, _ *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
			return &verificationpb.GetChallengeStatusResponse{Status: "failed"}, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, &mockRecipientFacade{}, verif, &mockTxProducer{})
	_, err := h.ExecuteTransfer(context.Background(), &pb.ExecuteTransferRequest{TransferId: 7, ChallengeId: 100})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestExecuteTransfer_ServiceError(t *testing.T) {
	tm := &mockTransferFacade{
		executeTransferFn: func(_ context.Context, _ uint64) error { return service.ErrTransferNotFound },
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ExecuteTransfer(context.Background(), &pb.ExecuteTransferRequest{TransferId: 99})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListTransfersByClient
// ---------------------------------------------------------------------------

func TestListTransfersByClient_Success(t *testing.T) {
	tm := &mockTransferFacade{
		listTransfersByAccountNumbersFn: func(_ []string, _, _ int) ([]model.Transfer, int64, error) {
			return []model.Transfer{{ID: 1, InitialAmount: decimal.NewFromInt(100)}}, 1, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	resp, err := h.ListTransfersByClient(context.Background(), &pb.ListTransfersByClientRequest{
		AccountNumbers: []string{"ACC-1"}, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
}

func TestListTransfersByClient_Error(t *testing.T) {
	tm := &mockTransferFacade{
		listTransfersByAccountNumbersFn: func(_ []string, _, _ int) ([]model.Transfer, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, tm, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.ListTransfersByClient(context.Background(), &pb.ListTransfersByClientRequest{AccountNumbers: []string{"ACC-1"}})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// UpdatePaymentRecipient
// ---------------------------------------------------------------------------

func TestUpdatePaymentRecipient_Success(t *testing.T) {
	rm := &mockRecipientFacade{
		updateFn: func(id uint64, name, acct *string) (*model.PaymentRecipient, error) {
			return &model.PaymentRecipient{ID: id, RecipientName: "Updated"}, nil
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, rm, &mockVerificationClient{}, &mockTxProducer{})
	name := "Updated"
	acct := "265-9-00"
	resp, err := h.UpdatePaymentRecipient(context.Background(), &pb.UpdatePaymentRecipientRequest{
		Id: 5, RecipientName: &name, AccountNumber: &acct,
	})
	require.NoError(t, err)
	assert.Equal(t, "Updated", resp.RecipientName)
}

func TestUpdatePaymentRecipient_NotFound(t *testing.T) {
	rm := &mockRecipientFacade{
		updateFn: func(_ uint64, _, _ *string) (*model.PaymentRecipient, error) {
			return nil, service.ErrRecipientNotFound
		},
	}
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, rm, &mockVerificationClient{}, &mockTxProducer{})
	_, err := h.UpdatePaymentRecipient(context.Background(), &pb.UpdatePaymentRecipientRequest{Id: 99})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// Sentinel passthrough — typed sentinels reach the wire intact
// ---------------------------------------------------------------------------

func TestSentinel_Passthrough_TransactionHandler(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		code     codes.Code
	}{
		{"ErrTransferNotFound", service.ErrTransferNotFound, codes.NotFound},
		{"ErrPaymentNotFound", service.ErrPaymentNotFound, codes.NotFound},
		{"ErrInsufficientBalance", service.ErrInsufficientBalance, codes.FailedPrecondition},
		{"ErrAccountInactive", service.ErrAccountInactive, codes.FailedPrecondition},
		{"ErrSameAccount", service.ErrSameAccount, codes.InvalidArgument},
		{"ErrTransferLimitExceeded", service.ErrTransferLimitExceeded, codes.ResourceExhausted},
		{"ErrFeeLookupFailed", service.ErrFeeLookupFailed, codes.Unavailable},
		{"ErrVerificationRequired", service.ErrVerificationRequired, codes.PermissionDenied},
		{"ErrIdempotencyMissing", service.ErrIdempotencyMissing, codes.InvalidArgument},
		{"ErrInvalidPayment", service.ErrInvalidPayment, codes.InvalidArgument},
		{"ErrInvalidTransfer", service.ErrInvalidTransfer, codes.InvalidArgument},
		{"ErrRecipientNotFound", service.ErrRecipientNotFound, codes.NotFound},
		{"ErrFeeNotFound", service.ErrFeeNotFound, codes.NotFound},
		{"ErrInvalidFee", service.ErrInvalidFee, codes.InvalidArgument},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, ok := status.FromError(tc.sentinel)
			require.True(t, ok)
			assert.Equal(t, tc.code, s.Code())
			wrapped := fmt.Errorf("op: %w", tc.sentinel)
			assert.True(t, errors.Is(wrapped, tc.sentinel))
		})
	}
}

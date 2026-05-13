package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// TestPaymentService_TrivialWrappers verifies the read-side wrappers pass
// through to the repository.
func TestPaymentService_TrivialWrappers(t *testing.T) {
	repo := newMockPaymentRepo()
	// Seed a payment.
	p := &model.Payment{IdempotencyKey: "T1", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(10), Status: "completed"}
	require.NoError(t, repo.Create(p))

	svc := NewPaymentService(repo, nil, &FeeService{repo: &mockFeeRepo{}}, nil, "", nil)

	got, err := svc.GetPayment(p.ID)
	require.NoError(t, err)
	assert.Equal(t, "A", got.FromAccountNumber)

	// ListPaymentsByAccount returns nil/0 from mockRepo — just exercise the call.
	_, _, err = svc.ListPaymentsByAccount("A", "", "", "", 0, 0, 1, 10)
	require.NoError(t, err)

	_, _, err = svc.ListPaymentsByClient([]string{"A"}, 1, 10)
	require.NoError(t, err)
}

// TestCreatePayment_NegativeAmount_Rejected verifies the negative-amount
// guard rails.
func TestCreatePayment_NegativeAmount_Rejected(t *testing.T) {
	repo := newMockPaymentRepo()
	feeSvc := newTestFeeService(0, 0.1)
	svc := NewPaymentService(repo, nil, feeSvc, nil, "", nil)

	p := &model.Payment{IdempotencyKey: "neg-001", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(-100), CurrencyCode: "RSD"}
	err := svc.CreatePayment(context.Background(), p)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPayment), "must wrap ErrInvalidPayment")
}

// TestCreatePayment_ZeroAmount_Rejected verifies the zero-amount guard rails.
func TestCreatePayment_ZeroAmount_Rejected(t *testing.T) {
	repo := newMockPaymentRepo()
	feeSvc := newTestFeeService(0, 0.1)
	svc := NewPaymentService(repo, nil, feeSvc, nil, "", nil)

	p := &model.Payment{IdempotencyKey: "zero-001", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.Zero, CurrencyCode: "RSD"}
	err := svc.CreatePayment(context.Background(), p)
	require.Error(t, err)
}

// TestCreatePayment_FeeServiceFails_Rejected verifies that a fee-lookup
// failure REJECTS the payment (per spec).
func TestCreatePayment_FeeServiceFails_Rejected(t *testing.T) {
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, nil, &FeeService{repo: &failingFeeRepo{}}, nil, "", nil)
	p := &model.Payment{IdempotencyKey: "feefail-001", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), CurrencyCode: "RSD"}
	err := svc.CreatePayment(context.Background(), p)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFeeLookupFailed), "must wrap ErrFeeLookupFailed")
}

// TestCreatePayment_SameClient_Rejected verifies that payments between the
// same client are rejected.
func TestCreatePayment_SameClient_Rejected(t *testing.T) {
	repo := newMockPaymentRepo()
	feeSvc := newTestFeeService(0, 0.1)
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"ACC-FROM": 7,
			"ACC-TO":   7, // same client
		},
	}
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	p := &model.Payment{IdempotencyKey: "same-client", FromAccountNumber: "ACC-FROM", ToAccountNumber: "ACC-TO", InitialAmount: decimal.NewFromInt(100), CurrencyCode: "RSD"}
	err := svc.CreatePayment(context.Background(), p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different clients")
}

// TestExecutePayment_AlreadyCompleted_Idempotent verifies that re-executing a
// completed payment is a no-op.
func TestExecutePayment_AlreadyCompleted_Idempotent(t *testing.T) {
	repo := newMockPaymentRepo()
	p := &model.Payment{IdempotencyKey: "done", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), Status: "completed"}
	require.NoError(t, repo.Create(p))

	svc := NewPaymentService(repo, nil, &FeeService{repo: &mockFeeRepo{}}, nil, "", nil)
	require.NoError(t, svc.ExecutePayment(context.Background(), p.ID))
}

// TestExecutePayment_NotPendingVerification_Rejected verifies that executing
// a payment in an unexpected state errors out.
func TestExecutePayment_NotPendingVerification_Rejected(t *testing.T) {
	repo := newMockPaymentRepo()
	p := &model.Payment{IdempotencyKey: "wrongstate", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), Status: "failed"}
	require.NoError(t, repo.Create(p))

	svc := NewPaymentService(repo, nil, &FeeService{repo: &mockFeeRepo{}}, nil, "", nil)
	err := svc.ExecutePayment(context.Background(), p.ID)
	require.Error(t, err)
}

// TestExecutePayment_NotFound verifies the not-found error path.
func TestExecutePayment_NotFound(t *testing.T) {
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, nil, &FeeService{repo: &mockFeeRepo{}}, nil, "", nil)
	err := svc.ExecutePayment(context.Background(), 99999)
	require.Error(t, err)
}

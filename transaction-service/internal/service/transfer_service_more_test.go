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

// TestTransferService_TrivialWrappers covers the read-side wrappers that just
// delegate to the repository.
func TestTransferService_TrivialWrappers(t *testing.T) {
	repo := newMockTransferRepo()
	tr := &model.Transfer{IdempotencyKey: "T1", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), Status: "completed"}
	require.NoError(t, repo.Create(tr))
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)

	got, err := svc.GetTransfer(tr.ID)
	require.NoError(t, err)
	assert.Equal(t, "A", got.FromAccountNumber)

	_, _, err = svc.ListTransfersByAccountNumbers([]string{"A"}, 1, 10)
	require.NoError(t, err)
}

// TestExecuteTransfer_NotFound covers the missing-record path.
func TestExecuteTransfer_NotFound(t *testing.T) {
	repo := newMockTransferRepo()
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	err := svc.ExecuteTransfer(context.Background(), 99999)
	require.Error(t, err)
}

// TestExecuteTransfer_AlreadyCompleted_Idempotent verifies idempotent re-execute.
func TestExecuteTransfer_AlreadyCompleted_Idempotent(t *testing.T) {
	repo := newMockTransferRepo()
	tr := &model.Transfer{IdempotencyKey: "done", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), Status: "completed"}
	require.NoError(t, repo.Create(tr))
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	require.NoError(t, svc.ExecuteTransfer(context.Background(), tr.ID))
}

// TestExecuteTransfer_WrongStatus_Rejected verifies state-machine guard.
func TestExecuteTransfer_WrongStatus_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	tr := &model.Transfer{IdempotencyKey: "wrong", FromAccountNumber: "A", ToAccountNumber: "B", InitialAmount: decimal.NewFromInt(100), Status: "failed"}
	require.NoError(t, repo.Create(tr))
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	err := svc.ExecuteTransfer(context.Background(), tr.ID)
	require.Error(t, err)
}

// TestCreateTransfer_IdempotencyKeyHit verifies the early-return when an
// existing transfer with the same idempotency key is found.
func TestCreateTransfer_IdempotencyKeyHit(t *testing.T) {
	repo := newMockTransferRepo()
	existing := &model.Transfer{
		IdempotencyKey:    "dup-001",
		FromAccountNumber: "X", ToAccountNumber: "Y",
		InitialAmount: decimal.NewFromInt(100),
		Status:        "completed",
	}
	require.NoError(t, repo.Create(existing))
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	tr := &model.Transfer{
		IdempotencyKey:    "dup-001",
		FromAccountNumber: "X", ToAccountNumber: "Y",
		InitialAmount: decimal.NewFromInt(100),
	}
	require.NoError(t, svc.CreateTransfer(context.Background(), tr))
	assert.Equal(t, existing.ID, tr.ID)
	assert.Equal(t, "completed", tr.Status, "must return existing transfer's status")
}

// TestCreateTransfer_SameAccount_Rejected verifies the same-account rejection.
func TestCreateTransfer_SameAccount_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	svc := NewTransferService(repo, nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	tr := &model.Transfer{FromAccountNumber: "ACC", ToAccountNumber: "ACC", InitialAmount: decimal.NewFromInt(100)}
	err := svc.CreateTransfer(context.Background(), tr)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSameAccount))
}

// TestCreateTransfer_CrossCurrency_NoExchangeClient_Rejected verifies that
// cross-currency requires an exchange client.
func TestCreateTransfer_CrossCurrency_NoExchangeClient_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := newTestFeeService(0, 0.1)
	svc := NewTransferService(repo, nil, nil, nil, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	tr := &model.Transfer{
		FromAccountNumber: "A", ToAccountNumber: "B",
		FromCurrency: "RSD", ToCurrency: "EUR",
		InitialAmount: decimal.NewFromInt(1000),
	}
	err := svc.CreateTransfer(context.Background(), tr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exchange service")
}

// TestCreateTransfer_ExchangeClientFails_Rejected verifies the path when the
// exchange client errors out.
func TestCreateTransfer_ExchangeClientFails_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := newTestFeeService(0, 0.1)
	exch := &mockExchangeClient{err: errors.New("rate down")}
	svc := NewTransferService(repo, exch, nil, nil, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	tr := &model.Transfer{
		FromAccountNumber: "A", ToAccountNumber: "B",
		FromCurrency: "RSD", ToCurrency: "EUR",
		InitialAmount: decimal.NewFromInt(1000),
	}
	err := svc.CreateTransfer(context.Background(), tr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "currency conversion")
}

// TestCreateTransfer_FeeLookupFails_Rejected verifies the cross-currency path
// rejects when the fee service errors.
func TestCreateTransfer_FeeLookupFails_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	failFee := &FeeService{repo: &failingFeeRepo{}}
	exch := &mockExchangeClient{convertedAmount: decimal.NewFromInt(50), effectiveRate: decimal.NewFromFloat(0.05)}
	svc := NewTransferService(repo, exch, nil, nil, failFee, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	tr := &model.Transfer{
		FromAccountNumber: "A", ToAccountNumber: "B",
		FromCurrency: "RSD", ToCurrency: "EUR",
		InitialAmount: decimal.NewFromInt(1000),
	}
	err := svc.CreateTransfer(context.Background(), tr)
	require.Error(t, err)
}

// TestFindBankAccountByCurrencyWithBalance_Found verifies the picker selects
// an account with sufficient available balance.
func TestFindBankAccountByCurrencyWithBalance_Found(t *testing.T) {
	accounts := standardBankAccounts()
	accounts[1].AvailableBalance = "500"
	num, err := findBankAccountByCurrencyWithBalance(accounts, "EUR", decimal.NewFromInt(100))
	require.NoError(t, err)
	assert.Equal(t, "BANK-EUR-001", num)
}

// TestFindBankAccountByCurrencyWithBalance_FallsBackToFirst verifies the
// fallback when no candidate has enough balance.
func TestFindBankAccountByCurrencyWithBalance_FallsBackToFirst(t *testing.T) {
	accounts := standardBankAccounts()
	// Both 0 balance — picker falls back to first matching.
	num, err := findBankAccountByCurrencyWithBalance(accounts, "EUR", decimal.NewFromInt(100))
	require.NoError(t, err)
	assert.Equal(t, "BANK-EUR-001", num)
}

// TestFindBankAccountByCurrencyWithBalance_NotFound verifies the not-found
// error path when no account in the requested currency exists.
func TestFindBankAccountByCurrencyWithBalance_NotFound(t *testing.T) {
	_, err := findBankAccountByCurrencyWithBalance(standardBankAccounts(), "XYZ", decimal.NewFromInt(100))
	require.Error(t, err)
}

// TestPaymentRecipientService_GetByID covers the read-by-id wrapper.
func TestPaymentRecipientService_GetByID(t *testing.T) {
	svc, _ := newRecipientService(t)
	pr := &model.PaymentRecipient{ClientID: 1, RecipientName: "X", AccountNumber: "ACC"}
	require.NoError(t, svc.Create(pr))
	got, err := svc.GetByID(pr.ID)
	require.NoError(t, err)
	assert.Equal(t, "X", got.RecipientName)
}

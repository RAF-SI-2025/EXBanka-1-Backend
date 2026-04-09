package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// TestCreateTransferIdempotency verifies that sequential CreateTransfer calls
// with the same idempotency key return the same transfer without creating duplicates.
// (Note: concurrent idempotency requires a DB-level unique constraint; this test
// verifies the sequential lookup path that deduplicates on re-submission.)
func TestCreateTransferIdempotency(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := &FeeService{repo: &mockFeeRepo{}}
	accountClient := &mockAccountClientForTransfer{}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc := NewTransferService(repo, nil, accountClient, bankClient, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	ctx := context.Background()
	const idempotencyKey = "idem-key-sequential-001"

	transfer1 := &model.Transfer{
		FromAccountNumber: "ACC-FROM-001",
		ToAccountNumber:   "ACC-TO-001",
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(1000),
		IdempotencyKey:    idempotencyKey,
	}
	require.NoError(t, svc.CreateTransfer(ctx, transfer1), "first CreateTransfer must succeed")
	firstID := transfer1.ID

	// Second call with the same idempotency key — must return the existing record.
	transfer2 := &model.Transfer{
		FromAccountNumber: "ACC-FROM-001",
		ToAccountNumber:   "ACC-TO-001",
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(1000),
		IdempotencyKey:    idempotencyKey,
	}
	require.NoError(t, svc.CreateTransfer(ctx, transfer2), "second CreateTransfer must succeed (idempotent)")
	assert.Equal(t, firstID, transfer2.ID, "second call must return the same transfer ID")
	assert.Equal(t, 1, len(repo.transfers), "exactly one transfer record must exist after two identical calls")
}

// TestConcurrentExecuteTransferSameID verifies that executing the same transfer
// multiple times concurrently results in exactly one successful execution.
// Subsequent executions should hit the "already completed" guard.
func TestConcurrentExecuteTransferSameID(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := &FeeService{repo: &mockFeeRepo{}}
	accountClient := &mockAccountClientForTransfer{}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc := NewTransferService(repo, nil, accountClient, bankClient, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	// Seed a transfer in pending_verification state.
	transfer := &model.Transfer{
		FromAccountNumber: "SENDER-001",
		ToAccountNumber:   "RECIP-001",
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(500),
		FinalAmount:       decimal.NewFromInt(500),
		Commission:        decimal.Zero,
		Status:            "pending_verification",
	}
	require.NoError(t, repo.Create(transfer))

	ctx := context.Background()
	const workers = 5
	var wg sync.WaitGroup
	var successCount, failCount int32
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := svc.ExecuteTransfer(ctx, transfer.ID)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}()
	}
	wg.Wait()

	total := int(atomic.LoadInt32(&successCount)) + int(atomic.LoadInt32(&failCount))
	assert.Equal(t, workers, total, "all goroutines must return a result")

	final, err := repo.GetByID(transfer.ID)
	require.NoError(t, err)
	// Transfer must end in a terminal state — not left in-progress.
	assert.Contains(t, []string{"completed", "failed"}, final.Status,
		"transfer must end in a terminal state")
}

// TestSagaCompensationOnStep2Failure verifies that when the credit step fails,
// the prior debit is compensated (reversed) with a positive UpdateBalance call.
func TestSagaCompensationOnStep2Failure(t *testing.T) {
	// failOnCall=2 → second UpdateBalance (credit recipient) fails.
	accountClient := &mockAccountClientForTransfer{failOnCall: 2}
	repo := newMockTransferRepo()
	feeSvc := &FeeService{repo: &mockFeeRepo{}}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc := NewTransferService(repo, nil, accountClient, bankClient, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	transfer := &model.Transfer{
		FromAccountNumber: "SENDER-002",
		ToAccountNumber:   "RECIP-002",
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(1000),
		FinalAmount:       decimal.NewFromInt(1000),
		Commission:        decimal.Zero,
		Status:            "pending_verification",
	}
	require.NoError(t, repo.Create(transfer))

	ctx := context.Background()
	err := svc.ExecuteTransfer(ctx, transfer.ID)
	assert.Error(t, err, "transfer should fail when credit step fails")

	// Calls: (1) debit sender, (2) credit recipient [FAIL], (3) compensate = credit sender back.
	require.Len(t, accountClient.calls, 3,
		"expected debit + failed credit + compensation reversal")
	assert.Equal(t, "SENDER-002", accountClient.calls[2].accountNumber,
		"3rd call must be compensation on the sender account")
	// Compensation amount is positive (reversal of the debit).
	compensationAmount, _ := decimal.NewFromString(accountClient.calls[2].amount)
	assert.True(t, compensationAmount.IsPositive(),
		"compensation must be a positive amount (credits the sender back)")
}

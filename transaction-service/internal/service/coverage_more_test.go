package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// ---- recipient service not-found error paths --------------------------------

// TestRecipientService_NotFoundPaths verifies that GetByID/Update/Delete map a
// missing row to the typed ErrPaymentRecipientNotFound sentinel.
func TestRecipientService_NotFoundPaths(t *testing.T) {
	svc, _ := newRecipientService(t)

	_, err := svc.GetByID(424242)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPaymentRecipientNotFound, "GetByID of a missing recipient must be not-found")

	name := "x"
	_, err = svc.Update(424242, &name, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPaymentRecipientNotFound, "Update of a missing recipient must be not-found")

	err = svc.Delete(424242)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPaymentRecipientNotFound, "Delete of a missing recipient must be not-found")
}

// ---- best-effort failed-event publish (real producer, cancelled ctx) --------

// TestPublishTransferFailed_RealProducer exercises the message-building and
// publish-error-log path of publishTransferFailed. A cancelled context makes the
// underlying shared producer return without dialing a broker.
func TestPublishTransferFailed_RealProducer(t *testing.T) {
	prod := kafka.NewProducer("127.0.0.1:1")
	defer func() { _ = prod.Close() }()

	svc := NewTransferService(newMockTransferRepo(), nil, nil, nil, &FeeService{repo: &mockFeeRepo{}}, prod, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	transfer := &model.Transfer{
		ID: 7, FromAccountNumber: "A", ToAccountNumber: "B",
		InitialAmount: decimal.NewFromInt(100),
	}
	// Must not panic; publish error is swallowed/logged.
	assert.NotPanics(t, func() {
		svc.publishTransferFailed(ctx, transfer, "limit_exceeded")
	})
}

// TestPublishPaymentFailed_RealProducer exercises publishPaymentFailed's
// message-building and publish-error-log branch.
func TestPublishPaymentFailed_RealProducer(t *testing.T) {
	prod := kafka.NewProducer("127.0.0.1:1")
	defer func() { _ = prod.Close() }()

	svc := NewPaymentService(newMockPaymentRepo(), nil, &FeeService{repo: &mockFeeRepo{}}, prod, "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	payment := &model.Payment{
		ID: 9, FromAccountNumber: "A", ToAccountNumber: "B",
		FinalAmount: decimal.NewFromInt(100),
	}
	assert.NotPanics(t, func() {
		svc.publishPaymentFailed(ctx, payment, "limit_exceeded")
	})
}

// ---- cron-registry recovery wiring ------------------------------------------

// TestStartCompensationRecovery_WithCronRegistry verifies that wiring a cron
// registry routes recovery through the AdminCron control plane: the startup
// tick runs under BeginRun/EndRun, and an admin Trigger drives a subsequent
// tick on the background goroutine.
func TestStartCompensationRecovery_WithCronRegistry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)
	accountClient := &mockAccountClientForTransfer{}

	svc := &TransferService{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
		retryConfig:   shared.RetryConfig{MaxAttempts: 1},
	}
	registry := cronreg.NewRegistry("transaction-service", nil)
	require.Same(t, svc, svc.WithCronRegistry(registry), "WithCronRegistry must return the service for chaining")

	// Seed a compensating step processed by the synchronous startup tick.
	startup := model.SagaLog{
		SagaID: "cron-startup", TransactionID: 1, TransactionType: "transfer",
		StepNumber: 1, StepName: "debit_sender", Status: "compensating", IsCompensation: true,
		AccountNumber: "ACC-CRON-1", Amount: decimal.NewFromInt(100),
	}
	require.NoError(t, db.Create(&startup).Error)

	svc.StartCompensationRecovery(ctx)

	var afterStartup model.SagaLog
	require.NoError(t, db.First(&afterStartup, startup.ID).Error)
	assert.Equal(t, "completed", afterStartup.Status, "startup tick must run under the cron entry")

	// An admin Trigger drives a second tick on the goroutine.
	triggered := model.SagaLog{
		SagaID: "cron-trigger", TransactionID: 2, TransactionType: "payment",
		StepNumber: 1, StepName: "credit_recipient", Status: "compensating", IsCompensation: true,
		AccountNumber: "ACC-CRON-2", Amount: decimal.NewFromInt(50),
	}
	require.NoError(t, db.Create(&triggered).Error)
	require.NoError(t, registry.Trigger("transfer-saga-recovery", false, 99))

	require.Eventually(t, func() bool {
		var row model.SagaLog
		if err := db.First(&row, triggered.ID).Error; err != nil {
			return false
		}
		return row.Status == "completed"
	}, 2*time.Second, 20*time.Millisecond, "admin-triggered tick must complete the compensation")
}

// ---- dead-letter publish error / nil publisher ------------------------------

type errDeadLetterProducer struct{ called *int }

func (e *errDeadLetterProducer) PublishSagaDeadLetter(_ context.Context, _ kafkamsg.SagaDeadLetterMessage) error {
	*e.called++
	return errors.New("kafka unavailable")
}

// TestSagaRecovery_DeadLetterPublishError verifies that a publish failure on the
// dead-letter event is logged and non-fatal: the step is still marked
// dead_letter in the DB.
func TestSagaRecovery_DeadLetterPublishError(t *testing.T) {
	ctx := context.Background()
	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID: "dlq-puberr", TransactionID: 1, TransactionType: "transfer",
		StepNumber: 1, StepName: "debit_sender", Status: "compensating", IsCompensation: true,
		AccountNumber: "ACC-DLQ-ERR", Amount: decimal.NewFromInt(500), RetryCount: 9,
	}
	require.NoError(t, db.Create(&comp).Error)

	called := 0
	svc := newTransferServiceForTest(sagaRepo, &mockAccountClientForTransfer{failOnCall: 1}, &errDeadLetterProducer{called: &called})
	svc.runRecoveryTick(ctx)

	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "dead_letter", updated.Status, "step must still reach dead_letter despite publish error")
	assert.Equal(t, 1, called, "dead-letter publish must have been attempted")
}

// TestSagaRecovery_DeadLetterNilPublisher verifies the nil-dlPublisher branch:
// the step reaches dead_letter without any publish attempt.
func TestSagaRecovery_DeadLetterNilPublisher(t *testing.T) {
	ctx := context.Background()
	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID: "dlq-nilpub", TransactionID: 1, TransactionType: "transfer",
		StepNumber: 1, StepName: "debit_sender", Status: "compensating", IsCompensation: true,
		AccountNumber: "ACC-DLQ-NIL", Amount: decimal.NewFromInt(500), RetryCount: 9,
	}
	require.NoError(t, db.Create(&comp).Error)

	svc := &TransferService{
		sagaRepo:      sagaRepo,
		accountClient: &mockAccountClientForTransfer{failOnCall: 1},
		dlPublisher:   nil,
		retryConfig:   shared.RetryConfig{MaxAttempts: 1},
	}
	svc.runRecoveryTick(ctx)

	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "dead_letter", updated.Status, "nil dlPublisher must still mark dead_letter")
}

// ---- same-currency ExecuteTransfer saga -------------------------------------

func buildSameCurrencyTransfer(repo *mockTransferRepo, from, to string) *model.Transfer {
	tr := &model.Transfer{
		FromAccountNumber: from,
		ToAccountNumber:   to,
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(1000),
		FinalAmount:       decimal.NewFromInt(1000),
		Commission:        decimal.Zero,
		ExchangeRate:      decimal.NewFromInt(1),
		Status:            "pending_verification",
	}
	_ = repo.Create(tr)
	return tr
}

// TestExecuteTransfer_SameCurrency_HappyPath verifies the two-step same-currency
// ("prenos") saga: debit sender then credit recipient, no bank intermediate.
func TestExecuteTransfer_SameCurrency_HappyPath(t *testing.T) {
	repo := newMockTransferRepo()
	accountClient := &mockAccountClientForTransfer{}
	svc := NewTransferService(repo, nil, accountClient, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	transfer := buildSameCurrencyTransfer(repo, "SC-FROM-1", "SC-TO-1")
	require.NoError(t, svc.ExecuteTransfer(context.Background(), transfer.ID))

	require.Len(t, accountClient.calls, 2, "same-currency transfer uses exactly two balance updates")
	assert.Equal(t, "SC-FROM-1", accountClient.calls[0].accountNumber)
	assert.Equal(t, decimal.NewFromInt(1000).Neg().StringFixed(4), accountClient.calls[0].amount, "step 1 debits the sender")
	assert.Equal(t, "SC-TO-1", accountClient.calls[1].accountNumber)
	assert.Equal(t, decimal.NewFromInt(1000).StringFixed(4), accountClient.calls[1].amount, "step 2 credits the recipient")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "completed", persisted.Status)
}

// TestExecuteTransfer_SameCurrency_Step2Fails verifies that when the recipient
// credit fails, the sender debit is compensated (refunded) and the transfer is
// marked failed.
func TestExecuteTransfer_SameCurrency_Step2Fails(t *testing.T) {
	repo := newMockTransferRepo()
	accountClient := &mockAccountClientForTransfer{failOnCall: 2}
	svc := NewTransferService(repo, nil, accountClient, nil, &FeeService{repo: &mockFeeRepo{}}, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	transfer := buildSameCurrencyTransfer(repo, "SC-FROM-2", "SC-TO-2")
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)
	require.Error(t, err)

	// step1 debit, step2 credit (fail), reverse-step1 (refund) = 3 calls.
	require.Len(t, accountClient.calls, 3)
	assert.Equal(t, "SC-FROM-2", accountClient.calls[2].accountNumber, "compensation refunds the sender")
	assert.Equal(t, decimal.NewFromInt(1000).StringFixed(4), accountClient.calls[2].amount, "refund is a positive credit")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

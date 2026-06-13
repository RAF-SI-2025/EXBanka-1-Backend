package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// recordingDLPublisher captures dead-letter publications for assertions.
type recordingDLPublisher struct {
	msgs []kafkamsg.SagaDeadLetterMessage
	err  error
}

func (r *recordingDLPublisher) PublishSagaDeadLetter(_ context.Context, msg kafkamsg.SagaDeadLetterMessage) error {
	r.msgs = append(r.msgs, msg)
	return r.err
}

// buildRecovery wires a CompensationRecovery over a real in-memory DB with the
// given saga clients, returning the recovery worker, its DB, saga repo, and
// the recording dead-letter publisher.
func buildRecovery(t *testing.T, bank *mockBankAccountClientForLoan, acct *mockAccountClientForLoan) (*CompensationRecovery, *gorm.DB, *repository.SagaLogRepository, *recordingDLPublisher) {
	t.Helper()
	db := newDisbursementTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	saga := NewLoanDisbursementSaga(bank, acct, loanRepo, sagaRepo)
	dl := &recordingDLPublisher{}
	r := &CompensationRecovery{
		sagaRepo:    sagaRepo,
		disbursment: saga,
		dlPublisher: dl,
	}
	return r, db, sagaRepo, dl
}

func seedCompensatingRow(t *testing.T, db *gorm.DB, loanID uint64, step string, retryCount int) *model.SagaLog {
	t.Helper()
	row := &model.SagaLog{
		SagaID:         "loan-disbursement-x",
		LoanID:         loanID,
		StepNumber:     1,
		StepName:       step,
		Status:         "compensating",
		IsCompensation: true,
		AccountNumber:  "ACC-1",
		Amount:         decimal.NewFromInt(-100),
		ErrorMessage:   "prior failure",
		RetryCount:     retryCount,
		CreatedAt:      time.Now(),
	}
	require.NoError(t, db.Create(row).Error)
	return row
}

func seedRecoveryLoan(t *testing.T, db *gorm.DB, status string) *model.Loan {
	t.Helper()
	return seedLoan(t, db, 1, "cash", "ACC-1", status, decimal.NewFromInt(5000))
}

func TestRunRecoveryTick_NilSagaRepo_NoPanic(t *testing.T) {
	r := &CompensationRecovery{}
	// Must return immediately without panicking.
	r.runRecoveryTick(context.Background())
}

func TestRunRecoveryTick_DeadLettersAfterMaxRetries(t *testing.T) {
	r, db, sagaRepo, dl := buildRecovery(t, &mockBankAccountClientForLoan{}, &mockAccountClientForLoan{})
	row := seedCompensatingRow(t, db, 1, "debit_bank", maxLoanCompensationRetries)

	r.runRecoveryTick(context.Background())

	// Row must be marked dead_letter (no longer surfaced as compensating).
	pending, err := sagaRepo.FindPendingCompensations()
	require.NoError(t, err)
	assert.Empty(t, pending, "dead-lettered row must not remain compensating")

	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	assert.Equal(t, "dead_letter", reloaded.Status)

	// A dead-letter event must have been published with the row's metadata.
	require.Len(t, dl.msgs, 1)
	assert.Equal(t, row.ID, dl.msgs[0].SagaLogID)
	assert.Equal(t, maxLoanCompensationRetries, dl.msgs[0].RetryCount)
	assert.Equal(t, "prior failure", dl.msgs[0].LastError)
	assert.Equal(t, "debit_bank", dl.msgs[0].StepName)
}

func TestRunRecoveryTick_DeadLetter_PublishErrorStillMarksDeadLetter(t *testing.T) {
	r, db, _, dl := buildRecovery(t, &mockBankAccountClientForLoan{}, &mockAccountClientForLoan{})
	dl.err = errors.New("kafka down")
	row := seedCompensatingRow(t, db, 1, "debit_bank", maxLoanCompensationRetries+5)

	r.runRecoveryTick(context.Background())

	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	// DB mark happens before publish, so publish failure must not block dead-lettering.
	assert.Equal(t, "dead_letter", reloaded.Status)
	require.Len(t, dl.msgs, 1, "publish was attempted even though it errored")
}

func TestRunRecoveryTick_SuccessfulRetry_MarksCompleted(t *testing.T) {
	// debit_bank compensation = CreditBankAccount; succeeds on the mock.
	bank := &mockBankAccountClientForLoan{}
	r, db, sagaRepo, _ := buildRecovery(t, bank, &mockAccountClientForLoan{})
	loan := seedRecoveryLoan(t, db, "disbursement_failed")
	row := seedCompensatingRow(t, db, loan.ID, "debit_bank", 0)

	r.runRecoveryTick(context.Background())

	// Compensation succeeded → the bank was credited back and the row completed.
	require.Len(t, bank.creditCalls, 1, "RetryStuckCompensation must credit the bank for debit_bank")
	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	assert.Equal(t, "completed", reloaded.Status)

	pending, err := sagaRepo.FindPendingCompensations()
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestRunRecoveryTick_MarkLoanActiveCompensation_FlipsLoanStatus(t *testing.T) {
	r, db, _, _ := buildRecovery(t, &mockBankAccountClientForLoan{}, &mockAccountClientForLoan{})
	loan := seedRecoveryLoan(t, db, "active")
	row := seedCompensatingRow(t, db, loan.ID, "mark_loan_active", 0)

	r.runRecoveryTick(context.Background())

	var reloadedLoan model.Loan
	require.NoError(t, db.First(&reloadedLoan, loan.ID).Error)
	assert.Equal(t, "disbursement_failed", reloadedLoan.Status, "mark_loan_active compensation must revert loan status")

	var reloadedRow model.SagaLog
	require.NoError(t, db.First(&reloadedRow, row.ID).Error)
	assert.Equal(t, "completed", reloadedRow.Status)
}

func TestRunRecoveryTick_RetryFails_IncrementsRetryCount(t *testing.T) {
	// CreditBankAccount returns an error → compensation fails → retry_count++.
	bank := &mockBankAccountClientForLoan{creditErr: errors.New("bank still down")}
	r, db, _, _ := buildRecovery(t, bank, &mockAccountClientForLoan{})
	loan := seedRecoveryLoan(t, db, "disbursement_failed")
	row := seedCompensatingRow(t, db, loan.ID, "debit_bank", 2)

	r.runRecoveryTick(context.Background())

	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	assert.Equal(t, 3, reloaded.RetryCount, "failed retry must increment retry_count")
	assert.Equal(t, "compensating", reloaded.Status, "still-failing row stays compensating")
}

func TestRunRecoveryTick_LoanLookupFails_IncrementsRetryCount(t *testing.T) {
	r, db, _, _ := buildRecovery(t, &mockBankAccountClientForLoan{}, &mockAccountClientForLoan{})
	// LoanID 99999 has no matching loan row.
	row := seedCompensatingRow(t, db, 99999, "debit_bank", 1)

	r.runRecoveryTick(context.Background())

	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	assert.Equal(t, 2, reloaded.RetryCount, "loan-lookup failure must increment retry_count")
	assert.Equal(t, "compensating", reloaded.Status)
}

func TestRunRecoveryTick_UnknownStep_IncrementsRetryCount(t *testing.T) {
	r, db, _, _ := buildRecovery(t, &mockBankAccountClientForLoan{}, &mockAccountClientForLoan{})
	loan := seedRecoveryLoan(t, db, "disbursement_failed")
	row := seedCompensatingRow(t, db, loan.ID, "bogus_step", 0)

	r.runRecoveryTick(context.Background())

	var reloaded model.SagaLog
	require.NoError(t, db.First(&reloaded, row.ID).Error)
	assert.Equal(t, 1, reloaded.RetryCount, "unknown step is an error → retry_count++")
}

func TestNewCompensationRecovery_And_Start_ProcessesQueue(t *testing.T) {
	db := newDisbursementTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	bank := &mockBankAccountClientForLoan{}
	saga := NewLoanDisbursementSaga(bank, &mockAccountClientForLoan{}, loanRepo, sagaRepo)

	// Real producer pointed at a dead broker — the success path never publishes,
	// so the broker is never dialed. Verifies the constructor accepts a real
	// *kafka.Producer and registers a cron entry.
	producer := kafka.NewProducer("localhost:9999")
	defer producer.Close()
	registry := nilRegistry()
	r := NewCompensationRecovery(sagaRepo, saga, producer, registry)
	require.NotNil(t, r)

	loan := seedRecoveryLoan(t, db, "disbursement_failed")
	row := seedCompensatingRow(t, db, loan.ID, "debit_bank", 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// The immediate catch-up tick should process and complete the row.
	require.Eventually(t, func() bool {
		var reloaded model.SagaLog
		if err := db.First(&reloaded, row.ID).Error; err != nil {
			return false
		}
		return reloaded.Status == "completed"
	}, 2*time.Second, 10*time.Millisecond, "Start's immediate tick must complete the queued compensation")
}

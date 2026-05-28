// Package saga adapts credit-service's credit_saga_logs table to the
// saga.Recorder interface from contract/shared/saga. All credit-service
// sagas (loan disbursement) route step bookkeeping through this recorder.
package saga

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// Recorder implements sharedsaga.Recorder over the SagaLogRepository.
type Recorder struct {
	repo   *repository.SagaLogRepository
	loanID uint64
}

// NewRecorder constructs a recorder bound to the given repository and loan.
func NewRecorder(repo *repository.SagaLogRepository, loanID uint64) *Recorder {
	return &Recorder{repo: repo, loanID: loanID}
}

// Compile-time guard.
var _ sharedsaga.Recorder = (*Recorder)(nil)

// State key conventions.
const (
	keyLoanID     = "loan_id"
	keyStepPrefix = "step:"
	keyAccount    = ":account_number"
	keyAmount     = ":amount"
)

func stepAccountKey(name sharedsaga.StepKind) string {
	return keyStepPrefix + string(name) + keyAccount
}
func stepAmountKey(name sharedsaga.StepKind) string {
	return keyStepPrefix + string(name) + keyAmount
}

// RecordForward writes a pending forward step row and returns its handle.
func (r *Recorder) RecordForward(ctx context.Context, sagaID string, stepName sharedsaga.StepKind, stepNumber int, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(sharedsaga.SagaStatusPending)
	row.IsCompensation = false
	if err := r.repo.RecordStep(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return sharedsaga.StepHandle{ID: row.ID}, nil
}

// MarkCompleted transitions a pending row to completed.
func (r *Recorder) MarkCompleted(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.repo.CompleteStep(h.ID)
}

// MarkFailed transitions a pending row to failed with a reason.
func (r *Recorder) MarkFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.repo.FailStep(h.ID, errMsg)
}

// RecordCompensation writes a compensating saga step row.
func (r *Recorder) RecordCompensation(ctx context.Context, sagaID string, stepName sharedsaga.StepKind, stepNumber int, forward sharedsaga.StepHandle, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(sharedsaga.SagaStatusCompensating)
	row.IsCompensation = true
	if forward.ID != 0 {
		fid := forward.ID
		row.CompensationOf = &fid
	}
	// Negate amount for compensation so the recovery loop can inspect direction.
	row.Amount = row.Amount.Neg()
	if err := r.repo.RecordStep(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return sharedsaga.StepHandle{ID: row.ID}, nil
}

// MarkCompensated transitions a compensation row to its successful end state.
func (r *Recorder) MarkCompensated(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.repo.CompleteStep(h.ID)
}

// MarkCompensationFailed leaves the row in compensating status with an error
// message; the recovery loop picks it up on the next tick.
func (r *Recorder) MarkCompensationFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.repo.SetErrorMessage(h.ID, errMsg)
}

// IsCompleted reports whether a forward step with the given saga_id+step_name
// has already reached completed status. Used by sharedsaga.Saga's restart-resume.
func (r *Recorder) IsCompleted(ctx context.Context, sagaID string, stepName sharedsaga.StepKind) (bool, error) {
	return r.repo.IsForwardCompleted(sagaID, string(stepName))
}

// buildRow assembles a SagaLog row from saga-level + per-step state keys.
func (r *Recorder) buildRow(sagaID string, stepName sharedsaga.StepKind, stepNumber int, st *sharedsaga.State) *model.SagaLog {
	row := &model.SagaLog{
		SagaID:     sagaID,
		LoanID:     r.loanID,
		StepNumber: stepNumber,
		StepName:   string(stepName),
		CreatedAt:  time.Now(),
	}
	if v, ok := st.Get(stepAccountKey(stepName)); ok {
		row.AccountNumber, _ = v.(string)
	}
	if v, ok := st.Get(stepAmountKey(stepName)); ok {
		if d, ok := v.(decimal.Decimal); ok {
			row.Amount = d
		}
	}
	return row
}


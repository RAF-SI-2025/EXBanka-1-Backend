// Package saga adapts transaction-service's saga_logs table to the
// shared.Recorder and shared.RecoveryRecorder interfaces. The adapter is
// thin: read the per-step metadata (account_number, amount, transaction_id,
// transaction_type) from *shared.State and persist a model.SagaLog row.
//
// Per-step metadata convention: the saga executor pre-populates the state
// with keys of the form "step:<name>:account_number" and "step:<name>:amount"
// before calling Execute. RecordForward and RecordCompensation read those
// keys to populate the audit row.
package saga

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// Recorder implements shared.Recorder + shared.RecoveryRecorder over the
// existing SagaLogRepository.
type Recorder struct {
	repo *repository.SagaLogRepository
}

// NewRecorder constructs a recorder bound to the given repository.
func NewRecorder(repo *repository.SagaLogRepository) *Recorder {
	return &Recorder{repo: repo}
}

// Compile-time guards.
var (
	_ shared.Recorder         = (*Recorder)(nil)
	_ shared.RecoveryRecorder = (*Recorder)(nil)
)

// State key helpers — the saga executor sets these per step before
// Execute is called so the recorder can persist the right account/amount.
const (
	keyTxID       = "transaction_id"
	keyTxType     = "transaction_type"
	keyStepPrefix = "step:"
	keyAccount    = ":account_number"
	keyAmount     = ":amount"
)

func stepAccountKey(name string) string { return keyStepPrefix + name + keyAccount }
func stepAmountKey(name string) string  { return keyStepPrefix + name + keyAmount }

// RecordForward writes a pending saga step row and returns its handle.
func (r *Recorder) RecordForward(ctx context.Context, sagaID, stepName string, stepNumber int, st *shared.State) (shared.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(shared.SagaStatusPending)
	row.IsCompensation = false
	if err := r.repo.RecordStep(row); err != nil {
		return shared.StepHandle{}, err
	}
	return shared.StepHandle{ID: row.ID}, nil
}

// MarkCompleted transitions a pending row to completed.
func (r *Recorder) MarkCompleted(ctx context.Context, h shared.StepHandle) error {
	return r.repo.CompleteStep(h.ID)
}

// MarkFailed transitions a pending row to failed with a reason.
func (r *Recorder) MarkFailed(ctx context.Context, h shared.StepHandle, errMsg string) error {
	return r.repo.FailStep(h.ID, errMsg)
}

// RecordCompensation writes a compensating saga step row.
func (r *Recorder) RecordCompensation(ctx context.Context, sagaID, stepName string, stepNumber int, forward shared.StepHandle, st *shared.State) (shared.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(shared.SagaStatusCompensating)
	row.IsCompensation = true
	if forward.ID != 0 {
		fid := forward.ID
		row.CompensationOf = &fid
	}
	// Compensation rows store the negated amount so the recovery loop
	// can replay UpdateBalance directly without re-deriving the sign.
	row.Amount = row.Amount.Neg()
	if err := r.repo.RecordStep(row); err != nil {
		return shared.StepHandle{}, err
	}
	return shared.StepHandle{ID: row.ID}, nil
}

// MarkCompensated transitions a compensation row to its successful end state.
// SagaLogRepository's CompleteStep writes status="completed" — same convention
// the existing code uses for both forward and compensation success.
func (r *Recorder) MarkCompensated(ctx context.Context, h shared.StepHandle) error {
	return r.repo.CompleteStep(h.ID)
}

// MarkCompensationFailed leaves the row in compensating status with an
// error message; the recovery loop picks it up on the next tick.
func (r *Recorder) MarkCompensationFailed(ctx context.Context, h shared.StepHandle, errMsg string) error {
	return r.repo.SetErrorMessage(h.ID, errMsg)
}

// IsCompleted reports whether a forward step with the given saga_id+step_name
// has already reached completed status. Used by shared.Saga's restart-resume.
func (r *Recorder) IsCompleted(ctx context.Context, sagaID, stepName string) (bool, error) {
	return r.repo.IsForwardCompleted(sagaID, stepName)
}

// ListStuck returns rows in pending or compensating status whose
// created_at (proxy for "last touched") is older than olderThan.
func (r *Recorder) ListStuck(ctx context.Context, olderThan time.Duration) ([]shared.StuckStep, error) {
	cutoff := time.Now().Add(-olderThan)
	rows, err := r.repo.ListStuckOlderThan(cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]shared.StuckStep, 0, len(rows))
	for _, r := range rows {
		out = append(out, shared.StuckStep{
			Handle:       shared.StepHandle{ID: r.ID},
			SagaID:       r.SagaID,
			StepName:     stripCompensationSuffix(r.StepName),
			StepNumber:   r.StepNumber,
			Status:       shared.SagaStatus(r.Status),
			RetryCount:   r.RetryCount,
			UpdatedAt:    r.CreatedAt,
			Compensation: r.IsCompensation,
			Payload: map[string]any{
				"account_number":   r.AccountNumber,
				"amount":           r.Amount,
				"transaction_id":   r.TransactionID,
				"transaction_type": r.TransactionType,
			},
		})
	}
	return out, nil
}

// IncrementRetry bumps retry_count on a stuck row.
func (r *Recorder) IncrementRetry(ctx context.Context, h shared.StepHandle) error {
	return r.repo.IncrementRetryCount(h.ID)
}

// MarkDeadLetter transitions a stuck row to dead_letter terminal state.
func (r *Recorder) MarkDeadLetter(ctx context.Context, h shared.StepHandle, reason string) error {
	return r.repo.MarkDeadLetter(h.ID, reason)
}

// buildRow assembles a SagaLog row from saga-level keys + per-step keys
// in the State. Missing keys default to zero values (which the underlying
// model accepts) so a partially-populated state still records.
func (r *Recorder) buildRow(sagaID, stepName string, stepNumber int, st *shared.State) *model.SagaLog {
	row := &model.SagaLog{
		SagaID:     sagaID,
		StepNumber: stepNumber,
		StepName:   stepName,
		CreatedAt:  time.Now(),
	}
	if v, ok := st.Get(keyTxID); ok {
		row.TransactionID, _ = v.(uint64)
	}
	if v, ok := st.Get(keyTxType); ok {
		row.TransactionType, _ = v.(string)
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

// stripCompensationSuffix removes the "_compensation" suffix that the
// legacy executor appended to compensation step names so the classifier
// sees the canonical step name.
func stripCompensationSuffix(name string) string {
	const suffix = "_compensation"
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(name, suffix)
	}
	return name
}

// ErrNotFound is returned by helpers that look up a row that doesn't exist.
var ErrNotFound = errors.New("saga: row not found")

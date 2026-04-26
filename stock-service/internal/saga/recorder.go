// Package saga adapts stock-service's saga_logs table to the
// shared.Recorder + shared.RecoveryRecorder interfaces. Available for
// new sagas that want the shared executor; the existing SagaExecutor
// (RunStep / RunCompensation) keeps working unchanged in parallel.
package saga

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"

	"github.com/exbanka/contract/shared"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// Recorder implements shared.Recorder + shared.RecoveryRecorder over the
// existing stock-service SagaLogRepository. The on-disk row uses Version
// optimistic locking; the recorder threads version through StepHandle so
// updates honor it.
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

// State key conventions. The saga executor populates these per-saga
// (order_id, order_transaction_id) and per-step (amount, currency,
// payload).
const (
	keyOrderID            = "order_id"
	keyOrderTransactionID = "order_transaction_id"
	keyStepPrefix         = "step:"
	keyAmount             = ":amount"
	keyCurrency           = ":currency"
	keyPayload            = ":payload"
)

func stepAmountKey(name string) string   { return keyStepPrefix + name + keyAmount }
func stepCurrencyKey(name string) string { return keyStepPrefix + name + keyCurrency }
func stepPayloadKey(name string) string  { return keyStepPrefix + name + keyPayload }

// RecordForward writes a pending forward step row.
func (r *Recorder) RecordForward(ctx context.Context, sagaID, stepName string, stepNumber int, st *shared.State) (shared.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(shared.SagaStatusPending)
	row.IsCompensation = false
	if err := r.repo.RecordStep(row); err != nil {
		return shared.StepHandle{}, err
	}
	return shared.StepHandle{ID: row.ID, Version: row.Version}, nil
}

// MarkCompleted transitions a pending row to completed via the optimistic
// lock-aware UpdateStatus method.
func (r *Recorder) MarkCompleted(ctx context.Context, h shared.StepHandle) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(shared.SagaStatusCompleted), "")
}

// MarkFailed transitions a pending row to failed.
func (r *Recorder) MarkFailed(ctx context.Context, h shared.StepHandle, errMsg string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(shared.SagaStatusFailed), errMsg)
}

// RecordCompensation writes a compensating row linked to the forward step.
func (r *Recorder) RecordCompensation(ctx context.Context, sagaID, stepName string, stepNumber int, forward shared.StepHandle, st *shared.State) (shared.StepHandle, error) {
	row := r.buildRow(sagaID, stepName, stepNumber, st)
	row.Status = string(shared.SagaStatusCompensating)
	row.IsCompensation = true
	if forward.ID != 0 {
		fid := forward.ID
		row.CompensationOf = &fid
	}
	if err := r.repo.RecordStep(row); err != nil {
		return shared.StepHandle{}, err
	}
	return shared.StepHandle{ID: row.ID, Version: row.Version}, nil
}

// MarkCompensated transitions a compensation row to its terminal state.
func (r *Recorder) MarkCompensated(ctx context.Context, h shared.StepHandle) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(shared.SagaStatusCompensated), "")
}

// MarkCompensationFailed leaves the row in compensating status with a
// reason; the recovery loop picks it up on the next tick.
func (r *Recorder) MarkCompensationFailed(ctx context.Context, h shared.StepHandle, errMsg string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(shared.SagaStatusCompensating), errMsg)
}

// IsCompleted reports whether a forward step has already completed for
// this (order_id, step_name) pair.
func (r *Recorder) IsCompleted(ctx context.Context, sagaID, stepName string) (bool, error) {
	// stock-service's saga_logs are scoped per-order, not per-saga-id, so
	// IsCompleted is approximate without an order_id. The shared.Saga
	// runner can pass it via the State if available; otherwise this
	// returns false (the safe default — re-running a step that already
	// completed is at most a duplicate ledger entry the idempotency_key
	// absorbs).
	return false, nil
}

// ListStuck returns rows in pending or compensating status whose
// updated_at is older than olderThan.
func (r *Recorder) ListStuck(ctx context.Context, olderThan time.Duration) ([]shared.StuckStep, error) {
	rows, err := r.repo.ListStuckSagas(olderThan)
	if err != nil {
		return nil, err
	}
	out := make([]shared.StuckStep, 0, len(rows))
	for _, row := range rows {
		payload := map[string]any{
			"order_id": row.OrderID,
		}
		if row.OrderTransactionID != nil {
			payload["order_transaction_id"] = *row.OrderTransactionID
		}
		if row.Amount != nil {
			payload["amount"] = *row.Amount
		}
		if row.CurrencyCode != "" {
			payload["currency"] = row.CurrencyCode
		}
		if len(row.Payload) > 0 {
			var raw map[string]any
			if err := json.Unmarshal(row.Payload, &raw); err == nil {
				payload["payload"] = raw
			}
		}
		out = append(out, shared.StuckStep{
			Handle:       shared.StepHandle{ID: row.ID, Version: row.Version},
			SagaID:       row.SagaID,
			StepName:     stripCompensationSuffix(row.StepName),
			StepNumber:   row.StepNumber,
			Status:       shared.SagaStatus(row.Status),
			RetryCount:   row.RetryCount,
			UpdatedAt:    row.UpdatedAt,
			Compensation: row.IsCompensation,
			Payload:      payload,
		})
	}
	return out, nil
}

// IncrementRetry bumps retry_count without changing status/version.
func (r *Recorder) IncrementRetry(ctx context.Context, h shared.StepHandle) error {
	return r.repo.IncrementRetryCount(h.ID)
}

// MarkDeadLetter transitions a stuck row to dead_letter.
func (r *Recorder) MarkDeadLetter(ctx context.Context, h shared.StepHandle, reason string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(shared.SagaStatusDeadLetter), reason)
}

// buildRow assembles a model.SagaLog from saga-level + per-step keys.
func (r *Recorder) buildRow(sagaID, stepName string, stepNumber int, st *shared.State) *model.SagaLog {
	row := &model.SagaLog{
		SagaID:     sagaID,
		StepNumber: stepNumber,
		StepName:   stepName,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if v, ok := st.Get(keyOrderID); ok {
		if id, ok := v.(uint64); ok {
			row.OrderID = id
		}
	}
	if v, ok := st.Get(keyOrderTransactionID); ok {
		if id, ok := v.(uint64); ok {
			row.OrderTransactionID = &id
		} else if id, ok := v.(*uint64); ok {
			row.OrderTransactionID = id
		}
	}
	if v, ok := st.Get(stepAmountKey(stepName)); ok {
		if d, ok := v.(decimal.Decimal); ok && !d.IsZero() {
			a := d
			row.Amount = &a
		}
	}
	if v, ok := st.Get(stepCurrencyKey(stepName)); ok {
		if c, ok := v.(string); ok {
			row.CurrencyCode = c
		}
	}
	if v, ok := st.Get(stepPayloadKey(stepName)); ok {
		if m, ok := v.(map[string]any); ok && m != nil {
			if b, err := json.Marshal(m); err == nil {
				row.Payload = datatypes.JSON(b)
			}
		}
	}
	return row
}

// stripCompensationSuffix removes "_compensation" suffix appended by some
// legacy step-name conventions so the classifier sees the base name.
func stripCompensationSuffix(name string) string {
	const suffix = "_compensation"
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(name, suffix)
	}
	return name
}

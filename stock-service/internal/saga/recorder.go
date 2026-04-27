// Package saga adapts stock-service's saga_logs table to the saga.Recorder +
// saga.RecoveryRecorder interfaces from contract/shared/saga. Available for
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

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// SagaRepoIF is the minimal subset of *repository.SagaLogRepository that
// Recorder needs for the saga-runner path (RecordForward, MarkCompleted,
// MarkFailed, RecordCompensation, MarkCompensated, IsCompleted). Callers
// pass either the concrete *repository.SagaLogRepository or a test mock
// satisfying this interface.
type SagaRepoIF interface {
	RecordStep(log *model.SagaLog) error
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	IsForwardCompleted(sagaID, stepName string) (bool, error)
}

// recoveryRepoIF extends SagaRepoIF with the queries the RecoveryRunner
// needs. The concrete *repository.SagaLogRepository implements both;
// when the RecoveryRecorder methods are called against a test mock that
// only satisfies SagaRepoIF the assertion fails fast (panic) — which is
// fine because tests that don't exercise recovery shouldn't call those
// methods.
type recoveryRepoIF interface {
	SagaRepoIF
	ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error)
	IncrementRetryCount(id uint64) error
}

// Recorder implements sharedsaga.Recorder over a SagaRepoIF, and
// sharedsaga.RecoveryRecorder when the underlying repo also satisfies the
// recoveryRepoIF surface (the concrete *repository.SagaLogRepository
// always does).
type Recorder struct {
	repo SagaRepoIF
}

// NewRecorder constructs a recorder bound to the given repository.
// The argument is typed as the interface so service-layer code that uses
// SagaLogRepo (the legacy interface) can hand it through unchanged.
func NewRecorder(repo SagaRepoIF) *Recorder {
	return &Recorder{repo: repo}
}

// Compile-time guards.
var (
	_ sharedsaga.Recorder         = (*Recorder)(nil)
	_ sharedsaga.RecoveryRecorder = (*Recorder)(nil)
	_ recoveryRepoIF              = (*repository.SagaLogRepository)(nil)
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
func (r *Recorder) RecordForward(ctx context.Context, sagaID string, step sharedsaga.StepKind, stepNumber int, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	row := r.buildRow(sagaID, string(step), stepNumber, st)
	row.Status = string(sharedsaga.SagaStatusPending)
	row.IsCompensation = false
	if err := r.repo.RecordStep(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return sharedsaga.StepHandle{ID: row.ID, Version: row.Version}, nil
}

// MarkCompleted transitions a pending row to completed via the optimistic
// lock-aware UpdateStatus method.
func (r *Recorder) MarkCompleted(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(sharedsaga.SagaStatusCompleted), "")
}

// MarkFailed transitions a pending row to failed.
func (r *Recorder) MarkFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(sharedsaga.SagaStatusFailed), errMsg)
}

// RecordCompensation writes a compensating row linked to the forward step.
func (r *Recorder) RecordCompensation(ctx context.Context, sagaID string, step sharedsaga.StepKind, stepNumber int, forward sharedsaga.StepHandle, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	row := r.buildRow(sagaID, string(step), stepNumber, st)
	row.Status = string(sharedsaga.SagaStatusCompensating)
	row.IsCompensation = true
	if forward.ID != 0 {
		fid := forward.ID
		row.CompensationOf = &fid
	}
	if err := r.repo.RecordStep(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return sharedsaga.StepHandle{ID: row.ID, Version: row.Version}, nil
}

// MarkCompensated transitions a compensation row to its terminal state.
func (r *Recorder) MarkCompensated(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(sharedsaga.SagaStatusCompensated), "")
}

// MarkCompensationFailed leaves the row in compensating status with a
// reason; the recovery loop picks it up on the next tick.
func (r *Recorder) MarkCompensationFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(sharedsaga.SagaStatusCompensating), errMsg)
}

// IsCompleted reports whether a forward step has already completed for
// this (saga_id, step_name) pair. Saga-scoped (NOT step-name-only)
// because two distinct sagas in the same service share step names —
// without saga scoping, every "persist_order_pending" step from any
// prior order would silently skip the current order's step, leaving
// the order unpersisted while the saga reports success.
func (r *Recorder) IsCompleted(ctx context.Context, sagaID string, step sharedsaga.StepKind) (bool, error) {
	return r.repo.IsForwardCompleted(sagaID, string(step))
}

// ListStuck returns rows in pending or compensating status whose
// updated_at is older than olderThan. Requires the underlying repo to
// satisfy recoveryRepoIF — the concrete *repository.SagaLogRepository
// always does. Returns an empty slice if the assertion fails (callers
// using a minimal mock for tests aren't exercising recovery anyway).
func (r *Recorder) ListStuck(ctx context.Context, olderThan time.Duration) ([]sharedsaga.StuckStep, error) {
	rec, ok := r.repo.(recoveryRepoIF)
	if !ok {
		return nil, nil
	}
	rows, err := rec.ListStuckSagas(olderThan)
	if err != nil {
		return nil, err
	}
	out := make([]sharedsaga.StuckStep, 0, len(rows))
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
		out = append(out, sharedsaga.StuckStep{
			Handle:       sharedsaga.StepHandle{ID: row.ID, Version: row.Version},
			SagaID:       row.SagaID,
			StepName:     stripCompensationSuffix(row.StepName),
			StepNumber:   row.StepNumber,
			Status:       sharedsaga.SagaStatus(row.Status),
			RetryCount:   row.RetryCount,
			UpdatedAt:    row.UpdatedAt,
			Compensation: row.IsCompensation,
			Payload:      payload,
		})
	}
	return out, nil
}

// IncrementRetry bumps retry_count without changing status/version.
// No-ops if the underlying repo doesn't satisfy recoveryRepoIF.
func (r *Recorder) IncrementRetry(ctx context.Context, h sharedsaga.StepHandle) error {
	rec, ok := r.repo.(recoveryRepoIF)
	if !ok {
		return nil
	}
	return rec.IncrementRetryCount(h.ID)
}

// MarkDeadLetter transitions a stuck row to dead_letter.
func (r *Recorder) MarkDeadLetter(ctx context.Context, h sharedsaga.StepHandle, reason string) error {
	return r.repo.UpdateStatus(h.ID, h.Version, string(sharedsaga.SagaStatusDeadLetter), reason)
}

// buildRow assembles a model.SagaLog from saga-level + per-step keys.
func (r *Recorder) buildRow(sagaID, stepName string, stepNumber int, st *sharedsaga.State) *model.SagaLog {
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

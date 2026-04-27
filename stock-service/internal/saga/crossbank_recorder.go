package saga

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// CrossBankRecorder is a sharedsaga.Recorder that persists every saga step
// transition into the inter_bank_saga_logs table. One recorder is bound to a
// single in-flight cross-bank saga and carries the per-saga immutable
// context (RemoteBankCode, SagaKind, OfferID, ContractID) so each row written
// has the full audit context the cross-bank reconciler crons need.
//
// A separate recorder per saga is intentional: the per-saga fields are part
// of the row, not the step, so threading them through Recorder method
// arguments would clutter every saga step closure.
//
// Compensation rows are stored under Phase = "compensate_<step>" so the
// (tx_id, phase, role) unique index lets a forward row and its compensation
// row coexist for the same step. The IdempotencyKey, derived from
// (sagaKind, txID, phase, role), automatically distinguishes them.
//
// Status updates (MarkCompleted / MarkFailed / MarkCompensated /
// MarkCompensationFailed) receive only an opaque sharedsaga.StepHandle from
// the executor. The shared.Saga handle carries no row-identifying string —
// just (uint64 ID, int64 Version). InterBankSagaLog rows are keyed by
// string UUID, so the recorder maintains an internal handle->row-key map
// populated when RecordForward / RecordCompensation issued the handle.
type CrossBankRecorder struct {
	repo           *repository.InterBankSagaLogRepository
	role           string
	sagaKind       string
	remoteBankCode string
	offerID        *uint64
	contractID     *uint64

	// nextHandle is the source of synthetic StepHandle.ID values handed back
	// to the saga executor. Atomic so multi-goroutine sagas don't race.
	nextHandle uint64

	// handles maps a previously-issued StepHandle.ID back to the (txID, phase,
	// role) triplet needed to load the row for status updates.
	mu      sync.Mutex
	handles map[uint64]rowKey
}

// rowKey captures the lookup tuple for an InterBankSagaLog row.
type rowKey struct {
	txID  string
	phase string
	role  string
}

// NewCrossBankRecorder constructs a recorder bound to a single cross-bank
// saga. Panics on invalid role or sagaKind to fail fast on caller bugs:
// these are stored in every row and a typo in production would corrupt
// the audit log silently.
//
// offerID / contractID may be nil; they are copied verbatim into every row
// the recorder writes, so callers should pass whichever identifiers the
// saga has at construction time (typically offerID for accept sagas,
// contractID for exercise/expire sagas, often both).
func NewCrossBankRecorder(
	repo *repository.InterBankSagaLogRepository,
	role string,
	sagaKind string,
	remoteBankCode string,
	offerID *uint64,
	contractID *uint64,
) *CrossBankRecorder {
	switch role {
	case model.SagaRoleInitiator, model.SagaRoleResponder:
	default:
		panic(fmt.Sprintf("crossbank recorder: invalid role %q", role))
	}
	switch sagaKind {
	case model.SagaKindAccept, model.SagaKindExercise, model.SagaKindExpire:
	default:
		panic(fmt.Sprintf("crossbank recorder: invalid saga kind %q", sagaKind))
	}
	return &CrossBankRecorder{
		repo:           repo,
		role:           role,
		sagaKind:       sagaKind,
		remoteBankCode: remoteBankCode,
		offerID:        offerID,
		contractID:     contractID,
		handles:        make(map[uint64]rowKey),
	}
}

// Compile-time guard.
var _ sharedsaga.Recorder = (*CrossBankRecorder)(nil)

// crossbankPayloadKey returns the State key under which a saga step stashes
// the serialized request body for the recorder to persist. Exported in
// lower-case to the saga package so step closures can write to it without
// reaching into recorder internals.
func crossbankPayloadKey(step sharedsaga.StepKind) string {
	return "crossbank:" + string(step) + ":payload"
}

// crossbankCompensationPhase is the canonical Phase column value for a
// compensation row of step. The forward row uses string(step); the
// compensation row uses the prefixed form so both rows fit the
// (tx_id, phase, role) unique index without colliding.
func crossbankCompensationPhase(step sharedsaga.StepKind) string {
	return "compensate_" + string(step)
}

// extractPayload pulls the per-step payload bytes from State if present.
// Returns nil if no payload was set; the caller writes nil JSONB which
// round-trips fine for steps whose payload is implicit.
func extractPayload(st *sharedsaga.State, step sharedsaga.StepKind) []byte {
	if st == nil {
		return nil
	}
	v, ok := st.Get(crossbankPayloadKey(step))
	if !ok {
		return nil
	}
	if b, ok := v.([]byte); ok {
		return b
	}
	return nil
}

// RecordForward upserts a pending row keyed by (sagaID, step, role). The
// repository auto-fills ID and IdempotencyKey on insert.
func (r *CrossBankRecorder) RecordForward(ctx context.Context, sagaID string, step sharedsaga.StepKind, _ int, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	phase := string(step)
	row := r.buildRow(sagaID, phase, model.IBSagaStatusPending, st, step)
	if err := r.repo.UpsertByTxPhaseRole(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return r.issueHandle(sagaID, phase), nil
}

// RecordCompensation upserts a SECOND row under Phase="compensate_<step>"
// in compensating status. The forward row stays at completed; this row
// captures the compensation's lifecycle separately.
func (r *CrossBankRecorder) RecordCompensation(ctx context.Context, sagaID string, step sharedsaga.StepKind, _ int, _ sharedsaga.StepHandle, st *sharedsaga.State) (sharedsaga.StepHandle, error) {
	phase := crossbankCompensationPhase(step)
	row := r.buildRow(sagaID, phase, model.IBSagaStatusCompensating, st, step)
	if err := r.repo.UpsertByTxPhaseRole(row); err != nil {
		return sharedsaga.StepHandle{}, err
	}
	return r.issueHandle(sagaID, phase), nil
}

// MarkCompleted reads the pending row via the stashed (txID, phase, role),
// flips status to completed, and saves it. Optimistic-locked via the
// model's BeforeUpdate hook.
func (r *CrossBankRecorder) MarkCompleted(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.updateStatus(h, model.IBSagaStatusCompleted, "")
}

// MarkFailed flips the row to failed and stores the reason.
func (r *CrossBankRecorder) MarkFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.updateStatus(h, model.IBSagaStatusFailed, errMsg)
}

// MarkCompensated flips the compensation row from compensating to
// compensated, the terminal "rolled back cleanly" state.
func (r *CrossBankRecorder) MarkCompensated(ctx context.Context, h sharedsaga.StepHandle) error {
	return r.updateStatus(h, model.IBSagaStatusCompensated, "")
}

// MarkCompensationFailed leaves the compensation row in compensating
// status with the failure reason recorded so the recovery cron can pick
// it up. The status doesn't transition out of compensating until either
// MarkCompensated is called or the cron resolves it.
func (r *CrossBankRecorder) MarkCompensationFailed(ctx context.Context, h sharedsaga.StepHandle, errMsg string) error {
	return r.updateStatus(h, model.IBSagaStatusCompensating, errMsg)
}

// IsCompleted reports whether the forward row for (sagaID, step, role) is
// in completed status. Returns (false, nil) if the row doesn't exist —
// the saga simply hasn't run this step yet, which is not an error.
//
// Saga-scoped (NOT step-name-only) by virtue of (tx_id, phase, role) being
// the row's unique key: two distinct sagas cannot collide on this lookup.
func (r *CrossBankRecorder) IsCompleted(ctx context.Context, sagaID string, step sharedsaga.StepKind) (bool, error) {
	row, err := r.repo.Get(sagaID, string(step), r.role)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return row.Status == model.IBSagaStatusCompleted, nil
}

// buildRow assembles a fresh InterBankSagaLog row with the recorder's
// per-saga immutable fields and the per-step variable fields (status +
// payload). Used by both RecordForward and RecordCompensation.
func (r *CrossBankRecorder) buildRow(sagaID, phase, status string, st *sharedsaga.State, step sharedsaga.StepKind) *model.InterBankSagaLog {
	row := &model.InterBankSagaLog{
		TxID:           sagaID,
		Phase:          phase,
		Role:           r.role,
		RemoteBankCode: r.remoteBankCode,
		SagaKind:       r.sagaKind,
		OfferID:        r.offerID,
		ContractID:     r.contractID,
		Status:         status,
	}
	if payload := extractPayload(st, step); len(payload) > 0 {
		row.PayloadJSON = datatypes.JSON(payload)
	}
	return row
}

// issueHandle mints a synthetic StepHandle.ID and remembers which row it
// refers to so subsequent status updates can find the row.
func (r *CrossBankRecorder) issueHandle(txID, phase string) sharedsaga.StepHandle {
	id := atomic.AddUint64(&r.nextHandle, 1)
	r.mu.Lock()
	r.handles[id] = rowKey{txID: txID, phase: phase, role: r.role}
	r.mu.Unlock()
	return sharedsaga.StepHandle{ID: id}
}

// updateStatus loads the row referenced by h via the stashed (txID, phase,
// role), updates status + error reason, and saves it. The model's
// BeforeUpdate hook supplies the optimistic-lock guard so concurrent
// recorder writes against the same row will surface as RowsAffected==0.
func (r *CrossBankRecorder) updateStatus(h sharedsaga.StepHandle, status, reason string) error {
	r.mu.Lock()
	key, ok := r.handles[h.ID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("crossbank recorder: unknown step handle %d", h.ID)
	}
	row, err := r.repo.Get(key.txID, key.phase, key.role)
	if err != nil {
		return err
	}
	row.Status = status
	row.ErrorReason = reason
	return r.repo.Save(row)
}

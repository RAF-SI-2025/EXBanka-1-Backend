package saga

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/shared"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// newCrossBankTestDB opens a fresh in-memory SQLite DB and auto-migrates the
// InterBankSagaLog table required by CrossBankRecorder tests.
func newCrossBankTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.InterBankSagaLog{}); err != nil {
		t.Fatalf("migrate inter_bank_saga_logs: %v", err)
	}
	return db
}

// payloadState builds a sharedsaga.State with the per-step payload key the
// CrossBankRecorder reads. Mirrors how a real cross-bank saga step would
// stash its serialized request body.
func payloadState(step sharedsaga.StepKind, payload []byte) *sharedsaga.State {
	st := sharedsaga.NewState()
	st.Set(crossbankPayloadKey(step), payload)
	return st
}

func u64Ptr(v uint64) *uint64 { return &v }

func TestCrossBankRecorder_RecordsForwardWithFullContext(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", u64Ptr(42), nil)

	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("payload"))
	h, err := rec.RecordForward(context.Background(), "tx-1", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}
	if h.ID == 0 {
		t.Fatalf("expected non-zero handle ID, got %+v", h)
	}

	row, err := repo.Get("tx-1", string(sharedsaga.StepReserveBuyerFunds), model.SagaRoleInitiator)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != model.IBSagaStatusPending {
		t.Errorf("status: got %q, want %q", row.Status, model.IBSagaStatusPending)
	}
	if row.RemoteBankCode != "123" {
		t.Errorf("remote bank code: got %q, want %q", row.RemoteBankCode, "123")
	}
	if row.SagaKind != model.SagaKindAccept {
		t.Errorf("saga kind: got %q, want %q", row.SagaKind, model.SagaKindAccept)
	}
	if row.OfferID == nil || *row.OfferID != 42 {
		t.Errorf("offer id: got %v, want 42", row.OfferID)
	}
	if row.ContractID != nil {
		t.Errorf("contract id: got %v, want nil", row.ContractID)
	}
	if string(row.PayloadJSON) != "payload" {
		t.Errorf("payload: got %q, want %q", string(row.PayloadJSON), "payload")
	}
	wantKey := model.IdempotencyKeyFor(model.SagaKindAccept, "tx-1",
		string(sharedsaga.StepReserveBuyerFunds), model.SagaRoleInitiator)
	if row.IdempotencyKey != wantKey {
		t.Errorf("idempotency key: got %q, want %q", row.IdempotencyKey, wantKey)
	}
}

func TestCrossBankRecorder_MarkCompletedFlipsStatus(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", nil, nil)

	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("payload"))
	h, err := rec.RecordForward(context.Background(), "tx-1", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}
	if err := rec.MarkCompleted(context.Background(), h); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	row, err := repo.Get("tx-1", string(sharedsaga.StepReserveBuyerFunds), model.SagaRoleInitiator)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != model.IBSagaStatusCompleted {
		t.Errorf("status: got %q, want %q", row.Status, model.IBSagaStatusCompleted)
	}
}

func TestCrossBankRecorder_MarkFailedRecordsReason(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleResponder, model.SagaKindExercise,
		"456", nil, u64Ptr(7))

	st := payloadState(sharedsaga.StepDebitStrike, []byte("p"))
	h, err := rec.RecordForward(context.Background(), "tx-2", sharedsaga.StepDebitStrike, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}
	if err := rec.MarkFailed(context.Background(), h, "peer rejected"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	row, err := repo.Get("tx-2", string(sharedsaga.StepDebitStrike), model.SagaRoleResponder)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != model.IBSagaStatusFailed {
		t.Errorf("status: got %q, want %q", row.Status, model.IBSagaStatusFailed)
	}
	if row.ErrorReason != "peer rejected" {
		t.Errorf("error reason: got %q, want %q", row.ErrorReason, "peer rejected")
	}
}

func TestCrossBankRecorder_CompensationCreatesSeparateRow(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", u64Ptr(42), nil)

	ctx := context.Background()
	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("payload"))
	fwd, err := rec.RecordForward(ctx, "tx-3", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}
	if err := rec.MarkCompleted(ctx, fwd); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	comp, err := rec.RecordCompensation(ctx, "tx-3", sharedsaga.StepReserveBuyerFunds, -1, fwd, st)
	if err != nil {
		t.Fatalf("RecordCompensation: %v", err)
	}
	if err := rec.MarkCompensated(ctx, comp); err != nil {
		t.Fatalf("MarkCompensated: %v", err)
	}

	rows, err := repo.ListByTxID("tx-3")
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}

	var fwdRow, compRow *model.InterBankSagaLog
	for i := range rows {
		switch rows[i].Phase {
		case string(sharedsaga.StepReserveBuyerFunds):
			fwdRow = &rows[i]
		case "compensate_" + string(sharedsaga.StepReserveBuyerFunds):
			compRow = &rows[i]
		}
	}
	if fwdRow == nil {
		t.Fatal("forward row missing")
	}
	if compRow == nil {
		t.Fatal("compensation row missing")
	}
	if fwdRow.Status != model.IBSagaStatusCompleted {
		t.Errorf("forward status: got %q, want %q", fwdRow.Status, model.IBSagaStatusCompleted)
	}
	if compRow.Status != model.IBSagaStatusCompensated {
		t.Errorf("compensation status: got %q, want %q", compRow.Status, model.IBSagaStatusCompensated)
	}
	if fwdRow.IdempotencyKey == compRow.IdempotencyKey {
		t.Errorf("idempotency keys must differ; both = %q", fwdRow.IdempotencyKey)
	}
}

func TestCrossBankRecorder_IsCompletedAfterMark(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", nil, nil)

	ctx := context.Background()

	got, err := rec.IsCompleted(ctx, "tx-4", sharedsaga.StepReserveBuyerFunds)
	if err != nil {
		t.Fatalf("IsCompleted (absent): %v", err)
	}
	if got {
		t.Error("expected false for unrecorded step")
	}

	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("p"))
	h, err := rec.RecordForward(ctx, "tx-4", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}

	// Pending row should not count as completed.
	got, err = rec.IsCompleted(ctx, "tx-4", sharedsaga.StepReserveBuyerFunds)
	if err != nil {
		t.Fatalf("IsCompleted (pending): %v", err)
	}
	if got {
		t.Error("expected false for pending step")
	}

	if err := rec.MarkCompleted(ctx, h); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	got, err = rec.IsCompleted(ctx, "tx-4", sharedsaga.StepReserveBuyerFunds)
	if err != nil {
		t.Fatalf("IsCompleted (completed): %v", err)
	}
	if !got {
		t.Error("expected true after MarkCompleted")
	}
}

func TestCrossBankRecorder_RolePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid role")
		}
	}()
	NewCrossBankRecorder(nil, "spy", model.SagaKindAccept, "123", nil, nil)
}

func TestCrossBankRecorder_SagaKindPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid saga kind")
		}
	}()
	NewCrossBankRecorder(nil, model.SagaRoleInitiator, "fraud", "123", nil, nil)
}

func TestCrossBankRecorder_MarkCompensationFailedKeepsCompensating(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", nil, nil)

	ctx := context.Background()
	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("p"))
	fwd, err := rec.RecordForward(ctx, "tx-5", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}
	if err := rec.MarkCompleted(ctx, fwd); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	comp, err := rec.RecordCompensation(ctx, "tx-5", sharedsaga.StepReserveBuyerFunds, -1, fwd, st)
	if err != nil {
		t.Fatalf("RecordCompensation: %v", err)
	}
	if err := rec.MarkCompensationFailed(ctx, comp, "peer 500"); err != nil {
		t.Fatalf("MarkCompensationFailed: %v", err)
	}

	row, err := repo.Get("tx-5", "compensate_"+string(sharedsaga.StepReserveBuyerFunds), model.SagaRoleInitiator)
	if err != nil {
		t.Fatalf("Get compensation row: %v", err)
	}
	if row.Status != model.IBSagaStatusCompensating {
		t.Errorf("status: got %q, want %q", row.Status, model.IBSagaStatusCompensating)
	}
	if row.ErrorReason != "peer 500" {
		t.Errorf("error reason: got %q, want %q", row.ErrorReason, "peer 500")
	}
}

// Compile-time guard: CrossBankRecorder implements sharedsaga.Recorder.
var _ sharedsaga.Recorder = (*CrossBankRecorder)(nil)

// TestCrossBankRecorder_OptimisticLockConflict exercises the optimistic-lock
// path through the recorder's MarkCompleted -> repo.Save chain.
//
// Setup:
//  1. Recorder writes a forward row (version=0).
//  2. We load a separate in-memory copy of the row (version=0).
//  3. The recorder's MarkCompleted runs end-to-end, bumping the row in-DB
//     to version=1.
//  4. We mutate the stale copy and call repo.Save directly. The
//     BeforeUpdate hook attaches WHERE version=0, which matches no row
//     because the DB version is now 1. RowsAffected==0 must surface as
//     shared.ErrOptimisticLock — the bank-grade safety contract called out
//     in CLAUDE.md's Optimistic Locking section.
//
// The "stale copy" step models the realistic concurrent-modification
// scenario: two recorders each loaded the row at version=0, one wrote
// first (winning the race), the other now holds a stale snapshot. The
// recorder's updateStatus atomically does Get-then-Save under one ctx, so
// the only way to inject a concurrent winner is via a separate snapshot
// path — which is what every cron-driven retry would do anyway when it
// re-loads a row mid-flight.
func TestCrossBankRecorder_OptimisticLockConflict(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", nil, nil)

	ctx := context.Background()
	st := payloadState(sharedsaga.StepReserveBuyerFunds, []byte("payload"))
	h, err := rec.RecordForward(ctx, "tx-lock", sharedsaga.StepReserveBuyerFunds, 1, st)
	if err != nil {
		t.Fatalf("RecordForward: %v", err)
	}

	// Snapshot the row at version=0 — this is the "loser" of the race.
	stale, err := repo.Get("tx-lock", string(sharedsaga.StepReserveBuyerFunds), model.SagaRoleInitiator)
	if err != nil {
		t.Fatalf("get stale snapshot: %v", err)
	}
	if stale.Version != 0 {
		t.Fatalf("snapshot version: got %d, want 0", stale.Version)
	}

	// "Concurrent" winner: recorder's MarkCompleted bumps in-DB version to 1.
	if err := rec.MarkCompleted(ctx, h); err != nil {
		t.Fatalf("MarkCompleted (winner): %v", err)
	}

	// Now save the stale snapshot — must surface optimistic-lock.
	stale.Status = model.IBSagaStatusFailed
	stale.ErrorReason = "stale write"
	err = repo.Save(stale)
	if err == nil {
		t.Fatal("expected optimistic-lock error, got nil")
	}
	if !errors.Is(err, shared.ErrOptimisticLock) {
		t.Fatalf("expected shared.ErrOptimisticLock, got %v", err)
	}
}

// Sanity check that gorm's NotFound error path returns (false, nil) from
// IsCompleted (no underlying error leaks).
func TestCrossBankRecorder_IsCompletedNotFoundIsNotError(t *testing.T) {
	db := newCrossBankTestDB(t)
	repo := repository.NewInterBankSagaLogRepository(db)
	rec := NewCrossBankRecorder(repo, model.SagaRoleInitiator, model.SagaKindAccept,
		"123", nil, nil)

	ok, err := rec.IsCompleted(context.Background(), "missing", sharedsaga.StepDebitBuyer)
	if err != nil {
		t.Fatalf("expected nil error for missing row, got %v (is errors.Is gorm.NotFound? %v)",
			err, errors.Is(err, gorm.ErrRecordNotFound))
	}
	if ok {
		t.Error("expected false for missing row")
	}
}

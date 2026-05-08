package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
)

// fakeSagaRepo implements SagaRepoIF + recoveryRepoIF in memory so the
// Recorder can be unit-tested without a real DB.
type fakeSagaRepo struct {
	rows           map[uint64]*model.SagaLog
	nextID         uint64
	recordErr      error
	updateErr      error
	completedQuery map[string]bool // key: sagaID|stepName
	completedErr   error
	stuck          []model.SagaLog
	stuckErr       error
	incrErr        error
}

func newFakeSagaRepo() *fakeSagaRepo {
	return &fakeSagaRepo{
		rows:           map[uint64]*model.SagaLog{},
		completedQuery: map[string]bool{},
	}
}

func (f *fakeSagaRepo) RecordStep(log *model.SagaLog) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.nextID++
	log.ID = f.nextID
	if log.Version == 0 {
		log.Version = 1
	}
	f.rows[log.ID] = log
	return nil
}

func (f *fakeSagaRepo) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	row, ok := f.rows[id]
	if !ok {
		return errors.New("not found")
	}
	row.Status = newStatus
	row.ErrorMessage = errMsg
	row.Version++
	return nil
}

func (f *fakeSagaRepo) IsForwardCompleted(sagaID, stepName string) (bool, error) {
	if f.completedErr != nil {
		return false, f.completedErr
	}
	return f.completedQuery[sagaID+"|"+stepName], nil
}

func (f *fakeSagaRepo) ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error) {
	if f.stuckErr != nil {
		return nil, f.stuckErr
	}
	return f.stuck, nil
}

func (f *fakeSagaRepo) IncrementRetryCount(id uint64) error {
	if f.incrErr != nil {
		return f.incrErr
	}
	row, ok := f.rows[id]
	if !ok {
		return errors.New("not found")
	}
	row.RetryCount++
	return nil
}

// minimalRepo only satisfies SagaRepoIF (not recoveryRepoIF). Used to
// exercise the early-return branches in ListStuck / IncrementRetry.
type minimalRepo struct{}

func (minimalRepo) RecordStep(_ *model.SagaLog) error                        { return nil }
func (minimalRepo) UpdateStatus(_ uint64, _ int64, _ string, _ string) error { return nil }
func (minimalRepo) IsForwardCompleted(_, _ string) (bool, error)             { return false, nil }

// ----------------------------------------------------------------------------
// RecordForward + MarkCompleted
// ----------------------------------------------------------------------------

func TestRecorder_RecordForward_HappyPath(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)

	st := sharedsaga.NewState()
	st.Set(keyOrderID, uint64(101))
	otID := uint64(202)
	st.Set(keyOrderTransactionID, otID)
	st.Set(stepAmountKey("step_a"), decimal.NewFromInt(50))
	st.Set(stepCurrencyKey("step_a"), "USD")
	st.Set(stepPayloadKey("step_a"), map[string]any{"foo": "bar"})

	h, err := r.RecordForward(context.Background(), "saga-1", sharedsaga.StepKind("step_a"), 1, st)
	if err != nil {
		t.Fatalf("record forward: %v", err)
	}
	if h.ID == 0 {
		t.Errorf("expected non-zero handle id")
	}
	row := repo.rows[h.ID]
	if row.Status != string(sharedsaga.SagaStatusPending) {
		t.Errorf("status=%s want pending", row.Status)
	}
	if row.OrderID != 101 {
		t.Errorf("order id=%d", row.OrderID)
	}
	if row.OrderTransactionID == nil || *row.OrderTransactionID != 202 {
		t.Errorf("order tx id wrong")
	}
	if row.Amount == nil || !row.Amount.Equal(decimal.NewFromInt(50)) {
		t.Errorf("amount=%v", row.Amount)
	}
	if row.CurrencyCode != "USD" {
		t.Errorf("currency=%s", row.CurrencyCode)
	}
	if len(row.Payload) == 0 {
		t.Error("expected payload bytes")
	}
}

func TestRecorder_RecordForward_RecordError(t *testing.T) {
	repo := newFakeSagaRepo()
	repo.recordErr = errors.New("db down")
	r := NewRecorder(repo)
	_, err := r.RecordForward(context.Background(), "saga-1", sharedsaga.StepKind("x"), 1, sharedsaga.NewState())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecorder_RecordForward_OrderTransactionIDPointer(t *testing.T) {
	// The recorder accepts *uint64 for order_transaction_id too.
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	st := sharedsaga.NewState()
	otID := uint64(99)
	st.Set(keyOrderTransactionID, &otID)
	h, err := r.RecordForward(context.Background(), "saga", sharedsaga.StepKind("s"), 1, st)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	row := repo.rows[h.ID]
	if row.OrderTransactionID == nil || *row.OrderTransactionID != 99 {
		t.Errorf("order tx id wrong: %v", row.OrderTransactionID)
	}
}

func TestRecorder_RecordForward_ZeroAmount_NotStored(t *testing.T) {
	// IsZero amounts should not be stored on the row.
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	st := sharedsaga.NewState()
	st.Set(stepAmountKey("step_a"), decimal.Zero)
	h, _ := r.RecordForward(context.Background(), "saga", sharedsaga.StepKind("step_a"), 1, st)
	row := repo.rows[h.ID]
	if row.Amount != nil {
		t.Errorf("expected nil amount for zero, got %v", row.Amount)
	}
}

func TestRecorder_MarkCompleted(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordForward(context.Background(), "saga", sharedsaga.StepKind("s"), 1, sharedsaga.NewState())
	if err := r.MarkCompleted(context.Background(), h); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if repo.rows[h.ID].Status != string(sharedsaga.SagaStatusCompleted) {
		t.Errorf("status not completed")
	}
}

func TestRecorder_MarkFailed(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordForward(context.Background(), "saga", sharedsaga.StepKind("s"), 1, sharedsaga.NewState())
	if err := r.MarkFailed(context.Background(), h, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	row := repo.rows[h.ID]
	if row.Status != string(sharedsaga.SagaStatusFailed) {
		t.Errorf("status=%s", row.Status)
	}
	if row.ErrorMessage != "boom" {
		t.Errorf("err msg=%q", row.ErrorMessage)
	}
}

// ----------------------------------------------------------------------------
// RecordCompensation + Mark{Compensated,CompensationFailed}
// ----------------------------------------------------------------------------

func TestRecorder_RecordCompensation(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	fwd, _ := r.RecordForward(context.Background(), "saga", sharedsaga.StepKind("s"), 1, sharedsaga.NewState())
	comp, err := r.RecordCompensation(context.Background(), "saga", sharedsaga.StepKind("s_comp"), 2, fwd, sharedsaga.NewState())
	if err != nil {
		t.Fatalf("comp: %v", err)
	}
	row := repo.rows[comp.ID]
	if !row.IsCompensation {
		t.Errorf("expected compensation flag set")
	}
	if row.CompensationOf == nil || *row.CompensationOf != fwd.ID {
		t.Errorf("compensation_of wrong: %v", row.CompensationOf)
	}
	if row.Status != string(sharedsaga.SagaStatusCompensating) {
		t.Errorf("status=%s want compensating", row.Status)
	}
}

func TestRecorder_RecordCompensation_NoForward(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	comp, err := r.RecordCompensation(context.Background(), "saga", sharedsaga.StepKind("s_comp"), 2, sharedsaga.StepHandle{}, sharedsaga.NewState())
	if err != nil {
		t.Fatalf("comp: %v", err)
	}
	row := repo.rows[comp.ID]
	if row.CompensationOf != nil {
		t.Errorf("expected nil CompensationOf when forward.ID=0")
	}
}

func TestRecorder_RecordCompensation_DBErr(t *testing.T) {
	repo := newFakeSagaRepo()
	repo.recordErr = errors.New("db")
	r := NewRecorder(repo)
	_, err := r.RecordCompensation(context.Background(), "saga", sharedsaga.StepKind("s_comp"), 2, sharedsaga.StepHandle{}, sharedsaga.NewState())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecorder_MarkCompensated(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordCompensation(context.Background(), "s", sharedsaga.StepKind("c"), 1, sharedsaga.StepHandle{}, sharedsaga.NewState())
	if err := r.MarkCompensated(context.Background(), h); err != nil {
		t.Fatalf("err: %v", err)
	}
	if repo.rows[h.ID].Status != string(sharedsaga.SagaStatusCompensated) {
		t.Errorf("status not compensated")
	}
}

func TestRecorder_MarkCompensationFailed(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordCompensation(context.Background(), "s", sharedsaga.StepKind("c"), 1, sharedsaga.StepHandle{}, sharedsaga.NewState())
	if err := r.MarkCompensationFailed(context.Background(), h, "still broken"); err != nil {
		t.Fatalf("err: %v", err)
	}
	row := repo.rows[h.ID]
	if row.Status != string(sharedsaga.SagaStatusCompensating) {
		t.Errorf("expected compensating status")
	}
	if row.ErrorMessage != "still broken" {
		t.Errorf("err msg=%q", row.ErrorMessage)
	}
}

func TestRecorder_MarkDeadLetter(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordForward(context.Background(), "s", sharedsaga.StepKind("a"), 1, sharedsaga.NewState())
	if err := r.MarkDeadLetter(context.Background(), h, "give up"); err != nil {
		t.Fatalf("err: %v", err)
	}
	row := repo.rows[h.ID]
	if row.Status != string(sharedsaga.SagaStatusDeadLetter) {
		t.Errorf("status=%s want dead_letter", row.Status)
	}
}

// ----------------------------------------------------------------------------
// IsCompleted
// ----------------------------------------------------------------------------

func TestRecorder_IsCompleted_True(t *testing.T) {
	repo := newFakeSagaRepo()
	repo.completedQuery["saga|s"] = true
	r := NewRecorder(repo)
	got, err := r.IsCompleted(context.Background(), "saga", sharedsaga.StepKind("s"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Errorf("expected completed=true")
	}
}

func TestRecorder_IsCompleted_Error(t *testing.T) {
	repo := newFakeSagaRepo()
	repo.completedErr = errors.New("db")
	r := NewRecorder(repo)
	_, err := r.IsCompleted(context.Background(), "saga", sharedsaga.StepKind("s"))
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----------------------------------------------------------------------------
// ListStuck + IncrementRetry
// ----------------------------------------------------------------------------

func TestRecorder_ListStuck_HappyPath(t *testing.T) {
	repo := newFakeSagaRepo()
	otID := uint64(50)
	amt := decimal.NewFromInt(7)
	repo.stuck = []model.SagaLog{
		{ID: 10, SagaID: "saga-x", StepNumber: 1, StepName: "abc", Status: string(sharedsaga.SagaStatusPending), Version: 1, OrderID: 11, OrderTransactionID: &otID, Amount: &amt, CurrencyCode: "USD", Payload: datatypes.JSON([]byte(`{"k":"v"}`)), RetryCount: 1, UpdatedAt: time.Now()},
		{ID: 11, SagaID: "saga-y", StepNumber: 2, StepName: "compensate_xyz_compensation", Status: string(sharedsaga.SagaStatusCompensating), Version: 2, IsCompensation: true},
	}
	r := NewRecorder(repo)
	stuck, err := r.ListStuck(context.Background(), 1*time.Minute)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(stuck) != 2 {
		t.Fatalf("got %d", len(stuck))
	}
	first := stuck[0]
	if first.SagaID != "saga-x" || first.StepName != "abc" {
		t.Errorf("first: %+v", first)
	}
	if first.Payload["order_id"] != uint64(11) {
		t.Errorf("missing order_id, got %v", first.Payload["order_id"])
	}
	if first.Payload["order_transaction_id"] != uint64(50) {
		t.Errorf("missing order_transaction_id, got %v", first.Payload["order_transaction_id"])
	}
	if first.Payload["currency"] != "USD" {
		t.Errorf("missing currency")
	}
	if _, ok := first.Payload["payload"]; !ok {
		t.Errorf("payload field missing")
	}
	// Compensation step should have its _compensation suffix stripped.
	second := stuck[1]
	if second.StepName != "compensate_xyz" {
		t.Errorf("expected compensation suffix stripped, got %q", second.StepName)
	}
}

func TestRecorder_ListStuck_Error(t *testing.T) {
	repo := newFakeSagaRepo()
	repo.stuckErr = errors.New("db")
	r := NewRecorder(repo)
	_, err := r.ListStuck(context.Background(), time.Minute)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecorder_ListStuck_NotRecoveryRepo(t *testing.T) {
	r := NewRecorder(minimalRepo{})
	out, err := r.ListStuck(context.Background(), time.Minute)
	if err != nil {
		t.Errorf("err: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil for non-recovery repo")
	}
}

func TestRecorder_IncrementRetry(t *testing.T) {
	repo := newFakeSagaRepo()
	r := NewRecorder(repo)
	h, _ := r.RecordForward(context.Background(), "s", sharedsaga.StepKind("a"), 1, sharedsaga.NewState())
	if err := r.IncrementRetry(context.Background(), h); err != nil {
		t.Fatalf("err: %v", err)
	}
	if repo.rows[h.ID].RetryCount != 1 {
		t.Errorf("retry not incremented")
	}
}

func TestRecorder_IncrementRetry_NotRecoveryRepo(t *testing.T) {
	r := NewRecorder(minimalRepo{})
	if err := r.IncrementRetry(context.Background(), sharedsaga.StepHandle{}); err != nil {
		t.Errorf("expected nil error on non-recovery repo, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// stripCompensationSuffix
// ----------------------------------------------------------------------------

func TestStripCompensationSuffix_WithSuffix(t *testing.T) {
	if got := stripCompensationSuffix("debit_compensation"); got != "debit" {
		t.Errorf("got %q want debit", got)
	}
}

func TestStripCompensationSuffix_NoSuffix(t *testing.T) {
	if got := stripCompensationSuffix("regular_step"); got != "regular_step" {
		t.Errorf("got %q want regular_step", got)
	}
}

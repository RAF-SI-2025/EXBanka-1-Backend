package saga

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

func newRecorderTestRepo(t *testing.T) *repository.SagaLogRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil {
		t.Fatal(err)
	}
	return repository.NewSagaLogRepository(db)
}

// stateWith returns a saga State pre-populated with the per-step account/amount
// metadata the recorder reads when building a row.
func stateWith(step sharedsaga.StepKind, account string, amount decimal.Decimal) *sharedsaga.State {
	st := sharedsaga.NewState()
	st.Set(stepAccountKey(step), account)
	st.Set(stepAmountKey(step), amount)
	return st
}

func TestRecorder_KeyHelpers(t *testing.T) {
	if got := stepAccountKey(sharedsaga.StepDebitBank); got != "step:debit_bank:account_number" {
		t.Fatalf("stepAccountKey = %q", got)
	}
	if got := stepAmountKey(sharedsaga.StepCreditBorrower); got != "step:credit_borrower:amount" {
		t.Fatalf("stepAmountKey = %q", got)
	}
}

func TestRecorder_RecordForward_PersistsPendingRowWithMetadata(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 77)
	ctx := context.Background()

	st := stateWith(sharedsaga.StepDebitBank, "bank:RSD", decimal.NewFromInt(500))
	h, err := r.RecordForward(ctx, "saga-fwd", sharedsaga.StepDebitBank, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID == 0 {
		t.Fatal("expected populated step handle")
	}

	rows, err := repo.GetBySagaID("saga-fwd")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Status != string(sharedsaga.SagaStatusPending) {
		t.Fatalf("expected pending status, got %q", row.Status)
	}
	if row.IsCompensation {
		t.Fatal("forward row must not be a compensation")
	}
	if row.LoanID != 77 {
		t.Fatalf("expected loan id 77, got %d", row.LoanID)
	}
	if row.AccountNumber != "bank:RSD" {
		t.Fatalf("expected account bank:RSD, got %q", row.AccountNumber)
	}
	if !row.Amount.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("expected amount 500, got %s", row.Amount)
	}
}

func TestRecorder_MarkCompleted_ThenIsCompleted(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 1)
	ctx := context.Background()

	h, err := r.RecordForward(ctx, "saga-done", sharedsaga.StepDebitBank, 1, stateWith(sharedsaga.StepDebitBank, "a", decimal.NewFromInt(1)))
	if err != nil {
		t.Fatal(err)
	}

	// Not completed yet.
	done, err := r.IsCompleted(ctx, "saga-done", sharedsaga.StepDebitBank)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("step should not be completed before MarkCompleted")
	}

	if err := r.MarkCompleted(ctx, h); err != nil {
		t.Fatal(err)
	}

	done, err = r.IsCompleted(ctx, "saga-done", sharedsaga.StepDebitBank)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("step should be completed after MarkCompleted")
	}
}

func TestRecorder_MarkFailed_SetsFailedStatusAndError(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 1)
	ctx := context.Background()

	h, err := r.RecordForward(ctx, "saga-fail", sharedsaga.StepCreditBorrower, 2, stateWith(sharedsaga.StepCreditBorrower, "acc", decimal.NewFromInt(10)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkFailed(ctx, h, "downstream exploded"); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.GetBySagaID("saga-fail")
	if rows[0].Status != "failed" {
		t.Fatalf("expected failed status, got %q", rows[0].Status)
	}
	if rows[0].ErrorMessage != "downstream exploded" {
		t.Fatalf("expected error message persisted, got %q", rows[0].ErrorMessage)
	}
}

func TestRecorder_RecordCompensation_NegatesAmountAndLinksForward(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 5)
	ctx := context.Background()

	// First a completed forward step we will compensate.
	fwd, err := r.RecordForward(ctx, "saga-comp", sharedsaga.StepDebitBank, 1, stateWith(sharedsaga.StepDebitBank, "bank:RSD", decimal.NewFromInt(900)))
	if err != nil {
		t.Fatal(err)
	}

	comp, err := r.RecordCompensation(ctx, "saga-comp", sharedsaga.StepDebitBank, 1, fwd, stateWith(sharedsaga.StepDebitBank, "bank:RSD", decimal.NewFromInt(900)))
	if err != nil {
		t.Fatal(err)
	}

	var row model.SagaLog
	rows, err := repo.GetBySagaID("saga-comp")
	if err != nil {
		t.Fatal(err)
	}
	for _, rr := range rows {
		if rr.ID == comp.ID {
			row = rr
		}
	}
	if row.ID == 0 {
		t.Fatal("compensation row not found")
	}
	if !row.IsCompensation {
		t.Fatal("compensation row must be flagged IsCompensation")
	}
	if row.Status != string(sharedsaga.SagaStatusCompensating) {
		t.Fatalf("expected compensating status, got %q", row.Status)
	}
	if row.CompensationOf == nil || *row.CompensationOf != fwd.ID {
		t.Fatalf("expected CompensationOf=%d, got %v", fwd.ID, row.CompensationOf)
	}
	// Amount must be negated relative to the forward amount.
	if !row.Amount.Equal(decimal.NewFromInt(-900)) {
		t.Fatalf("expected negated amount -900, got %s", row.Amount)
	}
}

func TestRecorder_MarkCompensated_CompletesRow(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 5)
	ctx := context.Background()

	comp, err := r.RecordCompensation(ctx, "saga-cdone", sharedsaga.StepDebitBank, 1, sharedsaga.StepHandle{}, stateWith(sharedsaga.StepDebitBank, "x", decimal.NewFromInt(1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkCompensated(ctx, comp); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.GetBySagaID("saga-cdone")
	if rows[0].Status != "completed" {
		t.Fatalf("expected completed status after MarkCompensated, got %q", rows[0].Status)
	}
	// A compensation with no forward handle must not link a forward row.
	if rows[0].CompensationOf != nil {
		t.Fatalf("expected nil CompensationOf for zero forward handle, got %v", rows[0].CompensationOf)
	}
}

func TestRecorder_MarkCompensationFailed_KeepsCompensatingStatus(t *testing.T) {
	repo := newRecorderTestRepo(t)
	r := NewRecorder(repo, 5)
	ctx := context.Background()

	comp, err := r.RecordCompensation(ctx, "saga-cfail", sharedsaga.StepCreditBorrower, 2, sharedsaga.StepHandle{ID: 999}, stateWith(sharedsaga.StepCreditBorrower, "acc", decimal.NewFromInt(3)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkCompensationFailed(ctx, comp, "still failing"); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.GetBySagaID("saga-cfail")
	// Status stays compensating so the recovery loop keeps picking it up.
	if rows[0].Status != string(sharedsaga.SagaStatusCompensating) {
		t.Fatalf("expected compensating status retained, got %q", rows[0].Status)
	}
	if rows[0].ErrorMessage != "still failing" {
		t.Fatalf("expected error message stored, got %q", rows[0].ErrorMessage)
	}
}

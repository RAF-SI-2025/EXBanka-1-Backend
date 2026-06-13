package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
)

func newTestSagaDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSagaLogRepo_CreateAndComplete(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	row := &model.SagaLog{
		SagaID:        "saga-1",
		LoanID:        7,
		StepNumber:    1,
		StepName:      "debit_bank",
		AccountNumber: "111000000",
		Amount:        decimal.NewFromInt(100),
	}
	if err := repo.RecordStep(row); err != nil {
		t.Fatal(err)
	}
	if row.ID == 0 {
		t.Fatal("expected ID populated after RecordStep")
	}
	if row.Status != "pending" {
		t.Fatalf("expected status pending, got %q", row.Status)
	}

	if err := repo.CompleteStep(row.ID); err != nil {
		t.Fatal(err)
	}

	completed, err := repo.IsForwardCompleted("saga-1", "debit_bank")
	if err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected IsForwardCompleted to return true after CompleteStep")
	}
}

func TestSagaLogRepo_FindPendingCompensations(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	a := &model.SagaLog{
		SagaID:         "s1",
		LoanID:         1,
		StepNumber:     1,
		StepName:       "debit_bank",
		AccountNumber:  "111",
		Amount:         decimal.NewFromInt(1),
		Status:         "compensating",
		IsCompensation: true,
	}
	b := &model.SagaLog{
		SagaID:        "s2",
		LoanID:        2,
		StepNumber:    1,
		StepName:      "credit_borrower",
		AccountNumber: "222",
		Amount:        decimal.NewFromInt(2),
	}
	if err := repo.RecordStep(a); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordStep(b); err != nil {
		t.Fatal(err)
	}
	// b is pending, not compensating — only a should appear

	rows, err := repo.FindPendingCompensations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("expected only a in compensating state, got %+v", rows)
	}
}

func TestSagaLogRepo_MarkDeadLetter(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	row := &model.SagaLog{
		SagaID:        "saga-dl",
		LoanID:        99,
		StepNumber:    1,
		StepName:      "debit_bank",
		AccountNumber: "111",
		Amount:        decimal.NewFromInt(50),
		Status:        "compensating",
	}
	if err := repo.RecordStep(row); err != nil {
		t.Fatal(err)
	}
	if err := repo.IncrementRetryCount(row.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDeadLetter(row.ID, "too many retries"); err != nil {
		t.Fatal(err)
	}

	rows, err := repo.FindPendingCompensations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no compensating rows after dead-letter, got %d", len(rows))
	}
}

func TestSagaLogRepo_FailStep(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	row := &model.SagaLog{
		SagaID: "s-fail", LoanID: 1, StepNumber: 1, StepName: "debit_bank",
		AccountNumber: "111", Amount: decimal.NewFromInt(10),
	}
	if err := repo.RecordStep(row); err != nil {
		t.Fatal(err)
	}
	if err := repo.FailStep(row.ID, "boom"); err != nil {
		t.Fatal(err)
	}

	var got model.SagaLog
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Fatalf("expected failed status, got %q", got.Status)
	}
	if got.ErrorMessage != "boom" {
		t.Fatalf("expected error message stored, got %q", got.ErrorMessage)
	}
}

func TestSagaLogRepo_SetErrorMessage_LeavesStatusUnchanged(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	row := &model.SagaLog{
		SagaID: "s-err", LoanID: 1, StepNumber: 1, StepName: "credit_borrower",
		AccountNumber: "222", Amount: decimal.NewFromInt(5), Status: "compensating",
	}
	if err := repo.RecordStep(row); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetErrorMessage(row.ID, "still failing"); err != nil {
		t.Fatal(err)
	}

	var got model.SagaLog
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	// Status must NOT change so the recovery loop keeps surfacing the row.
	if got.Status != "compensating" {
		t.Fatalf("expected status to remain compensating, got %q", got.Status)
	}
	if got.ErrorMessage != "still failing" {
		t.Fatalf("expected error message updated, got %q", got.ErrorMessage)
	}
}

func TestSagaLogRepo_ListStuckOlderThan(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now()

	// Stuck (pending, old) — should be returned.
	stuckPending := &model.SagaLog{
		SagaID: "s1", LoanID: 1, StepNumber: 1, StepName: "debit_bank",
		AccountNumber: "1", Amount: decimal.NewFromInt(1), Status: "pending", CreatedAt: old,
	}
	// Stuck (compensating, old) — should be returned.
	stuckComp := &model.SagaLog{
		SagaID: "s2", LoanID: 2, StepNumber: 1, StepName: "credit_borrower",
		AccountNumber: "2", Amount: decimal.NewFromInt(2), Status: "compensating", CreatedAt: old,
	}
	// Recent pending — newer than cutoff, excluded.
	recentPending := &model.SagaLog{
		SagaID: "s3", LoanID: 3, StepNumber: 1, StepName: "debit_bank",
		AccountNumber: "3", Amount: decimal.NewFromInt(3), Status: "pending", CreatedAt: recent,
	}
	// Old but completed — wrong status, excluded.
	oldCompleted := &model.SagaLog{
		SagaID: "s4", LoanID: 4, StepNumber: 1, StepName: "debit_bank",
		AccountNumber: "4", Amount: decimal.NewFromInt(4), Status: "completed", CreatedAt: old,
	}
	for _, r := range []*model.SagaLog{stuckPending, stuckComp, recentPending, oldCompleted} {
		if err := repo.RecordStep(r); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	rows, err := repo.ListStuckOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 stuck rows, got %d: %+v", len(rows), rows)
	}
	gotStatuses := map[string]bool{}
	for _, r := range rows {
		gotStatuses[r.Status] = true
	}
	if !gotStatuses["pending"] || !gotStatuses["compensating"] {
		t.Fatalf("expected both pending and compensating rows, got %+v", gotStatuses)
	}
}

func TestSagaLogRepo_GetBySagaID(t *testing.T) {
	db := newTestSagaDB(t)
	repo := NewSagaLogRepository(db)

	for i := 1; i <= 3; i++ {
		row := &model.SagaLog{
			SagaID:        "ordered-saga",
			LoanID:        10,
			StepNumber:    i,
			StepName:      "step",
			AccountNumber: "111",
			Amount:        decimal.NewFromInt(int64(i)),
		}
		if err := repo.RecordStep(row); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := repo.GetBySagaID("ordered-saga")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.StepNumber != i+1 {
			t.Fatalf("expected step_number %d, got %d", i+1, r.StepNumber)
		}
	}
}

package repository_test

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

func newSagaLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestSagaLogRepository_ListSagaLogs_FiltersAndPaginates(t *testing.T) {
	db := newSagaLogTestDB(t)
	r := repository.NewSagaLogRepository(db)

	// Seed: 3 transfer steps, 2 payment steps, mixed statuses.
	seed := []model.SagaLog{
		{SagaID: "s1", TransactionID: 1, TransactionType: "transfer", StepNumber: 1, StepName: "debit", Status: "completed", Amount: decimal.NewFromInt(-100), CreatedAt: time.Now()},
		{SagaID: "s1", TransactionID: 1, TransactionType: "transfer", StepNumber: 2, StepName: "credit", Status: "completed", Amount: decimal.NewFromInt(100), CreatedAt: time.Now()},
		{SagaID: "s2", TransactionID: 2, TransactionType: "transfer", StepNumber: 1, StepName: "debit", Status: "compensating", Amount: decimal.NewFromInt(-50), CreatedAt: time.Now()},
		{SagaID: "p1", TransactionID: 3, TransactionType: "payment", StepNumber: 1, StepName: "debit", Status: "completed", Amount: decimal.NewFromInt(-20), CreatedAt: time.Now()},
		{SagaID: "p1", TransactionID: 3, TransactionType: "payment", StepNumber: 2, StepName: "fee", Status: "failed", Amount: decimal.NewFromInt(-1), CreatedAt: time.Now()},
	}
	for i := range seed {
		if err := r.RecordStep(&seed[i]); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// No filter → all 5, newest first.
	logs, total, err := r.ListSagaLogs(repository.SagaLogFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 || len(logs) != 5 {
		t.Fatalf("want total=5 len=5, got total=%d len=%d", total, len(logs))
	}
	if logs[0].ID < logs[len(logs)-1].ID {
		t.Errorf("expected newest-first ordering (id DESC)")
	}

	// Filter by transaction_type.
	_, total, err = r.ListSagaLogs(repository.SagaLogFilter{TransactionType: "payment"})
	if err != nil || total != 2 {
		t.Fatalf("payment filter: total=%d err=%v", total, err)
	}

	// Filter by status.
	_, total, _ = r.ListSagaLogs(repository.SagaLogFilter{Status: "completed"})
	if total != 3 {
		t.Errorf("completed filter: want 3, got %d", total)
	}

	// Filter by saga_id.
	_, total, _ = r.ListSagaLogs(repository.SagaLogFilter{SagaID: "s1"})
	if total != 2 {
		t.Errorf("saga_id filter: want 2, got %d", total)
	}

	// Pagination: page size 2 → 2 rows, total still 5.
	page1, total, _ := r.ListSagaLogs(repository.SagaLogFilter{Page: 1, PageSize: 2})
	if total != 5 || len(page1) != 2 {
		t.Errorf("paginate: want total=5 len=2, got total=%d len=%d", total, len(page1))
	}
}

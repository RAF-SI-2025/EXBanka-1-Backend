package repository

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newTestDB opens a fresh in-memory SQLite DB and auto-migrates SagaLog for
// saga_log_repository tests. Internal test package variant (package
// repository) so it can exercise unexported helpers directly.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil {
		t.Fatalf("migrate saga_logs: %v", err)
	}
	return db
}

func TestSagaLogRepo_RecordAndUpdate(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	log := &model.SagaLog{
		SagaID:     "abc-123",
		OrderID:    42,
		StepNumber: 1,
		StepName:   "reserve_funds",
		Status:     model.SagaStatusPending,
	}
	if err := repo.RecordStep(log); err != nil {
		t.Fatal(err)
	}
	if log.ID == 0 {
		t.Fatal("expected non-zero ID after RecordStep")
	}
	if err := repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompleted, ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(log.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.SagaStatusCompleted {
		t.Errorf("status: got %s want completed", got.Status)
	}
}

func TestSagaLogRepo_ListPendingForOrder(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	for _, st := range []string{model.SagaStatusPending, model.SagaStatusCompensating, model.SagaStatusCompleted} {
		log := &model.SagaLog{
			SagaID: "s1", OrderID: 100, StepNumber: 1, StepName: "x", Status: st,
		}
		_ = repo.RecordStep(log)
	}
	pending, err := repo.ListPendingForOrder(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Errorf("expected 2 pending/compensating, got %d", len(pending))
	}
}

func TestSagaLogRepo_ListStuckSagas(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	past := time.Now().Add(-5 * time.Minute)
	recent := time.Now()

	// Old pending — stuck.
	_ = repo.RecordStep(&model.SagaLog{
		SagaID: "stuck1", OrderID: 1, StepNumber: 1, StepName: "x",
		Status: model.SagaStatusPending, CreatedAt: past, UpdatedAt: past,
	})
	// Old compensating — stuck.
	_ = repo.RecordStep(&model.SagaLog{
		SagaID: "stuck2", OrderID: 2, StepNumber: 1, StepName: "x",
		Status: model.SagaStatusCompensating, CreatedAt: past, UpdatedAt: past,
	})
	// Old completed — not stuck.
	_ = repo.RecordStep(&model.SagaLog{
		SagaID: "done", OrderID: 3, StepNumber: 1, StepName: "x",
		Status: model.SagaStatusCompleted, CreatedAt: past, UpdatedAt: past,
	})
	// Recent pending — not stuck yet.
	_ = repo.RecordStep(&model.SagaLog{
		SagaID: "fresh", OrderID: 4, StepNumber: 1, StepName: "x",
		Status: model.SagaStatusPending, CreatedAt: recent, UpdatedAt: recent,
	})

	stuck, err := repo.ListStuckSagas(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(stuck) != 2 {
		t.Errorf("expected 2 stuck (old pending + old compensating), got %d", len(stuck))
	}
}

func TestSagaLogRepo_IncrementRetryCount(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	log := &model.SagaLog{
		SagaID: "r1", OrderID: 77, StepNumber: 1, StepName: "settle_reservation",
		Status: model.SagaStatusPending,
	}
	if err := repo.RecordStep(log); err != nil {
		t.Fatal(err)
	}
	// RetryCount starts at 0 (column default).
	if err := repo.IncrementRetryCount(log.ID); err != nil {
		t.Fatalf("IncrementRetryCount: %v", err)
	}
	if err := repo.IncrementRetryCount(log.ID); err != nil {
		t.Fatalf("IncrementRetryCount(2): %v", err)
	}
	got, err := repo.GetByID(log.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetryCount != 2 {
		t.Errorf("retry_count: got %d want 2", got.RetryCount)
	}
	// Status must remain unchanged.
	if got.Status != model.SagaStatusPending {
		t.Errorf("status mutated by IncrementRetryCount: got %s", got.Status)
	}
}

func TestSagaLogRepo_GetByStepName(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	_ = repo.RecordStep(&model.SagaLog{SagaID: "s", OrderID: 10, StepNumber: 1, StepName: "reserve_funds", Status: model.SagaStatusCompleted})
	_ = repo.RecordStep(&model.SagaLog{SagaID: "s", OrderID: 10, StepNumber: 2, StepName: "settle_reservation", Status: model.SagaStatusPending})
	_ = repo.RecordStep(&model.SagaLog{SagaID: "s", OrderID: 10, StepNumber: 3, StepName: "settle_reservation", Status: model.SagaStatusCompleted})

	got, err := repo.GetByStepName(10, "settle_reservation")
	if err != nil {
		t.Fatal(err)
	}
	// Should return the LATEST (highest step_number) match.
	if got.StepNumber != 3 {
		t.Errorf("expected latest step #3, got #%d", got.StepNumber)
	}
}

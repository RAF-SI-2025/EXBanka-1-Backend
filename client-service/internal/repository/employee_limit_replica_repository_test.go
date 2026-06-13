package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/model"
)

func newLimitReplicaDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.EmployeeLimitReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestEmployeeLimitReplicaRepo_UpsertAndGet(t *testing.T) {
	repo := NewEmployeeLimitReplicaRepository(newLimitReplicaDB(t))
	ctx := context.Background()
	if err := repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(10000), Version: 1}); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	got, err := repo.GetByEmployeeID(ctx, 1)
	if err != nil || !got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(10000)) {
		t.Fatalf("get1: %+v err=%v", got, err)
	}
	// newer version applies
	if err := repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(20000), Version: 2}); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got, _ = repo.GetByEmployeeID(ctx, 1)
	if !got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(20000)) || got.Version != 2 {
		t.Fatalf("expected v2/20000, got %+v", got)
	}
	// older/equal version ignored (monotonic)
	if err := repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(999), Version: 1}); err != nil {
		t.Fatalf("upsert-stale: %v", err)
	}
	got, _ = repo.GetByEmployeeID(ctx, 1)
	if !got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(20000)) {
		t.Fatalf("stale event overwrote newer: %+v", got)
	}
}

func TestEmployeeLimitReplicaRepo_EqualVersionIgnored(t *testing.T) {
	repo := NewEmployeeLimitReplicaRepository(newLimitReplicaDB(t))
	ctx := context.Background()
	_ = repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(20000), Version: 2})
	_ = repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(111), Version: 2})
	got, _ := repo.GetByEmployeeID(ctx, 1)
	if !got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(20000)) {
		t.Fatalf("equal version must be no-op: %+v", got)
	}
}

func TestEmployeeLimitReplicaRepo_VersionPersistence(t *testing.T) {
	repo := NewEmployeeLimitReplicaRepository(newLimitReplicaDB(t))
	ctx := context.Background()
	_ = repo.Upsert(ctx, model.EmployeeLimitReplica{EmployeeID: 5, MaxClientDailyLimit: decimal.NewFromInt(5000), MaxClientMonthlyLimit: decimal.NewFromInt(50000), Version: 3})
	got, err := repo.GetByEmployeeID(ctx, 5)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("expected version=3, got %d", got.Version)
	}
	if !got.MaxClientDailyLimit.Equal(decimal.NewFromInt(5000)) {
		t.Fatalf("expected MaxClientDailyLimit=5000, got %s", got.MaxClientDailyLimit)
	}
	if !got.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(50000)) {
		t.Fatalf("expected MaxClientMonthlyLimit=50000, got %s", got.MaxClientMonthlyLimit)
	}
}

func TestEmployeeLimitReplicaRepo_GetMissing(t *testing.T) {
	repo := NewEmployeeLimitReplicaRepository(newLimitReplicaDB(t))
	_, err := repo.GetByEmployeeID(context.Background(), 999)
	if err == nil {
		t.Fatalf("expected error for missing replica")
	}
}

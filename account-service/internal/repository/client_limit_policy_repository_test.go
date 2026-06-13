package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

func newPolicyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.ClientLimitPolicy{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestClientLimitPolicyRepo_UpsertReturnsApplied(t *testing.T) {
	repo := NewClientLimitPolicyRepository(newPolicyDB(t))
	ctx := context.Background()
	applied, err := repo.Upsert(ctx, model.ClientLimitPolicy{ClientID: 1, DailyLimit: decimal.NewFromInt(1000), Version: 1})
	if err != nil || !applied {
		t.Fatalf("first insert must apply: applied=%v err=%v", applied, err)
	}
	// newer version applies
	applied, err = repo.Upsert(ctx, model.ClientLimitPolicy{ClientID: 1, DailyLimit: decimal.NewFromInt(2000), Version: 2})
	if err != nil || !applied {
		t.Fatalf("v2 must apply: %v %v", applied, err)
	}
	got, _ := repo.GetByClientID(ctx, 1)
	if !got.DailyLimit.Equal(decimal.NewFromInt(2000)) || got.Version != 2 {
		t.Fatalf("expected v2/2000: %+v", got)
	}
	// stale/equal version does NOT apply
	applied, err = repo.Upsert(ctx, model.ClientLimitPolicy{ClientID: 1, DailyLimit: decimal.NewFromInt(999), Version: 2})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if applied {
		t.Fatalf("equal version must NOT apply")
	}
	got, _ = repo.GetByClientID(ctx, 1)
	if !got.DailyLimit.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("stale must not overwrite: %+v", got)
	}
}

func TestClientLimitPolicyRepo_GetMissing(t *testing.T) {
	repo := NewClientLimitPolicyRepository(newPolicyDB(t))
	_, err := repo.GetByClientID(context.Background(), 999)
	if err == nil {
		t.Fatalf("expected error for missing policy")
	}
}

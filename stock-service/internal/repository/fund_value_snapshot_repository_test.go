package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestFundValueSnapshotRepository_UpsertAndList(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundValueSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundValueSnapshotRepository(db)

	d1 := time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)

	// Insert fund 1 on d1.
	if err := r.UpsertByFundAndDate(&model.FundValueSnapshot{FundID: 1, Date: d1, TotalValueRSD: decimal.NewFromInt(100)}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Same (fund, date) again → update, not duplicate.
	if err := r.UpsertByFundAndDate(&model.FundValueSnapshot{FundID: 1, Date: d1, TotalValueRSD: decimal.NewFromInt(150)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := r.UpsertByFundAndDate(&model.FundValueSnapshot{FundID: 1, Date: d2, TotalValueRSD: decimal.NewFromInt(160)}); err != nil {
		t.Fatalf("insert d2: %v", err)
	}
	if err := r.UpsertByFundAndDate(&model.FundValueSnapshot{FundID: 2, Date: d1, TotalValueRSD: decimal.NewFromInt(200)}); err != nil {
		t.Fatalf("insert fund2: %v", err)
	}

	rows, err := r.ListByFundSince(1, time.Time{})
	if err != nil {
		t.Fatalf("list fund1: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("fund1: want 2 rows (idempotent upsert), got %d", len(rows))
	}
	if !rows[0].TotalValueRSD.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("d1 value should be updated to 150, got %s", rows[0].TotalValueRSD)
	}
	if !rows[0].Date.Before(rows[1].Date) {
		t.Fatalf("expected ascending by date")
	}

	all, err := r.ListAllSince(time.Time{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all funds: want 3 rows, got %d", len(all))
	}
}

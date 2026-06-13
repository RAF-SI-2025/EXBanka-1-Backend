package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type fakeFundSnapshotSource struct {
	funds []model.InvestmentFund
	stats map[uint64]FundStatistics
}

func (f *fakeFundSnapshotSource) List(_ string, _ *bool, _, _ int) ([]model.InvestmentFund, int64, error) {
	return f.funds, int64(len(f.funds)), nil
}
func (f *fakeFundSnapshotSource) Statistics(_ context.Context, fund *model.InvestmentFund) (FundStatistics, error) {
	return f.stats[fund.ID], nil
}

func TestFundSnapshotCron_RunOnce_WritesOnePerFund(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.FundValueSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	snapRepo := repository.NewFundValueSnapshotRepository(db)

	src := &fakeFundSnapshotSource{
		funds: []model.InvestmentFund{{ID: 1}, {ID: 2}},
		stats: map[uint64]FundStatistics{
			1: {TotalValueRSD: decimal.NewFromInt(1000), LiquidRSDBal: decimal.NewFromInt(200), TotalHoldingsValueRSD: decimal.NewFromInt(800), InvestorCount: 3},
			2: {TotalValueRSD: decimal.NewFromInt(5000), InvestorCount: 1},
		},
	}
	cr := NewFundSnapshotCron(src, snapRepo, "23:50", nilRegistry())

	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Re-run same day → no duplicates (upsert).
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("re-run: %v", err)
	}

	var count int64
	db.Model(&model.FundValueSnapshot{}).Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 snapshots (one per fund, idempotent), got %d", count)
	}
	rows, _ := snapRepo.ListByFundSince(1, time.Time{})
	if len(rows) != 1 || !rows[0].TotalValueRSD.Equal(decimal.NewFromInt(1000)) || rows[0].InvestorCount != 3 {
		t.Fatalf("fund 1 snapshot mismatch: %+v", rows)
	}
}

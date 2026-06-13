package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func seedMonthly(t *testing.T, repo *repository.FundValueSnapshotRepository, fundID uint64, vals []float64) {
	t.Helper()
	for i, v := range vals {
		d := time.Date(2026, time.Month(1+i), 28, 0, 0, 0, 0, time.UTC)
		if err := repo.UpsertByFundAndDate(&model.FundValueSnapshot{FundID: fundID, Date: d, TotalValueRSD: decimal.NewFromFloat(v)}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestListSortedByMetric_OrdersByAnnualizedReturn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.InvestmentFund{}, &model.FundValueSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	fundRepo := repository.NewFundRepository(db)
	snapRepo := repository.NewFundValueSnapshotRepository(db)

	// Three active funds.
	high := &model.InvestmentFund{Name: "High", ManagerEmployeeID: 1, Active: true, RSDAccountID: 101}
	low := &model.InvestmentFund{Name: "Low", ManagerEmployeeID: 1, Active: true, RSDAccountID: 102}
	noHist := &model.InvestmentFund{Name: "NoHist", ManagerEmployeeID: 1, Active: true, RSDAccountID: 103}
	for _, f := range []*model.InvestmentFund{high, low, noHist} {
		if err := fundRepo.Create(f); err != nil {
			t.Fatalf("create fund: %v", err)
		}
	}
	// High: strong growth; Low: mild growth; NoHist: single snapshot (unavailable).
	seedMonthly(t, snapRepo, high.ID, []float64{100, 130, 170, 220})
	seedMonthly(t, snapRepo, low.ID, []float64{100, 102, 104, 106})
	seedMonthly(t, snapRepo, noHist.ID, []float64{100})

	svc := NewFundService(fundRepo, nil, nil).WithSnapshots(snapRepo, 2)

	funds, _, avail, total, err := svc.ListSortedByMetric("", "annualized_return", "desc", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(funds) != 3 {
		t.Fatalf("want 3 funds, got total=%d len=%d", total, len(funds))
	}
	if funds[0].Name != "High" || funds[1].Name != "Low" {
		t.Fatalf("expected High, Low, ... order, got %s, %s, %s", funds[0].Name, funds[1].Name, funds[2].Name)
	}
	if funds[2].Name != "NoHist" || avail[2] {
		t.Fatalf("expected NoHist last with metrics unavailable, got %s avail=%v", funds[2].Name, avail[2])
	}
}

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// countingSnapshotSource wraps a fakeFundSnapshotSource and counts Statistics
// calls so a manual trigger can be observed.
type countingSnapshotSource struct {
	inner *fakeFundSnapshotSource
	calls int64
}

func (c *countingSnapshotSource) List(s string, a *bool, p, ps int) ([]model.InvestmentFund, int64, error) {
	return c.inner.List(s, a, p, ps)
}
func (c *countingSnapshotSource) Statistics(ctx context.Context, f *model.InvestmentFund) (FundStatistics, error) {
	atomic.AddInt64(&c.calls, 1)
	return c.inner.Statistics(ctx, f)
}

func TestFundSnapshotCron_StartDailyCron_ManualTrigger(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.FundValueSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	snapRepo := repository.NewFundValueSnapshotRepository(db)
	src := &countingSnapshotSource{inner: &fakeFundSnapshotSource{
		funds: []model.InvestmentFund{{ID: 1}},
		stats: map[uint64]FundStatistics{1: {TotalValueRSD: decimal.NewFromInt(500)}},
	}}
	reg := cronreg.NewRegistry("test", nil)
	cr := NewFundSnapshotCron(src, snapRepo, "23:59", reg)

	ctx, cancel := context.WithCancel(context.Background())
	cr.StartDailyCron(ctx)

	// Wait for the startup pass.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&src.calls) < 1 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("startup run did not happen")
		}
		time.Sleep(5 * time.Millisecond)
	}
	startupCalls := atomic.LoadInt64(&src.calls)

	// Fire a manual trigger → the trigger branch runs RunOnce again.
	if err := reg.Trigger("fund-snapshot-cron", true, 0); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&src.calls) <= startupCalls {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("manual trigger did not run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
}

func TestOTCExpiryCron_Start_ManualTrigger(t *testing.T) {
	db := newOTCExpiryDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	contractRepo := repository.NewOptionContractRepository(db)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewOTCExpiryCron(contractRepo, nil, nil, 10, "23:59", reg)

	ctx, cancel := context.WithCancel(context.Background())
	cron.Start(ctx)
	time.Sleep(20 * time.Millisecond) // let the startup pass run

	// Seed an expired contract, then fire a manual trigger to expire it.
	buyer := uint64(7)
	seller := uint64(1)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(1),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(5),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -1),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyer,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &seller,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := reg.Trigger("otc-expiry-cron", true, 0); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := contractRepo.GetByID(c.ID)
		if got != nil && got.Status == model.OptionContractStatusExpired {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("manual trigger did not expire the contract")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
}

func TestListingCron_StartDailyCron_ManualTrigger(t *testing.T) {
	reg := cronreg.NewRegistry("test", nil)
	svc := NewListingCronService(&listingCronListingMock{}, &listingCronDailyMock{}, nil, reg)
	ctx, cancel := context.WithCancel(context.Background())
	svc.StartDailyCron(ctx)
	// Fire the manual trigger so the trigger-branch SnapshotDailyPrices runs.
	if err := reg.Trigger("listing-daily-snapshot", true, 0); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	cancel()
}

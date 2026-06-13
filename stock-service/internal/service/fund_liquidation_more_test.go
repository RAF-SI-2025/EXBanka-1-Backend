package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// fakeLiquidationPlacer credits the fund's RSD account on each CreateOrder so
// the LiquidateAndAwait poll loop observes the balance climbing to target.
type fakeLiquidationPlacer struct {
	mu       sync.Mutex
	calls    int
	accounts *fakeFundAccountClient
	fundID   uint64 // accounts key to credit
	perOrder decimal.Decimal
	err      error
}

func (f *fakeLiquidationPlacer) CreateOrder(_ context.Context, _ CreateOrderRequest) (*model.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	acct := f.accounts.accounts[f.fundID]
	cur, _ := decimal.NewFromString(acct.AvailableBalance)
	acct.AvailableBalance = cur.Add(f.perOrder).String()
	return &model.Order{}, nil
}

func newLiquidationSvc(t *testing.T) (*FundService, *gorm.DB, *fakeFundAccountClient, *model.InvestmentFund) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{}, &model.FundHolding{}, &model.Listing{}, &model.StockExchange{},
		&model.ClientFundPosition{}, &model.FundPositionSettlement{}, &model.FundContribution{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	accounts := newFakeFundAccountClient()
	bac := &fakeBankAccountClient{nextID: 6000}
	svc := NewFundService(repo, bac, nil).
		WithSaga(newFakeSagaRepo(), accounts, nil,
			repository.NewFundContributionRepository(db),
			repository.NewClientFundPositionRepository(db),
			repository.NewFundHoldingRepository(db), nil, nil).
		WithPositionReads(repository.NewListingRepository(db))

	fund := &model.InvestmentFund{Name: "Liq", ManagerEmployeeID: 7, RSDAccountID: 7001, Active: true}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(7001, "FUND", "0")
	return svc, db, accounts, fund
}

func TestLiquidate_NoHoldings_Insufficient(t *testing.T) {
	svc, _, accounts, fund := newLiquidationSvc(t)
	placer := &fakeLiquidationPlacer{accounts: accounts, fundID: 7001, perOrder: decimal.NewFromInt(100)}
	svc = svc.WithLiquidation(placer)
	err := svc.LiquidateAndAwait(context.Background(), fund, decimal.NewFromInt(200), "liq")
	if err != ErrInsufficientFundCash {
		t.Fatalf("want ErrInsufficientFundCash with no holdings, got %v", err)
	}
}

func TestLiquidate_NonPositiveDeficit_Nil(t *testing.T) {
	svc, _, accounts, fund := newLiquidationSvc(t)
	placer := &fakeLiquidationPlacer{accounts: accounts, fundID: 7001, perOrder: decimal.NewFromInt(100)}
	svc = svc.WithLiquidation(placer)
	if err := svc.LiquidateAndAwait(context.Background(), fund, decimal.Zero, "liq"); err != nil {
		t.Fatalf("zero deficit should be nil, got %v", err)
	}
}

func TestLiquidate_SellsUntilTargetReached(t *testing.T) {
	svc, db, accounts, fund := newLiquidationSvc(t)

	// Listing: RSD stock priced 100.
	listing := &model.Listing{ID: 1, SecurityID: 10, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(100)}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}
	// Fund holds 5 of security 10.
	fh := &model.FundHolding{FundID: fund.ID, SecurityType: "stock", SecurityID: 10, Quantity: 5, AveragePriceRSD: decimal.NewFromInt(80)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fh: %v", err)
	}

	placer := &fakeLiquidationPlacer{accounts: accounts, fundID: 7001, perOrder: decimal.NewFromInt(200)}
	svc = svc.WithLiquidation(placer)

	// Deficit 200 → sell ceil(200/100)=2 shares; placer credits 200 → target met.
	err := svc.LiquidateAndAwait(context.Background(), fund, decimal.NewFromInt(200), "liq")
	if err != nil {
		t.Fatalf("liquidate: %v", err)
	}
	if placer.calls != 1 {
		t.Errorf("expected 1 sell order, got %d", placer.calls)
	}
}

func TestLiquidate_SkipsZeroPriceListing(t *testing.T) {
	// This path never reaches target, so it falls into the poll loop until the
	// timeout. Shrink the (package-global) poll config so the test is fast.
	// Tests in this package run sequentially, so the restore is race-free.
	saved := defaultLiquidationConfig
	defaultLiquidationConfig = liquidationConfig{pollEvery: 5 * time.Millisecond, timeout: 30 * time.Millisecond}
	defer func() { defaultLiquidationConfig = saved }()

	svc, db, accounts, fund := newLiquidationSvc(t)
	// Holding whose listing has zero price → skipped; no other holdings → insufficient.
	listing := &model.Listing{ID: 2, SecurityID: 20, SecurityType: "stock", ExchangeID: 1, Price: decimal.Zero}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}
	fh := &model.FundHolding{FundID: fund.ID, SecurityType: "stock", SecurityID: 20, Quantity: 5, AveragePriceRSD: decimal.NewFromInt(80)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fh: %v", err)
	}
	placer := &fakeLiquidationPlacer{accounts: accounts, fundID: 7001, perOrder: decimal.NewFromInt(100)}
	svc = svc.WithLiquidation(placer)

	err := svc.LiquidateAndAwait(context.Background(), fund, decimal.NewFromInt(200), "liq")
	if err != ErrInsufficientFundCash {
		t.Fatalf("want ErrInsufficientFundCash (zero-price skipped), got %v", err)
	}
	if placer.calls != 0 {
		t.Errorf("zero-price listing should not place an order, got %d calls", placer.calls)
	}
}

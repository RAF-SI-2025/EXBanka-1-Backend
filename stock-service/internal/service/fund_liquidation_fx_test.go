package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestLiquidate_FXConvert_AndQtyCap covers LiquidateAndAwait's foreign-currency
// price conversion branch and the "sell at most the held quantity" cap.
func TestLiquidate_FXConvert_AndQtyCap(t *testing.T) {
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
	// USD listing → exchange converts 2 USD/share to 200 RSD/share.
	exch := &fakeFundExchangeClient{convert: "200"}
	svc := NewFundService(repo, &fakeBankAccountClient{nextID: 6000}, nil).
		WithSaga(newFakeSagaRepo(), accounts, exch,
			repository.NewFundContributionRepository(db),
			repository.NewClientFundPositionRepository(db),
			repository.NewFundHoldingRepository(db), nil, nil).
		WithPositionReads(repository.NewListingRepository(db))

	fund := &model.InvestmentFund{Name: "Liq", ManagerEmployeeID: 7, RSDAccountID: 7001, Active: true}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(7001, "FUND", "0")

	ex := &model.StockExchange{ID: 1, Acronym: "NYSE", MICCode: "XNYS", Currency: "USD"}
	if err := db.Create(ex).Error; err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	listing := &model.Listing{ID: 1, SecurityID: 10, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(2)}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}
	// Fund holds only 2 shares — fewer than the deficit implies, so the sell is capped.
	fh := &model.FundHolding{FundID: fund.ID, SecurityType: "stock", SecurityID: 10, Quantity: 2, AveragePriceRSD: decimal.NewFromInt(40)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fh: %v", err)
	}

	placer := &fakeLiquidationPlacer{accounts: accounts, fundID: 7001, perOrder: decimal.NewFromInt(1000)}
	svc = svc.WithLiquidation(placer)

	// Deficit 1000; priceRSD 200 → ceil(1000/200)=5 shares, capped to the 2 held.
	if err := svc.LiquidateAndAwait(context.Background(), fund, decimal.NewFromInt(1000), "liq-fx"); err != nil {
		t.Fatalf("liquidate: %v", err)
	}
	if placer.calls != 1 {
		t.Errorf("expected 1 capped sell order, got %d", placer.calls)
	}
}

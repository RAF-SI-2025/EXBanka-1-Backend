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

func openUnifiedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.Listing{},
		&model.ClientFundPosition{},
		&model.InvestmentFund{},
		&model.FundHolding{},
		&model.ClientFundPosition{},
		&model.FundPositionSettlement{},
		&model.DividendPayout{},
		&model.FundDividendPayment{},
		&model.DividendPayment{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newUnifiedService(db *gorm.DB, accounts FundAccountClient) *UnifiedPortfolioService {
	return NewUnifiedPortfolioService(
		repository.NewHoldingRepository(db),
		repository.NewClientFundPositionRepository(db),
		repository.NewFundRepository(db),
		repository.NewFundHoldingRepository(db),
		repository.NewListingRepository(db),
		accounts,
	)
}

func TestUnifiedPortfolio_OwnerIDRequired(t *testing.T) {
	db := openUnifiedTestDB(t)
	svc := newUnifiedService(db, nil)
	if _, err := svc.Get(context.Background(), "client", nil); err == nil {
		t.Fatalf("expected error when owner_id nil for client")
	}
}

func TestUnifiedPortfolio_ClientWithStockAndFund_Full(t *testing.T) {
	db := openUnifiedTestDB(t)
	uid := uint64(42)

	// Listing the stock holding references; current price 150.
	listing := &model.Listing{ID: 1, SecurityID: 10, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(150)}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}

	// Client stock holding: 10 shares @ avg 100 → value 1500, PL 500.
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		UserFirstName: "Ada", UserLastName: "L",
		SecurityType: "stock", SecurityID: 10, ListingID: 1,
		Ticker: "AAA", Name: "Alpha", Quantity: 10,
		AveragePrice: decimal.NewFromInt(100), ReservedQuantity: 3,
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("create holding: %v", err)
	}

	// Fund with a liquid RSD account and one fund-holding.
	fund := &model.InvestmentFund{ID: 5, Name: "Growth", ManagerEmployeeID: 1, RSDAccountID: 900}
	if err := db.Create(fund).Error; err != nil {
		t.Fatalf("create fund: %v", err)
	}
	fundListing := &model.Listing{ID: 2, SecurityID: 20, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(50)}
	if err := db.Create(fundListing).Error; err != nil {
		t.Fatalf("create fund listing: %v", err)
	}
	fh := &model.FundHolding{FundID: 5, SecurityType: "stock", SecurityID: 20, Quantity: 4, AveragePriceRSD: decimal.NewFromInt(40)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fund holding: %v", err)
	}
	// Two investors so pct_of_fund is meaningful: our client contributed 1000,
	// another contributed 1000 → 50% each.
	posRepo := repository.NewClientFundPositionRepository(db)
	other := uint64(99)
	if err := posRepo.IncrementContribution(5, model.OwnerClient, &uid, decimal.NewFromInt(1000), 1); err != nil {
		t.Fatalf("increment me: %v", err)
	}
	if err := posRepo.IncrementContribution(5, model.OwnerClient, &other, decimal.NewFromInt(1000), 2); err != nil {
		t.Fatalf("increment other: %v", err)
	}

	accts := newFakeFundAccountClient()
	accts.addAccount(900, "FUND-RSD", "200") // liquid 200

	svc := newUnifiedService(db, accts)
	out, err := svc.Get(context.Background(), "client", &uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Securities assertions.
	if len(out.Securities.Positions) != 1 {
		t.Fatalf("want 1 security position, got %d", len(out.Securities.Positions))
	}
	sp := out.Securities.Positions[0]
	if !sp.CurrentValueRSD.Equal(decimal.NewFromInt(1500)) {
		t.Errorf("security current value = %s, want 1500", sp.CurrentValueRSD)
	}
	if !sp.PLRSD.Equal(decimal.NewFromInt(500)) {
		t.Errorf("security PL = %s, want 500", sp.PLRSD)
	}
	if sp.AvailableQuantity != 7 {
		t.Errorf("available qty = %d, want 7", sp.AvailableQuantity)
	}
	if sp.AssetType != "stock" {
		t.Errorf("asset type = %s, want stock", sp.AssetType)
	}

	// Fund assertions: NAV = holdings(4*50=200) + liquid(200) = 400; pct 50 → 200.
	if len(out.Funds.Positions) != 1 {
		t.Fatalf("want 1 fund position, got %d", len(out.Funds.Positions))
	}
	fp := out.Funds.Positions[0]
	if !fp.PctOfFund.Equal(decimal.NewFromInt(50)) {
		t.Errorf("pct_of_fund = %s, want 50", fp.PctOfFund)
	}
	if !fp.CurrentValueRSD.Equal(decimal.NewFromInt(200)) {
		t.Errorf("fund current value = %s, want 200", fp.CurrentValueRSD)
	}
	if !fp.PLRSD.Equal(decimal.NewFromInt(-800)) {
		t.Errorf("fund PL = %s, want -800", fp.PLRSD)
	}
	if fp.FundName != "Growth" {
		t.Errorf("fund name = %s", fp.FundName)
	}

	// Grand totals: 1500 + 200 = 1700.
	if !out.TotalValueRSD.Equal(decimal.NewFromInt(1700)) {
		t.Errorf("total value = %s, want 1700", out.TotalValueRSD)
	}
}

func TestUnifiedPortfolio_FundHoldingFallsBackToAvgPrice(t *testing.T) {
	db := openUnifiedTestDB(t)
	uid := uint64(7)
	fund := &model.InvestmentFund{ID: 3, Name: "Opt", ManagerEmployeeID: 1, RSDAccountID: 0}
	if err := db.Create(fund).Error; err != nil {
		t.Fatalf("create fund: %v", err)
	}
	// Fund holding with NO matching listing → falls back to AveragePriceRSD (30).
	fh := &model.FundHolding{FundID: 3, SecurityType: "option", SecurityID: 77, Quantity: 2, AveragePriceRSD: decimal.NewFromInt(30)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fh: %v", err)
	}
	posRepo := repository.NewClientFundPositionRepository(db)
	if err := posRepo.IncrementContribution(3, model.OwnerClient, &uid, decimal.NewFromInt(100), 1); err != nil {
		t.Fatalf("increment: %v", err)
	}

	// nil accounts → liquid contributes 0; NAV = 2*30 = 60; only investor → pct 100.
	svc := newUnifiedService(db, nil)
	out, err := svc.Get(context.Background(), "client", &uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	fp := out.Funds.Positions[0]
	if !fp.CurrentValueRSD.Equal(decimal.NewFromInt(60)) {
		t.Errorf("fund value = %s, want 60", fp.CurrentValueRSD)
	}
}

func TestUnifiedPortfolio_WithDividendService(t *testing.T) {
	db := openUnifiedTestDB(t)
	uid := uint64(11)

	listing := &model.Listing{ID: 1, SecurityID: 10, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(100)}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		UserFirstName: "A", UserLastName: "B",
		SecurityType: "stock", SecurityID: 10, ListingID: 1,
		Ticker: "AAA", Name: "Alpha", Quantity: 5, AveragePrice: decimal.NewFromInt(80),
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("create holding: %v", err)
	}
	// Dividend payout to this owner: net 42.
	dp := &model.DividendPayout{
		DividendPaymentID: 1, HoldingOwnerType: "client", HoldingOwnerID: &uid,
		HoldingID: h.ID, Shares: 5, GrossAmountRSD: decimal.NewFromInt(50),
		TaxAmountRSD: decimal.NewFromInt(8), NetAmountRSD: decimal.NewFromInt(42),
		CreditedAccountID: 1, IdempotencyKey: "div-1-1",
	}
	if err := db.Create(dp).Error; err != nil {
		t.Fatalf("create payout: %v", err)
	}

	divSvc := newDividendService(db, newFakeDividendAccountClient())
	svc := newUnifiedService(db, nil).WithDividendService(divSvc)

	out, err := svc.Get(context.Background(), "client", &uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !out.Securities.Positions[0].DividendsReceivedRSD.Equal(decimal.NewFromInt(42)) {
		t.Errorf("dividends received = %s, want 42", out.Securities.Positions[0].DividendsReceivedRSD)
	}
}

func TestUnifiedPortfolio_EmptyBank(t *testing.T) {
	db := openUnifiedTestDB(t)
	svc := newUnifiedService(db, nil)
	out, err := svc.Get(context.Background(), "bank", nil)
	if err != nil {
		t.Fatalf("Get bank: %v", err)
	}
	if !out.TotalValueRSD.IsZero() {
		t.Errorf("empty bank total = %s, want 0", out.TotalValueRSD)
	}
	if len(out.Securities.Positions) != 0 || len(out.Funds.Positions) != 0 {
		t.Errorf("expected no positions")
	}
}

func TestUnifiedPortfolio_FundLiquidRSD_AccountError(t *testing.T) {
	db := openUnifiedTestDB(t)
	svc := newUnifiedService(db, newFakeFundAccountClient())
	// Account 900 not registered → GetAccount errors → liquid falls back to 0.
	got := svc.fundLiquidRSD(context.Background(), &model.InvestmentFund{ID: 1, RSDAccountID: 900})
	if !got.IsZero() {
		t.Errorf("expected 0 on account error, got %s", got)
	}
	// nil accounts → 0 without a call.
	svcNil := newUnifiedService(db, nil)
	if !svcNil.fundLiquidRSD(context.Background(), &model.InvestmentFund{RSDAccountID: 900}).IsZero() {
		t.Errorf("expected 0 with nil accounts")
	}
}

func TestMapSecurityType(t *testing.T) {
	cases := map[string]string{
		"stock": "stock", "option": "option",
		"futures": "future", "future": "future", "forex": "forex",
	}
	for in, want := range cases {
		if got := mapSecurityType(in); got != want {
			t.Errorf("mapSecurityType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPctCalc(t *testing.T) {
	if !pctCalc(decimal.NewFromInt(5), decimal.Zero).IsZero() {
		t.Errorf("pctCalc by zero should be 0")
	}
	if !pctCalc(decimal.NewFromInt(50), decimal.NewFromInt(200)).Equal(decimal.NewFromInt(25)) {
		t.Errorf("pctCalc(50,200) want 25")
	}
}

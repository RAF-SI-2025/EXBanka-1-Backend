// Tests covering uncovered methods on holding/listing/option/option_contract/
// fund/fund_holding/client_fund_position/listing_daily_price/capital_gain
// repositories. Uses sqlite in-memory.
package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// HoldingRepository
// ---------------------------------------------------------------------------

func newHoldingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Holding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestHoldingRepository_GetByID(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	uid := uint64(7)
	_ = r.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1,
		Ticker: "AAPL", Name: "Apple",
		Quantity:     10,
		AveragePrice: decimal.NewFromInt(100),
	})
	h, err := r.GetByID(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h.Ticker != "AAPL" {
		t.Errorf("got %q", h.Ticker)
	}
	if _, err := r.GetByID(9999); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHoldingRepository_Update(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	uid := uint64(7)
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1, Ticker: "AAPL",
		Quantity: 10, AveragePrice: decimal.NewFromInt(100),
	}
	_ = r.Upsert(context.Background(), h)
	h.Quantity = 20
	if err := r.Update(h); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := r.GetByID(h.ID)
	if got.Quantity != 20 {
		t.Errorf("got %d", got.Quantity)
	}
}

func TestHoldingRepository_Delete(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	uid := uint64(7)
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1, Ticker: "AAPL",
		Quantity: 10, AveragePrice: decimal.NewFromInt(100),
	}
	_ = r.Upsert(context.Background(), h)
	if err := r.Delete(h.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.GetByID(h.ID); err == nil {
		t.Error("expected not found after delete")
	}
}

func TestHoldingRepository_GetByOwnerAndTicker(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	uid := uint64(7)
	_ = r.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1, Ticker: "AAPL",
		Quantity: 10, AveragePrice: decimal.NewFromInt(100),
	})
	got, err := r.GetByOwnerAndTicker(model.OwnerClient, &uid, "stock", "AAPL")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Quantity != 10 {
		t.Errorf("got %d", got.Quantity)
	}
	if _, err := r.GetByOwnerAndTicker(model.OwnerClient, &uid, "stock", "MSFT"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestHoldingRepository_ListPublicAndOffers(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	uid := uint64(7)
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1, Ticker: "AAPL",
		Quantity: 10, AveragePrice: decimal.NewFromInt(100),
		PublicQuantity: 5,
	}
	_ = r.Upsert(context.Background(), h)
	got, err := r.ListPublic()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d", len(got))
	}
	rows, total, err := r.ListPublicOffers(OTCFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got total=%d len=%d", total, len(rows))
	}
}

// ---------------------------------------------------------------------------
// ListingRepository
// ---------------------------------------------------------------------------

func newListingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.Listing{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestListingRepository_Crud(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	l := &model.Listing{
		SecurityID: 1, SecurityType: "stock", ExchangeID: 0,
		Price: decimal.NewFromInt(100),
	}
	if err := r.Create(l); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(l.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SecurityID != 1 {
		t.Errorf("mismatch")
	}
	got2, err := r.GetBySecurityIDAndType(1, "stock")
	if err != nil {
		t.Fatalf("get sec: %v", err)
	}
	if got2.ID != l.ID {
		t.Errorf("id mismatch")
	}
	if _, err := r.GetBySecurityIDAndType(99, "stock"); err == nil {
		t.Error("expected not found")
	}
}

func TestListingRepository_ListBySecurityIDsAndType(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	_ = r.Create(&model.Listing{SecurityID: 1, SecurityType: "stock", Price: decimal.NewFromInt(100)})
	_ = r.Create(&model.Listing{SecurityID: 2, SecurityType: "stock", Price: decimal.NewFromInt(200)})
	got, err := r.ListBySecurityIDsAndType([]uint64{1, 2}, "stock")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
	got2, err := r.ListBySecurityIDsAndType(nil, "stock")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got2 != nil {
		t.Errorf("expected nil for empty input")
	}
}

func TestListingRepository_Update(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	l := &model.Listing{SecurityID: 1, SecurityType: "stock", Price: decimal.NewFromInt(100)}
	_ = r.Create(l)
	l.Price = decimal.NewFromInt(150)
	if err := r.Update(l); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestListingRepository_UpsertBySecurity(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	l := &model.Listing{SecurityID: 5, SecurityType: "stock", Price: decimal.NewFromInt(100)}
	if err := r.UpsertBySecurity(l); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	upd := &model.Listing{SecurityID: 5, SecurityType: "stock", Price: decimal.NewFromInt(200)}
	if err := r.UpsertBySecurity(upd); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ := r.GetBySecurityIDAndType(5, "stock")
	if !got.Price.Equal(decimal.NewFromInt(200)) {
		t.Errorf("got %s", got.Price)
	}
}

func TestListingRepository_UpsertForOption(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	l := &model.Listing{SecurityID: 1, SecurityType: "option", Price: decimal.NewFromInt(50)}
	got, err := r.UpsertForOption(l)
	if err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	if got.SecurityID != 1 {
		t.Errorf("got %+v", got)
	}
	upd := &model.Listing{SecurityID: 1, SecurityType: "option", Price: decimal.NewFromInt(75)}
	got, err = r.UpsertForOption(upd)
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if !got.Price.Equal(decimal.NewFromInt(75)) {
		t.Errorf("got %s", got.Price)
	}
}

func TestListingRepository_UpdatePriceByTicker_BadType(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	err := r.UpdatePriceByTicker("weird", "AAPL", decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(1))
	if err == nil {
		t.Error("expected error for unsupported security_type")
	}
}

func TestListingRepository_ListAllAndByType(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)
	_ = r.Create(&model.Listing{SecurityID: 1, SecurityType: "stock", Price: decimal.NewFromInt(100)})
	_ = r.Create(&model.Listing{SecurityID: 2, SecurityType: "futures", Price: decimal.NewFromInt(200)})
	got, err := r.ListAll()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
	got2, err := r.ListBySecurityType("stock")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got2) != 1 {
		t.Errorf("got %d", len(got2))
	}
}

// ---------------------------------------------------------------------------
// OptionRepository
// ---------------------------------------------------------------------------

func newOptionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Option{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOptionRepository_GetByIDAndTicker(t *testing.T) {
	db := newOptionTestDB(t)
	r := NewOptionRepository(db)
	o := &model.Option{
		Ticker: "AAPL260116C200", StockID: 1,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(200),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		Premium:        decimal.NewFromInt(5),
	}
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ticker != "AAPL260116C200" {
		t.Errorf("got %s", got.Ticker)
	}
	got2, err := r.GetByTicker("AAPL260116C200")
	if err != nil {
		t.Fatalf("get by ticker: %v", err)
	}
	if got2.ID != o.ID {
		t.Errorf("id mismatch")
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Error("expected error")
	}
	if _, err := r.GetByTicker("nope"); err == nil {
		t.Error("expected error")
	}
}

func TestOptionRepository_Update(t *testing.T) {
	db := newOptionTestDB(t)
	r := NewOptionRepository(db)
	o := &model.Option{
		Ticker: "AAPL", StockID: 1, OptionType: "call",
		StrikePrice:    decimal.NewFromInt(200),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		Premium:        decimal.NewFromInt(5),
	}
	_ = r.Create(o)
	o.Premium = decimal.NewFromInt(7)
	if err := r.Update(o); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestOptionRepository_List(t *testing.T) {
	db := newOptionTestDB(t)
	r := NewOptionRepository(db)
	for i, tk := range []string{"O1", "O2", "O3"} {
		_ = r.Create(&model.Option{
			Ticker: tk, StockID: uint64(i + 1), OptionType: "call",
			StrikePrice:    decimal.NewFromInt(100),
			SettlementDate: time.Now().Add(30 * 24 * time.Hour),
			Premium:        decimal.NewFromInt(1),
		})
	}
	got, total, err := r.List(OptionFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 3 || len(got) != 3 {
		t.Errorf("got %d/%d", total, len(got))
	}
}

// ---------------------------------------------------------------------------
// OptionContractRepository
// ---------------------------------------------------------------------------

func newOptionContractDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOptionContractRepository_DBAccessor(t *testing.T) {
	db := newOptionContractDB(t)
	r := NewOptionContractRepository(db)
	if r.DB() != db {
		t.Error("DB accessor mismatch")
	}
}

func TestOptionContractRepository_GetByOfferIDAndDelete(t *testing.T) {
	db := newOptionContractDB(t)
	r := NewOptionContractRepository(db)
	uid := uint64(7)
	c := &model.OptionContract{
		OfferID: 1, StockID: 42,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(100),
		PremiumPaid:     decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByOfferID(1)
	if err != nil {
		t.Fatalf("get by offer: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("mismatch")
	}
	if err := r.Delete(c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.GetByID(c.ID); err == nil {
		t.Error("expected not found after delete")
	}
}

func TestOptionContractRepository_ListByOwnerAndExpiring(t *testing.T) {
	db := newOptionContractDB(t)
	r := NewOptionContractRepository(db)
	uid := uint64(7)
	c := &model.OptionContract{
		OfferID: 1, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(-24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	_ = r.Create(c)
	rows, total, err := r.ListByOwner(model.OwnerClient, &uid, "buyer", nil, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d/%d", total, len(rows))
	}
	expiring, err := r.ListExpiring(time.Now().Format("2006-01-02"), 10)
	if err != nil {
		t.Fatalf("expiring: %v", err)
	}
	if len(expiring) != 1 {
		t.Errorf("expected 1 expiring, got %d", len(expiring))
	}
}

// ---------------------------------------------------------------------------
// CapitalGainRepository
// ---------------------------------------------------------------------------

func TestCapitalGainRepository_SumOps(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewCapitalGainRepository(db)
	uid := uint64(7)
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = r.Create(&model.CapitalGain{
			OwnerType: model.OwnerClient, OwnerID: &uid,
			SecurityType: "stock", Ticker: "X", Quantity: 1,
			BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(150),
			TotalGain: decimal.NewFromInt(50), Currency: "RSD", AccountID: uint64(i + 1),
			TaxYear: now.Year(), TaxMonth: int(now.Month()),
		})
	}
	allTime, err := r.SumByOwnerAllTime(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(allTime) == 0 {
		t.Errorf("expected entries")
	}
	uncoll, err := r.SumUncollectedByOwnerMonth(model.OwnerClient, &uid, now.Year(), int(now.Month()))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(uncoll) == 0 {
		t.Errorf("expected uncollected entries")
	}
	cnt, err := r.CountByOwnerYear(model.OwnerClient, &uid, now.Year())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 3 {
		t.Errorf("count=%d want 3", cnt)
	}
	emp := int64(99)
	_ = r.Create(&model.CapitalGain{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", Ticker: "Y", Quantity: 1,
		BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(150),
		TotalGain: decimal.NewFromInt(50), Currency: "RSD", AccountID: 99,
		TaxYear: now.Year(), TaxMonth: int(now.Month()),
		ActingEmployeeID: func() *uint64 { v := uint64(emp); return &v }(),
	})
	rows, err := r.SumByActingEmployee()
	if err != nil {
		t.Fatalf("acting: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected acting employee rows")
	}
}

// ---------------------------------------------------------------------------
// FundRepository (Save, List, ReassignManager)
// ---------------------------------------------------------------------------

func TestFundRepository_SaveAndList(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)
	f := &model.InvestmentFund{
		Name: "A", ManagerEmployeeID: 1,
		MinimumContributionRSD: decimal.NewFromInt(100), Active: true,
	}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Name = "A2"
	if err := r.Save(f); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, total, err := r.List("", nil, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d/%d", total, len(rows))
	}
	active := true
	rows, _, _ = r.List("", &active, 1, 10)
	if len(rows) != 1 {
		t.Errorf("active filter wrong")
	}
}

func TestFundRepository_ReassignManager(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)
	_ = r.Create(&model.InvestmentFund{Name: "A", ManagerEmployeeID: 5, Active: true, MinimumContributionRSD: decimal.Zero})
	_ = r.Create(&model.InvestmentFund{Name: "C", ManagerEmployeeID: 6, Active: true, MinimumContributionRSD: decimal.Zero})
	ids, err := r.ReassignManager(5, 99)
	if err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("got %d ids", len(ids))
	}
}

// ---------------------------------------------------------------------------
// FundHoldingRepository, ClientFundPositionRepository, ListingDailyPriceRepository
// ---------------------------------------------------------------------------

func TestFundHoldingRepository_DecrementAndGet(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundHolding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundHoldingRepository(db)
	h := &model.FundHolding{
		FundID: 1, SecurityType: "stock", SecurityID: 1, Quantity: 10,
		AveragePriceRSD: decimal.NewFromInt(100),
	}
	if err := r.Upsert(h); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetByFundAndSecurity(1, "stock", 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := r.DecrementQuantity(got.ID, 4); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	got2, err := r.GetByFundAndSecurity(1, "stock", 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got2.Quantity != 6 {
		t.Errorf("quantity=%d want 6", got2.Quantity)
	}
}

func TestClientFundPositionRepository_Ops(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)
	uid := uint64(7)
	if err := r.IncrementContribution(1, model.OwnerClient, &uid, decimal.NewFromInt(1000)); err != nil {
		t.Fatalf("increment: %v", err)
	}
	rows, err := r.ListByOwner(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d", len(rows))
	}
	total, err := r.SumTotalContributed(1)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if !total.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("total=%s want 1000", total)
	}
	got, err := r.GetByFundAndOwner(1, model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FundID != 1 {
		t.Errorf("fund mismatch")
	}
}

func TestListingDailyPriceRepository_Crud(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ListingDailyPriceInfo{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewListingDailyPriceRepository(db)
	now := time.Now()
	row := &model.ListingDailyPriceInfo{
		ListingID: 1, Date: now,
		Price: decimal.NewFromInt(110), High: decimal.NewFromInt(115),
		Low: decimal.NewFromInt(95), Change: decimal.NewFromInt(10),
		Volume: 5000,
	}
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	row2 := &model.ListingDailyPriceInfo{
		ListingID: 1, Date: now,
		Price: decimal.NewFromInt(120), High: decimal.NewFromInt(125),
		Low: decimal.NewFromInt(105), Change: decimal.NewFromInt(5),
		Volume: 6000,
	}
	if err := r.UpsertByListingAndDate(row2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, total, err := r.GetHistory(1, now.AddDate(0, -1, 0), now.AddDate(0, 0, 1), 1, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if total == 0 || len(got) == 0 {
		t.Errorf("expected history rows")
	}
}

// ---------------------------------------------------------------------------
// applySorting (helpers.go) — exercise sort fallback paths.
// ---------------------------------------------------------------------------

func TestApplySorting_VariousColumns(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Stock{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, by := range []string{"price", "name", "volume", "change", ""} {
		for _, ord := range []string{"asc", "desc", ""} {
			q := db.Model(&model.Stock{})
			q = applySorting(q, "stocks", by, ord)
			var rows []model.Stock
			if err := q.Limit(10).Find(&rows).Error; err != nil {
				t.Errorf("sort by=%s ord=%s: %v", by, ord, err)
			}
		}
	}
}

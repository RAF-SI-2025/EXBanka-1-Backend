// Tests covering the simple GORM-backed repositories that previously had no
// dedicated coverage: ExchangeRepository, ForexPairRepository,
// FuturesRepository, FundContributionRepository.
//
// Uses sqlite in-memory; ILIKE-based filters are skipped (sqlite does not
// support ILIKE).
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// ExchangeRepository
// ---------------------------------------------------------------------------

func newExchange(mic, acronym, name string) *model.StockExchange {
	return &model.StockExchange{
		Name: name, Acronym: acronym, MICCode: mic, Polity: "US",
		Currency: "USD", TimeZone: "America/New_York",
		OpenTime: "09:30", CloseTime: "16:00",
	}
}

func TestExchangeRepository_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "New York Stock Exchange")
	if err := r.Create(ex); err != nil {
		t.Fatalf("create: %v", err)
	}
	if ex.ID == 0 {
		t.Fatal("expected id")
	}
	got, err := r.GetByID(ex.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Acronym != "NYSE" {
		t.Errorf("acronym mismatch")
	}
	got2, err := r.GetByMICCode("XNYS")
	if err != nil {
		t.Fatalf("get by mic: %v", err)
	}
	if got2.ID != ex.ID {
		t.Errorf("mic match wrong id")
	}
	got3, err := r.GetByAcronym("NYSE")
	if err != nil {
		t.Fatalf("get by acronym: %v", err)
	}
	if got3.ID != ex.ID {
		t.Errorf("acronym match wrong id")
	}
}

func TestExchangeRepository_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_, err := r.GetByID(99)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestExchangeRepository_GetByMICCode_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_, err := r.GetByMICCode("XYZ")
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestExchangeRepository_GetByAcronym_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_, err := r.GetByAcronym("nope")
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestExchangeRepository_List_NoSearch(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_ = r.Create(newExchange("XNYS", "NYSE", "NYSE"))
	_ = r.Create(newExchange("XNAS", "NASDAQ", "NASDAQ"))
	got, total, err := r.List("", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
}

func TestExchangeRepository_List_DefaultsPageAndSize(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_ = r.Create(newExchange("X1", "A1", "X"))
	got, total, err := r.List("", 0, 0) // page=0, pageSize=0 → defaults applied
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
}

func TestExchangeRepository_UpsertByMICCode_Insert(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "old")
	if err := r.UpsertByMICCode(ex); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got, _ := r.GetByMICCode("XNYS")
	if got.Name != "old" {
		t.Errorf("got name %q", got.Name)
	}
}

func TestExchangeRepository_UpsertByMICCode_Update(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewExchangeRepository(db)
	_ = r.Create(newExchange("XNYS", "NYSE", "old"))
	ex := newExchange("XNYS", "NYSE2", "fresh")
	if err := r.UpsertByMICCode(ex); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ := r.GetByMICCode("XNYS")
	if got.Name != "fresh" || got.Acronym != "NYSE2" {
		t.Errorf("update fields wrong: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// FuturesRepository
// ---------------------------------------------------------------------------

func newFutures(ticker, name string, exchangeID uint64) *model.FuturesContract {
	return &model.FuturesContract{
		Ticker: ticker, Name: name,
		ContractSize: 100, ContractUnit: "barrels",
		SettlementDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		ExchangeID:     exchangeID,
		Price:          decimal.NewFromInt(70),
	}
}

func TestFuturesRepository_CreateGetByTickerAndID(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	f := newFutures("CLJ26", "Crude", ex.ID)
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(f.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Ticker != "CLJ26" {
		t.Errorf("ticker=%s", got.Ticker)
	}
	got2, err := r.GetByTicker("CLJ26")
	if err != nil {
		t.Fatalf("get by ticker: %v", err)
	}
	if got2.ID != f.ID {
		t.Errorf("ticker lookup wrong")
	}
}

func TestFuturesRepository_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFuturesRepository(db)
	if _, err := r.GetByID(99); err == nil {
		t.Error("expected error")
	}
}

func TestFuturesRepository_GetByTicker_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFuturesRepository(db)
	if _, err := r.GetByTicker("NONE"); err == nil {
		t.Error("expected error")
	}
}

func TestFuturesRepository_Update(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	f := newFutures("CLJ26", "Crude", ex.ID)
	_ = r.Create(f)
	f.Price = decimal.NewFromInt(80)
	if err := r.Update(f); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := r.GetByID(f.ID)
	if !got.Price.Equal(decimal.NewFromInt(80)) {
		t.Errorf("price=%s", got.Price)
	}
}

func TestFuturesRepository_UpdatePriceByTicker(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	_ = r.Create(newFutures("CLJ26", "Crude", ex.ID))
	if err := r.UpdatePriceByTicker("CLJ26", decimal.NewFromInt(99)); err != nil {
		t.Fatalf("update price: %v", err)
	}
	got, _ := r.GetByTicker("CLJ26")
	if !got.Price.Equal(decimal.NewFromInt(99)) {
		t.Errorf("got %s", got.Price)
	}
}

func TestFuturesRepository_UpsertByTicker_Insert(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	f := newFutures("CLJ26", "Crude", ex.ID)
	if err := r.UpsertByTicker(f); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got, _ := r.GetByTicker("CLJ26")
	if got == nil || got.Name != "Crude" {
		t.Errorf("got %+v", got)
	}
}

func TestFuturesRepository_UpsertByTicker_Update(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	_ = r.Create(newFutures("CLJ26", "Crude", ex.ID))
	upd := newFutures("CLJ26", "Crude Updated", ex.ID)
	upd.Price = decimal.NewFromInt(120)
	if err := r.UpsertByTicker(upd); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ := r.GetByTicker("CLJ26")
	if got.Name != "Crude Updated" || !got.Price.Equal(decimal.NewFromInt(120)) {
		t.Errorf("got %+v", got)
	}
}

func TestFuturesRepository_List_NoFilters(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	_ = r.Create(newFutures("CLJ26", "Crude", ex.ID))
	_ = r.Create(newFutures("GCJ26", "Gold", ex.ID))
	got, total, err := r.List(FuturesFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
}

func TestFuturesRepository_List_PriceAndDateRange(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XNYS", "NYSE", "NYSE")
	_ = exRepo.Create(ex)
	r := NewFuturesRepository(db)
	a := newFutures("AAA", "A", ex.ID)
	a.Price = decimal.NewFromInt(50)
	a.Volume = 100
	_ = r.Create(a)
	b := newFutures("BBB", "B", ex.ID)
	b.Price = decimal.NewFromInt(150)
	b.Volume = 500
	_ = r.Create(b)
	min := decimal.NewFromInt(60)
	max := decimal.NewFromInt(200)
	minVol := int64(200)
	maxVol := int64(1000)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	got, total, err := r.List(FuturesFilter{
		Page: 1, PageSize: 10,
		MinPrice: &min, MaxPrice: &max,
		MinVolume: &minVol, MaxVolume: &maxVol,
		SettlementDateFrom: &from, SettlementDateTo: &to,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Ticker != "BBB" {
		t.Errorf("got total=%d len=%d ticker=%v", total, len(got), got)
	}
}

// ---------------------------------------------------------------------------
// ForexPairRepository
// ---------------------------------------------------------------------------

func newForex(ticker, base, quote string, exchangeID uint64) *model.ForexPair {
	return &model.ForexPair{
		Ticker: ticker, Name: ticker, BaseCurrency: base, QuoteCurrency: quote,
		ExchangeRate: decimal.NewFromFloat(1.1),
		Liquidity:    "high", ExchangeID: exchangeID,
	}
}

func TestForexPairRepository_CreateGetUpdate(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XFOREX", "FX", "FX")
	_ = exRepo.Create(ex)
	r := NewForexPairRepository(db)
	fp := newForex("EURUSD", "EUR", "USD", ex.ID)
	if err := r.Create(fp); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(fp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ticker != "EURUSD" {
		t.Errorf("ticker=%s", got.Ticker)
	}
	got2, err := r.GetByTicker("EURUSD")
	if err != nil {
		t.Fatalf("get by ticker: %v", err)
	}
	if got2.ID != fp.ID {
		t.Errorf("id mismatch")
	}
	fp.ExchangeRate = decimal.NewFromFloat(1.2)
	if err := r.Update(fp); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestForexPairRepository_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewForexPairRepository(db)
	if _, err := r.GetByID(99); err == nil {
		t.Error("expected error")
	}
}

func TestForexPairRepository_GetByTicker_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewForexPairRepository(db)
	if _, err := r.GetByTicker("NONE"); err == nil {
		t.Error("expected error")
	}
}

func TestForexPairRepository_UpdatePriceByTicker(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XFOREX", "FX", "FX")
	_ = exRepo.Create(ex)
	r := NewForexPairRepository(db)
	_ = r.Create(newForex("EURUSD", "EUR", "USD", ex.ID))
	if err := r.UpdatePriceByTicker("EURUSD", decimal.NewFromFloat(1.5)); err != nil {
		t.Fatalf("update price: %v", err)
	}
	got, _ := r.GetByTicker("EURUSD")
	if !got.ExchangeRate.Equal(decimal.NewFromFloat(1.5)) {
		t.Errorf("got %s", got.ExchangeRate)
	}
}

func TestForexPairRepository_UpsertByTicker_InsertAndUpdate(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XFOREX", "FX", "FX")
	_ = exRepo.Create(ex)
	r := NewForexPairRepository(db)
	fp := newForex("EURUSD", "EUR", "USD", ex.ID)
	if err := r.UpsertByTicker(fp); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	upd := newForex("EURUSD", "EUR", "USD", ex.ID)
	upd.ExchangeRate = decimal.NewFromFloat(1.25)
	if err := r.UpsertByTicker(upd); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ := r.GetByTicker("EURUSD")
	if !got.ExchangeRate.Equal(decimal.NewFromFloat(1.25)) {
		t.Errorf("got %s", got.ExchangeRate)
	}
}

func TestForexPairRepository_List(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XFOREX", "FX", "FX")
	_ = exRepo.Create(ex)
	r := NewForexPairRepository(db)
	_ = r.Create(newForex("EURUSD", "EUR", "USD", ex.ID))
	_ = r.Create(newForex("GBPUSD", "GBP", "USD", ex.ID))
	_ = r.Create(newForex("EURGBP", "EUR", "GBP", ex.ID))
	got, total, err := r.List(ForexFilter{Page: 1, PageSize: 10, BaseCurrency: "EUR"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
	got, total, err = r.List(ForexFilter{Page: 1, PageSize: 10, QuoteCurrency: "USD"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
	got, total, err = r.List(ForexFilter{Page: 1, PageSize: 10, Liquidity: "high"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(got) != 3 {
		t.Errorf("got total=%d len=%d", total, len(got))
	}
}

func TestForexPairRepository_List_SortOrders(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exRepo := NewExchangeRepository(db)
	ex := newExchange("XFOREX", "FX", "FX")
	_ = exRepo.Create(ex)
	r := NewForexPairRepository(db)
	_ = r.Create(newForex("EURUSD", "EUR", "USD", ex.ID))
	_ = r.Create(newForex("GBPUSD", "GBP", "USD", ex.ID))
	for _, sortBy := range []string{"volume", "change", "other"} {
		for _, dir := range []string{"asc", "desc"} {
			_, _, err := r.List(ForexFilter{Page: 1, PageSize: 10, SortBy: sortBy, SortOrder: dir})
			if err != nil {
				t.Errorf("list sort=%s dir=%s: %v", sortBy, dir, err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// FundContributionRepository
// ---------------------------------------------------------------------------

func TestFundContributionRepository_CRUD(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundContribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundContributionRepository(db)
	uid := uint64(7)
	c := &model.FundContribution{
		FundID: 1, OwnerType: model.OwnerClient, OwnerID: &uid,
		Direction: "invest", AmountNative: decimal.NewFromInt(100),
		NativeCurrency: "RSD", AmountRSD: decimal.NewFromInt(100),
		FeeRSD: decimal.NewFromInt(1), Status: "pending",
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == 0 {
		t.Errorf("expected id")
	}
	if err := r.UpdateStatus(c.ID, "completed"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	rows, total, err := r.ListByFund(1, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Status != "completed" {
		t.Errorf("rows: %+v", rows)
	}
}

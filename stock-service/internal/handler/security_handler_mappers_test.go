package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeSecurityListingRepo is a hand-written stub implementing SecurityListingRepo.
type fakeSecurityListingRepo struct {
	getFn  func(securityID uint64, securityType string) (*model.Listing, error)
	listFn func(securityIDs []uint64, securityType string) ([]model.Listing, error)
}

func (f *fakeSecurityListingRepo) GetBySecurityIDAndType(id uint64, st string) (*model.Listing, error) {
	if f.getFn != nil {
		return f.getFn(id, st)
	}
	return nil, errors.New("not impl")
}

func (f *fakeSecurityListingRepo) ListBySecurityIDsAndType(ids []uint64, st string) ([]model.Listing, error) {
	if f.listFn != nil {
		return f.listFn(ids, st)
	}
	return nil, errors.New("not impl")
}

func TestResolveListingIDs_EmptyIDsReturnsEmptyMap(t *testing.T) {
	h := &SecurityHandler{listingRepo: &fakeSecurityListingRepo{}}
	got := h.resolveListingIDs(nil, "stock")
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestResolveListingIDs_NilRepoReturnsEmptyMap(t *testing.T) {
	h := &SecurityHandler{listingRepo: nil}
	got := h.resolveListingIDs([]uint64{1, 2}, "stock")
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestResolveListingIDs_BatchHappyPath(t *testing.T) {
	repo := &fakeSecurityListingRepo{
		listFn: func(ids []uint64, st string) ([]model.Listing, error) {
			if st != "stock" {
				t.Errorf("unexpected security type: %s", st)
			}
			return []model.Listing{
				{ID: 100, SecurityID: 1},
				{ID: 200, SecurityID: 2},
			}, nil
		},
	}
	h := &SecurityHandler{listingRepo: repo}
	got := h.resolveListingIDs([]uint64{1, 2, 3}, "stock")
	if got[1] != 100 || got[2] != 200 {
		t.Errorf("expected map[1->100, 2->200], got %v", got)
	}
	if got[3] != 0 { // 3 missing -> default zero
		t.Errorf("missing security should default to 0, got %d", got[3])
	}
}

func TestResolveListingIDs_RepoErrorReturnsEmptyMap(t *testing.T) {
	repo := &fakeSecurityListingRepo{
		listFn: func([]uint64, string) ([]model.Listing, error) {
			return nil, errors.New("db down")
		},
	}
	h := &SecurityHandler{listingRepo: repo}
	got := h.resolveListingIDs([]uint64{1}, "stock")
	if len(got) != 0 {
		t.Errorf("expected empty map on error, got %v", got)
	}
}

func TestResolveListingID_NilRepoReturnsZero(t *testing.T) {
	h := &SecurityHandler{listingRepo: nil}
	if got := h.resolveListingID(1, "stock"); got != 0 {
		t.Errorf("expected 0 with nil repo, got %d", got)
	}
}

func TestResolveListingID_HappyPath(t *testing.T) {
	repo := &fakeSecurityListingRepo{
		getFn: func(uint64, string) (*model.Listing, error) {
			return &model.Listing{ID: 999}, nil
		},
	}
	h := &SecurityHandler{listingRepo: repo}
	if got := h.resolveListingID(1, "stock"); got != 999 {
		t.Errorf("expected 999, got %d", got)
	}
}

func TestResolveListingID_ErrorReturnsZero(t *testing.T) {
	repo := &fakeSecurityListingRepo{
		getFn: func(uint64, string) (*model.Listing, error) {
			return nil, errors.New("not found")
		},
	}
	h := &SecurityHandler{listingRepo: repo}
	if got := h.resolveListingID(1, "stock"); got != 0 {
		t.Errorf("expected 0 on error, got %d", got)
	}
}

func TestToStockDetail_PopulatesOptionsList(t *testing.T) {
	stock := &model.Stock{
		ID:                10,
		Ticker:            "AAPL",
		Name:              "Apple",
		OutstandingShares: 1_000_000,
		DividendYield:     decimal.NewFromFloat(0.005),
		Price:             decimal.NewFromInt(180),
		High:              decimal.NewFromInt(182),
		Low:               decimal.NewFromInt(179),
		Change:            decimal.NewFromInt(1),
		Volume:            1234,
		LastRefresh:       time.Now(),
	}
	stock.Exchange.Acronym = "NASDAQ"
	stock.Exchange.Currency = "USD"
	options := []model.Option{
		{ID: 1, Ticker: "X", StrikePrice: decimal.Zero, Premium: decimal.Zero, SettlementDate: time.Now()},
	}
	d := toStockDetail(stock, options, 42)
	if d.Id != 10 {
		t.Errorf("Id: %d", d.Id)
	}
	if d.Listing.Id != 42 {
		t.Errorf("Listing.Id: %d", d.Listing.Id)
	}
	if len(d.Options) != 1 {
		t.Fatalf("Options len: %d", len(d.Options))
	}
	if d.Options[0].Id != 1 {
		t.Errorf("Options[0].Id: %d", d.Options[0].Id)
	}
}

func TestToFuturesDetail_PopulatesAllFields(t *testing.T) {
	f := &model.FuturesContract{
		ID:             7,
		Ticker:         "CL",
		Name:           "Crude Oil",
		ContractSize:   1000,
		ContractUnit:   "barrel",
		SettlementDate: time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC),
		Price:          decimal.NewFromFloat(75.50),
		High:           decimal.NewFromFloat(76.00),
		Low:            decimal.NewFromFloat(74.00),
		Volume:         500,
		LastRefresh:    time.Now(),
	}
	f.Exchange.Acronym = "NYMEX"
	f.Exchange.Currency = "USD"
	d := toFuturesDetail(f, 33)
	if d.Id != 7 {
		t.Errorf("Id: %d", d.Id)
	}
	if d.SettlementDate != "2026-12-15" {
		t.Errorf("SettlementDate: %q", d.SettlementDate)
	}
	if d.ContractSize != 1000 {
		t.Errorf("ContractSize: %d", d.ContractSize)
	}
	if d.Listing.Id != 33 {
		t.Errorf("Listing.Id: %d", d.Listing.Id)
	}
}

func TestToForexPairDetail_PopulatesAllFields(t *testing.T) {
	fp := &model.ForexPair{
		ID:            5,
		Ticker:        "EUR/USD",
		Name:          "EUR/USD",
		BaseCurrency:  "EUR",
		QuoteCurrency: "USD",
		ExchangeRate:  decimal.NewFromFloat(1.08),
		Liquidity:     "high",
		High:          decimal.NewFromFloat(1.09),
		Low:           decimal.NewFromFloat(1.07),
		Volume:        100,
		LastRefresh:   time.Now(),
	}
	fp.Exchange.Acronym = "FOREX"
	fp.Exchange.Currency = "USD"
	d := toForexPairDetail(fp, 77)
	if d.Id != 5 {
		t.Errorf("Id: %d", d.Id)
	}
	if d.BaseCurrency != "EUR" || d.QuoteCurrency != "USD" {
		t.Errorf("BaseCurrency/QuoteCurrency: %q/%q", d.BaseCurrency, d.QuoteCurrency)
	}
	if d.Liquidity != "high" {
		t.Errorf("Liquidity: %q", d.Liquidity)
	}
	if d.Listing.Id != 77 {
		t.Errorf("Listing.Id: %d", d.Listing.Id)
	}
}

func TestToPriceHistoryResponse_FormatsDateAndDecimals(t *testing.T) {
	when := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	hist := []model.ListingDailyPriceInfo{
		{
			Date:   when,
			Price:  decimal.NewFromFloat(100.50),
			High:   decimal.NewFromFloat(101.00),
			Low:    decimal.NewFromFloat(99.50),
			Change: decimal.NewFromFloat(0.50),
			Volume: 12345,
		},
	}
	resp := toPriceHistoryResponse(hist, 1)
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount: %d", resp.TotalCount)
	}
	if len(resp.History) != 1 {
		t.Fatalf("History len: %d", len(resp.History))
	}
	if resp.History[0].Date != "2026-04-01" {
		t.Errorf("Date: %q", resp.History[0].Date)
	}
	if resp.History[0].Price != "100.5000" {
		t.Errorf("Price: %q", resp.History[0].Price)
	}
	if resp.History[0].Volume != 12345 {
		t.Errorf("Volume: %d", resp.History[0].Volume)
	}
}

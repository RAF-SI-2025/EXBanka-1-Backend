package service

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
)

type fakeFuturesLookup struct{ t map[uint64]string }

func (f fakeFuturesLookup) GetByID(id uint64) (*model.FuturesContract, error) {
	if tk, ok := f.t[id]; ok {
		return &model.FuturesContract{ID: id, Ticker: tk}, nil
	}
	return nil, gorm.ErrRecordNotFound
}

type fakeForexLookup struct{ t map[uint64]string }

func (f fakeForexLookup) GetByID(id uint64) (*model.ForexPair, error) {
	if tk, ok := f.t[id]; ok {
		return &model.ForexPair{ID: id, Ticker: tk}, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func TestResolveTicker_AllTypesFound(t *testing.T) {
	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 1, Ticker: "AAPL"})
	options := newMockOptionRepo()
	options.addOption(&model.Option{ID: 2, Ticker: "AAPL-C"})
	futures := fakeFuturesLookup{t: map[uint64]string{3: "ESZ5"}}
	forex := fakeForexLookup{t: map[uint64]string{4: "EUR/USD"}}

	svc := NewWatchlistService(nil, nil, stocks, options, futures, forex)

	cases := []struct {
		secType string
		id      uint64
		want    string
	}{
		{"stock", 1, "AAPL"},
		{"option", 2, "AAPL-C"},
		{"futures", 3, "ESZ5"},
		{"forex", 4, "EUR/USD"},
		{"option", 999, ""},  // missing → empty
		{"futures", 999, ""}, // missing → empty
		{"forex", 999, ""},   // missing → empty
	}
	for _, c := range cases {
		if got := svc.resolveTicker(c.secType, c.id); got != c.want {
			t.Errorf("resolveTicker(%s,%d) = %q, want %q", c.secType, c.id, got, c.want)
		}
	}
}

func TestResolveTickerForCron_AllTypesFound(t *testing.T) {
	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 1, Ticker: "AAPL"})
	options := newMockOptionRepo()
	options.addOption(&model.Option{ID: 2, Ticker: "AAPL-C"})
	futures := fakeFuturesLookup{t: map[uint64]string{3: "ESZ5"}}
	forex := fakeForexLookup{t: map[uint64]string{4: "EUR/USD"}}

	reg := cronreg.NewRegistry("test", nil)
	cron := NewWatchlistNotificationCron(nil, stocks, options, futures, forex, &recordingWatchlistPublisher{}, time.Hour, reg)

	cases := []struct {
		secType string
		id      uint64
		want    string
	}{
		{"stock", 1, "AAPL"},
		{"option", 2, "AAPL-C"},
		{"futures", 3, "ESZ5"},
		{"forex", 4, "EUR/USD"},
		{"option", 999, ""},
		{"futures", 999, ""},
		{"forex", 999, ""},
	}
	for _, c := range cases {
		if got := resolveTickerForCron(cron, c.secType, c.id); got != c.want {
			t.Errorf("resolveTickerForCron(%s,%d) = %q, want %q", c.secType, c.id, got, c.want)
		}
	}
}

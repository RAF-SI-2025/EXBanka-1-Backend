package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
)

func TestResolveTicker(t *testing.T) {
	svc, _, _, stocks := newTestWatchlistService(t)
	stocks.addStock(&model.Stock{ID: 10, Ticker: "AAPL"})

	if got := svc.resolveTicker("stock", 10); got != "AAPL" {
		t.Errorf("stock = %q, want AAPL", got)
	}
	if got := svc.resolveTicker("stock", 999); got != "" {
		t.Errorf("missing stock = %q, want empty", got)
	}
	// options/futures/forex are not wired in this fixture → nil-guard returns "".
	if got := svc.resolveTicker("option", 1); got != "" {
		t.Errorf("option (nil repo) = %q, want empty", got)
	}
	if got := svc.resolveTicker("futures", 1); got != "" {
		t.Errorf("futures (nil repo) = %q, want empty", got)
	}
	if got := svc.resolveTicker("forex", 1); got != "" {
		t.Errorf("forex (nil repo) = %q, want empty", got)
	}
	if got := svc.resolveTicker("bogus", 1); got != "" {
		t.Errorf("unknown type = %q, want empty", got)
	}
}

func TestResolveTickerForCron(t *testing.T) {
	repo, _ := setupWatchlistCronFixture(t)
	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 20, Ticker: "TSLA"})
	pub := &recordingWatchlistPublisher{}
	cron := newWatchlistCronForTest(repo, stocks, pub)

	if got := resolveTickerForCron(cron, "stock", 20); got != "TSLA" {
		t.Errorf("stock = %q, want TSLA", got)
	}
	if got := resolveTickerForCron(cron, "stock", 999); got != "" {
		t.Errorf("missing stock = %q, want empty", got)
	}
	if got := resolveTickerForCron(cron, "option", 1); got != "" {
		t.Errorf("option (nil repo) = %q, want empty", got)
	}
	if got := resolveTickerForCron(cron, "futures", 1); got != "" {
		t.Errorf("futures (nil) = %q, want empty", got)
	}
	if got := resolveTickerForCron(cron, "forex", 1); got != "" {
		t.Errorf("forex (nil) = %q, want empty", got)
	}
	if got := resolveTickerForCron(cron, "weird", 1); got != "" {
		t.Errorf("unknown = %q, want empty", got)
	}
}

func TestWatchlistNotificationCron_Run(t *testing.T) {
	repo, _ := setupWatchlistCronFixture(t)
	stocks := newMockStockRepo()
	pub := &recordingWatchlistPublisher{}
	reg := cronreg.NewRegistry("test", nil)
	cron := NewWatchlistNotificationCron(repo, stocks, nil, nil, nil, pub, 10*time.Millisecond, reg)
	runCronBriefly(t, cron.Run)
}

func TestDailyChangePercent(t *testing.T) {
	// prev close = price - change = 106 - 6 = 100 → pct = 6%.
	got := dailyChangePercent(decimal.NewFromInt(106), decimal.NewFromInt(6))
	if !got.Equal(decimal.NewFromInt(6)) {
		t.Errorf("dailyChangePercent = %s, want 6", got)
	}
	// prev close zero → 0.
	if !dailyChangePercent(decimal.NewFromInt(5), decimal.NewFromInt(5)).IsZero() {
		t.Errorf("zero-prev change should be 0")
	}
}

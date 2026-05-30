package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// recordingWatchlistPublisher records published WatchlistPriceMoveMessages.
type recordingWatchlistPublisher struct {
	mu   sync.Mutex
	msgs []kafkamsg.WatchlistPriceMoveMessage
}

func (r *recordingWatchlistPublisher) PublishWatchlistAlert(_ context.Context, msg kafkamsg.WatchlistPriceMoveMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
	return nil
}

func (r *recordingWatchlistPublisher) published() []kafkamsg.WatchlistPriceMoveMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]kafkamsg.WatchlistPriceMoveMessage, len(r.msgs))
	copy(out, r.msgs)
	return out
}

// setupWatchlistCronFixture creates an in-memory DB with the necessary
// tables and seeds a watchlist item for a single client.
func setupWatchlistCronFixture(t *testing.T) (*repository.WatchlistRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchlistItem{},
		&model.Listing{},
		&model.StockExchange{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return repository.NewWatchlistRepository(db), db
}

// seedWatchlistItem persists a listing + watchlist item directly to the
// test DB, bypassing service validation (so we can control prices precisely).
func seedWatchlistItem(t *testing.T, db *gorm.DB, ownerID uint64, listingID, securityID uint64, secType string, price, change float64) {
	t.Helper()
	listing := &model.Listing{
		ID:           listingID,
		SecurityID:   securityID,
		SecurityType: secType,
		ExchangeID:   1,
		Price:        decimal.NewFromFloat(price),
		Change:       decimal.NewFromFloat(change),
		LastRefresh:  time.Now(),
	}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	item := &model.WatchlistItem{
		OwnerType: model.OwnerClient,
		OwnerID:   &ownerID,
		ListingID: listingID,
		AddedAt:   time.Now(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("seed watchlist item: %v", err)
	}
}

// newWatchlistCronForTest builds a WatchlistNotificationCron with a recording
// publisher and stub stock-ticker resolver.
func newWatchlistCronForTest(repo *repository.WatchlistRepository, stocks stockTickerLookup, pub *recordingWatchlistPublisher) *WatchlistNotificationCron {
	reg := cronreg.NewRegistry("test", nil)
	return NewWatchlistNotificationCron(
		repo, stocks, nil, nil, nil,
		pub,
		24*time.Hour, // interval (not used in tick-direct tests)
		reg,
	)
}

// TestWatchlistNotificationCron_ThresholdFilter verifies that only tickers
// with |daily_change_pct| >= 5% produce a notification.
func TestWatchlistNotificationCron_ThresholdFilter(t *testing.T) {
	repo, db := setupWatchlistCronFixture(t)

	// Client 1: AAPL moved -6.42% (below -5% threshold → should fire)
	// prev = 100 / (1 + 0.0678) ≈ 93.61; change = 100 - 93.61 = 6.39 => ~ -6.%
	// Simpler: price=100, change=-6.78 → prev=106.78, pct=-6.78/106.78*100 ≈ -6.35%
	seedWatchlistItem(t, db, 1, 1, 10, "stock", 100, -6.78)

	// Client 2: TSLA moved +1% (below threshold → should NOT fire)
	seedWatchlistItem(t, db, 2, 2, 20, "stock", 200, 2.0)

	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 10, Ticker: "AAPL"})
	stocks.addStock(&model.Stock{ID: 20, Ticker: "TSLA"})

	pub := &recordingWatchlistPublisher{}
	cron := newWatchlistCronForTest(repo, stocks, pub)
	cron.tick(context.Background())

	msgs := pub.published()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 notification (AAPL), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Ticker != "AAPL" {
		t.Fatalf("expected AAPL, got %s", msgs[0].Ticker)
	}
	if msgs[0].UserID != 1 {
		t.Fatalf("expected userID=1, got %d", msgs[0].UserID)
	}
}

// TestWatchlistNotificationCron_IdempotencyKey verifies that two watchlist
// items for the same (user, ticker) on the same day result in only one
// notification publish (in-memory dedup).
func TestWatchlistNotificationCron_IdempotencyKey(t *testing.T) {
	repo, db := setupWatchlistCronFixture(t)

	// Seed same user watching the same stock via two different listing IDs
	// (edge case: should only fire once per user+ticker).
	seedWatchlistItem(t, db, 1, 1, 10, "stock", 100, -6.78)
	// second listing for same security (rare but possible if seeding is odd)
	if err := db.Create(&model.Listing{
		ID: 2, SecurityID: 10, SecurityType: "stock", ExchangeID: 1,
		Price: decimal.NewFromFloat(100), Change: decimal.NewFromFloat(-6.78),
		LastRefresh: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed second listing: %v", err)
	}
	ownerID := uint64(1)
	if err := db.Create(&model.WatchlistItem{
		OwnerType: model.OwnerClient, OwnerID: &ownerID, ListingID: 2, AddedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed second watchlist item: %v", err)
	}

	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 10, Ticker: "AAPL"})

	pub := &recordingWatchlistPublisher{}
	cron := newWatchlistCronForTest(repo, stocks, pub)
	cron.tick(context.Background())

	msgs := pub.published()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 notification (deduped), got %d", len(msgs))
	}
}

// TestWatchlistNotificationCron_IdempotencyKeyFormat verifies the
// idempotency key format is "watchlist-alert-<uid>-<ticker>-<YYYYMMDD>".
func TestWatchlistNotificationCron_IdempotencyKeyFormat(t *testing.T) {
	repo, db := setupWatchlistCronFixture(t)
	seedWatchlistItem(t, db, 42, 1, 10, "stock", 100, -6.78)

	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 10, Ticker: "NVDA"})

	pub := &recordingWatchlistPublisher{}
	cron := newWatchlistCronForTest(repo, stocks, pub)
	cron.tick(context.Background())

	msgs := pub.published()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(msgs))
	}
	today := time.Now().UTC().Format("20060102")
	wantKey := "watchlist-alert-42-NVDA-" + today
	if msgs[0].IdempotencyKey != wantKey {
		t.Fatalf("idempotency key: want %q, got %q", wantKey, msgs[0].IdempotencyKey)
	}
}

// TestWatchlistNotificationCron_NoPriceSkipped verifies that listings with
// a zero price are skipped silently (no notification, no crash).
func TestWatchlistNotificationCron_NoPriceSkipped(t *testing.T) {
	repo, db := setupWatchlistCronFixture(t)
	// Price=0 should be skipped entirely.
	seedWatchlistItem(t, db, 1, 1, 10, "stock", 0, 0)

	stocks := newMockStockRepo()
	stocks.addStock(&model.Stock{ID: 10, Ticker: "AAPL"})

	pub := &recordingWatchlistPublisher{}
	cron := newWatchlistCronForTest(repo, stocks, pub)
	cron.tick(context.Background())

	msgs := pub.published()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 notifications for zero-price listing, got %d", len(msgs))
	}
}

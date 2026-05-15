package service

import (
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func newTestWatchlistService(t *testing.T) (*WatchlistService, *gorm.DB, *mockListingRepo, *mockStockRepo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.WatchlistItem{}, &model.Listing{}, &model.StockExchange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewWatchlistRepository(db)
	listing := newMockListingRepo()
	stocks := newMockStockRepo()
	svc := NewWatchlistService(repo, listing, stocks, nil, nil, nil)
	return svc, db, listing, stocks
}

func TestWatchlist_Add_ListingMissing(t *testing.T) {
	svc, _, _, _ := newTestWatchlistService(t)
	id := uint64(5)
	err := svc.Add(model.OwnerClient, &id, 999)
	if !errors.Is(err, ErrWatchlistListingNotFound) {
		t.Fatalf("want ErrWatchlistListingNotFound, got %v", err)
	}
}

func TestWatchlist_AddRemoveList(t *testing.T) {
	svc, db, listing, stocks := newTestWatchlistService(t)
	listing.addListing(&model.Listing{
		ID: 1, SecurityID: 100, SecurityType: "stock",
		Exchange: model.StockExchange{Currency: "USD"},
		Price:    decimal.NewFromFloat(50.00),
		Change:   decimal.NewFromFloat(2.50),
	})
	stocks.addStock(&model.Stock{ID: 100, Ticker: "AAPL"})

	// Persist the listing into the test DB so the JOIN in ListWithListings
	// finds it (the mock listing repo only feeds Add's existence check).
	if err := db.Create(&model.Listing{
		ID: 1, SecurityID: 100, SecurityType: "stock", ExchangeID: 1,
		Price: decimal.NewFromFloat(50.00), Change: decimal.NewFromFloat(2.50), LastRefresh: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	owner := uint64(7)
	if err := svc.Add(model.OwnerClient, &owner, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Idempotent: same add again is a no-op.
	if err := svc.Add(model.OwnerClient, &owner, 1); err != nil {
		t.Fatalf("add (re-add): %v", err)
	}

	entries, err := svc.List(model.OwnerClient, &owner, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(entries))
	}
	got := entries[0]
	if got.Ticker != "AAPL" {
		t.Fatalf("ticker: want AAPL, got %q", got.Ticker)
	}
	if !got.CurrentPrice.Equal(decimal.NewFromFloat(50.00)) {
		t.Fatalf("price: want 50, got %s", got.CurrentPrice)
	}
	// Daily change percent = 2.50 / (50 - 2.50) * 100 = 5.2631…
	wantPct := decimal.NewFromFloat(2.50).Div(decimal.NewFromFloat(47.50)).Mul(decimal.NewFromInt(100)).Round(4)
	if !got.DailyChangePercent.Equal(wantPct) {
		t.Fatalf("daily change %%: want %s, got %s", wantPct, got.DailyChangePercent)
	}

	if err := svc.Remove(model.OwnerClient, &owner, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := svc.Remove(model.OwnerClient, &owner, 1); !errors.Is(err, ErrWatchlistEntryNotFound) {
		t.Fatalf("re-remove: want ErrWatchlistEntryNotFound, got %v", err)
	}
}

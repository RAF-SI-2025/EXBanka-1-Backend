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
	if err := db.AutoMigrate(&model.Watchlist{}, &model.WatchlistItem{}, &model.Listing{}, &model.StockExchange{}); err != nil {
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
	err := svc.Add(model.OwnerClient, &id, 0, 999)
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
	if err := svc.Add(model.OwnerClient, &owner, 0, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Idempotent: same add again is a no-op.
	if err := svc.Add(model.OwnerClient, &owner, 0, 1); err != nil {
		t.Fatalf("add (re-add): %v", err)
	}

	entries, err := svc.List(model.OwnerClient, &owner, 0, "")
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

	if err := svc.Remove(model.OwnerClient, &owner, 0, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := svc.Remove(model.OwnerClient, &owner, 0, 1); !errors.Is(err, ErrWatchlistEntryNotFound) {
		t.Fatalf("re-remove: want ErrWatchlistEntryNotFound, got %v", err)
	}
}

// SP6 migration: legacy items (watchlist_id=0) get assigned to their owner's
// default list; idempotent on re-run.
func TestWatchlist_Migration(t *testing.T) {
	_, db, _, _ := newTestWatchlistService(t)
	repo := repository.NewWatchlistRepository(db)
	owner := uint64(7)
	// Two legacy items for one owner (watchlist_id default 0).
	for _, lid := range []uint64{1, 2} {
		if err := db.Create(&model.WatchlistItem{OwnerType: model.OwnerClient, OwnerID: &owner, ListingID: lid}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := MigrateWatchlistsToNamedLists(db, repo); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	def, err := repo.GetOrCreateDefault(model.OwnerClient, &owner)
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	var migrated int64
	db.Model(&model.WatchlistItem{}).Where("watchlist_id = ?", def.ID).Count(&migrated)
	if migrated != 2 {
		t.Fatalf("expected 2 items assigned to default list, got %d", migrated)
	}
	// Re-run is a no-op (no orphans left).
	if err := MigrateWatchlistsToNamedLists(db, repo); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var lists int64
	db.Model(&model.Watchlist{}).Count(&lists)
	if lists != 1 {
		t.Fatalf("expected 1 default list (idempotent), got %d", lists)
	}
}

// SP6: named lists — create multiple, same listing in two lists, ownership
// enforcement, and delete.
func TestWatchlist_NamedLists(t *testing.T) {
	svc, db, listing, _ := newTestWatchlistService(t)
	listing.addListing(&model.Listing{ID: 1, SecurityID: 100, SecurityType: "stock", Exchange: model.StockExchange{Currency: "USD"}, Price: decimal.NewFromInt(50)})
	if err := db.Create(&model.Listing{ID: 1, SecurityID: 100, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(50), LastRefresh: time.Now()}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	owner := uint64(7)

	tech, err := svc.CreateWatchlist(model.OwnerClient, &owner, "tech")
	if err != nil {
		t.Fatalf("create tech: %v", err)
	}
	fav, err := svc.CreateWatchlist(model.OwnerClient, &owner, "favorites")
	if err != nil {
		t.Fatalf("create favorites: %v", err)
	}
	// Same listing in two different lists is allowed.
	if err := svc.Add(model.OwnerClient, &owner, tech.ID, 1); err != nil {
		t.Fatalf("add to tech: %v", err)
	}
	if err := svc.Add(model.OwnerClient, &owner, fav.ID, 1); err != nil {
		t.Fatalf("add to favorites: %v", err)
	}
	techItems, _ := svc.List(model.OwnerClient, &owner, tech.ID, "")
	favItems, _ := svc.List(model.OwnerClient, &owner, fav.ID, "")
	if len(techItems) != 1 || len(favItems) != 1 {
		t.Fatalf("each list should have 1 item, got tech=%d fav=%d", len(techItems), len(favItems))
	}

	// ListWatchlists includes the lazily-created default + the two named lists.
	lists, err := svc.ListWatchlists(model.OwnerClient, &owner)
	if err != nil {
		t.Fatalf("list watchlists: %v", err)
	}
	if len(lists) != 3 {
		t.Fatalf("expected 3 lists (default + tech + favorites), got %d", len(lists))
	}

	// Another owner cannot touch this owner's list.
	other := uint64(8)
	if err := svc.Add(model.OwnerClient, &other, tech.ID, 1); !errors.Is(err, ErrWatchlistForbidden) {
		t.Fatalf("cross-owner add: want ErrWatchlistForbidden, got %v", err)
	}
	if err := svc.DeleteWatchlist(model.OwnerClient, &other, tech.ID); !errors.Is(err, ErrWatchlistForbidden) {
		t.Fatalf("cross-owner delete: want ErrWatchlistForbidden, got %v", err)
	}

	// Delete the tech list (and its item).
	if err := svc.DeleteWatchlist(model.OwnerClient, &owner, tech.ID); err != nil {
		t.Fatalf("delete tech: %v", err)
	}
	if _, err := svc.List(model.OwnerClient, &owner, tech.ID, ""); !errors.Is(err, ErrWatchlistNotFound) {
		t.Fatalf("list deleted: want ErrWatchlistNotFound, got %v", err)
	}
}

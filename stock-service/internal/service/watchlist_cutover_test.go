// Tests for DedupeWatchlistsAndEnforceUniqueness: the startup migration that
// removes duplicate (bank, NULL, name) watchlists and creates the partial
// unique index that prevents future duplicates.
//
// TDD: written BEFORE the function exists (will fail to compile until the
// function is added to watchlist_cutover.go).
package service

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newDedupTestDB opens a fresh in-memory SQLite DB and auto-migrates the
// Watchlist and WatchlistItem models.
func newDedupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Watchlist{}, &model.WatchlistItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestDedupeWatchlistsAndEnforceUniqueness seeds three duplicate
// (bank, NULL, "My Watchlist") rows (bypassing the now-idempotent
// CreateWatchlist to simulate the corrupt live state), attaches items to two
// of them — including a cross-dup collision (same listing_id on wl2 and wl3)
// and a unique listing on wl2 — then runs the dedup and asserts:
//
//   - Exactly ONE (bank,NULL,"My Watchlist") row remains (the MIN id).
//   - Its items are the correct union: the colliding listing once + the unique one.
//   - A subsequent direct db.Create of a second (bank,NULL,"My Watchlist") now
//     fails because the partial unique index was created.
//   - Running the dedup a second time is safe (idempotent, no error).
func TestDedupeWatchlistsAndEnforceUniqueness(t *testing.T) {
	db := newDedupTestDB(t)

	// Seed 3 duplicate (bank, NULL, "My Watchlist") rows directly.
	// At this point no partial unique index exists, so NULL-owner dups succeed.
	// db.Create triggers BeforeSave which validates (bank + nil) — that is valid.
	var wlIDs [3]uint64
	for i := range wlIDs {
		w := &model.Watchlist{OwnerType: model.OwnerBank, OwnerID: nil, Name: model.DefaultWatchlistName}
		if err := db.Create(w).Error; err != nil {
			t.Fatalf("seed watchlist %d: %v", i+1, err)
		}
		wlIDs[i] = w.ID
	}
	keepID := wlIDs[0] // MIN id is the keeper

	// Attach items:
	//   wl2 + wl3 both have listing_id=100  → collision path
	//   wl2 also has listing_id=200          → unique listing
	seeds := []struct {
		wlIdx     int
		listingID uint64
	}{
		{1, 100}, // wlIDs[1] (wl2), listing 100
		{2, 100}, // wlIDs[2] (wl3), listing 100 — cross-dup collision
		{1, 200}, // wlIDs[1] (wl2), listing 200 — unique
	}
	for _, s := range seeds {
		item := &model.WatchlistItem{
			WatchlistID: wlIDs[s.wlIdx],
			OwnerType:   model.OwnerBank,
			OwnerID:     nil,
			ListingID:   s.listingID,
		}
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("seed item wlIdx=%d lid=%d: %v", s.wlIdx, s.listingID, err)
		}
	}

	// --- Run dedup ---
	if err := DedupeWatchlistsAndEnforceUniqueness(db); err != nil {
		t.Fatalf("DedupeWatchlistsAndEnforceUniqueness: %v", err)
	}

	// Exactly 1 (bank, NULL, "My Watchlist") row remains.
	var wlCount int64
	db.Model(&model.Watchlist{}).
		Where("owner_type = ? AND owner_id IS NULL AND name = ?", string(model.OwnerBank), model.DefaultWatchlistName).
		Count(&wlCount)
	if wlCount != 1 {
		t.Errorf("expected 1 watchlist row after dedup, got %d", wlCount)
	}

	// The surviving row must be the minimum id.
	var survivor model.Watchlist
	db.Where("owner_type = ? AND owner_id IS NULL AND name = ?", string(model.OwnerBank), model.DefaultWatchlistName).
		First(&survivor)
	if survivor.ID != keepID {
		t.Errorf("expected surviving id=%d (MIN), got id=%d", keepID, survivor.ID)
	}

	// Keeper has exactly 2 items: listing_id=100 (once) and listing_id=200.
	var itemCount int64
	db.Model(&model.WatchlistItem{}).Where("watchlist_id = ?", keepID).Count(&itemCount)
	if itemCount != 2 {
		t.Errorf("expected 2 items on keeper, got %d", itemCount)
	}

	var listingIDs []uint64
	db.Model(&model.WatchlistItem{}).Where("watchlist_id = ?", keepID).Pluck("listing_id", &listingIDs)
	seen := make(map[uint64]bool)
	for _, lid := range listingIDs {
		if seen[lid] {
			t.Errorf("duplicate listing_id=%d on keeper", lid)
		}
		seen[lid] = true
	}
	if !seen[100] || !seen[200] {
		t.Errorf("expected listing_ids {100,200} on keeper, got %v", listingIDs)
	}

	// Partial unique index was created: a second (bank,NULL,"My Watchlist") must now fail.
	extra := &model.Watchlist{OwnerType: model.OwnerBank, OwnerID: nil, Name: model.DefaultWatchlistName}
	if err := db.Create(extra).Error; err == nil {
		t.Error("expected unique constraint violation for second (bank,NULL,'My Watchlist'), got nil")
	}

	// Idempotent: second run must not error.
	if err := DedupeWatchlistsAndEnforceUniqueness(db); err != nil {
		t.Fatalf("second DedupeWatchlistsAndEnforceUniqueness run: %v", err)
	}
}

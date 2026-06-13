// Tests for WatchlistRepository.CreateWatchlist idempotency, covering the
// NULL-owner (bank) unique-index bug: Postgres and SQLite both treat NULLs as
// DISTINCT in unique indexes, so ON CONFLICT DO NOTHING never fires for
// bank-owned rows, creating duplicates on every call.
//
// TDD: these tests are written BEFORE the fix so they can be used to confirm
// the before/after behaviour.
package repository

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newWatchlistTestDB opens a fresh in-memory SQLite DB and auto-migrates
// the Watchlist + WatchlistItem models.
func newWatchlistTestDB(t *testing.T) *gorm.DB {
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

// TestCreateWatchlist_BankNilOwner_Idempotent creates a bank-owned watchlist
// twice and asserts exactly one row exists and both calls return the same ID.
//
// Against the OLD code (ON CONFLICT DO NOTHING only) this FAILS because SQLite
// (and Postgres) treat NULLs as DISTINCT in unique indexes, so the conflict
// never fires and a new row is inserted on every call.
// After the Part-1 fix (explicit getByOwnerName first) this passes.
func TestCreateWatchlist_BankNilOwner_Idempotent(t *testing.T) {
	db := newWatchlistTestDB(t)
	repo := NewWatchlistRepository(db)

	w1 := &model.Watchlist{OwnerType: model.OwnerBank, OwnerID: nil, Name: "fav1"}
	if err := repo.CreateWatchlist(w1); err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstID := w1.ID
	if firstID == 0 {
		t.Fatal("first create returned zero ID")
	}

	// Second call with a fresh struct — must return the same row.
	w2 := &model.Watchlist{OwnerType: model.OwnerBank, OwnerID: nil, Name: "fav1"}
	if err := repo.CreateWatchlist(w2); err != nil {
		t.Fatalf("second create: %v", err)
	}

	if w2.ID != firstID {
		t.Errorf("second create returned ID %d, want %d (duplicate created)", w2.ID, firstID)
	}

	var count int64
	db.Model(&model.Watchlist{}).
		Where("owner_type = ? AND owner_id IS NULL AND name = ?", string(model.OwnerBank), "fav1").
		Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row in DB, got %d (bug: ON CONFLICT DO NOTHING did not fire for NULL owner)", count)
	}
}

// TestCreateWatchlist_Client_Idempotent verifies that non-NULL client owners
// are also idempotent (the ON CONFLICT path handles them, but the new explicit
// pre-check must not break them).
func TestCreateWatchlist_Client_Idempotent(t *testing.T) {
	db := newWatchlistTestDB(t)
	repo := NewWatchlistRepository(db)

	ownerID := uint64(5)
	w1 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &ownerID, Name: "fav1"}
	if err := repo.CreateWatchlist(w1); err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstID := w1.ID
	if firstID == 0 {
		t.Fatal("first create returned zero ID")
	}

	ownerID2 := uint64(5) // same value, different pointer
	w2 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &ownerID2, Name: "fav1"}
	if err := repo.CreateWatchlist(w2); err != nil {
		t.Fatalf("second create: %v", err)
	}

	if w2.ID != firstID {
		t.Errorf("second create returned ID %d, want %d", w2.ID, firstID)
	}

	var count int64
	db.Model(&model.Watchlist{}).
		Where("owner_type = ? AND owner_id = ? AND name = ?", string(model.OwnerClient), ownerID, "fav1").
		Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row, got %d", count)
	}
}

// TestCreateWatchlist_DifferentOwners_SameName_Allowed checks that different
// owners CAN use the same list name — only (owner, name) must be unique, not
// just (name).
func TestCreateWatchlist_DifferentOwners_SameName_Allowed(t *testing.T) {
	db := newWatchlistTestDB(t)
	repo := NewWatchlistRepository(db)

	id5 := uint64(5)
	id6 := uint64(6)

	w1 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &id5, Name: "fav1"}
	if err := repo.CreateWatchlist(w1); err != nil {
		t.Fatalf("client 5: %v", err)
	}

	w2 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &id6, Name: "fav1"}
	if err := repo.CreateWatchlist(w2); err != nil {
		t.Fatalf("client 6: %v", err)
	}

	w3 := &model.Watchlist{OwnerType: model.OwnerBank, OwnerID: nil, Name: "fav1"}
	if err := repo.CreateWatchlist(w3); err != nil {
		t.Fatalf("bank: %v", err)
	}

	// All three must be distinct rows.
	ids := []uint64{w1.ID, w2.ID, w3.ID}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if ids[i] == ids[j] {
				t.Errorf("owners returned same ID %d — should be distinct rows", ids[i])
			}
		}
	}

	var count int64
	db.Model(&model.Watchlist{}).Where("name = ?", "fav1").Count(&count)
	if count != 3 {
		t.Errorf("expected 3 distinct rows, got %d", count)
	}
}

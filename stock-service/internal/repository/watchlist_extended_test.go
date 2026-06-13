// Tests for WatchlistRepository — GetOrCreateDefault, GetWatchlist,
// ListWatchlists, DeleteWatchlist, Add, RemoveFromList,
// ListWithListingsByWatchlist, ListAllClientWatchlistItems.
package repository

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newWatchlistExtTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Watchlist{}, &model.WatchlistItem{}, &model.Listing{}); err != nil {
		t.Fatalf("migrate watchlist tables: %v", err)
	}
	return db
}

func seedListing(t *testing.T, db *gorm.DB, securityID uint64, secType string) *model.Listing {
	t.Helper()
	l := &model.Listing{
		SecurityID:   securityID,
		SecurityType: secType,
		Price:        decimal.NewFromFloat(100),
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	return l
}

func TestWatchlistRepository_GetOrCreateDefault(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(1)
	w, err := r.GetOrCreateDefault(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("get or create: %v", err)
	}
	if w.ID == 0 {
		t.Fatal("expected non-zero id")
	}
	if w.Name != model.DefaultWatchlistName {
		t.Errorf("name=%q want %q", w.Name, model.DefaultWatchlistName)
	}

	// second call should return the same row
	w2, err := r.GetOrCreateDefault(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("second get or create: %v", err)
	}
	if w2.ID != w.ID {
		t.Errorf("expected same id=%d, got %d", w.ID, w2.ID)
	}
}

func TestWatchlistRepository_GetWatchlist(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(2)
	w, _ := r.GetOrCreateDefault(model.OwnerClient, &uid)

	got, err := r.GetWatchlist(w.ID)
	if err != nil {
		t.Fatalf("get watchlist: %v", err)
	}
	if got.ID != w.ID {
		t.Errorf("id mismatch")
	}

	// not found
	if _, err := r.GetWatchlist(9999); err == nil {
		t.Error("expected error for missing watchlist")
	}
}

func TestWatchlistRepository_ListWatchlists(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(3)
	// create two named lists
	w1 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &uid, Name: "Tech"}
	w2 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &uid, Name: "Energy"}
	if err := r.CreateWatchlist(w1); err != nil {
		t.Fatalf("create w1: %v", err)
	}
	if err := r.CreateWatchlist(w2); err != nil {
		t.Fatalf("create w2: %v", err)
	}

	lists, err := r.ListWatchlists(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(lists) != 2 {
		t.Errorf("expected 2 lists, got %d", len(lists))
	}
}

func TestWatchlistRepository_DeleteWatchlist(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(4)
	w, _ := r.GetOrCreateDefault(model.OwnerClient, &uid)

	// add an item first
	l := seedListing(t, db, 1, "stock")
	_ = r.Add(&model.WatchlistItem{
		WatchlistID: w.ID,
		OwnerType:   model.OwnerClient,
		OwnerID:     &uid,
		ListingID:   l.ID,
	})

	removed, err := r.DeleteWatchlist(w.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// second delete returns false
	removed2, err := r.DeleteWatchlist(w.ID)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if removed2 {
		t.Error("expected removed=false on second delete")
	}

	// non-existent watchlist
	removed3, err := r.DeleteWatchlist(9999)
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if removed3 {
		t.Error("expected removed=false for missing watchlist")
	}
}

func TestWatchlistRepository_Add_And_Remove(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(5)
	w, _ := r.GetOrCreateDefault(model.OwnerClient, &uid)
	l := seedListing(t, db, 10, "stock")

	// add
	item := &model.WatchlistItem{
		WatchlistID: w.ID,
		OwnerType:   model.OwnerClient,
		OwnerID:     &uid,
		ListingID:   l.ID,
	}
	if err := r.Add(item); err != nil {
		t.Fatalf("add: %v", err)
	}

	// idempotent add
	item2 := &model.WatchlistItem{
		WatchlistID: w.ID,
		OwnerType:   model.OwnerClient,
		OwnerID:     &uid,
		ListingID:   l.ID,
	}
	if err := r.Add(item2); err != nil {
		t.Fatalf("add duplicate: %v", err)
	}

	// remove
	removed, err := r.RemoveFromList(w.ID, l.ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// second remove returns false
	removed2, err := r.RemoveFromList(w.ID, l.ID)
	if err != nil {
		t.Fatalf("remove again: %v", err)
	}
	if removed2 {
		t.Error("expected removed=false on second remove")
	}
}

func TestWatchlistRepository_ListWithListingsByWatchlist(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(6)
	w, _ := r.GetOrCreateDefault(model.OwnerClient, &uid)

	// seed 2 listings
	l1 := seedListing(t, db, 100, "stock")
	l2 := seedListing(t, db, 200, "futures")

	_ = r.Add(&model.WatchlistItem{WatchlistID: w.ID, OwnerType: model.OwnerClient, OwnerID: &uid, ListingID: l1.ID})
	_ = r.Add(&model.WatchlistItem{WatchlistID: w.ID, OwnerType: model.OwnerClient, OwnerID: &uid, ListingID: l2.ID})

	// all items
	rows, err := r.ListWithListingsByWatchlist(w.ID, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2, got %d", len(rows))
	}

	// filter by type
	stockRows, err := r.ListWithListingsByWatchlist(w.ID, "stock")
	if err != nil {
		t.Fatalf("list stock: %v", err)
	}
	if len(stockRows) != 1 {
		t.Errorf("expected 1 stock, got %d", len(stockRows))
	}
}

func TestWatchlistRepository_ListAllClientWatchlistItems(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid1 := uint64(7)
	uid2 := uint64(8)

	// client 7: 2 items
	w1, _ := r.GetOrCreateDefault(model.OwnerClient, &uid1)
	l1 := seedListing(t, db, 1000, "stock")
	l2 := seedListing(t, db, 2000, "stock")
	_ = r.Add(&model.WatchlistItem{WatchlistID: w1.ID, OwnerType: model.OwnerClient, OwnerID: &uid1, ListingID: l1.ID})
	_ = r.Add(&model.WatchlistItem{WatchlistID: w1.ID, OwnerType: model.OwnerClient, OwnerID: &uid1, ListingID: l2.ID})

	// client 8: 1 item
	w2, _ := r.GetOrCreateDefault(model.OwnerClient, &uid2)
	_ = r.Add(&model.WatchlistItem{WatchlistID: w2.ID, OwnerType: model.OwnerClient, OwnerID: &uid2, ListingID: l1.ID})

	all, err := r.ListAllClientWatchlistItems("")
	if err != nil {
		t.Fatalf("list all client items: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 items, got %d", len(all))
	}

	// with type filter
	stockAll, err := r.ListAllClientWatchlistItems("stock")
	if err != nil {
		t.Fatalf("list stock client items: %v", err)
	}
	if len(stockAll) != 3 {
		t.Errorf("expected 3 stock items, got %d", len(stockAll))
	}
}

func TestWatchlistRepository_CreateWatchlist_Idempotent(t *testing.T) {
	db := newWatchlistExtTestDB(t)
	r := NewWatchlistRepository(db)

	uid := uint64(9)
	w1 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &uid, Name: "MyList"}
	if err := r.CreateWatchlist(w1); err != nil {
		t.Fatalf("create: %v", err)
	}
	origID := w1.ID

	// second create with same (owner, name) should return the existing row
	w2 := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &uid, Name: "MyList"}
	if err := r.CreateWatchlist(w2); err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
	if w2.ID != origID {
		t.Errorf("expected same id=%d, got %d", origID, w2.ID)
	}
}

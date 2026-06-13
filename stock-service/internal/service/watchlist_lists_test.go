package service

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestWatchlist_CreateWatchlist_Validation(t *testing.T) {
	svc, _, _, _ := newTestWatchlistService(t)
	owner := uint64(7)
	if _, err := svc.CreateWatchlist(model.OwnerClient, &owner, ""); !errors.Is(err, ErrWatchlistNameInvalid) {
		t.Errorf("empty name should be invalid, got %v", err)
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.CreateWatchlist(model.OwnerClient, &owner, string(long)); !errors.Is(err, ErrWatchlistNameInvalid) {
		t.Errorf("over-long name should be invalid, got %v", err)
	}
	w, err := svc.CreateWatchlist(model.OwnerClient, &owner, "Tech")
	if err != nil || w.ID == 0 || w.Name != "Tech" {
		t.Fatalf("create: %v %+v", err, w)
	}
}

func TestWatchlist_ListWatchlists_CreatesDefault(t *testing.T) {
	svc, _, _, _ := newTestWatchlistService(t)
	owner := uint64(7)
	lists, err := svc.ListWatchlists(model.OwnerClient, &owner)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(lists) < 1 {
		t.Errorf("expected at least the lazily-created default list, got %d", len(lists))
	}
}

func TestWatchlist_DeleteWatchlist_OwnershipAndSuccess(t *testing.T) {
	svc, _, _, _ := newTestWatchlistService(t)
	owner := uint64(7)
	w, err := svc.CreateWatchlist(model.OwnerClient, &owner, "ToDelete")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Another owner cannot delete it.
	other := uint64(99)
	if err := svc.DeleteWatchlist(model.OwnerClient, &other, w.ID); !errors.Is(err, ErrWatchlistForbidden) {
		t.Errorf("cross-owner delete should be forbidden, got %v", err)
	}
	// Owner deletes successfully.
	if err := svc.DeleteWatchlist(model.OwnerClient, &owner, w.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Deleting a non-existent list → not found.
	if err := svc.DeleteWatchlist(model.OwnerClient, &owner, 99999); !errors.Is(err, ErrWatchlistNotFound) {
		t.Errorf("missing list delete should be not-found, got %v", err)
	}
}

func TestWatchlist_Add_DefaultList_AndRemoveMissing(t *testing.T) {
	svc, db, listings, _ := newTestWatchlistService(t)
	owner := uint64(7)
	listings.addListing(&model.Listing{ID: 1, SecurityID: 100, SecurityType: "stock", Price: decimal.NewFromInt(50)})
	if err := db.Create(&model.Listing{ID: 1, SecurityID: 100, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(50)}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	// watchlistID 0 → default list (created lazily by authorizeList).
	if err := svc.Add(model.OwnerClient, &owner, 0, 1); err != nil {
		t.Fatalf("add to default: %v", err)
	}
	// Remove a listing that isn't on the list → entry-not-found.
	if err := svc.Remove(model.OwnerClient, &owner, 0, 999); !errors.Is(err, ErrWatchlistEntryNotFound) {
		t.Errorf("removing absent entry should be entry-not-found, got %v", err)
	}
}

func TestWatchlist_AuthorizeList_NamedNotFound(t *testing.T) {
	svc, _, _, _ := newTestWatchlistService(t)
	owner := uint64(7)
	// Add to a non-existent named list → not found.
	if err := svc.Add(model.OwnerClient, &owner, 4242, 1); !errors.Is(err, ErrWatchlistNotFound) {
		t.Errorf("named-list not found expected, got %v", err)
	}
}

func TestWatchlistOwnerEqual(t *testing.T) {
	a, b := uint64(1), uint64(1)
	c := uint64(2)
	if !watchlistOwnerEqual(nil, nil) {
		t.Error("nil,nil equal")
	}
	if watchlistOwnerEqual(&a, nil) {
		t.Error("a,nil not equal")
	}
	if !watchlistOwnerEqual(&a, &b) {
		t.Error("1,1 equal")
	}
	if watchlistOwnerEqual(&a, &c) {
		t.Error("1,2 not equal")
	}
}

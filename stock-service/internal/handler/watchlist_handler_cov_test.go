package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newWatchlistHandlerForCov(t *testing.T) (*WatchlistHandler, *gorm.DB) {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.Watchlist{}, &model.WatchlistItem{}, &model.Listing{}, &model.Stock{})
	svc := service.NewWatchlistService(
		repository.NewWatchlistRepository(db),
		repository.NewListingRepository(db),
		repository.NewStockRepository(db),
		nil, nil, nil,
	)
	return NewWatchlistHandler(svc), db
}

func seedWatchlistStockListing(t *testing.T, db *gorm.DB, listingID, securityID uint64, ticker string) {
	t.Helper()
	if err := db.Create(&model.Stock{ID: securityID, Ticker: ticker}).Error; err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	if err := db.Create(&model.Listing{
		ID: listingID, SecurityID: securityID, SecurityType: "stock",
		Price: decimal.NewFromFloat(100), Change: decimal.NewFromFloat(5),
	}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
}

func TestWatchlistHandler_AddListRemove(t *testing.T) {
	h, db := newWatchlistHandlerForCov(t)
	seedWatchlistStockListing(t, db, 1, 100, "AAPL")
	ctx := context.Background()

	added, err := h.AddItem(ctx, &pb.AddWatchlistItemRequest{OwnerType: "client", OwnerId: 7, ListingId: 1})
	testutil.RequireNoGRPCError(t, err)
	if added.GetListingId() != 1 {
		t.Fatalf("want listing 1, got %d", added.GetListingId())
	}
	if added.GetTicker() != "AAPL" {
		t.Fatalf("want enriched ticker AAPL, got %q", added.GetTicker())
	}
	if added.GetCurrentPrice() == "" {
		t.Fatal("expected current price")
	}

	list, err := h.ListMy(ctx, &pb.ListMyWatchlistRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(list.GetItems()) != 1 {
		t.Fatalf("want 1 item, got %d", len(list.GetItems()))
	}

	// Filter by listing type stock → still present.
	stockList, err := h.ListMy(ctx, &pb.ListMyWatchlistRequest{OwnerType: "client", OwnerId: 7, ListingType: "stock"})
	testutil.RequireNoGRPCError(t, err)
	if len(stockList.GetItems()) != 1 {
		t.Fatalf("want 1 stock item, got %d", len(stockList.GetItems()))
	}

	rm, err := h.RemoveItem(ctx, &pb.RemoveWatchlistItemRequest{OwnerType: "client", OwnerId: 7, ListingId: 1})
	testutil.RequireNoGRPCError(t, err)
	if !rm.GetRemoved() {
		t.Fatal("expected removed=true")
	}

	// Removing again → not found.
	_, err = h.RemoveItem(ctx, &pb.RemoveWatchlistItemRequest{OwnerType: "client", OwnerId: 7, ListingId: 1})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestWatchlistHandler_AddErrors(t *testing.T) {
	h, _ := newWatchlistHandlerForCov(t)
	ctx := context.Background()

	// Invalid owner type.
	_, err := h.AddItem(ctx, &pb.AddWatchlistItemRequest{OwnerType: "bad", ListingId: 1})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Missing listing_id.
	_, err = h.AddItem(ctx, &pb.AddWatchlistItemRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Non-existent listing → NotFound.
	_, err = h.AddItem(ctx, &pb.AddWatchlistItemRequest{OwnerType: "client", OwnerId: 7, ListingId: 999})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Remove missing listing_id.
	_, err = h.RemoveItem(ctx, &pb.RemoveWatchlistItemRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.RemoveItem(ctx, &pb.RemoveWatchlistItemRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// ListMy invalid owner.
	_, err = h.ListMy(ctx, &pb.ListMyWatchlistRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestWatchlistHandler_NamedListsLifecycle(t *testing.T) {
	h, _ := newWatchlistHandlerForCov(t)
	ctx := context.Background()

	created, err := h.CreateWatchlist(ctx, &pb.CreateWatchlistRequest{OwnerType: "client", OwnerId: 7, Name: "tech"})
	testutil.RequireNoGRPCError(t, err)
	if created.GetName() != "tech" || created.GetId() == 0 {
		t.Fatalf("unexpected create: %+v", created)
	}

	// ListWatchlists lazily makes the default list, so we should see >= 2.
	lists, err := h.ListWatchlists(ctx, &pb.ListWatchlistsRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(lists.GetWatchlists()) < 2 {
		t.Fatalf("want >=2 lists (default + tech), got %d", len(lists.GetWatchlists()))
	}

	// Delete the named list.
	del, err := h.DeleteWatchlist(ctx, &pb.DeleteWatchlistRequest{OwnerType: "client", OwnerId: 7, WatchlistId: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if !del.GetRemoved() {
		t.Fatal("expected removed=true")
	}

	// Delete missing watchlist_id → InvalidArgument.
	_, err = h.DeleteWatchlist(ctx, &pb.DeleteWatchlistRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestWatchlistHandler_NamedListsOwnerErrors(t *testing.T) {
	h, _ := newWatchlistHandlerForCov(t)
	ctx := context.Background()

	_, err := h.CreateWatchlist(ctx, &pb.CreateWatchlistRequest{OwnerType: "bad", Name: "x"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Empty name → InvalidArgument from the service.
	_, err = h.CreateWatchlist(ctx, &pb.CreateWatchlistRequest{OwnerType: "client", OwnerId: 7, Name: ""})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	_, err = h.ListWatchlists(ctx, &pb.ListWatchlistsRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	_, err = h.DeleteWatchlist(ctx, &pb.DeleteWatchlistRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Delete a non-existent named list → NotFound.
	_, err = h.DeleteWatchlist(ctx, &pb.DeleteWatchlistRequest{OwnerType: "client", OwnerId: 7, WatchlistId: 4242})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestWatchlistHandler_BankOwnerOmitsID(t *testing.T) {
	h, _ := newWatchlistHandlerForCov(t)
	ctx := context.Background()
	// Bank owner does not require owner_id.
	_, err := h.CreateWatchlist(ctx, &pb.CreateWatchlistRequest{OwnerType: "bank", Name: "bank-list"})
	testutil.RequireNoGRPCError(t, err)
}

func TestNewWatchlistHandler_Constructor(t *testing.T) {
	if NewWatchlistHandler(nil) == nil {
		t.Fatal("nil handler")
	}
}

package handler

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newRecurringOrderHandlerForCov(t *testing.T) (*RecurringOrderHandler, *repository.ListingRepository) {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.RecurringOrder{}, &model.Listing{})
	repo := repository.NewRecurringOrderRepository(db)
	listingRepo := repository.NewListingRepository(db)
	svc := service.NewRecurringOrderService(repo, listingRepo, nil, nil)
	return NewRecurringOrderHandler(svc), listingRepo
}

func seedCovListing(t *testing.T, listingRepo *repository.ListingRepository, id uint64) {
	t.Helper()
	if err := listingRepo.Create(&model.Listing{ID: id, SecurityID: id, SecurityType: "stock"}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
}

func TestRecurringOrderHandler_CreateGetListLifecycle(t *testing.T) {
	h, listingRepo := newRecurringOrderHandlerForCov(t)
	seedCovListing(t, listingRepo, 1)
	ctx := context.Background()

	created, err := h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{
		OwnerType: "client", OwnerId: 7,
		ListingId: 1, AccountId: 5,
		Side: "buy", Quantity: 10,
		Interval: "weekly", DayOfWeek: 3,
	})
	testutil.RequireNoGRPCError(t, err)
	if created.GetId() == 0 {
		t.Fatal("expected non-zero id")
	}
	if created.GetInterval() != "weekly" || created.GetDayOfWeek() != 3 {
		t.Fatalf("unexpected proto: %+v", created)
	}
	if created.GetStatus() != model.RecurringOrderStatusActive {
		t.Fatalf("want active, got %q", created.GetStatus())
	}
	if created.GetNextRunUnix() == 0 {
		t.Fatal("expected NextRun populated")
	}

	// Get round-trips.
	got, err := h.GetOrder(ctx, &pb.GetRecurringOrderRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if got.GetId() != created.GetId() {
		t.Fatalf("get id mismatch: %d vs %d", got.GetId(), created.GetId())
	}

	// Pause → status paused.
	paused, err := h.PauseOrder(ctx, &pb.GetRecurringOrderRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if paused.GetStatus() != model.RecurringOrderStatusPaused {
		t.Fatalf("want paused, got %q", paused.GetStatus())
	}

	// Resume → active again.
	resumed, err := h.ResumeOrder(ctx, &pb.GetRecurringOrderRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if resumed.GetStatus() != model.RecurringOrderStatusActive {
		t.Fatalf("want active, got %q", resumed.GetStatus())
	}

	// ListMy returns the one row.
	list, err := h.ListMy(ctx, &pb.ListMyRecurringOrdersRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(list.GetItems()) != 1 {
		t.Fatalf("want 1 item, got %d", len(list.GetItems()))
	}

	// Cancel → status cancelled.
	cancelled, err := h.CancelOrder(ctx, &pb.GetRecurringOrderRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if cancelled.GetStatus() != model.RecurringOrderStatusCancelled {
		t.Fatalf("want cancelled, got %q", cancelled.GetStatus())
	}
}

func TestRecurringOrderHandler_CreateMonthlyWithEndDate(t *testing.T) {
	h, listingRepo := newRecurringOrderHandlerForCov(t)
	seedCovListing(t, listingRepo, 2)
	ctx := context.Background()

	start := time.Now().UTC().Unix()
	end := time.Now().UTC().AddDate(1, 0, 0).Unix()
	created, err := h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{
		OwnerType: "client", OwnerId: 9,
		ListingId: 2, AccountId: 6,
		Side: "sell", Quantity: 4,
		Interval: "monthly", DayOfMonth: 15,
		StartDateUnix: start, EndDateUnix: end,
	})
	testutil.RequireNoGRPCError(t, err)
	if created.GetDayOfMonth() != 15 {
		t.Fatalf("want day_of_month 15, got %d", created.GetDayOfMonth())
	}
	if created.GetEndDateUnix() != end {
		t.Fatalf("want end %d, got %d", end, created.GetEndDateUnix())
	}
}

func TestRecurringOrderHandler_CreateValidationErrors(t *testing.T) {
	h, listingRepo := newRecurringOrderHandlerForCov(t)
	seedCovListing(t, listingRepo, 1)
	ctx := context.Background()

	// Invalid owner_type.
	_, err := h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{OwnerType: "bogus", ListingId: 1, AccountId: 5, Side: "buy", Quantity: 1, Interval: "weekly"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Missing listing_id.
	_, err = h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{OwnerType: "client", OwnerId: 1, AccountId: 5, Side: "buy", Quantity: 1, Interval: "weekly"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Missing account_id.
	_, err = h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{OwnerType: "client", OwnerId: 1, ListingId: 1, Side: "buy", Quantity: 1, Interval: "weekly"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Listing not found → NotFound from the service.
	_, err = h.CreateOrder(ctx, &pb.CreateRecurringOrderRequest{OwnerType: "client", OwnerId: 1, ListingId: 999, AccountId: 5, Side: "buy", Quantity: 1, Interval: "weekly", DayOfWeek: 1})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestRecurringOrderHandler_GetNotFoundAndOwnerErrors(t *testing.T) {
	h, _ := newRecurringOrderHandlerForCov(t)
	ctx := context.Background()

	// Unknown id → NotFound.
	_, err := h.GetOrder(ctx, &pb.GetRecurringOrderRequest{OwnerType: "client", OwnerId: 7, Id: 12345})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Invalid owner_type propagates from each entrypoint.
	bad := &pb.GetRecurringOrderRequest{OwnerType: "nope", Id: 1}
	for _, call := range []func() error{
		func() error { _, e := h.GetOrder(ctx, bad); return e },
		func() error { _, e := h.PauseOrder(ctx, bad); return e },
		func() error { _, e := h.ResumeOrder(ctx, bad); return e },
		func() error { _, e := h.CancelOrder(ctx, bad); return e },
	} {
		testutil.RequireGRPCCode(t, call(), codes.InvalidArgument)
	}
	_, err = h.ListMy(ctx, &pb.ListMyRecurringOrdersRequest{OwnerType: "nope"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestNewRecurringOrderHandler_Constructor(t *testing.T) {
	if NewRecurringOrderHandler(nil) == nil {
		t.Fatal("nil handler")
	}
}

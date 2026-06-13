package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newPriceAlertHandlerForCov(t *testing.T) (*PriceAlertHandler, *repository.ListingRepository) {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.PriceAlert{}, &model.Listing{})
	svc := service.NewPriceAlertService(
		repository.NewPriceAlertRepository(db),
		repository.NewListingRepository(db),
		nil,
	)
	listingRepo := repository.NewListingRepository(db)
	return NewPriceAlertHandler(svc), listingRepo
}

func TestPriceAlertHandler_CreateGetUpdateDelete(t *testing.T) {
	h, listingRepo := newPriceAlertHandlerForCov(t)
	seedCovListing(t, listingRepo, 1)
	ctx := context.Background()

	created, err := h.CreateAlert(ctx, &pb.CreatePriceAlertRequest{
		OwnerType: "client", OwnerId: 7,
		ListingId: 1, Condition: "gte", Threshold: "150.5",
		IsRecurring: true, CooldownSeconds: 0, EmailToo: true,
	})
	testutil.RequireNoGRPCError(t, err)
	if created.GetId() == 0 || created.GetThreshold() != "150.5" {
		t.Fatalf("unexpected create: %+v", created)
	}
	if created.GetCooldownSeconds() != 3600 {
		t.Fatalf("want default cooldown 3600, got %d", created.GetCooldownSeconds())
	}
	if !created.GetActive() {
		t.Fatal("expected active alert")
	}

	got, err := h.GetAlert(ctx, &pb.GetPriceAlertRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if got.GetId() != created.GetId() {
		t.Fatalf("get id mismatch")
	}

	updated, err := h.UpdateAlert(ctx, &pb.UpdatePriceAlertRequest{
		OwnerType: "client", OwnerId: 7, Id: created.GetId(),
		Condition: "lte", Threshold: "120", CooldownSeconds: 600,
		IsRecurring: false, EmailToo: false, Active: true,
	})
	testutil.RequireNoGRPCError(t, err)
	if updated.GetCondition() != "lte" || updated.GetThreshold() != "120" {
		t.Fatalf("unexpected update: %+v", updated)
	}
	if updated.GetCooldownSeconds() != 600 {
		t.Fatalf("want cooldown 600, got %d", updated.GetCooldownSeconds())
	}

	list, err := h.ListMy(ctx, &pb.ListMyPriceAlertsRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(list.GetAlerts()) != 1 {
		t.Fatalf("want 1 alert, got %d", len(list.GetAlerts()))
	}

	del, err := h.DeleteAlert(ctx, &pb.DeletePriceAlertRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if !del.GetDeleted() {
		t.Fatal("expected deleted=true")
	}

	// Gone now.
	_, err = h.GetAlert(ctx, &pb.GetPriceAlertRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId()})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestPriceAlertHandler_CreateErrors(t *testing.T) {
	h, listingRepo := newPriceAlertHandlerForCov(t)
	seedCovListing(t, listingRepo, 1)
	ctx := context.Background()

	// Invalid owner type.
	_, err := h.CreateAlert(ctx, &pb.CreatePriceAlertRequest{OwnerType: "bad", Condition: "gte", Threshold: "1"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Bad threshold string.
	_, err = h.CreateAlert(ctx, &pb.CreatePriceAlertRequest{OwnerType: "client", OwnerId: 7, ListingId: 1, Condition: "gte", Threshold: "notanumber"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Listing missing → NotFound.
	_, err = h.CreateAlert(ctx, &pb.CreatePriceAlertRequest{OwnerType: "client", OwnerId: 7, ListingId: 999, Condition: "gte", Threshold: "10"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestPriceAlertHandler_UpdateErrors(t *testing.T) {
	h, listingRepo := newPriceAlertHandlerForCov(t)
	seedCovListing(t, listingRepo, 1)
	ctx := context.Background()

	created, err := h.CreateAlert(ctx, &pb.CreatePriceAlertRequest{
		OwnerType: "client", OwnerId: 7, ListingId: 1, Condition: "gte", Threshold: "10",
	})
	testutil.RequireNoGRPCError(t, err)

	// Update unknown id → NotFound.
	_, err = h.UpdateAlert(ctx, &pb.UpdatePriceAlertRequest{OwnerType: "client", OwnerId: 7, Id: 9999, Active: true})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Update with a bad threshold string → InvalidArgument.
	_, err = h.UpdateAlert(ctx, &pb.UpdatePriceAlertRequest{OwnerType: "client", OwnerId: 7, Id: created.GetId(), Threshold: "xx", Active: true})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Invalid owner type on the various entrypoints.
	_, err = h.UpdateAlert(ctx, &pb.UpdatePriceAlertRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.GetAlert(ctx, &pb.GetPriceAlertRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.DeleteAlert(ctx, &pb.DeletePriceAlertRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.ListMy(ctx, &pb.ListMyPriceAlertsRequest{OwnerType: "bad"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestNewPriceAlertHandler_Constructor(t *testing.T) {
	if NewPriceAlertHandler(nil) == nil {
		t.Fatal("nil handler")
	}
}

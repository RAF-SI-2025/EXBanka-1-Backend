package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
)

func TestUpdateOTCOfferQuantity_Success(t *testing.T) {
	// UpdateQuantity sums outstanding committed quantity from otc_negotiations,
	// so that table must exist alongside the standard fixture tables.
	db := testutil.SetupTestDB(t,
		&model.Holding{}, &model.OTCOffer{}, &model.OTCOfferRevision{},
		&model.OptionContract{}, &model.OTCOfferReadReceipt{},
		&model.Listing{}, &model.Stock{}, &model.OTCNegotiation{},
	)
	fx := newOTCOptionsHandlerFixtureFromDB(t, db)
	fx.seedSellerHolding(t, 7, 42, 100)
	offerID := fx.createOffer(t, 7, 42)

	resp, err := fx.h.UpdateOTCOfferQuantity(context.Background(), &stockpb.UpdateOTCOfferQuantityRequest{
		OfferId: offerID, Quantity: "5", ActingOwnerType: "client", ActingOwnerId: 7,
	})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetQuantity() != "5" {
		t.Fatalf("want quantity 5, got %q", resp.GetQuantity())
	}
}

func TestUpdateOTCOfferQuantity_Errors(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	ctx := context.Background()

	// Invalid quantity decimal.
	_, err := fx.h.UpdateOTCOfferQuantity(ctx, &stockpb.UpdateOTCOfferQuantityRequest{OfferId: 1, Quantity: "abc", ActingOwnerType: "client", ActingOwnerId: 7})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Invalid acting owner type.
	_, err = fx.h.UpdateOTCOfferQuantity(ctx, &stockpb.UpdateOTCOfferQuantityRequest{OfferId: 1, Quantity: "5", ActingOwnerType: "bogus"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Non-bank acting owner with id 0 → InvalidArgument from resolveOwnerID.
	_, err = fx.h.UpdateOTCOfferQuantity(ctx, &stockpb.UpdateOTCOfferQuantityRequest{OfferId: 1, Quantity: "5", ActingOwnerType: "client", ActingOwnerId: 0})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Unknown offer → NotFound (mapped by service sentinel).
	_, err = fx.h.UpdateOTCOfferQuantity(ctx, &stockpb.UpdateOTCOfferQuantityRequest{OfferId: 999, Quantity: "5", ActingOwnerType: "client", ActingOwnerId: 7})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

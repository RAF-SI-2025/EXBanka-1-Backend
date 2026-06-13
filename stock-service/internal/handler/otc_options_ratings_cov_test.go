package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newRatingsHandler(t *testing.T) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.OTCOffer{}, &model.OTCTraderRating{})
	ratingSvc := service.NewOTCRatingService(
		repository.NewOTCTraderRatingRepository(db),
		repository.NewOTCOfferRepository(db),
	)
	h := NewOTCOptionsHandler(nil, nil).WithRatings(ratingSvc)
	return h, db
}

// seedAcceptedOffer inserts an offer row directly (hooks skipped) so the test
// controls the exact owner pair + status the rating service reads.
func seedOffer(t *testing.T, db *gorm.DB, id uint64, status string, cptType model.OwnerType, cptID uint64) {
	t.Helper()
	cp := cptType
	cpID := cptID
	initID := uint64(7)
	o := &model.OTCOffer{
		ID: id, Local: true, Status: status, Direction: "buy_initiated",
		StockID: 1, Quantity: decimal.NewFromInt(10),
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &initID,
		CounterpartyOwnerType: &cp, CounterpartyOwnerID: &cpID,
	}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(o).Error; err != nil {
		t.Fatalf("seed offer: %v", err)
	}
}

func TestOTCRatings_SubmitGetList_Success(t *testing.T) {
	h, db := newRatingsHandler(t)
	seedOffer(t, db, 1, model.OTCOfferStatusAccepted, model.OwnerClient, 8)
	ctx := context.Background()

	// Initiator (client 7) rates the counterparty (client 8).
	row, err := h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{
		OfferId: 1, RaterOwnerType: "client", RaterOwnerId: 7, Score: 5, Comment: "great",
	})
	testutil.RequireNoGRPCError(t, err)
	if row.GetRatedOwnerType() != "client" || row.GetRatedOwnerId() != 8 {
		t.Fatalf("rated party wrong: %+v", row)
	}
	if row.GetScore() != 5 {
		t.Fatalf("want score 5, got %d", row.GetScore())
	}

	// Profile for the rated party.
	prof, err := h.GetTraderProfile(ctx, &stockpb.GetTraderProfileRequest{OwnerType: "client", OwnerId: 8, RecentLimit: 10})
	testutil.RequireNoGRPCError(t, err)
	if prof.GetCount() != 1 || prof.GetAverage() != 5 {
		t.Fatalf("unexpected profile: count=%d avg=%v", prof.GetCount(), prof.GetAverage())
	}
	if len(prof.GetRecent()) != 1 {
		t.Fatalf("want 1 recent, got %d", len(prof.GetRecent()))
	}

	// ListReceived for the rated party.
	recv, err := h.ListReceivedRatings(ctx, &stockpb.ListReceivedRatingsRequest{OwnerType: "client", OwnerId: 8, Limit: 10})
	testutil.RequireNoGRPCError(t, err)
	if len(recv.GetRatings()) != 1 {
		t.Fatalf("want 1 received, got %d", len(recv.GetRatings()))
	}
}

func TestOTCRatings_Submit_Errors(t *testing.T) {
	h, db := newRatingsHandler(t)
	ctx := context.Background()

	// Nil ratings → Unimplemented.
	bare := NewOTCOptionsHandler(nil, nil)
	_, err := bare.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 1, RaterOwnerType: "client", RaterOwnerId: 7, Score: 5})
	testutil.RequireGRPCCode(t, err, codes.Unimplemented)

	// Invalid rater owner type.
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 1, RaterOwnerType: "nope", Score: 5})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Missing rater_owner_id for non-bank.
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 1, RaterOwnerType: "client", Score: 5})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Score out of range.
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 1, RaterOwnerType: "client", RaterOwnerId: 7, Score: 9})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Offer not found → NotFound.
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 999, RaterOwnerType: "client", RaterOwnerId: 7, Score: 5})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Offer not accepted → FailedPrecondition.
	seedOffer(t, db, 2, model.OTCOfferStatusOpen, model.OwnerClient, 8)
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 2, RaterOwnerType: "client", RaterOwnerId: 7, Score: 5})
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)

	// Rater not a participant → PermissionDenied.
	seedOffer(t, db, 3, model.OTCOfferStatusAccepted, model.OwnerClient, 8)
	_, err = h.SubmitRating(ctx, &stockpb.SubmitOTCRatingRequest{OfferId: 3, RaterOwnerType: "client", RaterOwnerId: 4242, Score: 5})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestOTCRatings_Profile_And_Received_Errors(t *testing.T) {
	h, _ := newRatingsHandler(t)
	ctx := context.Background()
	bare := NewOTCOptionsHandler(nil, nil)

	// Nil ratings.
	_, err := bare.GetTraderProfile(ctx, &stockpb.GetTraderProfileRequest{OwnerType: "client", OwnerId: 8})
	testutil.RequireGRPCCode(t, err, codes.Unimplemented)
	_, err = bare.ListReceivedRatings(ctx, &stockpb.ListReceivedRatingsRequest{OwnerType: "client", OwnerId: 8})
	testutil.RequireGRPCCode(t, err, codes.Unimplemented)

	// Invalid owner types.
	_, err = h.GetTraderProfile(ctx, &stockpb.GetTraderProfileRequest{OwnerType: "nope"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.ListReceivedRatings(ctx, &stockpb.ListReceivedRatingsRequest{OwnerType: "nope"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Missing owner_id for non-bank.
	_, err = h.GetTraderProfile(ctx, &stockpb.GetTraderProfileRequest{OwnerType: "client"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
	_, err = h.ListReceivedRatings(ctx, &stockpb.ListReceivedRatingsRequest{OwnerType: "client"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestOTCRatings_BankProfileOmitsID(t *testing.T) {
	h, _ := newRatingsHandler(t)
	ctx := context.Background()
	// Bank owner profile/received require no owner_id and return empty cleanly.
	prof, err := h.GetTraderProfile(ctx, &stockpb.GetTraderProfileRequest{OwnerType: "bank"})
	testutil.RequireNoGRPCError(t, err)
	if prof.GetCount() != 0 {
		t.Fatalf("expected empty bank profile, got count %d", prof.GetCount())
	}
	recv, err := h.ListReceivedRatings(ctx, &stockpb.ListReceivedRatingsRequest{OwnerType: "bank"})
	testutil.RequireNoGRPCError(t, err)
	if len(recv.GetRatings()) != 0 {
		t.Fatal("expected empty bank received")
	}
}

func TestOTCOptionsHandler_WithRatingsAndFundRepo_ReturnCopy(t *testing.T) {
	h := NewOTCOptionsHandler(nil, nil)
	if cp := h.WithRatings(nil); cp == nil {
		t.Fatal("nil WithRatings copy")
	}
	if cp := h.WithFundRepo(nil); cp == nil {
		t.Fatal("nil WithFundRepo copy")
	}
}

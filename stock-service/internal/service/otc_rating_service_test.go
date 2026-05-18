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

func newRatingFixture(t *testing.T) (*OTCRatingService, *repository.OTCOfferRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCTraderRating{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offers := repository.NewOTCOfferRepository(db)
	ratings := repository.NewOTCTraderRatingRepository(db)
	return NewOTCRatingService(ratings, offers), offers
}

func ownerTypePtrSvc(o model.OwnerType) *model.OwnerType { return &o }

func seedAcceptedOffer(t *testing.T, offers *repository.OTCOfferRepository, initiatorID, counterID uint64) *model.OTCOffer {
	t.Helper()
	o := &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &initiatorID,
		CounterpartyOwnerType: ownerTypePtrSvc(model.OwnerClient),
		CounterpartyOwnerID:   &counterID,
		Status:                model.OTCOfferStatusAccepted,
		Direction:             "sell_initiated",
		Quantity:              decimal.NewFromInt(1),
		StrikePrice:           decimal.NewFromInt(10),
		Premium:               decimal.NewFromInt(1),
		SettlementDate:        time.Now().Add(30 * 24 * time.Hour),
	}
	if err := offers.Create(o); err != nil {
		t.Fatalf("seed offer: %v", err)
	}
	return o
}

func TestRating_Submit_Success(t *testing.T) {
	svc, offers := newRatingFixture(t)
	initiator := uint64(10)
	counter := uint64(20)
	offer := seedAcceptedOffer(t, offers, initiator, counter)

	row, err := svc.Submit(SubmitInput{
		OfferID:        offer.ID,
		RaterOwnerType: model.OwnerClient,
		RaterOwnerID:   &initiator,
		Score:          5,
		Comment:        "great",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if row.RatedOwnerID == nil || *row.RatedOwnerID != counter {
		t.Fatalf("rated owner: want %d, got %v", counter, row.RatedOwnerID)
	}
}

func TestRating_Submit_RejectsDoubleSubmit(t *testing.T) {
	svc, offers := newRatingFixture(t)
	initiator := uint64(10)
	counter := uint64(20)
	offer := seedAcceptedOffer(t, offers, initiator, counter)

	if _, err := svc.Submit(SubmitInput{OfferID: offer.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &initiator, Score: 4}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.Submit(SubmitInput{OfferID: offer.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &initiator, Score: 5})
	if !errors.Is(err, ErrRatingAlreadyExists) {
		t.Fatalf("want ErrRatingAlreadyExists, got %v", err)
	}
}

func TestRating_Submit_RejectsNonParticipant(t *testing.T) {
	svc, offers := newRatingFixture(t)
	offer := seedAcceptedOffer(t, offers, 10, 20)
	stranger := uint64(99)
	_, err := svc.Submit(SubmitInput{OfferID: offer.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &stranger, Score: 5})
	if !errors.Is(err, ErrRatingNotParticipant) {
		t.Fatalf("want ErrRatingNotParticipant, got %v", err)
	}
}

func TestRating_GetProfile_Aggregates(t *testing.T) {
	svc, offers := newRatingFixture(t)
	initiator := uint64(10)
	counter := uint64(20)
	o1 := seedAcceptedOffer(t, offers, initiator, counter)
	o2 := seedAcceptedOffer(t, offers, initiator, counter)
	if _, err := svc.Submit(SubmitInput{OfferID: o1.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &initiator, Score: 5}); err != nil {
		t.Fatalf("submit1: %v", err)
	}
	if _, err := svc.Submit(SubmitInput{OfferID: o2.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &initiator, Score: 3}); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	prof, err := svc.GetProfile(model.OwnerClient, &counter, 10)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Count != 2 || prof.Avg != 4.0 {
		t.Fatalf("avg: want 4.0/count=2, got %f/%d", prof.Avg, prof.Count)
	}
}

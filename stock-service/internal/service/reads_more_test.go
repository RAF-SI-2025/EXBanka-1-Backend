package service

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func TestOTCOfferService_WithFundHolding(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	out := fx.svc.WithFundHolding(nil)
	if out == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestOTCOfferService_ListNegotiationHistory(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	owner := uint64(10)
	// A terminal (accepted) offer where the owner is the initiator.
	counter := uint64(20)
	o := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &owner,
		CounterpartyOwnerType:       ownerTypePtrSvc(model.OwnerClient),
		CounterpartyOwnerID:         &counter,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(5),
		Status:                      model.OTCOfferStatusAccepted,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   10,
		InitiatorAccountID:          100,
	}
	if err := fx.offers.Create(o); err != nil {
		t.Fatalf("seed offer: %v", err)
	}
	rows, total, err := fx.svc.ListNegotiationHistory(10, "client", repository.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if total < 1 || len(rows) < 1 {
		t.Errorf("expected at least 1 terminal offer, got total=%d len=%d", total, len(rows))
	}
}

func TestOTCOfferService_LastReadReceipt_NilRepo(t *testing.T) {
	// receipts not wired → (nil, nil).
	svc := NewOTCOfferService(nil, nil, nil, nil, nil, nil)
	rec, err := svc.LastReadReceipt(10, "client", 1)
	if err != nil || rec != nil {
		t.Fatalf("nil receipts repo should return (nil,nil); got (%v,%v)", rec, err)
	}
}

func TestOTCOfferService_LastReadReceipt_Wired(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// receipts wired but none recorded → the repo surfaces record-not-found.
	if _, err := fx.svc.LastReadReceipt(10, "client", 1); err == nil {
		t.Errorf("expected not-found error for an unopened offer")
	}
}

func TestOTCRatingService_ListReceived(t *testing.T) {
	svc, offers := newRatingFixture(t)
	initiator := uint64(10)
	counter := uint64(20)
	offer := seedAcceptedOffer(t, offers, initiator, counter)
	if _, err := svc.Submit(SubmitInput{
		OfferID: offer.ID, RaterOwnerType: model.OwnerClient, RaterOwnerID: &initiator, Score: 5, Comment: "ok",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// The counterparty (rated) received one rating.
	rows, err := svc.ListReceived(model.OwnerClient, &counter, 10)
	if err != nil {
		t.Fatalf("list received: %v", err)
	}
	if len(rows) != 1 || rows[0].Score != 5 {
		t.Fatalf("expected 1 rating score 5, got %+v", rows)
	}
}

func TestPortfolioService_GetHoldingByID(t *testing.T) {
	svc, mocks := buildPortfolioService()
	uid := uint64(7)
	mocks.holdingRepo.holdings[42] = &model.Holding{
		ID: 42, OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", Ticker: "AAA", Quantity: 10, AveragePrice: decimal.NewFromInt(50),
	}
	got, err := svc.GetHoldingByID(42)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != 42 {
		t.Errorf("got id %d, want 42", got.ID)
	}
	// Missing → ErrHoldingNotFound.
	if _, err := svc.GetHoldingByID(999); !errors.Is(err, ErrHoldingNotFound) {
		t.Fatalf("want ErrHoldingNotFound, got %v", err)
	}
}

func TestListingService_SnapshotIntradayPrices(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Listing{}, &model.ListingDailyPriceInfo{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	listingRepo := repository.NewListingRepository(db)
	dailyRepo := repository.NewListingDailyPriceRepository(db)
	svc := NewListingService(listingRepo, dailyRepo, nil, nil, nil)

	for i := uint64(1); i <= 3; i++ {
		if err := db.Create(&model.Listing{
			ID: i, SecurityID: i, SecurityType: "stock", ExchangeID: 1,
			Price: decimal.NewFromInt(100), Volume: 5, LastRefresh: time.Now(),
		}).Error; err != nil {
			t.Fatalf("seed listing: %v", err)
		}
	}

	svc.SnapshotIntradayPrices()

	var count int64
	db.Model(&model.ListingDailyPriceInfo{}).Count(&count)
	if count != 3 {
		t.Errorf("expected 3 daily snapshots, got %d", count)
	}
}

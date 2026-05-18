package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type recordingAlertNotifier struct {
	mu    sync.Mutex
	calls []kafkamsg.GeneralNotificationMessage
}

func (r *recordingAlertNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, m)
	return nil
}

func newAlertFixture(t *testing.T) (*PriceAlertService, *gorm.DB, *mockListingRepo, *recordingAlertNotifier) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PriceAlert{}, &model.Listing{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewPriceAlertRepository(db)
	listings := newMockListingRepo()
	notifier := &recordingAlertNotifier{}
	svc := NewPriceAlertService(repo, listings, notifier)
	return svc, db, listings, notifier
}

func TestPriceAlert_Create_ListingMissing(t *testing.T) {
	svc, _, _, _ := newAlertFixture(t)
	owner := uint64(7)
	err := svc.Create(&model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 999,
		Condition: model.PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(100),
		Cooldown:  3600,
	})
	if !errors.Is(err, ErrPriceAlertListingNotFound) {
		t.Fatalf("want ErrPriceAlertListingNotFound, got %v", err)
	}
}

func TestPriceAlert_EvaluateForListing_SingleShotDeactivates(t *testing.T) {
	svc, _, listings, notifier := newAlertFixture(t)
	listings.addListing(&model.Listing{ID: 1, SecurityType: "stock", SecurityID: 50, Price: decimal.NewFromFloat(105), Change: decimal.NewFromFloat(5)})
	owner := uint64(7)
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 1,
		Condition: model.PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(100),
		Cooldown:  3600,
		Active:    true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.EvaluateForListing(context.Background(), 1, decimal.NewFromFloat(105), decimal.NewFromFloat(5))
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier: want 1 call, got %d", len(notifier.calls))
	}
	if notifier.calls[0].Type != "PRICE_ALERT_TRIGGERED" {
		t.Fatalf("type: want PRICE_ALERT_TRIGGERED, got %s", notifier.calls[0].Type)
	}
	// A second evaluation must NOT re-fire (alert is now inactive).
	svc.EvaluateForListing(context.Background(), 1, decimal.NewFromFloat(110), decimal.NewFromFloat(6))
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier: single-shot must not re-fire, got %d calls", len(notifier.calls))
	}
}

func TestPriceAlert_EvaluateForListing_NoMatchNoFire(t *testing.T) {
	svc, _, listings, notifier := newAlertFixture(t)
	listings.addListing(&model.Listing{ID: 1, SecurityType: "stock", SecurityID: 50, Price: decimal.NewFromFloat(95), Change: decimal.NewFromFloat(1)})
	owner := uint64(7)
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 1,
		Condition: model.PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(100),
		Cooldown:  3600,
		Active:    true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.EvaluateForListing(context.Background(), 1, decimal.NewFromFloat(95), decimal.NewFromFloat(1))
	if len(notifier.calls) != 0 {
		t.Fatalf("notifier: want 0, got %d", len(notifier.calls))
	}
}

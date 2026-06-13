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

// TestPriceAlert_EvaluateForListing_DailyChangePctNegativeThresholdFires
// verifies the fix for the sign bug: a daily_change_pct_lte alert with a
// negative threshold fires when the daily change drops below that value.
// Before the fix, such alerts could not be created (BeforeSave rejected
// threshold<0), so they never fired.
func TestPriceAlert_EvaluateForListing_DailyChangePctNegativeThresholdFires(t *testing.T) {
	svc, _, listings, notifier := newAlertFixture(t)
	// Daily change: price went from 106 to 100, so change=-6, pct ≈ -5.66%.
	listings.addListing(&model.Listing{
		ID: 2, SecurityType: "stock", SecurityID: 51,
		Price:  decimal.NewFromFloat(100),
		Change: decimal.NewFromFloat(-6),
	})
	owner := uint64(9)
	// Alert threshold is -5 (i.e., fire when daily change is ≤ -5%).
	// daily_change_pct ≈ -5.66 which is ≤ -5, so the alert must fire.
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 2,
		Condition: model.PriceAlertConditionDailyChangePctLTE,
		Threshold: decimal.NewFromFloat(-5),
		Cooldown:  3600,
		Active:    true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.EvaluateForListing(context.Background(), 2, decimal.NewFromFloat(100), decimal.NewFromFloat(-6))
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier: want 1 call (alert should fire), got %d", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.Type != "PRICE_ALERT_TRIGGERED" {
		t.Fatalf("type: want PRICE_ALERT_TRIGGERED, got %s", call.Type)
	}
	if call.Data["condition"] != "daily_change_pct_lte" {
		t.Fatalf("condition: want daily_change_pct_lte, got %s", call.Data["condition"])
	}
}

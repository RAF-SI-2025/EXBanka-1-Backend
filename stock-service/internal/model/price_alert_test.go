package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

func ownerForTest() *uint64 {
	id := uint64(1)
	return &id
}

// TestPriceAlert_BeforeSave_PriceLTERejectsNegative verifies that a
// price_lte alert with a negative threshold (e.g. -100) is rejected.
// Prices are always positive, so a negative threshold is nonsensical.
func TestPriceAlert_BeforeSave_PriceLTERejectsNegative(t *testing.T) {
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   ownerForTest(),
		ListingID: 1,
		Condition: PriceAlertConditionLTE,
		Threshold: decimal.NewFromFloat(-100),
		Cooldown:  3600,
		Active:    true,
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for price_lte with negative threshold, got nil")
	}
}

// TestPriceAlert_BeforeSave_DailyChangePctLTENegativeAccepted verifies
// that a daily_change_pct_lte alert with threshold=-5 (the bug case) is
// accepted. "-5" means "alert me if the daily change falls below -5%".
func TestPriceAlert_BeforeSave_DailyChangePctLTENegativeAccepted(t *testing.T) {
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   ownerForTest(),
		ListingID: 1,
		Condition: PriceAlertConditionDailyChangePctLTE,
		Threshold: decimal.NewFromFloat(-5),
		Cooldown:  3600,
		Active:    true,
	}
	if err := a.BeforeSave(nil); err != nil {
		t.Fatalf("expected no error for daily_change_pct_lte=-5, got: %v", err)
	}
}

// TestPriceAlert_BeforeSave_DailyChangePctLTEOutOfRangeRejected verifies
// that a daily_change_pct_lte alert with threshold=-200 is rejected
// (|threshold| > 100 is unreasonable for a percent change).
func TestPriceAlert_BeforeSave_DailyChangePctLTEOutOfRangeRejected(t *testing.T) {
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   ownerForTest(),
		ListingID: 1,
		Condition: PriceAlertConditionDailyChangePctLTE,
		Threshold: decimal.NewFromFloat(-200),
		Cooldown:  3600,
		Active:    true,
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for daily_change_pct_lte=-200 (|threshold|>100), got nil")
	}
}

// TestPriceAlert_BeforeSave_DailyChangePctGTEPositiveAccepted verifies
// that a daily_change_pct_gte alert with a positive threshold is still
// accepted after the fix.
func TestPriceAlert_BeforeSave_DailyChangePctGTEPositiveAccepted(t *testing.T) {
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   ownerForTest(),
		ListingID: 1,
		Condition: PriceAlertConditionDailyChangePctGTE,
		Threshold: decimal.NewFromFloat(5),
		Cooldown:  3600,
		Active:    true,
	}
	if err := a.BeforeSave(nil); err != nil {
		t.Fatalf("expected no error for daily_change_pct_gte=5, got: %v", err)
	}
}

// TestPriceAlert_BeforeSave_PriceGTEZeroRejected verifies that a price_gte
// alert with threshold=0 is still rejected (price must be positive).
func TestPriceAlert_BeforeSave_PriceGTEZeroRejected(t *testing.T) {
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   ownerForTest(),
		ListingID: 1,
		Condition: PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(0),
		Cooldown:  3600,
		Active:    true,
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for price_gte=0, got nil")
	}
}

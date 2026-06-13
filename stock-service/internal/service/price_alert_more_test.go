package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func seedAlert(t *testing.T, svc *PriceAlertService, listings *mockListingRepo, owner uint64, cond model.PriceAlertCondition, thr float64, recurring bool) *model.PriceAlert {
	t.Helper()
	if _, err := listings.GetByID(1); err != nil {
		listings.addListing(&model.Listing{ID: 1, SecurityType: "stock", SecurityID: 50, Price: decimal.NewFromFloat(105), Change: decimal.NewFromFloat(5)})
	}
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient, OwnerID: &owner,
		ListingID: 1, Condition: cond, Threshold: decimal.NewFromFloat(thr),
		Cooldown: 3600, Active: true, IsRecurring: recurring,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create alert: %v", err)
	}
	return a
}

func TestPriceAlert_GetUpdateDeleteListMy(t *testing.T) {
	svc, _, listings, _ := newAlertFixture(t)
	owner := uint64(7)
	a := seedAlert(t, svc, listings, owner, model.PriceAlertConditionGTE, 100, false)

	// Get — owner match.
	got, err := svc.Get(a.ID, model.OwnerClient, &owner)
	if err != nil || got.ID != a.ID {
		t.Fatalf("get own: %v", err)
	}
	// Get — owner mismatch → not found.
	other := uint64(99)
	if _, err := svc.Get(a.ID, model.OwnerClient, &other); !errors.Is(err, ErrPriceAlertNotFound) {
		t.Fatalf("want not found on owner mismatch, got %v", err)
	}
	// Get — missing id.
	if _, err := svc.Get(987654, model.OwnerClient, &owner); !errors.Is(err, ErrPriceAlertNotFound) {
		t.Fatalf("want not found missing id, got %v", err)
	}

	// Update.
	got.Threshold = decimal.NewFromInt(200)
	if err := svc.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}

	// ListMy.
	rows, err := svc.ListMy(model.OwnerClient, &owner)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listmy: %v len=%d", err, len(rows))
	}
	if !rows[0].Threshold.Equal(decimal.NewFromInt(200)) {
		t.Errorf("update not persisted, threshold=%s", rows[0].Threshold)
	}

	// Delete — wrong owner → not found.
	if err := svc.Delete(a.ID, model.OwnerClient, &other); !errors.Is(err, ErrPriceAlertNotFound) {
		t.Fatalf("want not found deleting other's alert, got %v", err)
	}
	// Delete — correct owner.
	if err := svc.Delete(a.ID, model.OwnerClient, &owner); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(a.ID, model.OwnerClient, &owner); !errors.Is(err, ErrPriceAlertNotFound) {
		t.Fatalf("expected gone after delete, got %v", err)
	}
}

func TestPriceAlert_Recurring_CooldownGate(t *testing.T) {
	svc, _, listings, notifier := newAlertFixture(t)
	owner := uint64(7)
	listings.addListing(&model.Listing{ID: 1, SecurityType: "stock", SecurityID: 50, Price: decimal.NewFromFloat(105), Change: decimal.NewFromFloat(5)})
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient, OwnerID: &owner, ListingID: 1,
		Condition: model.PriceAlertConditionGTE, Threshold: decimal.NewFromFloat(100),
		Cooldown: 3600, Active: true, IsRecurring: true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// First evaluation fires.
	svc.EvaluateForListing(context.Background(), 1, decimal.NewFromFloat(105), decimal.NewFromFloat(5))
	if len(notifier.calls) != 1 {
		t.Fatalf("want 1 fire, got %d", len(notifier.calls))
	}
	// Second evaluation immediately after is gated by cooldown → no re-fire.
	svc.EvaluateForListing(context.Background(), 1, decimal.NewFromFloat(106), decimal.NewFromFloat(6))
	if len(notifier.calls) != 1 {
		t.Fatalf("cooldown should gate, got %d fires", len(notifier.calls))
	}
}

func TestPriceAlertCron_Tick_EvaluatesActiveListings(t *testing.T) {
	svc, db, listings, notifier := newAlertFixture(t)
	owner := uint64(7)
	listings.addListing(&model.Listing{ID: 1, SecurityType: "stock", SecurityID: 50, Price: decimal.NewFromFloat(150), Change: decimal.NewFromFloat(10)})
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient, OwnerID: &owner, ListingID: 1,
		Condition: model.PriceAlertConditionGTE, Threshold: decimal.NewFromFloat(100),
		Cooldown: 3600, Active: true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}

	repo := repository.NewPriceAlertRepository(db)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewPriceAlertCron(svc, listings, repo, 0, reg) // 0 → defaulted interval
	cron.tick(context.Background())

	if len(notifier.calls) != 1 {
		t.Fatalf("cron tick should fire alert once, got %d", len(notifier.calls))
	}
}

func TestPriceAlertCron_Tick_SkipsMissingListing(t *testing.T) {
	svc, db, listings, notifier := newAlertFixture(t)
	owner := uint64(7)
	// Create requires listing to exist; seed then delete from mock so the cron
	// path hits the "listing lookup failed → continue" branch.
	listings.addListing(&model.Listing{ID: 5, SecurityType: "stock", SecurityID: 51, Price: decimal.NewFromFloat(10)})
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient, OwnerID: &owner, ListingID: 5,
		Condition: model.PriceAlertConditionGTE, Threshold: decimal.NewFromFloat(1),
		Cooldown: 3600, Active: true,
	}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	delete(listings.listings, 5) // listing now missing for the cron lookup

	repo := repository.NewPriceAlertRepository(db)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewPriceAlertCron(svc, listings, repo, time.Minute, reg)
	cron.tick(context.Background())

	if len(notifier.calls) != 0 {
		t.Fatalf("missing listing should be skipped, got %d fires", len(notifier.calls))
	}
}

func TestAlertMatches_AllConditions(t *testing.T) {
	price := decimal.NewFromInt(100)
	pct := decimal.NewFromInt(5)
	cases := []struct {
		cond model.PriceAlertCondition
		thr  int64
		want bool
	}{
		{model.PriceAlertConditionGTE, 90, true},
		{model.PriceAlertConditionGTE, 110, false},
		{model.PriceAlertConditionLTE, 110, true},
		{model.PriceAlertConditionLTE, 90, false},
		{model.PriceAlertConditionDailyChangePctGTE, 3, true},
		{model.PriceAlertConditionDailyChangePctLTE, 3, false},
		{"unknown", 0, false},
	}
	for _, c := range cases {
		a := &model.PriceAlert{Condition: c.cond, Threshold: decimal.NewFromInt(c.thr)}
		if got := alertMatches(a, price, pct); got != c.want {
			t.Errorf("cond %s thr %d: got %v want %v", c.cond, c.thr, got, c.want)
		}
	}
}

func TestBoolStr(t *testing.T) {
	if boolStr(true) != "true" || boolStr(false) != "false" {
		t.Errorf("boolStr broken")
	}
}

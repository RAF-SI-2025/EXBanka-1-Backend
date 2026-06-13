package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeOrderPlacer struct {
	calls []PlaceMarketInput
	err   error
}

func (f *fakeOrderPlacer) PlaceMarketOrder(_ context.Context, in PlaceMarketInput) error {
	f.calls = append(f.calls, in)
	return f.err
}

type fakeRecurringNotifier struct {
	msgs []kafkamsg.GeneralNotificationMessage
}

func (f *fakeRecurringNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	f.msgs = append(f.msgs, m)
	return nil
}

func openRecurringOrderDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.RecurringOrder{}, &model.Listing{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func dow(d int) *int { return &d }

func newRecurringOrderSvc(t *testing.T, placer orderPlacer, notifier recurringOrderNotifier) (*RecurringOrderService, *gorm.DB) {
	db := openRecurringOrderDB(t)
	// Seed a listing so Create's existence check passes.
	if err := db.Create(&model.Listing{ID: 1, SecurityID: 1, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(10)}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	svc := NewRecurringOrderService(
		repository.NewRecurringOrderRepository(db),
		repository.NewListingRepository(db),
		placer, notifier,
	)
	return svc, db
}

func baseWeeklyOrder() *model.RecurringOrder {
	uid := uint64(7)
	return &model.RecurringOrder{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, Side: "buy", Quantity: 2, AccountID: 100,
		Interval: model.RecurrenceWeekly, DayOfWeek: dow(3),
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:    model.RecurringOrderStatusActive,
	}
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestRecurringOrder_Create_OK(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	row := baseWeeklyOrder()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == 0 {
		t.Errorf("expected ID assigned")
	}
	if row.NextRun.IsZero() {
		t.Errorf("expected NextRun computed")
	}
}

func TestRecurringOrder_Create_ListingNotFound(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	row := baseWeeklyOrder()
	row.ListingID = 999
	if err := svc.Create(row); !errors.Is(err, ErrRecurringOrderListingNotFound) {
		t.Fatalf("want ErrRecurringOrderListingNotFound, got %v", err)
	}
}

// ── Get / ownership ─────────────────────────────────────────────────────────

func TestRecurringOrder_Get_OwnershipMismatch(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	row := baseWeeklyOrder()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	other := uint64(99)
	if _, err := svc.Get(row.ID, model.OwnerClient, &other); !errors.Is(err, ErrRecurringOrderNotFound) {
		t.Fatalf("want not found on owner mismatch, got %v", err)
	}
	// Missing id.
	if _, err := svc.Get(12345, model.OwnerClient, row.OwnerID); !errors.Is(err, ErrRecurringOrderNotFound) {
		t.Fatalf("want not found on missing id, got %v", err)
	}
	// Correct owner.
	got, err := svc.Get(row.ID, model.OwnerClient, row.OwnerID)
	if err != nil || got.ID != row.ID {
		t.Fatalf("get own: %v", err)
	}
}

// ── Pause / Resume / Cancel ────────────────────────────────────────────────

func TestRecurringOrder_PauseResumeCancel(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	row := baseWeeklyOrder()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Pause active → paused.
	if err := svc.Pause(row.ID, model.OwnerClient, row.OwnerID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	g, _ := svc.Get(row.ID, model.OwnerClient, row.OwnerID)
	if g.Status != model.RecurringOrderStatusPaused {
		t.Errorf("status = %s, want paused", g.Status)
	}
	// Pause again → FailedPrecondition (not active).
	if err := svc.Pause(row.ID, model.OwnerClient, row.OwnerID); err == nil {
		t.Errorf("expected error pausing a paused order")
	}
	// Resume paused → active.
	if err := svc.Resume(row.ID, model.OwnerClient, row.OwnerID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// Cancel.
	if err := svc.Cancel(row.ID, model.OwnerClient, row.OwnerID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	g, _ = svc.Get(row.ID, model.OwnerClient, row.OwnerID)
	if g.Status != model.RecurringOrderStatusCancelled {
		t.Errorf("status = %s, want cancelled", g.Status)
	}
}

func TestRecurringOrder_ListMy(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	row := baseWeeklyOrder()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows, err := svc.ListMy(model.OwnerClient, row.OwnerID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1", len(rows))
	}
}

// ── RunDue / runOne ─────────────────────────────────────────────────────────

func TestRecurringOrder_RunDue_NilPlacer(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, nil, nil)
	// Should not panic and is a no-op.
	svc.RunDue(context.Background(), time.Now())
}

func TestRecurringOrder_RunDue_PlacesAndNotifies(t *testing.T) {
	placer := &fakeOrderPlacer{}
	notifier := &fakeRecurringNotifier{}
	svc, db := newRecurringOrderSvc(t, placer, notifier)

	row := baseWeeklyOrder()
	// Make it due: NextRun in the past.
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	svc.RunDue(context.Background(), now)

	if len(placer.calls) != 1 {
		t.Fatalf("placer calls = %d, want 1", len(placer.calls))
	}
	if placer.calls[0].Quantity != 2 || placer.calls[0].Side != "buy" {
		t.Errorf("unexpected place input %+v", placer.calls[0])
	}
	if len(notifier.msgs) != 1 || notifier.msgs[0].Type != "RECURRING_ORDER_EXECUTED" {
		t.Fatalf("expected executed notification, got %+v", notifier.msgs)
	}
	// NextRun advanced and LastRun stamped.
	var reloaded model.RecurringOrder
	if err := db.First(&reloaded, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.NextRun.After(now) {
		t.Errorf("NextRun not advanced: %v", reloaded.NextRun)
	}
	if reloaded.LastRun == nil {
		t.Errorf("LastRun not stamped")
	}
}

func TestRecurringOrder_RunDue_PlacementErrorSkips(t *testing.T) {
	placer := &fakeOrderPlacer{err: errors.New("insufficient funds")}
	notifier := &fakeRecurringNotifier{}
	svc, db := newRecurringOrderSvc(t, placer, notifier)

	row := baseWeeklyOrder()
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.RunDue(context.Background(), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	if len(notifier.msgs) != 1 || notifier.msgs[0].Type != "RECURRING_ORDER_SKIPPED" {
		t.Fatalf("expected skipped notification, got %+v", notifier.msgs)
	}
	if notifier.msgs[0].Data["reason"] != "insufficient funds" {
		t.Errorf("reason = %q", notifier.msgs[0].Data["reason"])
	}
}

func TestRecurringOrder_RunOne_EndDatePassedFinishes(t *testing.T) {
	placer := &fakeOrderPlacer{}
	svc, db := newRecurringOrderSvc(t, placer, nil)

	row := baseWeeklyOrder()
	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	row.EndDate = &end
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// now is past EndDate → finished, no placement. (ListDue filters elapsed
	// end_dates, so exercise runOne directly to cover the finish branch.)
	svc.runOne(context.Background(), row, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	if len(placer.calls) != 0 {
		t.Errorf("expected no placement after end_date, got %d", len(placer.calls))
	}
	var reloaded model.RecurringOrder
	if err := db.First(&reloaded, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != model.RecurringOrderStatusFinished {
		t.Errorf("status = %s, want finished", reloaded.Status)
	}
}

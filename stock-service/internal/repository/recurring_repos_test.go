// Tests for RecurringOrderRepository and RecurringFundInvestmentRepository.
package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// RecurringOrderRepository
// ---------------------------------------------------------------------------

func newRecurringOrderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.RecurringOrder{}); err != nil {
		t.Fatalf("migrate recurring_orders: %v", err)
	}
	return db
}

func makeRecurringOrder(ownerID uint64, listingID uint64) *model.RecurringOrder {
	uid := ownerID
	dom := 5
	return &model.RecurringOrder{
		OwnerType:  model.OwnerClient,
		OwnerID:    &uid,
		ListingID:  listingID,
		Side:       "buy",
		Quantity:   10,
		AccountID:  1,
		Interval:   model.RecurrenceMonthly,
		DayOfMonth: &dom,
		StartDate:  time.Now().UTC(),
		Status:     model.RecurringOrderStatusActive,
		NextRun:    time.Now().Add(24 * time.Hour).UTC(),
	}
}

func TestRecurringOrderRepository_Create_And_GetByID(t *testing.T) {
	db := newRecurringOrderTestDB(t)
	r := NewRecurringOrderRepository(db)

	row := makeRecurringOrder(1, 100)
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := r.GetByID(row.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ListingID != 100 {
		t.Errorf("listing_id mismatch: got %d", got.ListingID)
	}
}

func TestRecurringOrderRepository_GetByID_NotFound(t *testing.T) {
	db := newRecurringOrderTestDB(t)
	r := NewRecurringOrderRepository(db)

	if _, err := r.GetByID(9999); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestRecurringOrderRepository_Save(t *testing.T) {
	db := newRecurringOrderTestDB(t)
	r := NewRecurringOrderRepository(db)

	row := makeRecurringOrder(2, 200)
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	row.Status = model.RecurringOrderStatusPaused
	if err := r.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := r.GetByID(row.ID)
	if got.Status != model.RecurringOrderStatusPaused {
		t.Errorf("expected paused, got %q", got.Status)
	}
}

func TestRecurringOrderRepository_ListByOwner(t *testing.T) {
	db := newRecurringOrderTestDB(t)
	r := NewRecurringOrderRepository(db)

	uid := uint64(3)
	for i := 1; i <= 3; i++ {
		row := makeRecurringOrder(3, uint64(i*10))
		if err := r.Create(row); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	rows, err := r.ListByOwner(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}

	// bank owner should get 0
	bankRows, err := r.ListByOwner(model.OwnerBank, nil)
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if len(bankRows) != 0 {
		t.Errorf("expected 0 bank rows, got %d", len(bankRows))
	}
}

func TestRecurringOrderRepository_ListDue(t *testing.T) {
	db := newRecurringOrderTestDB(t)
	r := NewRecurringOrderRepository(db)

	uid := uint64(4)
	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(24 * time.Hour).UTC()
	dom := 5

	dueRow := &model.RecurringOrder{
		OwnerType:  model.OwnerClient,
		OwnerID:    &uid,
		ListingID:  10,
		Side:       "buy",
		Quantity:   5,
		AccountID:  1,
		Interval:   model.RecurrenceMonthly,
		DayOfMonth: &dom,
		StartDate:  past,
		Status:     model.RecurringOrderStatusActive,
		NextRun:    past, // in the past → due
	}
	notDueRow := &model.RecurringOrder{
		OwnerType:  model.OwnerClient,
		OwnerID:    &uid,
		ListingID:  20,
		Side:       "sell",
		Quantity:   5,
		AccountID:  1,
		Interval:   model.RecurrenceMonthly,
		DayOfMonth: &dom,
		StartDate:  past,
		Status:     model.RecurringOrderStatusActive,
		NextRun:    future, // in the future → not due
	}
	pausedRow := &model.RecurringOrder{
		OwnerType:  model.OwnerClient,
		OwnerID:    &uid,
		ListingID:  30,
		Side:       "buy",
		Quantity:   5,
		AccountID:  1,
		Interval:   model.RecurrenceMonthly,
		DayOfMonth: &dom,
		StartDate:  past,
		Status:     model.RecurringOrderStatusPaused, // paused → not due
		NextRun:    past,
	}
	for _, row := range []*model.RecurringOrder{dueRow, notDueRow, pausedRow} {
		if err := r.Create(row); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	due, err := r.ListDue(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("expected 1 due, got %d", len(due))
	}
	if due[0].ListingID != 10 {
		t.Errorf("wrong due row: listing_id=%d", due[0].ListingID)
	}
}

// ---------------------------------------------------------------------------
// RecurringFundInvestmentRepository
// ---------------------------------------------------------------------------

func newRecurringFundInvestmentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.RecurringFundInvestment{}); err != nil {
		t.Fatalf("migrate recurring_fund_investments: %v", err)
	}
	return db
}

func makeRecurringFundInvestment(clientID, fundID uint64) *model.RecurringFundInvestment {
	return &model.RecurringFundInvestment{
		ClientID:        clientID,
		FundID:          fundID,
		AmountRSD:       decimal.NewFromFloat(500),
		SourceAccountID: 1,
		DayOfMonth:      15,
		Active:          true,
		NextRun:         time.Now().Add(24 * time.Hour).UTC(),
	}
}

func TestRecurringFundInvestmentRepository_Create_And_GetByID(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	row := makeRecurringFundInvestment(1, 10)
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := r.GetByID(row.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.FundID != 10 {
		t.Errorf("fund_id mismatch: got %d", got.FundID)
	}
}

func TestRecurringFundInvestmentRepository_GetByID_NotFound(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	if _, err := r.GetByID(9999); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestRecurringFundInvestmentRepository_Save(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	row := makeRecurringFundInvestment(2, 20)
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	row.Active = false
	if err := r.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := r.GetByID(row.ID)
	if got.Active {
		t.Error("expected active=false after save")
	}
}

func TestRecurringFundInvestmentRepository_Delete(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	clientID := uint64(3)
	row := makeRecurringFundInvestment(clientID, 30)
	if err := r.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	removed, err := r.Delete(row.ID, clientID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// delete with wrong clientID should return false
	row2 := makeRecurringFundInvestment(clientID, 31)
	if err := r.Create(row2); err != nil {
		t.Fatalf("create row2: %v", err)
	}
	removed2, err := r.Delete(row2.ID, 9999) // wrong client
	if err != nil {
		t.Fatalf("delete wrong client: %v", err)
	}
	if removed2 {
		t.Error("expected removed=false for wrong clientID")
	}
}

func TestRecurringFundInvestmentRepository_ListByClient(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	clientID := uint64(4)
	for i := 1; i <= 3; i++ {
		if err := r.Create(makeRecurringFundInvestment(clientID, uint64(i*100))); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// one for a different client
	if err := r.Create(makeRecurringFundInvestment(999, 999)); err != nil {
		t.Fatalf("create other client: %v", err)
	}

	rows, err := r.ListByClient(clientID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3, got %d", len(rows))
	}
}

func TestRecurringFundInvestmentRepository_ListDue(t *testing.T) {
	db := newRecurringFundInvestmentTestDB(t)
	r := NewRecurringFundInvestmentRepository(db)

	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(24 * time.Hour).UTC()

	// 1 due row (active, next_run in the past)
	dueRow := &model.RecurringFundInvestment{
		ClientID:        5,
		FundID:          50,
		AmountRSD:       decimal.NewFromFloat(100),
		SourceAccountID: 1,
		DayOfMonth:      10,
		Active:          true,
		NextRun:         past,
	}
	// 1 active but not yet due (future next_run)
	notDueRow := &model.RecurringFundInvestment{
		ClientID:        5,
		FundID:          60,
		AmountRSD:       decimal.NewFromFloat(100),
		SourceAccountID: 1,
		DayOfMonth:      10,
		Active:          true,
		NextRun:         future,
	}
	for _, row := range []*model.RecurringFundInvestment{dueRow, notDueRow} {
		if err := r.Create(row); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// Manually flip one row to inactive so we can test the active filter.
	// GORM skips bool false (zero value) on Create when a default:true tag
	// is present, so we insert active=true and then flip with raw SQL.
	inactiveRow := &model.RecurringFundInvestment{
		ClientID:        5,
		FundID:          70,
		AmountRSD:       decimal.NewFromFloat(100),
		SourceAccountID: 1,
		DayOfMonth:      10,
		Active:          true, // inserted as true; flipped below
		NextRun:         past,
	}
	if err := r.Create(inactiveRow); err != nil {
		t.Fatalf("create inactive: %v", err)
	}
	if err := db.Exec("UPDATE recurring_fund_investments SET active = false WHERE id = ?", inactiveRow.ID).Error; err != nil {
		t.Fatalf("flip active: %v", err)
	}

	due, err := r.ListDue(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("expected 1 due, got %d", len(due))
	}
	if due[0].FundID != 50 {
		t.Errorf("wrong due row: fund_id=%d", due[0].FundID)
	}
}

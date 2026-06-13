// Tests for PriceAlertRepository.
package repository

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newPriceAlertTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.PriceAlert{}); err != nil {
		t.Fatalf("migrate price_alerts: %v", err)
	}
	return db
}

func makePriceAlert(ownerID uint64, listingID uint64, condition model.PriceAlertCondition, threshold decimal.Decimal) *model.PriceAlert {
	uid := ownerID
	return &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &uid,
		ListingID: listingID,
		Condition: condition,
		Threshold: threshold,
		Cooldown:  3600,
		Active:    true,
	}
}

func TestPriceAlertRepository_DB(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)
	if r.DB() != db {
		t.Error("DB() should return the underlying db")
	}
}

func TestPriceAlertRepository_Create_And_GetByID(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	a := makePriceAlert(1, 10, model.PriceAlertConditionGTE, decimal.NewFromFloat(150))
	if err := r.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := r.GetByID(a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ListingID != 10 {
		t.Errorf("listing_id mismatch: got %d", got.ListingID)
	}
}

func TestPriceAlertRepository_GetByID_NotFound(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)
	if _, err := r.GetByID(99999); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestPriceAlertRepository_Save(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	a := makePriceAlert(2, 20, model.PriceAlertConditionLTE, decimal.NewFromFloat(100))
	if err := r.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// flip active
	a.Active = false
	if err := r.Save(a); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := r.GetByID(a.ID)
	if got.Active {
		t.Error("expected active=false after save")
	}
}

func TestPriceAlertRepository_Delete(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	uid := uint64(3)
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &uid,
		ListingID: 30,
		Condition: model.PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(200),
		Cooldown:  3600,
		Active:    true,
	}
	if err := r.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}

	removed, err := r.Delete(a.ID, model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// second delete should return false (no row)
	removed2, err := r.Delete(a.ID, model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("delete 2: %v", err)
	}
	if removed2 {
		t.Error("expected removed=false on second delete")
	}
}

func TestPriceAlertRepository_Delete_WrongOwner(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	uid := uint64(4)
	a := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &uid,
		ListingID: 40,
		Condition: model.PriceAlertConditionGTE,
		Threshold: decimal.NewFromFloat(200),
		Cooldown:  3600,
		Active:    true,
	}
	if err := r.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}

	otherUID := uint64(999)
	removed, err := r.Delete(a.ID, model.OwnerClient, &otherUID)
	if err != nil {
		t.Fatalf("delete wrong owner: %v", err)
	}
	if removed {
		t.Error("expected removed=false for wrong owner")
	}
}

func TestPriceAlertRepository_ListByOwner(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	uid := uint64(5)
	for i := 1; i <= 3; i++ {
		a := makePriceAlert(5, uint64(i*100), model.PriceAlertConditionGTE, decimal.NewFromFloat(float64(i*10)))
		if err := r.Create(a); err != nil {
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

	// bank owner with no alerts
	bankRows, err := r.ListByOwner(model.OwnerBank, nil)
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if len(bankRows) != 0 {
		t.Errorf("expected 0 bank rows, got %d", len(bankRows))
	}
}

func TestPriceAlertRepository_ListActiveByListing(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)

	uid := uint64(6)
	// 2 active alerts for listing 600
	for i := 1; i <= 2; i++ {
		a := makePriceAlert(6, 600, model.PriceAlertConditionGTE, decimal.NewFromFloat(float64(i*10)))
		if err := r.Create(a); err != nil {
			t.Fatalf("create active: %v", err)
		}
	}
	// 1 inactive for listing 600 — GORM skips bool=false (zero value) on Create
	// when default:true is set, so we insert active=true then flip with raw SQL.
	inactive := &model.PriceAlert{
		OwnerType: model.OwnerClient,
		OwnerID:   &uid,
		ListingID: 600,
		Condition: model.PriceAlertConditionLTE,
		Threshold: decimal.NewFromFloat(5),
		Cooldown:  3600,
		Active:    true, // will be flipped below
	}
	if err := r.Create(inactive); err != nil {
		t.Fatalf("create inactive: %v", err)
	}
	if err := db.Exec("UPDATE price_alerts SET active = false WHERE id = ?", inactive.ID).Error; err != nil {
		t.Fatalf("flip active: %v", err)
	}

	active, err := r.ListActiveByListing(600)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active, got %d", len(active))
	}
}

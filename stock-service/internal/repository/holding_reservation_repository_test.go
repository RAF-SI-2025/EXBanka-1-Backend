package repository

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// uint64Ptr is a one-liner helper for tests that need to set the
// HoldingReservation OrderID / OTCContractID pointer fields.
func uint64Ptr(v uint64) *uint64 { return &v }

// newHoldingReservationTestDB opens a fresh in-memory SQLite DB and
// auto-migrates the tables required by HoldingReservationRepository tests:
// Holding, HoldingReservation, HoldingReservationSettlement.
func newHoldingReservationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
	); err != nil {
		t.Fatalf("migrate holding reservation tables: %v", err)
	}
	return db
}

// seedHolding inserts a minimal Holding row for tests that only need a
// HoldingID foreign key reference.
func seedHolding(t *testing.T, db *gorm.DB) *model.Holding {
	t.Helper()
	uid := uint64(1)
	h := &model.Holding{
		OwnerType:     model.OwnerClient,
		OwnerID:       &uid,
		UserFirstName: "Test",
		UserLastName:  "User",
		SecurityType:  "stock",
		SecurityID:    1,
		ListingID:     1,
		Ticker:        "TEST",
		Name:          "Test Stock",
		Quantity:      10000,
		AveragePrice:  decimal.NewFromInt(100),
		AccountID:     1,
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}
	return h
}

func TestHoldingReservationRepo_InsertAndGetByOrderID(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	r := &model.HoldingReservation{
		HoldingID: h.ID,
		OrderID:   uint64Ptr(1001),
		Quantity:  500,
		Status:    model.HoldingReservationStatusActive,
	}
	if err := repo.Create(r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.GetByOrderID(1001)
	if err != nil {
		t.Fatalf("GetByOrderID: %v", err)
	}
	if got.ID != r.ID {
		t.Fatalf("ID mismatch: got %d want %d", got.ID, r.ID)
	}
	if got.Quantity != 500 {
		t.Fatalf("quantity: got %d", got.Quantity)
	}
}

func TestHoldingReservationRepo_InsertIfAbsent_Idempotent(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	r := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: uint64Ptr(2001), Quantity: 500,
		Status: model.HoldingReservationStatusActive,
	}
	inserted, existing, err := repo.InsertIfAbsent(r)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Error("first call should insert")
	}
	if existing.ID == 0 {
		t.Fatal("existing should have ID")
	}

	// Retry with a different quantity but same OrderID: must be a no-op and
	// return the first row.
	retry := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: uint64Ptr(2001), Quantity: 999,
		Status: model.HoldingReservationStatusActive,
	}
	insertedAgain, existingAgain, err := repo.InsertIfAbsent(retry)
	if err != nil {
		t.Fatal(err)
	}
	if insertedAgain {
		t.Error("second call should NOT insert")
	}
	if existingAgain.ID != existing.ID {
		t.Errorf("should return original row, got id %d vs %d", existingAgain.ID, existing.ID)
	}
	if existingAgain.Quantity != 500 {
		t.Errorf("should keep original quantity, got %d", existingAgain.Quantity)
	}
}

func TestHoldingReservationRepo_SumSettlements(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	r := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: uint64Ptr(3001), Quantity: 1000,
		Status: model.HoldingReservationStatusActive,
	}
	if err := repo.Create(r); err != nil {
		t.Fatal(err)
	}

	s1 := &model.HoldingReservationSettlement{
		HoldingReservationID: r.ID, OrderTransactionID: 9001, Quantity: 300,
	}
	s2 := &model.HoldingReservationSettlement{
		HoldingReservationID: r.ID, OrderTransactionID: 9002, Quantity: 200,
	}
	if err := repo.CreateSettlement(s1); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSettlement(s2); err != nil {
		t.Fatal(err)
	}

	total, err := repo.SumSettlements(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 500 {
		t.Fatalf("expected 500, got %d", total)
	}
}

func TestHoldingReservationRepo_CreateSettlement_UniqueOnOrderTransactionID(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	r := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: uint64Ptr(4001), Quantity: 1000,
		Status: model.HoldingReservationStatusActive,
	}
	_ = repo.Create(r)

	s := &model.HoldingReservationSettlement{
		HoldingReservationID: r.ID, OrderTransactionID: 7001, Quantity: 100,
	}
	if err := repo.CreateSettlement(s); err != nil {
		t.Fatal(err)
	}
	// Same OrderTransactionID → unique violation.
	s2 := &model.HoldingReservationSettlement{
		HoldingReservationID: r.ID, OrderTransactionID: 7001, Quantity: 100,
	}
	if err := repo.CreateSettlement(s2); err == nil {
		t.Fatal("expected unique violation, got nil")
	}
}

func TestHoldingReservationRepo_ListSettlements_Ordered(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	r := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: uint64Ptr(5001), Quantity: 1000,
		Status: model.HoldingReservationStatusActive,
	}
	_ = repo.Create(r)

	ids := []uint64{8003, 8001, 8002}
	for _, txnID := range ids {
		_ = repo.CreateSettlement(&model.HoldingReservationSettlement{
			HoldingReservationID: r.ID, OrderTransactionID: txnID, Quantity: 10,
		})
	}
	got, err := repo.ListSettlements(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// Ordered by id ASC (insertion order).
	if got[0].OrderTransactionID != 8003 || got[1].OrderTransactionID != 8001 || got[2].OrderTransactionID != 8002 {
		t.Errorf("unexpected order: %v %v %v", got[0].OrderTransactionID, got[1].OrderTransactionID, got[2].OrderTransactionID)
	}
}

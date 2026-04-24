package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
)

func TestReservationRepo_InsertAndGetByOrderID(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID:    1,
		OrderID:      1001,
		Amount:       decimal.NewFromInt(500),
		CurrencyCode: "RSD",
		Status:       model.ReservationStatusActive,
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
	if !got.Amount.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("amount: got %s", got.Amount)
	}
}

func TestReservationRepo_InsertIfAbsent_Idempotent(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 2001, Amount: decimal.NewFromInt(500),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
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

	// Retry with a different amount but same OrderID: must be a no-op and
	// return the first row.
	retry := &model.AccountReservation{
		AccountID: 1, OrderID: 2001, Amount: decimal.NewFromInt(999),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
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
	if !existingAgain.Amount.Equal(decimal.NewFromInt(500)) {
		t.Errorf("should keep original amount, got %s", existingAgain.Amount)
	}
}

func TestReservationRepo_SumSettlements(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 3001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	if err := repo.Create(r); err != nil {
		t.Fatal(err)
	}

	s1 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9001, Amount: decimal.NewFromInt(300),
	}
	s2 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9002, Amount: decimal.NewFromInt(200),
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
	if !total.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("expected 500, got %s", total)
	}
}

func TestReservationRepo_CreateSettlement_UniqueOnOrderTransactionID(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 4001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	_ = repo.Create(r)

	s := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 7001, Amount: decimal.NewFromInt(100),
	}
	if err := repo.CreateSettlement(s); err != nil {
		t.Fatal(err)
	}
	// Same OrderTransactionID → unique violation.
	s2 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 7001, Amount: decimal.NewFromInt(100),
	}
	if err := repo.CreateSettlement(s2); err == nil {
		t.Fatal("expected unique violation, got nil")
	}
}

func TestReservationRepo_ListSettlements_Ordered(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 5001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	_ = repo.Create(r)

	ids := []uint64{8003, 8001, 8002}
	for _, txnID := range ids {
		_ = repo.CreateSettlement(&model.AccountReservationSettlement{
			ReservationID: r.ID, OrderTransactionID: txnID, Amount: decimal.NewFromInt(10),
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

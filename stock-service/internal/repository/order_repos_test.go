package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// OrderRepository
// ---------------------------------------------------------------------------

func newOrderTestDB(t *testing.T) (*OrderRepository, *OrderTransactionRepository, uint64) {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Order{}, &model.OrderTransaction{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewOrderRepository(db), NewOrderTransactionRepository(db), 1
}

func TestOrderRepository_CrudAndList(t *testing.T) {
	r, _, _ := newOrderTestDB(t)
	uid := uint64(7)
	o := &model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market",
		Quantity:         10,
		PricePerUnit:     decimal.NewFromInt(150),
		ApproximatePrice: decimal.NewFromInt(1500),
		Status:           "pending",
	}
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ticker != "AAPL" {
		t.Errorf("got %s", got.Ticker)
	}
	o.Status = "approved"
	if err := r.Update(o); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, total, err := r.ListAll(OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got total=%d len=%d", total, len(rows))
	}
}

func TestOrderRepository_ListActiveApproved(t *testing.T) {
	r, _, _ := newOrderTestDB(t)
	uid := uint64(7)
	_ = r.Create(&model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 10,
		PricePerUnit:     decimal.NewFromInt(150),
		ApproximatePrice: decimal.NewFromInt(1500),
		Status:           "approved", IsDone: false,
	})
	_ = r.Create(&model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 10,
		PricePerUnit:     decimal.NewFromInt(150),
		ApproximatePrice: decimal.NewFromInt(1500),
		Status:           "approved", IsDone: true,
	})
	rows, err := r.ListActiveApproved()
	if err != nil {
		t.Fatalf("list active approved: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1, got %d", len(rows))
	}
}

func TestOrderRepository_ApplyFilters(t *testing.T) {
	r, _, _ := newOrderTestDB(t)
	uid := uint64(7)
	for _, status := range []string{"approved", "pending", "cancelled", "declined"} {
		_ = r.Create(&model.Order{
			OwnerType: model.OwnerClient, OwnerID: &uid,
			ListingID: 1, SecurityType: "stock", Ticker: "T",
			Direction: "buy", OrderType: "market", Quantity: 1,
			PricePerUnit: decimal.NewFromInt(1), ApproximatePrice: decimal.NewFromInt(1),
			Status: status,
		})
	}
	for _, f := range []OrderFilter{
		{Status: "done", Page: 1, PageSize: 10},
		{Status: "filling", Page: 1, PageSize: 10},
		{Status: "filled", Page: 1, PageSize: 10},
		{Status: "approved", Page: 1, PageSize: 10},
		{Direction: "buy", Page: 1, PageSize: 10},
		{OrderType: "market", Page: 1, PageSize: 10},
	} {
		if _, _, err := r.ListAll(f); err != nil {
			t.Errorf("filter=%+v: %v", f, err)
		}
	}
}

func TestOrderRepository_Delete(t *testing.T) {
	r, _, _ := newOrderTestDB(t)
	uid := uint64(7)
	o := &model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 10,
		PricePerUnit:     decimal.NewFromInt(150),
		ApproximatePrice: decimal.NewFromInt(1500),
		Status:           "pending",
	}
	_ = r.Create(o)
	if err := r.Delete(o.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OrderTransactionRepository
// ---------------------------------------------------------------------------

func TestOrderTransactionRepository_Crud(t *testing.T) {
	_, txRepo, _ := newOrderTestDB(t)
	tx := &model.OrderTransaction{
		OrderID:      1,
		Quantity:     5,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(500),
		ExecutedAt:   time.Now(),
	}
	if err := txRepo.Create(tx); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx.Quantity = 6
	if err := txRepo.Update(tx); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, err := txRepo.ListByOrderID(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// OptionRepository.DeleteExpiredBefore
// ---------------------------------------------------------------------------

func TestOptionRepository_DeleteExpiredBefore(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Option{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOptionRepository(db)
	_ = r.Create(&model.Option{
		Ticker: "OLD", StockID: 1, OptionType: "call",
		StrikePrice:    decimal.NewFromInt(100),
		SettlementDate: time.Now().Add(-72 * time.Hour),
		Premium:        decimal.NewFromInt(1),
	})
	_ = r.Create(&model.Option{
		Ticker: "FUT", StockID: 1, OptionType: "call",
		StrikePrice:    decimal.NewFromInt(100),
		SettlementDate: time.Now().Add(72 * time.Hour),
		Premium:        decimal.NewFromInt(1),
	})
	deleted, err := r.DeleteExpiredBefore(time.Now())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
}

// ---------------------------------------------------------------------------
// HoldingReservationRepository: WithTx + Get* methods.
// ---------------------------------------------------------------------------

func TestHoldingReservationRepository_GetByOTCContractID(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	r := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)
	otcID := uint64(99)
	res := &model.HoldingReservation{
		HoldingID: h.ID, OTCContractID: &otcID,
		Quantity: 10, Status: model.HoldingReservationStatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(res).Error; err != nil {
		t.Fatalf("seed res: %v", err)
	}
	got, err := r.GetByOTCContractID(otcID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch")
	}
	got2, err := r.GetByOTCContractIDForUpdate(otcID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got2.ID != res.ID {
		t.Errorf("id mismatch on for-update")
	}
}

func TestHoldingReservationRepository_GetByPeerOptionContractID(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	r := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)
	peerID := uint64(101)
	res := &model.HoldingReservation{
		HoldingID: h.ID, PeerOptionContractID: &peerID,
		Quantity: 5, Status: model.HoldingReservationStatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(res).Error; err != nil {
		t.Fatalf("seed res: %v", err)
	}
	got, err := r.GetByPeerOptionContractID(peerID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch")
	}
}

func TestHoldingReservationRepository_WithTx(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	r := NewHoldingReservationRepository(db)
	tx := db.Begin()
	defer tx.Rollback()
	r2 := r.WithTx(tx)
	if r2 == nil {
		t.Error("WithTx returned nil")
	}
}

func TestHoldingReservationRepository_GetByOrderIDForUpdate(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	r := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)
	orderID := uint64(50)
	res := &model.HoldingReservation{
		HoldingID: h.ID, OrderID: &orderID,
		Quantity: 5, Status: model.HoldingReservationStatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(res).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := r.GetByOrderIDForUpdate(orderID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch")
	}
}

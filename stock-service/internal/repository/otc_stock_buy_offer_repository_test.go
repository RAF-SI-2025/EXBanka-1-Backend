package repository

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

func newOTCStockBuyOfferTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCStockBuyOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newSampleBuyOffer(buyerID *uint64, status string, ticker string, qty int64) *model.OTCStockBuyOffer {
	amount := decimal.NewFromInt(qty).Mul(decimal.NewFromFloat(150.50))
	return &model.OTCStockBuyOffer{
		BuyerOwnerType:            model.OwnerClient,
		BuyerOwnerID:              buyerID,
		BuyerAccountID:            999,
		BuyerAccountNumber:        "111000123456789011",
		StockID:                   1,
		ListingID:                 1,
		Ticker:                    ticker,
		Name:                      "Test Stock",
		OriginalQuantity:          qty,
		RemainingQuantity:         qty,
		PricePerUnit:              decimal.NewFromFloat(150.50),
		CurrencyCode:              "USD",
		ReservedAmount:            amount,
		OriginalReservedAmount:    amount,
		AccountReservationOrderID: 1_000_001,
		Status:                    status,
	}
}

func TestOTCStockBuyOffer_CreateAndGet(t *testing.T) {
	db := newOTCStockBuyOfferTestDB(t)
	r := NewOTCStockBuyOfferRepository(db)

	o := newSampleBuyOffer(uint64Ptr(7), model.OTCStockBuyOfferStatusActive, "AAPL", 5)
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	if o.ID == 0 {
		t.Fatalf("expected non-zero ID after create")
	}

	got, err := r.GetByID(o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ticker != "AAPL" || got.OriginalQuantity != 5 {
		t.Errorf("unexpected row: %+v", got)
	}
}

func TestOTCStockBuyOffer_ListActive_OnlyReturnsActive(t *testing.T) {
	db := newOTCStockBuyOfferTestDB(t)
	r := NewOTCStockBuyOfferRepository(db)

	for i, s := range []string{
		model.OTCStockBuyOfferStatusActive,
		model.OTCStockBuyOfferStatusFilled,
		model.OTCStockBuyOfferStatusCancelled,
		model.OTCStockBuyOfferStatusActive,
	} {
		o := newSampleBuyOffer(uint64Ptr(uint64(i+1)), s, "AAPL", 1)
		o.AccountReservationOrderID = uint64(1_000_000 + i)
		if err := r.Create(o); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	rows, total, err := r.ListActive(OTCStockBuyOfferFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total=2 active, got %d", total)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 returned rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Status != model.OTCStockBuyOfferStatusActive {
			t.Errorf("ListActive returned non-active row: status=%s", r.Status)
		}
	}
}

func TestOTCStockBuyOffer_ListByOwner_FiltersOwnerAndStatus(t *testing.T) {
	db := newOTCStockBuyOfferTestDB(t)
	r := NewOTCStockBuyOfferRepository(db)

	// Owner 7: 1 active, 1 cancelled. Owner 8: 1 active.
	rows := []*model.OTCStockBuyOffer{
		newSampleBuyOffer(uint64Ptr(7), model.OTCStockBuyOfferStatusActive, "AAPL", 5),
		newSampleBuyOffer(uint64Ptr(7), model.OTCStockBuyOfferStatusCancelled, "MSFT", 3),
		newSampleBuyOffer(uint64Ptr(8), model.OTCStockBuyOfferStatusActive, "GOOG", 2),
	}
	for i, o := range rows {
		o.AccountReservationOrderID = uint64(2_000_000 + i)
		if err := r.Create(o); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	got, total, err := r.ListByOwner(model.OwnerClient, uint64Ptr(7), nil, 1, 20)
	if err != nil {
		t.Fatalf("list owner 7: %v", err)
	}
	if total != 2 {
		t.Errorf("owner 7 total=%d want 2", total)
	}
	if len(got) != 2 {
		t.Errorf("owner 7 got %d rows", len(got))
	}

	// Filter to active only.
	got, total, err = r.ListByOwner(model.OwnerClient, uint64Ptr(7),
		[]string{model.OTCStockBuyOfferStatusActive}, 1, 20)
	if err != nil {
		t.Fatalf("list owner 7 active: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Errorf("owner 7 active total=%d got=%d, want 1", total, len(got))
	}
	if got[0].Ticker != "AAPL" {
		t.Errorf("expected AAPL row, got %s", got[0].Ticker)
	}
}

func TestOTCStockBuyOffer_OwnerValidation_RejectsBadCombo(t *testing.T) {
	db := newOTCStockBuyOfferTestDB(t)
	r := NewOTCStockBuyOfferRepository(db)

	// Client owner with NIL owner_id should be rejected by BeforeSave.
	bad := newSampleBuyOffer(nil, model.OTCStockBuyOfferStatusActive, "AAPL", 1)
	if err := r.Create(bad); err == nil {
		t.Fatalf("expected error for client owner with nil owner_id, got none")
	}
}

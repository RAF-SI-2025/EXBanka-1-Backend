package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

func ownerTypePtr(o model.OwnerType) *model.OwnerType { return &o }

func newHistoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedOffer(t *testing.T, db *gorm.DB, o *model.OTCOffer) {
	t.Helper()
	o.Quantity = decimal.NewFromInt(1)
	o.StrikePrice = decimal.NewFromInt(10)
	o.Premium = decimal.NewFromInt(1)
	o.SettlementDate = time.Now().Add(30 * 24 * time.Hour)
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestListNegotiationHistory_OnlyTerminal(t *testing.T) {
	db := newHistoryTestDB(t)
	repo := NewOTCOfferRepository(db)
	caller := uint64(7)
	other := uint64(99)

	seedOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &caller,
		CounterpartyOwnerType: ownerTypePtr(model.OwnerClient),
		CounterpartyOwnerID:   &other,
		Status:                model.OTCOfferStatusPending,
		Direction:             "sell_initiated",
	})
	seedOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &caller,
		CounterpartyOwnerType: ownerTypePtr(model.OwnerClient),
		CounterpartyOwnerID:   &other,
		Status:                model.OTCOfferStatusAccepted,
		Direction:             "sell_initiated",
	})
	seedOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &caller,
		CounterpartyOwnerType: ownerTypePtr(model.OwnerClient),
		CounterpartyOwnerID:   &other,
		Status:                model.OTCOfferStatusRejected,
		Direction:             "sell_initiated",
	})

	rows, total, err := repo.ListNegotiationHistory(model.OwnerClient, &caller, HistoryFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("total: want 2 (accepted+rejected), got %d", total)
	}
	for _, r := range rows {
		if r.Status == model.OTCOfferStatusPending {
			t.Fatalf("history must not include pending: got %+v", r)
		}
	}
}

func TestListNegotiationHistory_CounterpartyFilter(t *testing.T) {
	db := newHistoryTestDB(t)
	repo := NewOTCOfferRepository(db)
	caller := uint64(7)
	cpA := uint64(40)
	cpB := uint64(41)

	seedOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &caller,
		CounterpartyOwnerType: ownerTypePtr(model.OwnerClient),
		CounterpartyOwnerID:   &cpA,
		Status:                model.OTCOfferStatusAccepted,
		Direction:             "sell_initiated",
	})
	seedOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &caller,
		CounterpartyOwnerType: ownerTypePtr(model.OwnerClient),
		CounterpartyOwnerID:   &cpB,
		Status:                model.OTCOfferStatusAccepted,
		Direction:             "sell_initiated",
	})

	_, total, err := repo.ListNegotiationHistory(model.OwnerClient, &caller, HistoryFilter{CounterpartyID: &cpA})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 {
		t.Fatalf("counterparty filter: want 1 (just cpA), got %d", total)
	}
}

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

// otcCRUDFixture provides an isolated OTCOfferService backed by an in-memory
// sqlite DB so the CRUD-level methods (Create / Counter / Reject / List /
// Get) can be tested without the saga-layer dependencies.
type otcCRUDFixture struct {
	svc      *OTCOfferService
	offers   *repository.OTCOfferRepository
	holdings *repository.HoldingRepository
}

func newOTCCRUDFixture(t *testing.T) *otcCRUDFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.OTCOffer{},
		&model.OTCOfferRevision{},
		&model.OptionContract{},
		&model.OTCOfferReadReceipt{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	revRepo := repository.NewOTCOfferRevisionRepository(db)
	contractRepo := repository.NewOptionContractRepository(db)
	receiptRepo := repository.NewOTCReadReceiptRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)
	svc := NewOTCOfferService(offerRepo, revRepo, contractRepo, holdingRepo, receiptRepo, nil)
	return &otcCRUDFixture{svc: svc, offers: offerRepo, holdings: holdingRepo}
}

func (f *otcCRUDFixture) seedHolding(t *testing.T, ownerID uint64, stockID uint64, qty int64) {
	t.Helper()
	uid := ownerID
	if err := f.holdings.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: stockID, Quantity: qty,
		AveragePrice: decimal.NewFromInt(100),
	}); err != nil {
		t.Fatalf("seed holding: %v", err)
	}
}

// ---------------- Create ----------------

func TestOTCOfferService_Create_SellInitiated_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)

	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:     7,
		ActorSystemType: "client",
		Direction:       model.OTCDirectionSellInitiated,
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(150),
		Premium:         decimal.NewFromInt(20),
		SettlementDate:  time.Now().AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Status != model.OTCOfferStatusPending {
		t.Errorf("status=%s want pending", out.Status)
	}
	if out.InitiatorOwnerType != model.OwnerClient {
		t.Errorf("initiator owner type = %v", out.InitiatorOwnerType)
	}
}

func TestOTCOfferService_Create_StoresInitiatorAccount(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)

	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:        7,
		ActorSystemType:    "client",
		Direction:          model.OTCDirectionSellInitiated,
		StockID:            42,
		Quantity:           decimal.NewFromInt(10),
		StrikePrice:        decimal.NewFromInt(150),
		Premium:            decimal.NewFromInt(20),
		SettlementDate:     time.Now().AddDate(0, 0, 30),
		InitiatorAccountID: 9001,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.InitiatorAccountID != 9001 {
		t.Errorf("got %d, want 9001", out.InitiatorAccountID)
	}
}

func TestOTCOfferService_Create_RejectsZeroQuantity(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity:       decimal.Zero,
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected error for zero quantity")
	}
}

func TestOTCOfferService_Create_RejectsZeroStrikePrice(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.Zero,
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected error for zero strike price")
	}
}

func TestOTCOfferService_Create_RejectsNegativePremium(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(-1),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected error for negative premium")
	}
}

func TestOTCOfferService_Create_RejectsPastSettlementDate(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, -1),
	})
	if err == nil {
		t.Fatal("expected error for past settlement date")
	}
}

func TestOTCOfferService_Create_RejectsUnknownDirection(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: "weird",
		StockID:   42,
		Quantity:  decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected error for unknown direction")
	}
}

func TestOTCOfferService_Create_BuyInitiated_RequiresCounterparty(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionBuyInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected error: buy_initiated requires counterparty")
	}
}

func TestOTCOfferService_Create_CounterpartyHalfSet(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	cpID := int64(99)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction:          model.OTCDirectionSellInitiated,
		StockID:            42,
		Quantity:           decimal.NewFromInt(10),
		StrikePrice:        decimal.NewFromInt(150),
		Premium:            decimal.NewFromInt(20),
		SettlementDate:     time.Now().AddDate(0, 0, 30),
		CounterpartyUserID: &cpID,
		// CounterpartySystemType intentionally nil
	})
	if err == nil {
		t.Fatal("expected error when counterparty user_id is set without system_type")
	}
}

func TestOTCOfferService_Create_SellInitiated_NoSharesHeld(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// no holdings seeded
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity:       decimal.NewFromInt(10),
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected seller-no-holdings error")
	}
}

func TestOTCOfferService_Create_SellInitiated_InsufficientShares(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 5) // only 5 shares
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity:       decimal.NewFromInt(10), // requesting 10 > 5
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(20),
		SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err == nil {
		t.Fatal("expected insufficient-shares error")
	}
}

// ---------------- Counter ----------------

func TestOTCOfferService_Counter_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("seed offer: %v", err)
	}
	// Different actor (the buyer counters)
	updated, err := fx.svc.Counter(context.Background(), CounterInput{
		OfferID: out.ID, ActorUserID: 8, ActorSystemType: "client",
		Quantity: decimal.NewFromInt(5), StrikePrice: decimal.NewFromInt(160),
		Premium: decimal.NewFromInt(25), SettlementDate: time.Now().AddDate(0, 0, 31),
	})
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if updated.Status != model.OTCOfferStatusCountered {
		t.Errorf("status=%s want countered", updated.Status)
	}
	if !updated.Quantity.Equal(decimal.NewFromInt(5)) {
		t.Errorf("quantity=%s want 5", updated.Quantity)
	}
}

func TestOTCOfferService_Counter_MissingOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Counter(context.Background(), CounterInput{
		OfferID: 9999, ActorUserID: 8, ActorSystemType: "client",
		Quantity: decimal.NewFromInt(5), StrikePrice: decimal.NewFromInt(160),
		Premium: decimal.NewFromInt(25), SettlementDate: time.Now().AddDate(0, 0, 31),
	})
	if err == nil {
		t.Fatal("expected error for missing offer")
	}
}

func TestOTCOfferService_Counter_TerminalOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	out.Status = model.OTCOfferStatusRejected
	_ = fx.offers.Save(out)
	_, err := fx.svc.Counter(context.Background(), CounterInput{
		OfferID: out.ID, ActorUserID: 8, ActorSystemType: "client",
		Quantity: decimal.NewFromInt(5), StrikePrice: decimal.NewFromInt(160),
		Premium: decimal.NewFromInt(25), SettlementDate: time.Now().AddDate(0, 0, 31),
	})
	if err == nil {
		t.Fatal("expected error for terminal offer")
	}
}

func TestOTCOfferService_Counter_LastMoverGuard(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	// Same actor (the initiator/seller) cannot counter their own most-recent terms
	_, err := fx.svc.Counter(context.Background(), CounterInput{
		OfferID: out.ID, ActorUserID: 7, ActorSystemType: "client",
		Quantity: decimal.NewFromInt(5), StrikePrice: decimal.NewFromInt(160),
		Premium: decimal.NewFromInt(25), SettlementDate: time.Now().AddDate(0, 0, 31),
	})
	if err == nil {
		t.Fatal("expected last-mover guard")
	}
}

// ---------------- Reject ----------------

func TestOTCOfferService_Reject_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	rej, err := fx.svc.Reject(context.Background(), RejectInput{
		OfferID: out.ID, ActorUserID: 8, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rej.Status != model.OTCOfferStatusRejected {
		t.Errorf("status=%s want rejected", rej.Status)
	}
}

func TestOTCOfferService_Reject_MissingOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Reject(context.Background(), RejectInput{
		OfferID: 9999, ActorUserID: 8, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOfferService_Reject_TerminalOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	out.Status = model.OTCOfferStatusAccepted
	_ = fx.offers.Save(out)
	_, err := fx.svc.Reject(context.Background(), RejectInput{
		OfferID: out.ID, ActorUserID: 7, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error for terminal offer")
	}
}

// ---------------- ListMyOffers / GetOffer ----------------

func TestOTCOfferService_ListMyOffers_FindsByOwner(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	_, _ = fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	rows, total, err := fx.svc.ListMyOffers(7, "client", "initiator", nil, 0, 1, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d rows total %d", len(rows), total)
	}
}

func TestOTCOfferService_LastReadReceipt_NoOpWhenReceiptsNil(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// Bypass receipts wiring: copy svc with receipts=nil.
	bare := *fx.svc
	bare.receipts = nil
	r, err := bare.LastReadReceipt(7, "client", 1)
	if err != nil {
		t.Errorf("err=%v", err)
	}
	if r != nil {
		t.Errorf("expected nil")
	}
}

func TestOTCOfferService_GetOffer_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	got, revs, err := fx.svc.GetOffer(out.ID, 7, "client")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != out.ID {
		t.Errorf("id mismatch")
	}
	if len(revs) == 0 {
		t.Errorf("expected at least one revision")
	}
}

func TestOTCOfferService_GetOffer_NonParticipantRejected(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
		Premium: decimal.NewFromInt(20), SettlementDate: time.Now().AddDate(0, 0, 30),
	})
	_, _, err := fx.svc.GetOffer(out.ID, 999, "client")
	if err == nil {
		t.Fatal("expected non-participant rejection")
	}
}

func TestOTCOfferService_GetOffer_MissingOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, _, err := fx.svc.GetOffer(9999, 7, "client")
	if err == nil {
		t.Fatal("expected error for missing offer")
	}
}

// ---------------- Helpers: ptrCounterparty / otcOtherParty / actorToOwnerParty ----------------

func TestPtrCounterparty_Nil(t *testing.T) {
	o := &model.OTCOffer{}
	if got := ptrCounterparty(o); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestPtrCounterparty_NonNil(t *testing.T) {
	tp := model.OwnerClient
	uid := uint64(99)
	o := &model.OTCOffer{CounterpartyOwnerType: &tp, CounterpartyOwnerID: &uid}
	got := ptrCounterparty(o)
	if got == nil || got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 99 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_ActorIsInitiator(t *testing.T) {
	uid := uint64(7)
	cpType := model.OwnerClient
	cpID := uint64(8)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		CounterpartyOwnerType: &cpType, CounterpartyOwnerID: &cpID,
	}
	got := otcOtherParty(o, 7, "client")
	if got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 8 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_ActorIsCounterparty(t *testing.T) {
	uid := uint64(7)
	cpType := model.OwnerClient
	cpID := uint64(8)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		CounterpartyOwnerType: &cpType, CounterpartyOwnerID: &cpID,
	}
	got := otcOtherParty(o, 8, "client")
	if got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 7 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_NoCounterparty(t *testing.T) {
	uid := uint64(7)
	o := &model.OTCOffer{InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid}
	got := otcOtherParty(o, 7, "client")
	if (got != kafkamsg.OTCParty{}) {
		t.Errorf("expected zero OTCParty, got %+v", got)
	}
}

func TestActorToOwnerParty_Employee(t *testing.T) {
	tp, id := actorToOwnerParty(123, "employee")
	if tp != "bank" || id != nil {
		t.Errorf("got %s/%v want bank/nil", tp, id)
	}
}

func TestActorToOwnerParty_Bank(t *testing.T) {
	tp, id := actorToOwnerParty(0, "bank")
	if tp != "bank" || id != nil {
		t.Errorf("got %s/%v want bank/nil", tp, id)
	}
}

func TestActorToOwnerParty_Client(t *testing.T) {
	tp, id := actorToOwnerParty(99, "client")
	if tp != "client" || id == nil || *id != 99 {
		t.Errorf("got %s/%v want client/99", tp, id)
	}
}

// ---------------- assertSellerHasShares: nil holdings ----------------

func TestOTCOfferService_AssertSellerHasShares_NilLookup(t *testing.T) {
	svc := &OTCOfferService{}
	svc.holdings = nil
	uid := uint64(7)
	err := svc.assertSellerHasShares(model.OwnerClient, &uid, 42, decimal.NewFromInt(1))
	if err == nil || !errors.Is(err, err) { // sanity
		t.Fatalf("expected error: %v", err)
	}
}

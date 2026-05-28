package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func newOTCExpiryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
		&model.OTCOffer{},
		&model.OTCOfferRevision{},
		&model.OptionContract{},
		&model.PeerOptionContract{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOTCExpiryCron_NewDefaultsBatchAndCron(t *testing.T) {
	cr := NewOTCExpiryCron(nil, nil, nil, nil, 0, "", nilRegistry())
	if cr.batchSize != 500 {
		t.Errorf("batchSize=%d", cr.batchSize)
	}
	if cr.cronUTC != "02:00" {
		t.Errorf("cron=%q", cr.cronUTC)
	}
}

// expireOffer marks an offer expired and tries to publish (no producer wired).
func TestOTCExpiryCron_ExpireOffer(t *testing.T) {
	db := newOTCExpiryDB(t)
	offerRepo := repository.NewOTCOfferRepository(db)
	cr := NewOTCExpiryCron(repository.NewOptionContractRepository(db), offerRepo, nil, nil, 10, "02:00", nilRegistry())

	uid := uint64(7)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100),
		Premium: decimal.NewFromInt(20), Status: model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7,
		SettlementDate: time.Now().Add(-24 * time.Hour),
	}
	if err := offerRepo.Create(o); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.expireOffer(context.Background(), o); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ := offerRepo.GetByID(o.ID)
	if got.Status != model.OTCOfferStatusExpired {
		t.Errorf("status=%s", got.Status)
	}
}

// expireContract marks the contract expired without a holdingRes wired.
func TestOTCExpiryCron_ExpireContract_NoHoldingRes(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, repository.NewOTCOfferRepository(db), nil, nil, 10, "02:00", nilRegistry())

	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(-24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ := contractRepo.GetByID(c.ID)
	if got.Status != model.OptionContractStatusExpired {
		t.Errorf("status=%s", got.Status)
	}
	if got.ExpiredAt == nil {
		t.Error("expected expired_at to be set")
	}
}

// expireOffer emits OTC_OFFER_EXPIRED to the initiator (and counterparty when
// it is a client party).
func TestOTCExpiryCron_ExpireOffer_EmitsNotifications(t *testing.T) {
	db := newOTCExpiryDB(t)
	offerRepo := repository.NewOTCOfferRepository(db)
	cr := NewOTCExpiryCron(repository.NewOptionContractRepository(db), offerRepo, nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	initiatorUID := uint64(7)
	cpType := model.OwnerClient
	cpUID := uint64(9)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &initiatorUID,
		CounterpartyOwnerType: &cpType, CounterpartyOwnerID: &cpUID,
		Direction: model.OTCDirectionSellInitiated, StockID: 42, Ticker: "AAPL",
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100),
		Premium: decimal.NewFromInt(20), Status: model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7,
		SettlementDate: time.Now().Add(-24 * time.Hour),
	}
	if err := offerRepo.Create(o); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.expireOffer(context.Background(), o); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_OFFER_EXPIRED"); got != 2 {
		t.Fatalf("OTC_OFFER_EXPIRED count = %d, want 2 (initiator + counterparty)", got)
	}
	sawInitiator, sawCounterparty := false, false
	for _, m := range notifier.notifs {
		if m.RefType != "otc_offer" || m.RefID != o.ID {
			t.Errorf("notif ref = %s/%d, want otc_offer/%d", m.RefType, m.RefID, o.ID)
		}
		if m.Data["ticker"] != "AAPL" {
			t.Errorf("notif ticker = %q, want AAPL", m.Data["ticker"])
		}
		if m.UserID == initiatorUID {
			sawInitiator = true
		}
		if m.UserID == cpUID {
			sawCounterparty = true
		}
	}
	if !sawInitiator || !sawCounterparty {
		t.Errorf("expected notifications to initiator (%d) and counterparty (%d); sawInitiator=%v sawCounterparty=%v", initiatorUID, cpUID, sawInitiator, sawCounterparty)
	}
}

// expireOffer with no counterparty (open offer) notifies only the initiator.
func TestOTCExpiryCron_ExpireOffer_NoCounterparty_NotifiesInitiatorOnly(t *testing.T) {
	db := newOTCExpiryDB(t)
	offerRepo := repository.NewOTCOfferRepository(db)
	cr := NewOTCExpiryCron(repository.NewOptionContractRepository(db), offerRepo, nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	initiatorUID := uint64(7)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &initiatorUID,
		Direction: model.OTCDirectionSellInitiated, StockID: 42, Ticker: "AAPL",
		Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100),
		Premium: decimal.NewFromInt(20), Status: model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7,
		SettlementDate: time.Now().Add(-24 * time.Hour),
	}
	if err := offerRepo.Create(o); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.expireOffer(context.Background(), o); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_OFFER_EXPIRED"); got != 1 {
		t.Fatalf("OTC_OFFER_EXPIRED count = %d, want 1 (initiator only)", got)
	}
}

// expireContract emits OTC_CONTRACT_EXPIRED to both client parties; a bank
// party is skipped.
func TestOTCExpiryCron_ExpireContract_EmitsNotifications(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, repository.NewOTCOfferRepository(db), nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	buyerUID := uint64(7)
	sellerUID := uint64(8)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(-24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_CONTRACT_EXPIRED"); got != 2 {
		t.Fatalf("OTC_CONTRACT_EXPIRED count = %d, want 2 (buyer + seller)", got)
	}
	sawBuyer, sawSeller := false, false
	for _, m := range notifier.notifs {
		if m.RefType != "otc_contract" || m.RefID != c.ID {
			t.Errorf("notif ref = %s/%d, want otc_contract/%d", m.RefType, m.RefID, c.ID)
		}
		if m.Data["ticker"] != "AAPL" {
			t.Errorf("notif ticker = %q, want AAPL", m.Data["ticker"])
		}
		if m.UserID == buyerUID {
			sawBuyer = true
		}
		if m.UserID == sellerUID {
			sawSeller = true
		}
	}
	if !sawBuyer || !sawSeller {
		t.Errorf("expected notifications to both buyer (%d) and seller (%d); sawBuyer=%v sawSeller=%v", buyerUID, sellerUID, sawBuyer, sawSeller)
	}
}

// expireContract against a bank seller notifies only the client buyer.
func TestOTCExpiryCron_ExpireContract_BankParty_Skipped(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, repository.NewOTCOfferRepository(db), nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	buyerUID := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(-24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_CONTRACT_EXPIRED"); got != 1 {
		t.Fatalf("OTC_CONTRACT_EXPIRED count = %d, want 1 (buyer only; bank seller skipped)", got)
	}
	for _, m := range notifier.notifs {
		if m.UserID != buyerUID {
			t.Errorf("unexpected notification to UserID=%d (want only buyer %d)", m.UserID, buyerUID)
		}
	}
}

// otcNextRunAt: same day vs next day handling.
func TestOTCNextRunAt_FutureToday(t *testing.T) {
	now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "02:00")
	if got.Hour() != 2 || got.Minute() != 0 {
		t.Errorf("got %v", got)
	}
	if got.Day() != 1 {
		t.Errorf("expected same day, got %v", got)
	}
}

func TestOTCNextRunAt_PastTodayWrapsTomorrow(t *testing.T) {
	now := time.Date(2026, 5, 1, 5, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "02:00")
	if got.Day() != 2 {
		t.Errorf("expected next day, got %v", got)
	}
}

func TestOTCNextRunAt_BadFormatFallsBackTo0200(t *testing.T) {
	now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "garbage")
	if got.Hour() != 2 || got.Minute() != 0 {
		t.Errorf("got %v want 02:00", got)
	}
}

// Start triggers RunOnce immediately + spawns a goroutine; cancel ctx
// to make it return promptly.
func TestOTCExpiryCron_StartAndCancel(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		repository.NewOTCOfferRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cr.Start(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// RunOnce should be a no-op when nothing is expiring.
func TestOTCExpiryCron_RunOnce_NothingToExpire(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		repository.NewOTCOfferRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// TestOTCExpiryCron_WithOutbox covers the WithOutbox wiring helper.
func TestOTCExpiryCron_WithOutbox(t *testing.T) {
	cr := NewOTCExpiryCron(nil, nil, nil, nil, 10, "02:00", nilRegistry())
	out := cr.WithOutbox(nil, nil)
	if out == nil {
		t.Error("WithOutbox returned nil")
	}
}

// WithPeerContracts wires the optional peer-contracts repo + verifies
// RunOnce still completes without error when peer rows are absent.
func TestOTCExpiryCron_WithPeerContracts(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		repository.NewOTCOfferRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	cr2 := cr.WithPeerContracts(repository.NewPeerOptionContractRepository(db))
	if cr2.peerContracts == nil {
		t.Fatal("expected peer repo wired")
	}
	if err := cr2.RunOnce(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// expirePeerContract on a CREDIT-direction row: just sets status, does NOT
// touch holdings (because the buyer's bank holds no lock).
func TestOTCExpiryCron_ExpirePeerContract_CreditDirection(t *testing.T) {
	db := newOTCExpiryDB(t)
	peerRepo := repository.NewPeerOptionContractRepository(db)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		repository.NewOTCOfferRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	).WithPeerContracts(peerRepo)

	c := &model.PeerOptionContract{
		CrossbankTxID: "tx-1", PostingIndex: 0,
		Ticker: "AAPL", Quantity: 10, StrikePrice: decimal.NewFromInt(100),
		Currency: "USD", SettlementDate: "2025-01-01",
		Direction: "CREDIT", Status: "active",
	}
	if err := peerRepo.UpsertIdempotent(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.expirePeerContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ := peerRepo.GetByID(c.ID)
	if got.Status != "expired" {
		t.Errorf("status=%s", got.Status)
	}
}

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

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------- mocks ----------------

type fakeOTCAccountClient struct {
	*fakeFundAccountClient // re-uses Get/Credit/Debit + accounts map

	failReserveOnce error
	failSettleOnce  error
	releaseCalls    int
	reserveCalls    int
	settleCalls     int
}

func (f *fakeOTCAccountClient) ReserveFunds(_ context.Context, _, _ uint64, amount decimal.Decimal, _ string, _ string, _ string) (*accountpb.ReserveFundsResponse, error) {
	f.reserveCalls++
	if f.failReserveOnce != nil {
		err := f.failReserveOnce
		f.failReserveOnce = nil
		return nil, err
	}
	return &accountpb.ReserveFundsResponse{}, nil
}

func (f *fakeOTCAccountClient) ReleaseReservation(_ context.Context, _ uint64, _ string, _ string) (*accountpb.ReleaseReservationResponse, error) {
	f.releaseCalls++
	return &accountpb.ReleaseReservationResponse{}, nil
}

func (f *fakeOTCAccountClient) PartialSettleReservation(_ context.Context, _, _ uint64, _ decimal.Decimal, _ string, _ string, _ string) (*accountpb.PartialSettleReservationResponse, error) {
	f.settleCalls++
	if f.failSettleOnce != nil {
		err := f.failSettleOnce
		f.failSettleOnce = nil
		return nil, err
	}
	return &accountpb.PartialSettleReservationResponse{}, nil
}

// ---------------- fixture ----------------

type acceptSagaFixture struct {
	svc           *OTCOfferService
	offers        *repository.OTCOfferRepository
	contracts     *repository.OptionContractRepository
	holdings      *repository.HoldingRepository
	holdingResSvc *HoldingReservationService
	accounts      *fakeOTCAccountClient
	exchange      *fakeFundExchangeClient
	saga          *fakeSagaRepo
	notifier      *recordingOTCNotifier
	offer         *model.OTCOffer
	stockID       uint64
	sellerID      int64
	buyerID       int64
}

func newAcceptSagaFixture(t *testing.T) *acceptSagaFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
		&model.HoldingCreditMarker{},
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
	holdingResRepo := repository.NewHoldingReservationRepository(db)
	holdingResSvc := NewHoldingReservationService(db, holdingRepo, holdingResRepo)
	saga := newFakeSagaRepo()
	accountFake := newFakeFundAccountClient()
	accounts := &fakeOTCAccountClient{fakeFundAccountClient: accountFake}
	exch := &fakeFundExchangeClient{}

	svc := NewOTCOfferService(offerRepo, revRepo, contractRepo, holdingRepo, receiptRepo, nil)
	svc = svc.WithSaga(saga, accounts, exch, holdingResSvc, holdingRepo)
	notifier := &recordingOTCNotifier{}
	svc.notifier = notifier

	stockID := uint64(42)
	sellerID := int64(87)
	buyerID := int64(55)
	// Seed seller's holding so the seller-invariant + reservation succeed.
	sellerUID := uint64(sellerID)
	_ = holdingRepo.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &sellerUID,
		SecurityType: "stock", SecurityID: stockID, Quantity: 100,
		AveragePrice: decimal.NewFromInt(100),
	})
	// Seed accounts.
	accounts.addAccount(5001, "BUYER-RSD", "1000000")
	accounts.accounts[5001].CurrencyCode = "RSD"
	accounts.addAccount(6001, "SELLER-RSD", "0")
	accounts.accounts[6001].CurrencyCode = "RSD"
	accounts.addAccount(5002, "BUYER-EUR", "1000000")
	accounts.accounts[5002].CurrencyCode = "EUR"

	// Seed offer.
	offer := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &sellerUID,
		Direction: model.OTCDirectionSellInitiated,
		StockID:   stockID,
		Quantity:  decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(5000),
		Premium:                     decimal.NewFromInt(50000),
		SettlementDate:              time.Now().AddDate(0, 0, 7),
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   uint64(sellerID),
		InitiatorAccountID:          6001, // sell_initiated → initiator is the seller
	}
	if err := offerRepo.Create(offer); err != nil {
		t.Fatalf("seed offer: %v", err)
	}

	return &acceptSagaFixture{
		svc: svc, offers: offerRepo, contracts: contractRepo, holdings: holdingRepo,
		holdingResSvc: holdingResSvc, accounts: accounts, exchange: exch, saga: saga,
		notifier: notifier,
		offer:    offer, stockID: stockID, sellerID: sellerID, buyerID: buyerID,
	}
}

// countNotifs returns how many recorded notifications have the given Type.
func countNotifs(notifs []kafkamsg.GeneralNotificationMessage, notifType string) int {
	n := 0
	for _, m := range notifs {
		if m.Type == notifType {
			n++
		}
	}
	return n
}

// ---------------- happy path ----------------

func TestAcceptSaga_SameCurrency_HappyPath(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	out, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if out.Status != model.OptionContractStatusActive {
		t.Errorf("status %s", out.Status)
	}
	// sell_initiated: initiator (seller) account bound at create = 6001;
	// acceptor (buyer) binds 5001 now. Contract records both.
	if out.SellerAccountID != 6001 || out.BuyerAccountID != 5001 {
		t.Errorf("contract accounts: buyer=%d seller=%d, want 5001/6001", out.BuyerAccountID, out.SellerAccountID)
	}
	if !fx.accounts.sumCredited("SELLER-RSD").Equal(decimal.NewFromInt(50000)) {
		t.Errorf("seller credit: got %s want 50000", fx.accounts.sumCredited("SELLER-RSD"))
	}
	got, _ := fx.offers.GetByID(fx.offer.ID)
	if got.Status != model.OTCOfferStatusAccepted {
		t.Errorf("offer status %s want ACCEPTED", got.Status)
	}
}

// ---------------- in-app notifications ----------------

// On a successful accept both client parties (buyer + seller) get an
// OTC_CONTRACT_CREATED in-app notification.
func TestAcceptSaga_EmitsContractCreatedNotifications(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got := countNotifs(fx.notifier.notifs, "OTC_CONTRACT_CREATED"); got != 2 {
		t.Fatalf("OTC_CONTRACT_CREATED count = %d, want 2 (buyer + seller)", got)
	}
	buyerUID := uint64(fx.buyerID)
	sellerUID := uint64(fx.sellerID)
	sawBuyer, sawSeller := false, false
	for _, m := range fx.notifier.notifs {
		if m.Type != "OTC_CONTRACT_CREATED" {
			continue
		}
		if m.RefType != "otc_contract" || m.RefID != contract.ID {
			t.Errorf("notif ref = %s/%d, want otc_contract/%d", m.RefType, m.RefID, contract.ID)
		}
		if m.Data["ticker"] != contract.Ticker {
			t.Errorf("notif ticker = %q, want %q", m.Data["ticker"], contract.Ticker)
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

// A bank counterparty receives no in-app notification (notifyOTCParty no-ops
// for non-client owners). Here the offer is buy_initiated by the bank, so the
// bank is the buyer and the accepting client is the seller.
func TestAcceptSaga_BankParty_NoNotification(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Re-shape the seeded offer into a buy_initiated offer where the bank is
	// the initiator (buyer). The client seller (sellerID) accepts it and
	// binds account 6001; the bank-buyer account is the funded 5001.
	fx.offer.Direction = model.OTCDirectionBuyInitiated
	fx.offer.InitiatorOwnerType = model.OwnerBank
	fx.offer.InitiatorOwnerID = nil
	fx.offer.InitiatorAccountID = 5001 // buy_initiated → initiator account is the buyer account
	// Last-modified-by must not be the accepting client (self-accept guard).
	fx.offer.LastModifiedByPrincipalType = "employee"
	fx.offer.LastModifiedByPrincipalID = 1
	if err := fx.offers.Save(fx.offer); err != nil {
		t.Fatalf("save offer: %v", err)
	}
	if _, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.sellerID, ActorSystemType: "client",
		AcceptorAccountID: 6001,
	}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Only the seller (client) should be notified — the bank buyer must not.
	if got := countNotifs(fx.notifier.notifs, "OTC_CONTRACT_CREATED"); got != 1 {
		t.Fatalf("OTC_CONTRACT_CREATED count = %d, want 1 (seller only; bank buyer skipped)", got)
	}
	sellerUID := uint64(fx.sellerID)
	for _, m := range fx.notifier.notifs {
		if m.UserID != sellerUID {
			t.Errorf("unexpected notification to UserID=%d (want only seller %d)", m.UserID, sellerUID)
		}
	}
}

// ---------------- cross-currency ----------------

func TestAcceptSaga_CrossCurrency_DebitsBuyerInBuyerCcy(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	fx.exchange.rate = "0.0086"
	fx.exchange.convert = "430"
	out, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5002, // buyer EUR, seller RSD
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if out.PremiumCurrency != "RSD" {
		t.Errorf("premium currency on contract %s want RSD", out.PremiumCurrency)
	}
	// Seller credited 50000 RSD (no conversion of seller-side amount).
	if !fx.accounts.sumCredited("SELLER-RSD").Equal(decimal.NewFromInt(50000)) {
		t.Errorf("seller credit got %s want 50000", fx.accounts.sumCredited("SELLER-RSD"))
	}
}

// ---------------- compensation: reserve_premium fails ----------------

func TestAcceptSaga_ReservePremiumFails_DropsContract(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	fx.accounts.failReserveOnce = errors.New("buyer has no money")
	_, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Contract should not exist (dropped in compensation).
	if _, err := fx.contracts.GetByOfferID(fx.offer.ID); err == nil {
		t.Error("contract row should have been dropped")
	}
	// Seller's holding reservation should have been released.
	checkUID := uint64(fx.sellerID)
	h, _ := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &checkUID, "stock", fx.stockID)
	if h.ReservedQuantity != 0 {
		t.Errorf("seller's holding still has %d reserved (expected 0 after compensation)", h.ReservedQuantity)
	}
}

// ---------------- compensation: settle_premium_buyer fails ----------------

func TestAcceptSaga_SettlePremiumFails_ReleasesReservationAndDropsContract(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	fx.accounts.failSettleOnce = errors.New("settle boom")
	_, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Reservation release was triggered.
	if fx.accounts.releaseCalls == 0 {
		t.Errorf("expected at least one ReleaseReservation call, got 0")
	}
	if _, err := fx.contracts.GetByOfferID(fx.offer.ID); err == nil {
		t.Error("contract row should have been dropped")
	}
}

// ---------------- last-mover rule ----------------

func TestAcceptSaga_LastMoverRule_RejectsSelfAccept(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Last-modified-by is the seller (initiator). Seller trying to accept
	// their own offer must be rejected.
	_, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.sellerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err == nil {
		t.Fatal("expected last-mover rejection")
	}
}

// ---------------- terminal-state guard ----------------

func TestAcceptSaga_TerminalOffer_Rejected(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	fx.offer.Status = model.OTCOfferStatusRejected
	_ = fx.offers.Save(fx.offer)
	_, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err == nil {
		t.Fatal("expected terminal-state rejection")
	}
}

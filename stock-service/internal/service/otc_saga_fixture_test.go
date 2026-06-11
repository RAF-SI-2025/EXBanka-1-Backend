package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// This file holds the shared OTC contract-formation test fixture. It used to
// live alongside the legacy single-chain accept saga tests (deleted in R12);
// the fixture + mocks + helpers survive because the LIVE exercise/expiry tests
// still build on them. The legacy accept saga itself is gone — to obtain an
// ACTIVE contract for exercise/expiry tests use mintActiveContract, which mints
// the contract + reserves the seller's holding directly (the precondition the
// exercise saga operates on) without any legacy code path.

// ---------------- mocks ----------------

type fakeOTCAccountClient struct {
	*fakeFundAccountClient // re-uses Get/Credit/Debit + accounts map

	failReserveOnce error
	failSettleOnce  error
	releaseCalls    int
	reserveCalls    int
	settleCalls     int
	// settleTxnIDs records every order_transaction_id passed to
	// PartialSettleReservation. account-service enforces a global
	// UNIQUE(order_transaction_id) on the settlements table, so a constant
	// (e.g. literal 1) silently no-ops every OTC settle after the first one
	// ever — the buyer is never debited while the seller is still credited
	// (money created from nothing). This slice lets a test assert the id is
	// collision-resistant.
	settleTxnIDs []uint64
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

func (f *fakeOTCAccountClient) PartialSettleReservation(_ context.Context, _, orderTransactionID uint64, _ decimal.Decimal, _ string, _ string, _ string) (*accountpb.PartialSettleReservationResponse, error) {
	f.settleCalls++
	f.settleTxnIDs = append(f.settleTxnIDs, orderTransactionID)
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

	// Seed a termless listing. Option terms (strike/premium/settlement) live on
	// the negotiation chain / minted contract, not on the offer (R12).
	offer := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &sellerUID,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Quantity:                    decimal.NewFromInt(10),
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

// mintActiveContract mints an ACTIVE OptionContract for the fixture's offer and
// reserves the seller's underlying holding — the exact precondition the
// exercise/expiry sagas operate on. It replaces the deleted legacy
// OTCOfferService.Accept as a test setup primitive: terms (strike 5000,
// premium 50000, qty from the offer, settlement 7 days out) match what the
// fixture's offer used to carry before the R12 termless-listing refactor, so
// the exercise/expiry assertions are unchanged.
func (fx *acceptSagaFixture) mintActiveContract(t *testing.T) *model.OptionContract {
	t.Helper()
	buyerUID := uint64(fx.buyerID)
	sellerUID := uint64(fx.sellerID)
	offerID := fx.offer.ID
	c := &model.OptionContract{
		OfferID:        &offerID,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		StockID: fx.stockID, Ticker: fx.offer.Ticker,
		Quantity: fx.offer.Quantity, StrikePrice: decimal.NewFromInt(5000),
		PremiumPaid: decimal.NewFromInt(50000), PremiumCurrency: "RSD", StrikeCurrency: "RSD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, 7), Status: model.OptionContractStatusActive,
		SagaID: uuid.NewString(), PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID: 5001, SellerAccountID: 6001,
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("mint contract: %v", err)
	}
	if _, err := fx.holdingResSvc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &sellerUID, "stock", fx.stockID, c.ID, c.Quantity.IntPart()); err != nil {
		t.Fatalf("reserve seller holding: %v", err)
	}
	return c
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

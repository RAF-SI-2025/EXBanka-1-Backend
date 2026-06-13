package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// otcOptionsHandlerFixture wires an OTCOptionsHandler against a sqlite DB so
// the gRPC RPCs can be exercised end-to-end without docker / kafka. The
// service underneath is a real OTCOfferService with a real holdings repo.
type otcOptionsHandlerFixture struct {
	h         *OTCOptionsHandler
	db        *gorm.DB
	holdings  *repository.HoldingRepository
	offers    *repository.OTCOfferRepository
	contracts *repository.OptionContractRepository
}

func newOTCOptionsHandlerFixture(t *testing.T) *otcOptionsHandlerFixture {
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
		&model.Listing{},
		&model.Stock{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return newOTCOptionsHandlerFixtureFromDB(t, db)
}

// newOTCOptionsHandlerFixtureFromDB wires the fixture against a caller-supplied
// (already-migrated) DB so tests that need extra tables (e.g. OTCNegotiation
// for the SP-2b my_negotiation_id stamping) can pre-migrate them.
func newOTCOptionsHandlerFixtureFromDB(t *testing.T, db *gorm.DB) *otcOptionsHandlerFixture {
	t.Helper()
	offerRepo := repository.NewOTCOfferRepository(db)
	revRepo := repository.NewOTCOfferRevisionRepository(db)
	contractRepo := repository.NewOptionContractRepository(db)
	receiptRepo := repository.NewOTCReadReceiptRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)

	svc := service.NewOTCOfferService(offerRepo, revRepo, contractRepo, holdingRepo, receiptRepo, nil)
	h := NewOTCOptionsHandler(svc, contractRepo)
	return &otcOptionsHandlerFixture{
		h: h, db: db,
		holdings: holdingRepo, offers: offerRepo, contracts: contractRepo,
	}
}

func (f *otcOptionsHandlerFixture) seedSellerHolding(t *testing.T, ownerID uint64, stockID uint64, qty int64) {
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

func (f *otcOptionsHandlerFixture) createOffer(t *testing.T, sellerID int64, stockID uint64) uint64 {
	t.Helper()
	resp, err := f.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: sellerID, ActorSystemType: "client",
		Direction:      model.OTCDirectionSellInitiated,
		StockId:        stockID,
		Quantity:       "10",
		StrikePrice:    "150",
		Premium:        "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return resp.GetId()
}

// ---------------- CreateOffer ----------------

func TestOTCOptionsHandler_CreateOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetId() == 0 || resp.GetStatus() != model.OTCOfferStatusPending {
		t.Errorf("got %+v", resp)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadQuantity(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "abc", StrikePrice: "1", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadStrike(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "abc", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadPremium(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "abc", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadDate(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "1", SettlementDate: "not-a-date",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BankOffer_CapturesActingEmployee(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	// Employee 17 acting AS the bank: gateway sends actor_system_type "bank",
	// actor_user_id 0, acting_employee_id 17. Buy_initiated needs no holdings.
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 0, ActorSystemType: "bank", ActingEmployeeId: 17,
		Direction: model.OTCDirectionBuyInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got, err := fx.offers.GetByID(resp.GetId())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.InitiatorOwnerType != model.OwnerBank {
		t.Errorf("initiator owner type = %v, want bank", got.InitiatorOwnerType)
	}
	if got.ActingEmployeeID == nil || *got.ActingEmployeeID != 17 {
		t.Fatalf("persisted ActingEmployeeID = %v, want 17", got.ActingEmployeeID)
	}
}

func TestOTCOptionsHandler_CreateOffer_ClientOffer_NoActingEmployee(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	// Even if an acting_employee_id is present, a client-owned offer must not
	// capture it (employee acting on behalf of a client).
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client", ActingEmployeeId: 17,
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got, err := fx.offers.GetByID(resp.GetId())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ActingEmployeeID != nil {
		t.Errorf("persisted ActingEmployeeID = %v, want nil for client offer", *got.ActingEmployeeID)
	}
}

func TestOTCOptionsHandler_CreateOffer_WithCounterparty(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		Counterparty:   &stockpb.PartyRef{UserId: 8, SystemType: "client"},
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetCounterparty() == nil || resp.GetCounterparty().UserId != 8 {
		t.Errorf("counterparty wiring lost: %+v", resp.GetCounterparty())
	}
}

func TestOTCOptionsHandler_CreateOffer_NoSharesError(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err == nil {
		t.Fatal("expected error from service")
	}
}

// ---------------- ListMyOffers ----------------

func TestOTCOptionsHandler_ListMyOffers(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	_ = fx.createOffer(t, 7, 42)
	resp, err := fx.h.ListMyOffers(context.Background(), &stockpb.ListMyOTCOffersRequest{
		ActorUserId: 7, ActorSystemType: "client", Role: "initiator",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 1 || len(resp.GetOffers()) != 1 {
		t.Errorf("got total=%d len=%d", resp.GetTotal(), len(resp.GetOffers()))
	}
	// caller is last-modifier so unread should be false
	if resp.GetOffers()[0].GetUnread() {
		t.Errorf("expected unread=false (caller was last modifier)")
	}
}

// ---------------- GetOffer ----------------

func TestOTCOptionsHandler_GetOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	resp, err := fx.h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: id, ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOffer().GetId() != id {
		t.Errorf("id mismatch")
	}
	if len(resp.GetRevisions()) == 0 {
		t.Errorf("expected at least one revision")
	}
}

func TestOTCOptionsHandler_GetOffer_NotFound(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// fakeRemoteOffers is a stub RemoteOfferGetter for GetOffer's remote-resolution
// path. row==nil + err set simulates a mirror miss.
type fakeRemoteOffers struct {
	row *model.OTCOffer
	err error
}

func (f *fakeRemoteOffers) GetRemoteByID(uint64) (*model.OTCOffer, error) {
	if f.row != nil {
		return f.row, nil
	}
	if f.err != nil {
		return nil, f.err
	}
	return nil, gorm.ErrRecordNotFound
}

// SP-1: a local offer carries provenance (kind/routing/bank_code) and me_owner
// computed from the acting identity.
func TestOTCOptionsHandler_GetOffer_LocalProvenanceAndMeOwner(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	h := fx.h.WithPeerContracts(nil, 111).WithRemoteOffers(&fakeRemoteOffers{}, "111")

	// Owner (client 7) sees me_owner=true.
	resp, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: id, ActorUserId: 7, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	o := resp.GetOffer()
	if o.GetKind() != "local" {
		t.Errorf("kind = %q, want local", o.GetKind())
	}
	if o.GetRoutingNumber() != 111 {
		t.Errorf("routing = %d, want 111", o.GetRoutingNumber())
	}
	if o.GetBankCode() != "111" {
		t.Errorf("bank_code = %q, want 111", o.GetBankCode())
	}
	if !o.GetMeOwner() {
		t.Errorf("me_owner = false, want true for owner")
	}

	// A different client is a participant only if counterparty; here client 8
	// is not on the offer, so GetOffer would reject. Instead assert that the
	// owner-id mismatch flips me_owner off when computed directly: re-fetch as
	// the offer's owner but with a non-matching acting identity is not a valid
	// participant scenario, so cover the helper via me_owner=false through a
	// bank caller (bank does not own a client-seller listing).
	respBank, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: id, ActorUserId: 7, ActorSystemType: "client",
		ActingOwnerType: "bank", ActingOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if respBank.GetOffer().GetMeOwner() {
		t.Errorf("me_owner = true, want false for bank caller on client listing")
	}
}

// SP-1: an id that is not a local offer resolves from the cross-bank mirror
// with kind="remote" and me_owner=false.
func TestOTCOptionsHandler_GetOffer_RemoteResolution(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	foreignID := "abc"
	bankCode := "222"
	sellerID := "client-9"
	remote := &model.OTCOffer{
		ID: 555, RoutingNumber: 222, NativeID: &foreignID,
		InitiatorBankCode: &bankCode, RemoteSellerID: &sellerID,
		InitiatorOwnerType: model.OwnerBank,
		Direction:          model.OTCDirectionSellInitiated,
		Ticker:             "AAPL", Quantity: decimal.NewFromInt(10),
		Status: "open",
	}
	h := fx.h.WithRemoteOffers(&fakeRemoteOffers{row: remote}, "111")

	resp, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 555, ActorUserId: 7, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	o := resp.GetOffer()
	if o.GetKind() != "remote" {
		t.Errorf("kind = %q, want remote", o.GetKind())
	}
	if o.GetId() != 555 {
		t.Errorf("id = %d, want 555", o.GetId())
	}
	if o.GetRoutingNumber() != 222 || o.GetBankCode() != "222" {
		t.Errorf("routing/bank = %d/%q, want 222/222", o.GetRoutingNumber(), o.GetBankCode())
	}
	if o.GetStockTicker() != "AAPL" {
		t.Errorf("ticker = %q, want AAPL", o.GetStockTicker())
	}
	if o.GetQuantity() != "10" {
		t.Errorf("quantity = %q, want 10", o.GetQuantity())
	}
	if o.GetMeOwner() {
		t.Errorf("me_owner = true, want false for remote")
	}
	if len(resp.GetRevisions()) != 0 {
		t.Errorf("remote offer should carry no revisions")
	}
}

// SP-1: neither a local offer nor a mirror row exists -> NotFound.
func TestOTCOptionsHandler_GetOffer_RemoteMissStillNotFound(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	h := fx.h.WithRemoteOffers(&fakeRemoteOffers{err: gorm.ErrRecordNotFound}, "111")
	_, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

// SP-1 review: a non-NotFound error from the remote mirror must surface as
// Internal, not as a false 404. Before the fix, the error was dropped and
// the handler fell through to return the original local NotFound.
func TestOTCOptionsHandler_GetOffer_RemoteInternalErrorSurfacedAsInternal(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	h := fx.h.WithRemoteOffers(&fakeRemoteOffers{err: errors.New("db down")}, "111")
	_, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal (mirror DB error must not look like 404)", status.Code(err))
	}
}

// ---------------- ListMyContracts ----------------

func TestOTCOptionsHandler_ListMyContracts_NoContractsRepoWired(t *testing.T) {
	// When constructed without a contracts repo, ListMyContracts returns empty.
	svc := &service.OTCOfferService{}
	h := &OTCOptionsHandler{svc: svc} // intentionally bare
	resp, err := h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 0 {
		t.Errorf("expected empty")
	}
}

func TestOTCOptionsHandler_ListMyContracts_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	resp, err := fx.h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client", Role: "buyer",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 0 {
		t.Errorf("expected 0 contracts initially")
	}
}

// TestOTCOptionsHandler_ListMyContracts_WithPeerContracts verifies that wiring
// the peer-contracts repo (empty) results in no remote contracts in the unified
// list. Remote contracts appear only in Contracts[] with kind=remote (SP-1
// double-listing fix). The legacy PeerContracts/PeerTotal response fields were
// removed in SP-2b, so there is nothing separate to assert empty — Contracts[]
// is the single merged list.
func TestOTCOptionsHandler_ListMyContracts_WithPeerContracts(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	peerRepo := repository.NewOptionContractRepository(fx.db)
	h := fx.h.WithPeerContracts(peerRepo, 111)
	resp, err := h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// No local contracts seeded and the peer repo is empty → the unified list
	// is empty. (No double-listing: remote rows would land in Contracts[].)
	if resp.GetTotal() != 0 || len(resp.GetContracts()) != 0 {
		t.Errorf("contracts must be empty; total=%d len=%d", resp.GetTotal(), len(resp.GetContracts()))
	}
}

// ---------------- GetContract ----------------

func TestOTCOptionsHandler_GetContract_NoRepoWired(t *testing.T) {
	h := &OTCOptionsHandler{}
	_, err := h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: 1, ActorUserId: 7, ActorSystemType: "client",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

func TestOTCOptionsHandler_GetContract_NotFound(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOptionsHandler_GetContract_HappyPath_BuyerView(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromInt(20),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  time.Now().Add(30 * 24 * time.Hour),
		Status:          model.OptionContractStatusActive,
		BuyerOwnerType:  model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	resp, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: c.ID, ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetId() != c.ID {
		t.Errorf("id mismatch")
	}
}

func TestOTCOptionsHandler_GetContract_NonParticipant(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	_ = fx.contracts.Create(c)
	_, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: c.ID, ActorUserId: 99, ActorSystemType: "client",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------- marketRefPrice / WithListings ----------------

func TestOTCOptionsHandler_MarketRefPrice_NoListings(t *testing.T) {
	h := &OTCOptionsHandler{}
	if got := h.marketRefPrice(42); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestOTCOptionsHandler_WithListings_AddsListingsRepo(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	listingRepo := repository.NewListingRepository(fx.db)
	h2 := fx.h.WithListings(listingRepo)
	// Pure smoke — no listing rows present, returns "".
	if got := h2.marketRefPrice(42); got != "" {
		t.Errorf("expected empty for unknown stock, got %q", got)
	}
}

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// newHistoryFixture builds an OTCOptionsHandler whose history path is backed by
// a sqlite OTCOfferService (LOCAL terminal offers) plus a fake peer lister
// (REMOTE terminal chains), with own routing/bank-code wired like main.go.
func newHistoryFixture(t *testing.T, ownRouting int64, ownBankCode string, peer PeerNegotiationLister) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCOfferReadReceipt{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	revRepo := repository.NewOTCOfferRevisionRepository(db)
	receiptRepo := repository.NewOTCReadReceiptRepository(db)
	offerSvc := service.NewOTCOfferService(offerRepo, revRepo, nil, nil, receiptRepo, nil)

	h := NewOTCOptionsHandler(offerSvc, nil).
		WithPeerContracts(nil, ownRouting).
		WithRemoteOffers(nil, ownBankCode).
		WithPeerNegotiations(peer)
	return h, db
}

// seedTerminalOffer inserts a terminal-status local OTCOffer with the given
// initiator + counterparty owner ids.
func seedTerminalOffer(t *testing.T, db *gorm.DB, initiatorID, counterpartyID uint64, status string) uint64 {
	t.Helper()
	ini := initiatorID
	cp := counterpartyID
	cpType := model.OwnerClient
	o := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &ini,
		CounterpartyOwnerType:       &cpType,
		CounterpartyOwnerID:         &cp,
		Direction:                   "sell_initiated",
		StockID:                     1,
		Ticker:                      "ACME",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      status,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   initiatorID,
		InitiatorAccountID:          9001,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed terminal offer: %v", err)
	}
	return o.ID
}

// TestHistory_LocalAsBidder_MeOwnerFalse: a terminal local chain where the
// caller was the COUNTERPARTY (bidder) → kind=local, me_owner=false.
func TestHistory_LocalAsBidder_MeOwnerFalse(t *testing.T) {
	const ownRouting int64 = 111
	h, db := newHistoryFixture(t, ownRouting, "111", &fakePeerNegLister{})
	// Caller (id 7) is the COUNTERPARTY; poster is id 3.
	seedTerminalOffer(t, db, 3, 7, model.OTCOfferStatusAccepted)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 offer, got %d", len(resp.GetOffers()))
	}
	got := resp.GetOffers()[0]
	if got.GetKind() != "local" {
		t.Errorf("kind = %q want local", got.GetKind())
	}
	if got.GetRoutingNumber() != ownRouting {
		t.Errorf("routing_number = %d want %d", got.GetRoutingNumber(), ownRouting)
	}
	if got.GetBankCode() != "111" {
		t.Errorf("bank_code = %q want 111", got.GetBankCode())
	}
	if got.GetMeOwner() {
		t.Errorf("me_owner = true; caller was the bidder/counterparty, not the poster")
	}
}

// TestHistory_LocalAsPoster_MeOwnerTrue: a terminal local chain where the caller
// posted/originated the offer → me_owner=true.
func TestHistory_LocalAsPoster_MeOwnerTrue(t *testing.T) {
	const ownRouting int64 = 111
	h, db := newHistoryFixture(t, ownRouting, "111", &fakePeerNegLister{})
	// Caller (id 7) is the INITIATOR/poster; counterparty is id 3.
	seedTerminalOffer(t, db, 7, 3, model.OTCOfferStatusAccepted)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 offer, got %d", len(resp.GetOffers()))
	}
	if !resp.GetOffers()[0].GetMeOwner() {
		t.Errorf("me_owner = false; caller posted the offer so it must be true")
	}
}

// TestHistory_MergesLocalAndRemote: a local terminal chain plus a remote
// terminal peer chain are merged into one list, each with its own kind. The
// remote chain's me_owner follows the seller-side rule.
func TestHistory_MergesLocalAndRemote(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// Remote chain: WE host the buyer (client-7), so me_owner=false.
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", peerSellerRouting, "client-3", "accepted"),
	}}
	h, db := newHistoryFixture(t, ownRouting, "111", peer)
	seedTerminalOffer(t, db, 3, 7, model.OTCOfferStatusRejected) // caller bidder, me_owner=false

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 2 {
		t.Fatalf("want 2 merged offers, got %d", len(resp.GetOffers()))
	}
	var sawLocal, sawRemote bool
	for _, o := range resp.GetOffers() {
		switch o.GetKind() {
		case "local":
			sawLocal = true
			if o.GetMeOwner() {
				t.Errorf("local entry me_owner=true; caller was the bidder")
			}
		case "remote":
			sawRemote = true
			if o.GetId() != 55 {
				t.Errorf("remote id = %d want 55 (peer surrogate id)", o.GetId())
			}
			if o.GetMeOwner() {
				t.Errorf("remote me_owner=true; we host the BUYER, not the seller")
			}
			if o.GetRoutingNumber() != peerSellerRouting {
				t.Errorf("remote routing_number = %d want %d (counterparty seller bank)", o.GetRoutingNumber(), peerSellerRouting)
			}
			if o.GetStatus() != "accepted" {
				t.Errorf("remote status = %q want accepted", o.GetStatus())
			}
			if o.GetStockTicker() != "ACME" {
				t.Errorf("remote ticker = %q want ACME", o.GetStockTicker())
			}
		}
	}
	if !sawLocal || !sawRemote {
		t.Errorf("merged list missing a kind: local=%v remote=%v", sawLocal, sawRemote)
	}
}

// TestHistory_RemoteWeHostSeller_MeOwnerTrue: a terminal remote peer chain where
// WE host the seller/poster → me_owner=true.
func TestHistory_RemoteWeHostSeller_MeOwnerTrue(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 333
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(77, peerBuyerRouting, "client-9", ownRouting, "client-7", "accepted"),
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 offer, got %d", len(resp.GetOffers()))
	}
	if !resp.GetOffers()[0].GetMeOwner() {
		t.Errorf("me_owner=false; we host the SELLER/poster, so it must be true")
	}
}

// TestHistory_RemoteExcludesActiveStatus: an ongoing (non-terminal) remote chain
// is NOT surfaced in the history view.
func TestHistory_RemoteExcludesActiveStatus(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", 222, "client-3", "ongoing"), // active, not terminal
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 0 {
		t.Fatalf("want 0 (ongoing remote excluded from history), got %d", len(resp.GetOffers()))
	}
}

// TestHistory_RemoteStatusFilterMapping: a status filter of ACCEPTED includes an
// accepted remote chain but excludes a cancelled/rejected one.
func TestHistory_RemoteStatusFilterMapping(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", 222, "client-3", "accepted"),
		peerRow(56, ownRouting, "client-7", 222, "client-3", "cancelled"),
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Statuses: []string{"ACCEPTED"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 (only the accepted remote), got %d", len(resp.GetOffers()))
	}
	if resp.GetOffers()[0].GetStatus() != "accepted" {
		t.Errorf("status = %q want accepted", resp.GetOffers()[0].GetStatus())
	}
}

// TestHistory_BankCallerDoesNotSeeClientChains: an employee acting as the
// bank must NOT receive CLIENT cross-bank chains (those live in the client
// lister, keyed by exact "client-<N>" principal). SP-3 Task 5b no-leak guard.
func TestHistory_BankCallerDoesNotSeeClientChains(t *testing.T) {
	const ownRouting int64 = 111
	// A CLIENT remote chain in `rows` — must NEVER appear for a bank caller.
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", 222, "client-3", "accepted"),
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 0, ActorSystemType: "bank",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 0 {
		t.Fatalf("want 0 (bank caller must not see CLIENT chains), got %d", len(resp.GetOffers()))
	}
}

// TestHistory_BankCaller_SeesBuyerChain: the bank sees its own terminal
// cross-bank BID chain (we host the bank as BUYER; party id "employee-<N>").
// SP-3 Task 5b completeness.
func TestHistory_BankCaller_SeesBuyerChain(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// WE host the bank as BUYER (our cross-bank bid on a seller at 222).
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRow(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "accepted"),
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 0, ActorSystemType: "bank",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 bank buyer chain, got %d", len(resp.GetOffers()))
	}
	got := resp.GetOffers()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if got.GetId() != 91 {
		t.Errorf("id = %d want 91 (bank's buyer chain surrogate id)", got.GetId())
	}
	if got.GetStatus() != "accepted" {
		t.Errorf("status = %q want accepted", got.GetStatus())
	}
}

// TestHistory_BankCaller_SeesSellerChain: the bank sees its own terminal
// cross-bank SELLER chain (we host the bank as SELLER on a listing; a peer
// bid on it). History covers both buyer + seller chains (role=""). SP-3 T5b.
func TestHistory_BankCaller_SeesSellerChain(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 333
	// WE host the bank as SELLER (employee-9); a client at 333 is the buyer.
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRow(92, peerBuyerRouting, "client-7", ownRouting, "employee-9", "rejected"),
	}}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 0, ActorSystemType: "bank",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want 1 bank seller chain, got %d", len(resp.GetOffers()))
	}
	got := resp.GetOffers()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if got.GetId() != 92 {
		t.Errorf("id = %d want 92 (bank's seller chain surrogate id)", got.GetId())
	}
	// Bank is the seller (we host seller routing == ownRouting) → me_owner=true.
	if !got.GetMeOwner() {
		t.Errorf("me_owner=false; the bank is the SELLER on this chain, so it must be true")
	}
}

// TestHistory_ClientCaller_NoBankChainLeak: a client caller must only ever
// see its OWN exact-principal chains, never the bank's. SP-3 Task 5b
// no-cross-party-leak guard for the history view.
func TestHistory_ClientCaller_NoBankChainLeak(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{
		// A client chain for client-7 (the caller) — should appear.
		rows: []model.OTCNegotiation{
			peerRow(55, ownRouting, "client-7", 222, "client-3", "accepted"),
		},
		// A bank chain — must NOT leak to the client caller.
		bankRows: []model.OTCNegotiation{
			peerRow(91, ownRouting, "employee-5", 222, "client-3", "accepted"),
		},
	}
	h, _ := newHistoryFixture(t, ownRouting, "111", peer)

	resp, err := h.ListNegotiationHistory(context.Background(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetOffers()) != 1 {
		t.Fatalf("want exactly 1 (client's own chain only), got %d", len(resp.GetOffers()))
	}
	if resp.GetOffers()[0].GetId() != 55 {
		t.Errorf("id = %d want 55 (client's chain); a bank chain leaked", resp.GetOffers()[0].GetId())
	}
}

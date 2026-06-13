package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// fakePeerNegLister is an in-memory PeerNegotiationLister for the unified
// ListMyNegotiations merge tests. It returns whatever rows it was given,
// ignoring the role filter (the handler always passes "" today). SP-2a:
// remote chains are now model.OTCNegotiation rows with the Remote* columns set.
type fakePeerNegLister struct {
	rows []model.OTCNegotiation
	err  error
	// bankRows feeds ListRemoteNegByBankParty (SP-3 Task 5b). When the role
	// is "buyer"/"seller" the rows are filtered on the matching employee-side;
	// "" returns all. A separate field keeps the client-path tests (which set
	// `rows`) unaffected by the bank lister.
	bankRows []model.OTCNegotiation
	bankErr  error
}

func (f *fakePeerNegLister) ListRemoteNegByClient(_ int64, _ string, _ string) ([]model.OTCNegotiation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakePeerNegLister) ListRemoteNegByBankParty(_ int64, role string) ([]model.OTCNegotiation, error) {
	if f.bankErr != nil {
		return nil, f.bankErr
	}
	out := make([]model.OTCNegotiation, 0, len(f.bankRows))
	for _, r := range f.bankRows {
		switch role {
		case "buyer":
			if r.RemoteBuyerID != nil && hasEmployeePrefix(*r.RemoteBuyerID) {
				out = append(out, r)
			}
		case "seller":
			if r.RemoteSellerID != nil && hasEmployeePrefix(*r.RemoteSellerID) {
				out = append(out, r)
			}
		default:
			out = append(out, r)
		}
	}
	return out, nil
}

func hasEmployeePrefix(id string) bool {
	const p = "employee-"
	return len(id) >= len(p) && id[:len(p)] == p
}

// newUnifiedNegFixture builds an OTCOptionsHandler backed by a sqlite
// negotiation repo (for LOCAL chains) plus a fake peer lister (for REMOTE
// chains), with own routing/bank-code wired the same way main.go does.
func newUnifiedNegFixture(t *testing.T, ownRouting int64, ownBankCode string, peer PeerNegotiationLister) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCNegotiation{}, &model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	negRepo := repository.NewOTCNegotiationRepository(db)
	negSvc := service.NewOTCNegotiationService(db, offerRepo, negRepo)

	h := NewOTCOptionsHandler(nil, nil).
		WithNegotiations(negSvc).
		// WithPeerContracts threads ownRouting; pass a nil peer-contracts
		// repo (unused on this path) just to set the routing number.
		WithPeerContracts(nil, ownRouting).
		WithRemoteOffers(nil, ownBankCode).
		WithPeerNegotiations(peer)
	return h, db
}

// seedBidderChain inserts a LOCAL negotiation row where the given client is
// the bidder (so ListMyNegotiations surfaces it as a bidder chain).
func seedBidderChain(t *testing.T, db *gorm.DB, bidderID uint64, parentOfferID uint64) uint64 {
	t.Helper()
	bid := bidderID
	neg := &model.OTCNegotiation{
		ParentOfferID:             parentOfferID,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bid,
		BidderAccountID:           9001,
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromInt(150),
		Premium:                   decimal.NewFromInt(20),
		SettlementDate:            time.Now().AddDate(0, 0, 30),
		Status:                    model.OTCNegotiationStatusOpen,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   bidderID,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bid,
		LastActionAt:              time.Now(),
		CreatedAt:                 time.Now(),
		UpdatedAt:                 time.Now(),
	}
	if err := db.Create(neg).Error; err != nil {
		t.Fatalf("seed bidder chain: %v", err)
	}
	return neg.ID
}

// withTicker overrides a peer row's RemoteOfferJSON ticker (peerRow hardcodes
// "ACME"). Used to place a chain on a DIFFERENT listing under the termless model,
// where one open offer per (owner, ticker, direction) means the TICKER — not a
// lot key — discriminates a seller's listings.
func withTicker(row model.OTCNegotiation, ticker string) model.OTCNegotiation {
	var offer contractsitx.OtcOffer
	if row.RemoteOfferJSON != nil {
		_ = json.Unmarshal([]byte(*row.RemoteOfferJSON), &offer)
	}
	offer.Ticker = ticker
	js, _ := json.Marshal(offer)
	s := string(js)
	row.RemoteOfferJSON = &s
	return row
}

// peerRow builds a REMOTE model.OTCNegotiation (SP-2a) with the cross-bank
// parties + offer in the Remote* columns. The row's RoutingNumber is the
// counterparty/peer routing — the side WE do NOT host (so the unified read
// shaping treats it as remote and derives bank_code from it).
func peerRow(id uint64, buyerRouting int64, buyerID string, sellerRouting int64, sellerID, status string) model.OTCNegotiation {
	offer := contractsitx.OtcOffer{
		Ticker:         "ACME",
		Amount:         5,
		PricePerStock:  decimal.NewFromInt(200),
		Premium:        decimal.NewFromInt(15),
		SettlementDate: "2030-01-01",
	}
	js, _ := json.Marshal(offer)
	jsStr := string(js)
	native := "neg-uuid"
	bR := buyerRouting
	sR := sellerRouting
	bID := buyerID
	sID := sellerID
	// The peer/counterparty routing is whichever side is NOT ownRouting on the
	// fixture (111). Tests pass exactly one side as 111, so the OTHER is the
	// counterparty routing the remote row should be keyed under.
	peerRouting := buyerRouting
	if buyerRouting == 111 {
		peerRouting = sellerRouting
	}
	return model.OTCNegotiation{
		ID:                        id,
		RoutingNumber:             peerRouting,
		NativeID:                  &native,
		BidderOwnerType:           model.OwnerBank,
		Status:                    status,
		RemoteOfferJSON:           &jsStr,
		RemoteBuyerRouting:        &bR,
		RemoteBuyerID:             &bID,
		RemoteSellerRouting:       &sR,
		RemoteSellerID:            &sID,
		LastActionByPrincipalType: "system",
		LastActionByOwnerType:     string(model.OwnerBank),
		CreatedAt:                 time.Now(),
		UpdatedAt:                 time.Now(),
	}
}

// TestListMyNegotiations_LocalBidderChain: a local chain the caller opened
// as the bidder → kind="local", me_owner=false, own provenance stamped.
func TestListMyNegotiations_LocalBidderChain(t *testing.T) {
	const ownRouting int64 = 111
	h, db := newUnifiedNegFixture(t, ownRouting, "111", &fakePeerNegLister{})
	seedBidderChain(t, db, 7, 100)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 negotiation, got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
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
		t.Errorf("me_owner = true; a bidder is NOT an owner")
	}
}

// TestListMyNegotiations_RemoteWeHostBuyer: a remote peer negotiation where
// WE host the buyer (our routing == buyer routing) → kind="remote",
// me_owner=false, surrogate id = peer row id, counterparty = seller bank.
func TestListMyNegotiations_RemoteWeHostBuyer(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing"),
	}}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 negotiation, got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if got.GetId() != 55 {
		t.Errorf("id = %d want 55 (peer surrogate id)", got.GetId())
	}
	if got.GetMeOwner() {
		t.Errorf("me_owner = true; we host the BUYER, not the seller")
	}
	if got.GetRoutingNumber() != peerSellerRouting {
		t.Errorf("routing_number = %d want %d (counterparty seller bank)", got.GetRoutingNumber(), peerSellerRouting)
	}
	// SP-2a: bank_code is derived from the counterparty routing (the side we
	// do NOT host). We host the buyer (111), so the counterparty is the
	// seller's bank (222).
	if got.GetBankCode() != "222" {
		t.Errorf("bank_code = %q want 222 (counterparty seller bank)", got.GetBankCode())
	}
	// Terms mapped from RemoteOfferJSON.
	if got.GetQuantity() != "5" {
		t.Errorf("quantity = %q want 5", got.GetQuantity())
	}
	if got.GetStrikePrice() != "200" {
		t.Errorf("strike_price = %q want 200", got.GetStrikePrice())
	}
	if got.GetStatus() != "ongoing" {
		t.Errorf("status = %q want ongoing", got.GetStatus())
	}
}

// TestListMyNegotiations_RemoteWeHostSeller: a remote peer negotiation where
// WE host the seller/poster (our routing == seller routing) → me_owner=true,
// counterparty = buyer bank.
func TestListMyNegotiations_RemoteWeHostSeller(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 333
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(77, peerBuyerRouting, "client-9", ownRouting, "client-7", "ongoing"),
	}}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 negotiation, got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if !got.GetMeOwner() {
		t.Errorf("me_owner = false; we host the SELLER/poster, so it must be true")
	}
	if got.GetRoutingNumber() != peerBuyerRouting {
		t.Errorf("routing_number = %d want %d (counterparty buyer bank)", got.GetRoutingNumber(), peerBuyerRouting)
	}
	// SP-2a: bank_code is derived from the counterparty routing. We host the
	// seller (111), so the counterparty is the buyer's bank (333).
	if got.GetBankCode() != "333" {
		t.Errorf("bank_code = %q want 333 (counterparty buyer bank)", got.GetBankCode())
	}
}

// TestListMyNegotiations_MergesLocalAndRemote: both a local bidder chain and
// a remote peer chain are returned in one list, each with its own kind.
func TestListMyNegotiations_MergesLocalAndRemote(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", 222, "client-3", "ongoing"),
	}}
	h, db := newUnifiedNegFixture(t, ownRouting, "111", peer)
	seedBidderChain(t, db, 7, 100)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 2 {
		t.Fatalf("want 2 merged negotiations, got %d", len(resp.GetNegotiations()))
	}
	var sawLocal, sawRemote bool
	for _, n := range resp.GetNegotiations() {
		switch n.GetKind() {
		case "local":
			sawLocal = true
		case "remote":
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Errorf("merged list missing a kind: local=%v remote=%v", sawLocal, sawRemote)
	}
}

// TestListMyNegotiations_RemoteStatusFilter: a status filter that excludes
// the remote row's status drops it from the merged list (remote rows honor
// the same status filter as local ones).
func TestListMyNegotiations_RemoteStatusFilter(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRow(55, ownRouting, "client-7", 222, "client-3", "ongoing"),
	}}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
		Statuses: []string{"accepted"}, // excludes "ongoing"
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 0 {
		t.Fatalf("want 0 (ongoing filtered out), got %d", len(resp.GetNegotiations()))
	}
}

// TestListMyNegotiations_BankCallerDoesNotSeeClientChains: an employee acting
// as the bank must NOT receive CLIENT cross-bank chains (those live in the
// client lister, keyed by exact "client-<N>" principal). The bank lister is
// prefix-matched on "employee-", so a bank caller with no bank-party chains
// gets nothing — and a client's remote chain never leaks into the bank view
// (no-cross-party leak). SP-3 Task 5b.
func TestListMyNegotiations_BankCallerDoesNotSeeClientChains(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		// A CLIENT remote chain — must NEVER appear for a bank caller.
		peerRow(55, ownRouting, "client-7", 222, "client-3", "ongoing"),
	}}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "bank",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 0 {
		t.Fatalf("want 0 (bank caller must not see CLIENT chains), got %d", len(resp.GetNegotiations()))
	}
}

// TestListMyNegotiations_BankCaller_SeesOwnRemoteBidChain: an employee acting
// as the bank sees the bank's OWN cross-bank BID chain (we host the bank as the
// BUYER; party id "employee-<N>"). The surrogate id is present so the bank can
// act on it. SP-3 Task 5b.
func TestListMyNegotiations_BankCaller_SeesOwnRemoteBidChain(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		// WE host the bank as BUYER (our cross-bank bid); counterparty seller on 222.
		peerRow(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "ongoing"),
	}}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "bank",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 bank bid chain, got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if got.GetId() != 91 {
		t.Errorf("id = %d want 91 (bank's remote bid surrogate id)", got.GetId())
	}
	if got.GetMeOwner() {
		t.Errorf("me_owner = true; the bank is the BIDDER here, not the listing owner")
	}
	if got.GetRoutingNumber() != peerSellerRouting {
		t.Errorf("routing_number = %d want %d (counterparty seller bank)", got.GetRoutingNumber(), peerSellerRouting)
	}
}

// TestListMyNegotiations_ClientCaller_NoBankChainLeak: a client caller must
// only ever see its OWN exact-principal chains, never the bank's. The bank's
// remote chain (employee-<N>) lives in bankRows (the bank lister) which a
// client request never invokes. SP-3 Task 5b no-leak guard.
func TestListMyNegotiations_ClientCaller_NoBankChainLeak(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{
		// A client chain for client-7 (the caller) — should appear.
		rows: []model.OTCNegotiation{
			peerRow(55, ownRouting, "client-7", 222, "client-3", "ongoing"),
		},
		// A bank chain — must NOT leak to the client caller.
		bankRows: []model.OTCNegotiation{
			peerRow(91, ownRouting, "employee-5", 222, "client-3", "ongoing"),
		},
	}
	h, _ := newUnifiedNegFixture(t, ownRouting, "111", peer)

	resp, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: "client", OwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want exactly 1 (client's own chain only), got %d", len(resp.GetNegotiations()))
	}
	if resp.GetNegotiations()[0].GetId() != 55 {
		t.Errorf("id = %d want 55 (client's chain); a bank chain leaked", resp.GetNegotiations()[0].GetId())
	}
}

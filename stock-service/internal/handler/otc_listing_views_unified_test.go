package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
)

// seedLocalListing inserts a local OTCOffer with the given id, owned (posted)
// by the given client, so the per-listing audience check passes for that
// poster.
func seedLocalListing(t *testing.T, db *gorm.DB, offerID, posterID uint64) {
	t.Helper()
	pid := posterID
	o := &model.OTCOffer{
		ID:                          offerID,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &pid,
		Direction:                   "sell_initiated",
		StockID:                     1,
		Ticker:                      "ACME",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      "open",
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   posterID,
		InitiatorAccountID:          9001,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed local listing: %v", err)
	}
}

// seedBankListing inserts a BANK-owned local OTCOffer with a NativeID (the
// listing UUID a peer bidder echoes back as the parent lot key). Returns the
// native id so the test can key the remote chain's RemoteParent* to it.
func seedBankListing(t *testing.T, db *gorm.DB, offerID uint64, native string) {
	t.Helper()
	nid := native
	o := &model.OTCOffer{
		ID:                          offerID,
		RoutingNumber:               model.OwnRouting(),
		NativeID:                    &nid,
		InitiatorOwnerType:          model.OwnerBank,
		InitiatorOwnerID:            nil,
		Direction:                   "sell_initiated",
		StockID:                     1,
		Ticker:                      "ACME",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      "open",
		LastModifiedByPrincipalType: "employee",
		LastModifiedByPrincipalID:   1,
		InitiatorAccountID:          9001,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed bank listing: %v", err)
	}
}

// fakeRemoteOfferGetter is an in-memory RemoteOfferGetter for the per-listing
// remote-id tests. A nil entry for an id models "not a remote mirror" via
// gorm.ErrRecordNotFound.
type fakeRemoteOfferGetter struct {
	byID map[uint64]*model.OTCOffer
	err  error
}

func (f *fakeRemoteOfferGetter) GetRemoteByID(id uint64) (*model.OTCOffer, error) {
	if f.err != nil {
		return nil, f.err
	}
	if m, ok := f.byID[id]; ok {
		return m, nil
	}
	return nil, gorm.ErrRecordNotFound
}

// remoteMirrorRow builds a folded-in remote OTCOffer row for the listing-view
// tests: routing=<peer>, native_id=<foreign id>, plus the remote-display
// columns. Mirrors what the refresher writes via UpsertRemote.
func remoteMirrorRow(id uint64, peerRouting int64, foreignID, bankCode, sellerID, ticker, status string) *model.OTCOffer {
	nid := foreignID
	bc := bankCode
	sid := sellerID
	return &model.OTCOffer{
		ID: id, RoutingNumber: peerRouting, NativeID: &nid,
		InitiatorBankCode: &bc, RemoteSellerID: &sid,
		InitiatorOwnerType: model.OwnerBank,
		Ticker:             ticker, Status: status,
	}
}

// peerRowWithParent is peerRow plus the (RemoteParentRouting,
// RemoteParentNativeID) lot key that ties a remote chain to a specific remote
// listing (SP-2a).
func peerRowWithParent(id uint64, buyerRouting int64, buyerID string, sellerRouting int64, sellerID, status string, parentRouting int64, parentID string) model.OTCNegotiation {
	row := peerRow(id, buyerRouting, buyerID, sellerRouting, sellerID, status)
	pr := parentRouting
	pid := parentID
	row.RemoteParentRouting = &pr
	row.RemoteParentNativeID = &pid
	return row
}

// newListingViewsFixture builds an OTCOptionsHandler whose per-listing path is
// backed by a sqlite negotiation service (LOCAL listings) plus a fake remote
// mirror + fake peer lister (REMOTE listings).
func newListingViewsFixture(t *testing.T, ownRouting int64, remote RemoteOfferGetter, peer PeerNegotiationLister) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	h, db := newUnifiedNegFixture(t, ownRouting, "111", peer)
	// Re-wire remote offers with the supplied getter (newUnifiedNegFixture
	// wires a nil one).
	h = h.WithRemoteOffers(remote, "111")
	return h, db
}

// --- ListNegotiationsByListing -------------------------------------------

// TestListingNegotiations_LocalUnchanged_Stamped: the listing's poster (client
// 3) views all chains; each item is kind=local, own provenance, and
// me_owner=true (spec §5: me_owner ⇔ the caller owns the PARENT OFFER).
func TestListingNegotiations_LocalUnchanged_Stamped(t *testing.T) {
	const ownRouting int64 = 111
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, &fakePeerNegLister{})
	// Local offer id 100 posted by client 3; two bidders open chains.
	seedLocalListing(t, db, 100, 3)
	seedBidderChain(t, db, 7, 100)
	seedBidderChain(t, db, 8, 100)

	// Poster (client 3) views all chains on their listing.
	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 100, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 2 {
		t.Fatalf("want 2 chains on the local listing, got %d", len(resp.GetNegotiations()))
	}
	for _, n := range resp.GetNegotiations() {
		if n.GetKind() != "local" {
			t.Errorf("kind = %q want local", n.GetKind())
		}
		if n.GetRoutingNumber() != ownRouting {
			t.Errorf("routing_number = %d want %d", n.GetRoutingNumber(), ownRouting)
		}
		// Poster owns the parent offer → me_owner must be true for all chains.
		if !n.GetMeOwner() {
			t.Errorf("me_owner=false; poster owns the parent offer so me_owner must be true")
		}
	}
}

// TestListingNegotiations_EmployeeViewer_MeOwnerFalse: an employee viewing a
// client-owned listing via otc.read.all (owner_type="bank") does NOT own the
// offer, so me_owner must be false for every returned chain.
func TestListingNegotiations_EmployeeViewer_MeOwnerFalse(t *testing.T) {
	const ownRouting int64 = 111
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, &fakePeerNegLister{})
	// Local offer id 200 posted by client 5; one bidder opens a chain.
	seedLocalListing(t, db, 200, 5)
	seedBidderChain(t, db, 9, 200)

	// Employee (owner_type="bank") views chains — gateway already enforced otc.read.all.
	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 200, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 chain, got %d", len(resp.GetNegotiations()))
	}
	if resp.GetNegotiations()[0].GetMeOwner() {
		t.Errorf("me_owner=true; employee viewing a client-owned listing is NOT the owner")
	}
}

// TestListingNegotiations_BidderForbidden: a client who is NOT the listing
// poster must receive PermissionDenied (403) and must NOT be silently routed
// into the remote-mirror path (Fix 3 test).
func TestListingNegotiations_BidderForbidden(t *testing.T) {
	const ownRouting int64 = 111
	// Wire a fake remote-offer getter that has NO mirrors, so a fallback to
	// the remote path would return ok=false and then re-surface the error.
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, &fakePeerNegLister{})
	// Local offer id 300 posted by client 3.
	seedLocalListing(t, db, 300, 3)
	// Client 7 is a bidder on this listing (has a chain) but is not the poster.
	seedBidderChain(t, db, 7, 300)

	_, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 300, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err == nil {
		t.Fatalf("want PermissionDenied error for bidder, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("want PermissionDenied, got %v", err)
	}
}

// TestListingNegotiations_RemoteId_OwnChainOnly: a remote listing id surfaces
// ONLY the caller's own chain(s) against it, never other parties'.
func TestListingNegotiations_RemoteId_OwnChainOnly(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "", ""),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		// Caller's own chain against the remote listing (matching lot key).
		peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
		// Caller's chain against a DIFFERENT remote listing — must be excluded.
		peerRowWithParent(56, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-other"),
		// A chain with no lot key — must be excluded.
		peerRow(57, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing"),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, peer)

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want exactly 1 own chain on the remote listing, got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" {
		t.Errorf("kind = %q want remote", got.GetKind())
	}
	if got.GetId() != 55 {
		t.Errorf("id = %d want 55 (caller's matching peer chain)", got.GetId())
	}
	if got.GetRoutingNumber() != peerSellerRouting {
		t.Errorf("routing_number = %d want %d (counterparty seller bank)", got.GetRoutingNumber(), peerSellerRouting)
	}
}

// TestListingNegotiations_RemoteId_NoOwnChain_Empty: a remote listing on which
// the caller has no chain returns an empty list, NOT a 404.
func TestListingNegotiations_RemoteId_NoOwnChain_Empty(t *testing.T) {
	const ownRouting int64 = 111
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, 222, "foreign-7", "222", "client-3", "", ""),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, &fakePeerNegLister{})

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v (want empty list, not error)", err)
	}
	if len(resp.GetNegotiations()) != 0 {
		t.Fatalf("want 0 chains, got %d", len(resp.GetNegotiations()))
	}
}

// TestListingNegotiations_UnknownId_NotFound: an id that is neither a local
// offer nor a remote mirror returns NotFound (existing behavior preserved).
func TestListingNegotiations_UnknownId_NotFound(t *testing.T) {
	const ownRouting int64 = 111
	h, _ := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, &fakePeerNegLister{})

	_, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 99999, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err == nil {
		t.Fatalf("want NotFound error, got nil")
	}
}

// TestListingNegotiations_BankOwnedOffer_PeerBid: a BANK-owned LOCAL offer that
// peers bid on → the bank caller sees the remote chains on THAT listing,
// correlated by (seller, TICKER) under the termless model (one open offer per
// owner+ticker — the TICKER discriminates a seller's listings, NOT a lot key).
// A free-form bid (no parentOfferId — the realistic cross-bank case) MUST appear;
// a bid on a different ticker must NOT.
func TestListingNegotiations_BankOwnedOffer_PeerBid(t *testing.T) {
	const ownRouting int64 = 111
	model.SetOwnRouting("111")
	const peerBuyerRouting int64 = 222
	const offerNative = "bank-offer-uuid"
	// Remote chains where WE host the SELLER as the BANK (employee-9). The local
	// listing (seedBankListing) is ACME.
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		// A peer bid on ACME WITH a lot key — shows.
		peerRowWithParent(70, peerBuyerRouting, "client-7", ownRouting, "employee-9", "ongoing", ownRouting, offerNative),
		// A FREE-FORM peer bid on ACME (no lot key — a peer that omits parentOfferId).
		// MUST now appear: the old lot-key-only filter silently dropped it (the bug
		// this fix closes).
		peerRow(72, peerBuyerRouting, "client-9", ownRouting, "employee-9", "ongoing"),
		// A peer bid on a DIFFERENT ticker (employee-9's OTHR listing) — excluded.
		withTicker(peerRow(71, peerBuyerRouting, "client-8", ownRouting, "employee-9", "ongoing"), "OTHR"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedBankListing(t, db, 400, offerNative) // ACME
	// A LOCAL chain on the same bank offer (an intra-bank bidder) — should also
	// appear, proving the merge doesn't drop the local set.
	seedBidderChain(t, db, 12, 400)

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 400, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// 1 local + 2 ACME peer bids (70 lot-keyed + 72 free-form). OTHR (71) excluded.
	if len(resp.GetNegotiations()) != 3 {
		t.Fatalf("want 3 chains (1 local + 2 ACME peer bids), got %d", len(resp.GetNegotiations()))
	}
	ids := map[uint64]bool{}
	var sawLocal bool
	for _, n := range resp.GetNegotiations() {
		switch n.GetKind() {
		case "local":
			sawLocal = true
		case "remote":
			ids[n.GetId()] = true
			if !n.GetMeOwner() {
				t.Errorf("me_owner=false; the bank owns this listing")
			}
		}
	}
	if !sawLocal {
		t.Errorf("merged list missing the local chain")
	}
	if !ids[70] || !ids[72] {
		t.Errorf("expected both ACME peer bids (70 lot-keyed + 72 free-form), got remote ids %v", ids)
	}
	if ids[71] {
		t.Errorf("a peer bid on a DIFFERENT ticker (71/OTHR) must be excluded")
	}
}

// seedBankListingNoNative inserts a BANK-owned LOCAL OTCOffer with NO native_id
// — the production reality: a local offer's native_id column stays empty, and
// its cross-bank "native id" (the lot key a peer bidder echoes back) is the
// offer's SURROGATE id as a string (strconv(o.ID)).
func seedBankListingNoNative(t *testing.T, db *gorm.DB, offerID uint64) {
	t.Helper()
	o := &model.OTCOffer{
		ID:            offerID,
		RoutingNumber: model.OwnRouting(),
		// NativeID intentionally nil (matches a real local offer).
		InitiatorOwnerType:          model.OwnerBank,
		InitiatorOwnerID:            nil,
		Direction:                   "sell_initiated",
		StockID:                     1,
		Ticker:                      "ACME",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      "open",
		LastModifiedByPrincipalType: "employee",
		LastModifiedByPrincipalID:   1,
		InitiatorAccountID:          9001,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed bank listing (no native): %v", err)
	}
}

// TestListingNegotiations_BankOwnedOffer_PeerBid_SurrogateIdKey reproduces the
// LIVE cross-bank bug: a BANK-owned LOCAL offer has no native_id, so a peer's
// inbound chain carries RemoteParentNativeID = the offer's SURROGATE id string.
// The on-listing bank merge must correlate against that surrogate id, NOT the
// empty native_id column.
func TestListingNegotiations_BankOwnedOffer_NoNativeId_TickerCorrelated(t *testing.T) {
	const ownRouting int64 = 111
	model.SetOwnRouting("111")
	const peerBuyerRouting int64 = 222
	const offerID uint64 = 71
	// A BANK-owned LOCAL offer with NO native_id (the production reality — a local
	// offer's native_id column stays empty). Peer bids correlate by (seller,
	// TICKER), so a free-form bid (no lot key) on the listing's ticker shows, and a
	// bid on a different ticker is excluded.
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		// A FREE-FORM peer bid on the bank's ACME listing (no lot key) — shows.
		peerRow(90, peerBuyerRouting, "client-7", ownRouting, "employee-1", "ongoing"),
		// A peer bid on a DIFFERENT ticker — excluded.
		withTicker(peerRow(91, peerBuyerRouting, "client-8", ownRouting, "employee-1", "ongoing"), "OTHR"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedBankListingNoNative(t, db, offerID) // ACME, no native_id

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: offerID, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 chain (the free-form ACME peer bid), got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" || got.GetId() != 90 {
		t.Errorf("got kind=%q id=%d, want remote/90", got.GetKind(), got.GetId())
	}
	if !got.GetMeOwner() {
		t.Errorf("me_owner=false; the bank owns this listing")
	}
}

// TestListingNegotiations_ClientOwnedOffer_FreeFormRemoteBidShows covers the live
// cross-bank scenario: a remote bank bids on OUR client's listing WITHOUT echoing
// a parentOfferId (free-form, RemoteParentNativeID nil — what the Banka-4 cohort
// partner sends). The bid MUST appear on the seller's per-listing view, correlated
// by (seller, ticker); a remote bid on a different ticker is excluded. This is the
// exact "remote bank bid on our stock doesn't show up" regression.
func TestListingNegotiations_ClientOwnedOffer_FreeFormRemoteBidShows(t *testing.T) {
	const ownRouting int64 = 111
	model.SetOwnRouting("111")
	const peerBuyerRouting int64 = 222
	// Client 3 hosts the seller; remote bids arrive via ListRemoteNegByClient (rows).
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		// Free-form remote bid (NO lot key) on client-3's ACME listing — must show.
		peerRow(95, peerBuyerRouting, "client-7", ownRouting, "client-3", "ongoing"),
		// Remote bid on a DIFFERENT ticker — excluded.
		withTicker(peerRow(96, peerBuyerRouting, "client-8", ownRouting, "client-3", "ongoing"), "OTHR"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedLocalListing(t, db, 600, 3) // client-3, ACME

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 600, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 chain (the free-form remote bid on the ACME listing), got %d", len(resp.GetNegotiations()))
	}
	got := resp.GetNegotiations()[0]
	if got.GetKind() != "remote" || got.GetId() != 95 {
		t.Errorf("got kind=%q id=%d, want remote/95", got.GetKind(), got.GetId())
	}
	if !got.GetMeOwner() {
		t.Errorf("me_owner=false; client 3 owns this listing")
	}
}

// TestListingNegotiations_ClientOwnedOffer_NoBankMerge: a CLIENT-owned local
// listing must NOT pull bank-party remote chains (the bank merge is gated on
// owner_type="bank"). Only the client's local chains appear. SP-3 Task 5b
// no-leak guard.
func TestListingNegotiations_ClientOwnedOffer_NoBankMerge(t *testing.T) {
	const ownRouting int64 = 111
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRowWithParent(80, 222, "client-7", ownRouting, "employee-9", "ongoing", ownRouting, "x"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedLocalListing(t, db, 500, 3)
	seedBidderChain(t, db, 7, 500)

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 500, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 (client's local chain only, no bank merge), got %d", len(resp.GetNegotiations()))
	}
	if resp.GetNegotiations()[0].GetKind() != "local" {
		t.Errorf("kind = %q want local", resp.GetNegotiations()[0].GetKind())
	}
}

// --- GetOfferTimeline -----------------------------------------------------

// TestTimeline_RemoteId_OwnChainOnly: a remote listing timeline surfaces the
// offer header from the mirror plus one entry per the caller's own chain.
func TestTimeline_RemoteId_OwnChainOnly(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
		peerRowWithParent(56, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-other"),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, peer)

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "remote" {
		t.Fatalf("offer header missing/not remote: %+v", resp.GetOffer())
	}
	if resp.GetOffer().GetRoutingNumber() != peerSellerRouting {
		t.Errorf("offer routing_number = %d want %d", resp.GetOffer().GetRoutingNumber(), peerSellerRouting)
	}
	if len(resp.GetTimeline()) != 1 {
		t.Fatalf("want exactly 1 timeline entry (caller's own chain), got %d", len(resp.GetTimeline()))
	}
	if resp.GetTimeline()[0].GetNegotiationId() != 55 {
		t.Errorf("entry negotiation_id = %d want 55", resp.GetTimeline()[0].GetNegotiationId())
	}
}

// TestTimeline_RemoteId_NoOwnChain_HeaderOnly: a remote listing the caller has
// no chain on returns the offer header with an empty timeline, not a 404.
func TestTimeline_RemoteId_NoOwnChain_HeaderOnly(t *testing.T) {
	const ownRouting int64 = 111
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, 222, "foreign-7", "222", "client-3", "ACME", ""),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, &fakePeerNegLister{})

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v (want header + empty timeline)", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "remote" {
		t.Fatalf("offer header missing/not remote")
	}
	if len(resp.GetTimeline()) != 0 {
		t.Fatalf("want 0 timeline entries, got %d", len(resp.GetTimeline()))
	}
}

// TestTimeline_UnknownId_NotFound: an id that is neither local nor remote → 404.
func TestTimeline_UnknownId_NotFound(t *testing.T) {
	const ownRouting int64 = 111
	h, _ := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, &fakePeerNegLister{})

	_, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 99999, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err == nil {
		t.Fatalf("want NotFound error, got nil")
	}
}

// TestTimeline_BankCaller_SeesOwnChainOnRemoteListing: the bank (acting as the
// buyer of a remote listing) sees its own chain in the timeline for that remote
// listing, correlated by the (RemoteParentRouting, RemoteParentNativeID) lot
// key. Chains on OTHER remote listings must NOT appear. SP-3 Task 5b.
func TestTimeline_BankCaller_SeesOwnChainOnRemoteListing(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		// The bank's bid on THIS remote listing (matching lot key).
		peerRowWithParent(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
		// The bank's bid on a DIFFERENT remote listing — must be excluded.
		peerRowWithParent(92, ownRouting, "employee-5", peerSellerRouting, "client-9", "ongoing", peerSellerRouting, "foreign-other"),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, peer)

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "remote" {
		t.Fatalf("offer header missing/not remote: %+v", resp.GetOffer())
	}
	if len(resp.GetTimeline()) != 1 {
		t.Fatalf("want exactly 1 timeline entry (bank's chain on THIS listing), got %d", len(resp.GetTimeline()))
	}
	if resp.GetTimeline()[0].GetNegotiationId() != 91 {
		t.Errorf("entry negotiation_id = %d want 91 (bank's matching chain)", resp.GetTimeline()[0].GetNegotiationId())
	}
}

// TestTimeline_BankCaller_NoChainOnRemoteListing_HeaderOnly: the bank views a
// remote listing it has NO chain on — returns offer header + empty timeline,
// not a 404. SP-3 Task 5b.
func TestTimeline_BankCaller_NoChainOnRemoteListing_HeaderOnly(t *testing.T) {
	const ownRouting int64 = 111
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, 222, "foreign-7", "222", "client-3", "ACME", ""),
	}}
	h, _ := newListingViewsFixture(t, ownRouting, remote, &fakePeerNegLister{})

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v (want header + empty timeline)", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "remote" {
		t.Fatalf("offer header missing/not remote")
	}
	if len(resp.GetTimeline()) != 0 {
		t.Fatalf("want 0 timeline entries, got %d", len(resp.GetTimeline()))
	}
}

// TestTimeline_ClientCaller_NoBankChainLeak: a client viewing a remote
// timeline must only see its OWN exact-principal chains, never the bank's.
// SP-3 Task 5b no-cross-party-leak guard for the timeline view.
func TestTimeline_ClientCaller_NoBankChainLeak(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{
		// A client chain for client-7 (the caller) on THIS listing — should appear.
		rows: []model.OTCNegotiation{
			peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
		},
		// A bank chain on the same listing — must NOT leak to the client caller.
		bankRows: []model.OTCNegotiation{
			peerRowWithParent(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
		},
	}
	h, _ := newListingViewsFixture(t, ownRouting, remote, peer)

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetTimeline()) != 1 {
		t.Fatalf("want exactly 1 timeline entry (client's own chain only), got %d", len(resp.GetTimeline()))
	}
	if resp.GetTimeline()[0].GetNegotiationId() != 55 {
		t.Errorf("negotiation_id = %d want 55 (client's chain); a bank chain leaked", resp.GetTimeline()[0].GetNegotiationId())
	}
}

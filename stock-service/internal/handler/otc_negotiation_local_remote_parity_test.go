package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
)

// These tests reproduce the LIVE local/remote negotiation-chain visibility bugs
// that the existing unified-views tests miss. The gap: those tests keep remote
// mirror offers in a SEPARATE fakeRemoteOfferGetter, while in production a remote
// mirror offer lives in the SAME otc_offers table (local=false). The audience
// check (authorizeListingAudience → getByID) does not filter `local`, so it
// resolves the remote mirror and short-circuits the remote fallback.
//
// To be production-faithful, every test below ALSO inserts the remote mirror
// offer into the shared sqlite otc_offers table (BeforeCreate stamps local=false
// because routing != own), exactly as otccache.UpsertRemote does in production.

func strptr(s string) *string { return &s }

// seedRemoteOfferRow inserts a folded-in REMOTE OTCOffer into the shared
// otc_offers table (local=false) — the production reality the fake getter elides.
func seedRemoteOfferRow(t *testing.T, db *gorm.DB, id uint64, peerRouting int64, native, sellerID string) {
	t.Helper()
	nid := native
	o := &model.OTCOffer{
		ID:                          id,
		RoutingNumber:               peerRouting, // != own → BeforeCreate stamps local=false
		NativeID:                    &nid,
		InitiatorOwnerType:          model.OwnerBank,
		InitiatorBankCode:           strptr("222"),
		RemoteSellerID:              strptr(sellerID),
		Direction:                   "sell_initiated",
		Ticker:                      "ACME",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      "open",
		LastModifiedByPrincipalType: "system",
		LastModifiedByPrincipalID:   0,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed remote offer row: %v", err)
	}
	if o.Local {
		t.Fatalf("seeded remote offer got local=true (own routing collision?)")
	}
}

// seedBidRevision appends a BID revision so OfferTimeline produces a local entry
// for the chain (the timeline reads the revisions table, not the snapshot).
func seedBidRevision(t *testing.T, db *gorm.DB, negID uint64) {
	t.Helper()
	if err := db.AutoMigrate(&model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate revisions: %v", err)
	}
	rev := &model.OTCNegotiationRevision{
		NegotiationID:           negID,
		RevisionNumber:          1,
		Quantity:                decimal.NewFromInt(10),
		StrikePrice:             decimal.NewFromInt(150),
		Premium:                 decimal.NewFromInt(20),
		SettlementDate:          time.Now().AddDate(0, 0, 30),
		ModifiedByPrincipalType: "client",
		ModifiedByPrincipalID:   7,
		Action:                  model.OTCNegotiationActionBid,
		CreatedAt:               time.Now(),
	}
	if err := db.Create(rev).Error; err != nil {
		t.Fatalf("seed bid revision: %v", err)
	}
}

// --- RC-A: bidder views a REMOTE listing (mirror present in the shared table) --

// TestParity_RemoteListing_ClientBidder_PerListing: our CLIENT bids on a peer's
// listing; the mirror offer is in the shared table. The bidder must still see his
// own chain on the per-listing endpoint (not a 403).
func TestParity_RemoteListing_ClientBidder_PerListing(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	// Production reality: the mirror offer is ALSO in the shared otc_offers table.
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("bidder must see his own chain on a remote listing, got err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 {
		t.Fatalf("want 1 own chain on the remote listing, got %d", len(resp.GetNegotiations()))
	}
	if got := resp.GetNegotiations()[0]; got.GetId() != 55 || got.GetKind() != "remote" {
		t.Errorf("got id=%d kind=%q, want 55/remote", got.GetId(), got.GetKind())
	}
}

// TestParity_RemoteListing_ClientBidder_Timeline: same scenario, timeline view.
func TestParity_RemoteListing_ClientBidder_Timeline(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("bidder must see his own timeline on a remote listing, got err: %v", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "remote" {
		t.Fatalf("offer header missing/not remote: %+v", resp.GetOffer())
	}
	if len(resp.GetTimeline()) != 1 || resp.GetTimeline()[0].GetNegotiationId() != 55 {
		t.Fatalf("want 1 timeline entry for chain 55, got %d: %+v", len(resp.GetTimeline()), resp.GetTimeline())
	}
}

// TestParity_RemoteListing_BankBidder_PerListing: the BANK bids on a peer's
// listing; the bank must see its own chain (employee-prefixed) on the per-listing
// endpoint despite the mirror offer being in the shared table.
func TestParity_RemoteListing_BankBidder_PerListing(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRowWithParent(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 900, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("bank bidder must see its own chain on a remote listing, got err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 || resp.GetNegotiations()[0].GetId() != 91 {
		t.Fatalf("want 1 own chain (id 91), got %d: %+v", len(resp.GetNegotiations()), resp.GetNegotiations())
	}
}

// --- RC-B: owner views a LOCAL listing that a REMOTE peer bid on ----------------

// TestParity_LocalListing_ClientOwner_SeesRemoteBidder_PerListing: a CLIENT posts
// a local listing; a peer bids on it (remote mirror row where WE host the seller
// as client-3). The poster must see BOTH the local chains and the remote bid.
func TestParity_LocalListing_ClientOwner_SeesRemoteBidder_PerListing(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 222
	model.SetOwnRouting("111")

	// Remote bid on our client-owned listing 500: we host the seller (client-3),
	// the peer hosts the buyer; lot key = (own routing, "500") — the surrogate id.
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(60, peerBuyerRouting, "client-9", ownRouting, "client-3", "ongoing", ownRouting, "500"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedLocalListing(t, db, 500, 3) // client 3 is the poster
	seedBidderChain(t, db, 7, 500)  // a local bidder chain

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 500, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 2 {
		t.Fatalf("client owner must see local + remote chains (2), got %d", len(resp.GetNegotiations()))
	}
	var sawLocal, sawRemote bool
	for _, n := range resp.GetNegotiations() {
		switch n.GetKind() {
		case "local":
			sawLocal = true
		case "remote":
			sawRemote = true
			if n.GetId() != 60 {
				t.Errorf("remote chain id = %d want 60", n.GetId())
			}
		}
	}
	if !sawLocal || !sawRemote {
		t.Errorf("missing a kind: local=%v remote=%v", sawLocal, sawRemote)
	}
}

// TestParity_LocalListing_ClientOwner_SeesRemoteBidder_Timeline: same scenario,
// timeline must include BOTH the local chain's revision and the remote bid.
func TestParity_LocalListing_ClientOwner_SeesRemoteBidder_Timeline(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 222
	model.SetOwnRouting("111")

	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(60, peerBuyerRouting, "client-9", ownRouting, "client-3", "ongoing", ownRouting, "500"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedLocalListing(t, db, 500, 3)
	localNeg := seedBidderChain(t, db, 7, 500)
	seedBidRevision(t, db, localNeg) // one local timeline entry

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 500, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOffer() == nil || resp.GetOffer().GetKind() != "local" {
		t.Fatalf("offer header missing/not local: %+v", resp.GetOffer())
	}
	if len(resp.GetTimeline()) != 2 {
		t.Fatalf("timeline must show BOTH local + remote (2 entries), got %d: %+v",
			len(resp.GetTimeline()), resp.GetTimeline())
	}
}

// TestParity_LocalListing_ClientOwner_SeesRemoteBankBidder: the remote BIDDER can
// be a peer BANK (employee-<N>), not just a peer client — the owner must see it
// either way ("from both types of users"). The owner-side merge keys on the
// SELLER (us), never on the buyer's type.
func TestParity_LocalListing_ClientOwner_SeesRemoteBankBidder(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 222
	model.SetOwnRouting("111")

	// A peer BANK (employee-5 buyer) bids on our client-owned listing 500.
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(61, peerBuyerRouting, "employee-5", ownRouting, "client-3", "ongoing", ownRouting, "500"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedLocalListing(t, db, 500, 3)

	resp, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: 500, CallerOwnerType: "client", CallerOwnerId: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetNegotiations()) != 1 || resp.GetNegotiations()[0].GetId() != 61 {
		t.Fatalf("owner must see the peer-BANK bidder's chain (id 61), got %d: %+v",
			len(resp.GetNegotiations()), resp.GetNegotiations())
	}
	if resp.GetNegotiations()[0].GetKind() != "remote" {
		t.Errorf("kind = %q want remote", resp.GetNegotiations()[0].GetKind())
	}
}

// TestParity_LocalListing_BankOwner_SeesRemoteBidder_Timeline: the timeline merge
// (Fix B2) also works for a BANK-owned listing — local chain + remote peer bid.
func TestParity_LocalListing_BankOwner_SeesRemoteBidder_Timeline(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 222
	model.SetOwnRouting("111")

	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRowWithParent(70, peerBuyerRouting, "client-7", ownRouting, "employee-1", "ongoing", ownRouting, "bank-uuid"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, &fakeRemoteOfferGetter{}, peer)
	seedBankListing(t, db, 400, "bank-uuid")
	localNeg := seedBidderChain(t, db, 12, 400)
	seedBidRevision(t, db, localNeg)

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 400, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetTimeline()) != 2 {
		t.Fatalf("bank-owner timeline must show local + remote (2), got %d: %+v",
			len(resp.GetTimeline()), resp.GetTimeline())
	}
}

// TestParity_RemoteListing_BankBidder_Timeline: production-faithful (mirror offer
// in the shared table) timeline for the BANK bidding on a peer's listing.
func TestParity_RemoteListing_BankBidder_Timeline(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{bankRows: []model.OTCNegotiation{
		peerRowWithParent(91, ownRouting, "employee-5", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "bank", CallerOwnerId: 0,
	})
	if err != nil {
		t.Fatalf("bank bidder must see its own timeline on a remote listing, got err: %v", err)
	}
	if len(resp.GetTimeline()) != 1 || resp.GetTimeline()[0].GetNegotiationId() != 91 {
		t.Fatalf("want 1 timeline entry for chain 91, got %d: %+v", len(resp.GetTimeline()), resp.GetTimeline())
	}
}

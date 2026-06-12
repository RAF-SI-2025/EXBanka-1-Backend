package handler_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// fakeParentChecker is the LocalParentChecker stub for inbound orphan-accept
// tests: it reports whether a given local parent offer id is "open" from a
// fixed allow-set.
type fakeParentChecker struct {
	open  map[uint64]bool
	calls []uint64
	// Termless-key surface: sellerOpen reports whether the seller's (owner,ticker)
	// listing is open; consumed records the keys ConsumeLocalSellOfferForSeller saw.
	sellerOpen    bool
	sellerOpenSet bool
	consumed      []string
}

func (f *fakeParentChecker) LocalParentIsOpen(offerID uint64) bool {
	f.calls = append(f.calls, offerID)
	return f.open[offerID]
}

func (f *fakeParentChecker) LocalSellOfferOpenForSeller(_ model.OwnerType, _ *uint64, _ string) bool {
	if f.sellerOpenSet {
		return f.sellerOpen
	}
	return true // default: open, so unrelated inbound-accept tests are unaffected
}

func (f *fakeParentChecker) ConsumeLocalSellOfferForSeller(ownerType model.OwnerType, ownerID *uint64, ticker string, _ int64) error {
	id := uint64(0)
	if ownerID != nil {
		id = *ownerID
	}
	f.consumed = append(f.consumed, string(ownerType)+":"+strconv.FormatUint(id, 10)+":"+ticker)
	return nil
}

// peerLastModifiedOffer builds an inbound PeerOtcOffer carrying the supplied
// lastModifiedBy routing/id (the field a malicious peer would forge).
func peerLastModifiedOffer(lmRouting int64, lmID string) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock: "150", Currency: "USD",
		Premium: "20", PremiumCurrency: "USD",
		SettlementDate: "2026-12-31",
		LastModifiedBy: &stockpb.PeerForeignBankId{RoutingNumber: lmRouting, Id: lmID},
	}
}

// storedLastModifiedBy decodes the persisted RemoteOfferJSON for a remote
// negotiation row and returns its lastModifiedBy routing + opaque id — the
// DERIVED value the accept guard later reads.
func storedLastModifiedBy(t *testing.T, db *gorm.DB, peerRouting int64, nativeID string) (int64, string) {
	t.Helper()
	row, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(peerRouting, nativeID)
	if err != nil {
		t.Fatalf("read stored neg: %v", err)
	}
	var offer contractsitx.OtcOffer
	if row.RemoteOfferJSON == nil {
		t.Fatalf("stored neg has nil RemoteOfferJSON")
	}
	if err := json.Unmarshal([]byte(*row.RemoteOfferJSON), &offer); err != nil {
		t.Fatalf("decode stored offer: %v", err)
	}
	return offer.LastModifiedBy.RoutingNumber, offer.LastModifiedBy.ID
}

// --- HOLE 1: derive lastModifiedBy from the authenticated sender ---

// TestInbound_CreateNegotiation_ForgedLastModifiedBy_RoutingOverridden: a peer
// (222) that POSTs a bid forging lastModifiedBy={111,...} (claiming WE last
// proposed) now SUCCEEDS (the payload routing is irrelevant), but the receiving
// bank DERIVES the stored lastModifiedBy.routingNumber from the AUTHENTICATED
// sender (222), overriding the forged 111. The opaque id is kept verbatim (the
// peer's participant — §2.3, never interpreted).
func TestInbound_CreateNegotiation_ForgedLastModifiedBy_RoutingOverridden(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111

	resp, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		// FORGED: claims WE (111) last modified, though the peer (222) is posting.
		Offer: peerLastModifiedOffer(111, "client-7"),
	})
	if err != nil {
		t.Fatalf("inbound bid must succeed (forged routing is overridden, not rejected): %v", err)
	}
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 1 {
		t.Errorf("expected 1 persisted row, got %d", n)
	}
	// The forged routing (111) is OVERRIDDEN to the authenticated sender (222).
	gotRouting, gotID := storedLastModifiedBy(t, db, 222, resp.GetNegotiationId().GetId())
	if gotRouting != 222 {
		t.Errorf("stored lastModifiedBy.routingNumber: got %d, want 222 (authenticated sender)", gotRouting)
	}
	// The opaque id is kept verbatim — not interpreted (§2.3).
	if gotID != "client-7" {
		t.Errorf("stored lastModifiedBy.id: got %q, want %q (verbatim)", gotID, "client-7")
	}
}

// TestInbound_CreateNegotiation_HonestLastModifiedBy_Accepted: lastModifiedBy
// pointing at the authenticated peer (or absent — zero value) is accepted, and
// the stored routing is always the authenticated sender's.
func TestInbound_CreateNegotiation_HonestLastModifiedBy_Accepted(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)

	// peer marks itself as last actor
	r1, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer:        peerLastModifiedOffer(222, "client-7"),
	})
	if err != nil {
		t.Fatalf("honest peer lastModifiedBy: %v", err)
	}
	// absent lastModifiedBy (zero value) is tolerated — routing still derived.
	r2, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 10, PricePerStock: "150", Currency: "USD",
			Premium: "20", PremiumCurrency: "USD", SettlementDate: "2026-12-31",
		},
	})
	if err != nil {
		t.Fatalf("absent lastModifiedBy: %v", err)
	}
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 2 {
		t.Errorf("expected 2 persisted rows, got %d", n)
	}
	if gotR, _ := storedLastModifiedBy(t, db, 222, r1.GetNegotiationId().GetId()); gotR != 222 {
		t.Errorf("honest bid stored routing: got %d, want 222", gotR)
	}
	if gotR, _ := storedLastModifiedBy(t, db, 222, r2.GetNegotiationId().GetId()); gotR != 222 {
		t.Errorf("absent-lm bid stored routing: got %d, want 222 (derived)", gotR)
	}
}

// TestInbound_UpdateNegotiation_ForgedLastModifiedBy_RoutingOverridden: the
// inbound counter (UpdateNegotiation) likewise SUCCEEDS with a forged
// lastModifiedBy={111,...} but the PERSISTED routing is DERIVED from the
// authenticated sender (222), and the opaque id is kept verbatim.
func TestInbound_UpdateNegotiation_ForgedLastModifiedBy_RoutingOverridden(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	createResp, err := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer:        peerLastModifiedOffer(222, "client-7"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// §3.3 turn rule: make it the peer's turn (we last acted) so the counter is
	// in-turn — this test exercises the HOLE-1 routing-override, not the turn
	// guard.
	setStoredLastModifiedRouting(t, db, 222, createResp.GetNegotiationId().GetId(), 111)

	if _, err = h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
		// FORGED counter: claims WE (111) last modified.
		Offer: peerLastModifiedOffer(111, "client-7"),
	}); err != nil {
		t.Fatalf("inbound counter must succeed (forged routing overridden, not rejected): %v", err)
	}
	gotRouting, gotID := storedLastModifiedBy(t, db, 222, createResp.GetNegotiationId().GetId())
	if gotRouting != 222 {
		t.Errorf("counter stored lastModifiedBy.routingNumber: got %d, want 222 (authenticated sender)", gotRouting)
	}
	if gotID != "client-7" {
		t.Errorf("counter stored lastModifiedBy.id: got %q, want %q (verbatim)", gotID, "client-7")
	}
}

// TestInbound_ForgedCounter_ThenSelfAccept_Blocked is the end-to-end attack:
// peer 222 POSTs a bid then PUTs a forged counter claiming lastModifiedBy={111}
// (trying to look like WE proposed), then GET /accepts its own forged terms.
// Because the stored routing is DERIVED to 222, the accept guard sees
// 222 != ownRouting(111) → 403, NO settlement SI-TX dispatched, NO contract.
func TestInbound_ForgedCounter_ThenSelfAccept_Blocked(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111
	ctx := context.Background()

	createResp, err := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer:        peerLastModifiedOffer(222, "client-7"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// §3.3 turn rule: make it the peer's turn so the forged counter is in-turn
	// (200) — this test then proves the SELF-ACCEPT is still blocked by the
	// HOLE-1 derive, independent of the turn guard.
	setStoredLastModifiedRouting(t, db, 222, createResp.GetNegotiationId().GetId(), 111)

	// Forged counter — claims WE (111) last proposed.
	if _, err = h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
		Offer:         peerLastModifiedOffer(111, "client-9"),
	}); err != nil {
		t.Fatalf("forged counter (should 200): %v", err)
	}

	// Self-accept attempt by the same peer.
	_, err = h.AcceptNegotiation(ctx, &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if err == nil {
		t.Fatal("expected forged-counter self-accept to be rejected, got nil (self-accept loophole)")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
	if peerTx.gotReq != nil {
		t.Errorf("forged self-accept must NOT dispatch a settlement SI-TX; got %+v", peerTx.gotReq)
	}
	// No contract row formed.
	var contracts int64
	db.Table("option_contracts").Count(&contracts)
	if contracts != 0 {
		t.Errorf("forged self-accept must form NO contract, got %d", contracts)
	}
	after, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, createResp.GetNegotiationId().GetId())
	if after.Status != "ongoing" {
		t.Errorf("status after blocked self-accept: got %q, want ongoing", after.Status)
	}
}

// TestInbound_AcceptNegotiation_PeerAcceptsOwnTerms_Rejected is the core
// forged-self-accept repro: even if a forged counter slipped through, the
// authoritative accept guard rejects unless the LOCAL side last proposed.
// Here the stored lastModifiedBy is the peer (222) — the peer cannot accept its
// own terms; NO settlement SI-TX may be dispatched.
func TestInbound_AcceptNegotiation_PeerAcceptsOwnTerms_Rejected(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111

	// Seed a remote chain directly (peer is the last proposer).
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-7"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	row := buildRemoteNegForTest(222, "neg-peer-self", offer, string(offerJSON),
		222, "client-7", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-peer-self"},
	})
	if err == nil {
		t.Fatal("expected peer-accepts-own-terms to be rejected, got nil (self-accept loophole)")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
	if peerTx.gotReq != nil {
		t.Errorf("self-accept must NOT dispatch a settlement SI-TX; got %+v", peerTx.gotReq)
	}
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-peer-self")
	if after.Status != "ongoing" {
		t.Errorf("status after rejected self-accept: got %q, want ongoing", after.Status)
	}
}

// TestInbound_AcceptNegotiation_LegitOppositePartyAccept_Succeeds: the legit
// flow — peer bids, WE (local) counter (lastModifiedBy=ownRouting), peer
// accepts → succeeds and dispatches the 4-posting settlement.
func TestInbound_AcceptNegotiation_LegitOppositePartyAccept_Succeeds(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111

	// WE (111) last proposed → peer (222) accepting is the legit counterparty.
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	row := buildRemoteNegForTest(222, "neg-legit", offer, string(offerJSON),
		222, "client-7", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-legit"},
	})
	if err != nil {
		t.Fatalf("legit opposite-party accept: %v", err)
	}
	if peerTx.gotReq == nil {
		t.Fatal("legit accept must dispatch the settlement SI-TX")
	}
	if len(peerTx.gotReq.GetPostings()) != 4 {
		t.Errorf("expected 4 postings, got %d", len(peerTx.gotReq.GetPostings()))
	}
	if resp.GetTransactionId() == "" {
		t.Error("expected a transaction id on success")
	}
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-legit")
	if after.Status != "accepted" {
		t.Errorf("status after legit accept: got %q, want accepted", after.Status)
	}
}

// --- HOLE 2: inbound orphan-accept (cancelled local parent listing) ---

// TestInbound_AcceptNegotiation_OrphanCancelledParent_Rejected: WE host the
// listing (remote_parent_routing == ownRouting). An inbound accept against a
// child of a CANCELLED local listing must be rejected (FailedPrecondition),
// authoritatively (regardless of cascade timing), with NO settlement dispatched.
func TestInbound_AcceptNegotiation_OrphanCancelledParent_Rejected(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t)                     // ownRouting 111
	parentChecker := &fakeParentChecker{open: map[uint64]bool{}} // parent 42 NOT open
	h = h.WithParentChecker(parentChecker)

	// Remote child chain: WE host the seller (client-9@111); buyer on peer 222.
	// lastModifiedBy = the LOCAL side (111) so the self-accept guard PASSES (the
	// peer is the legit counterparty) — the ONLY thing that should block is the
	// cancelled local parent listing.
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	pr := int64(111)
	pn := "42"
	row := buildRemoteNegForTest(222, "neg-orphan-in", offer, string(offerJSON),
		222, "client-3", 111, "client-9")
	row.RemoteParentRouting = &pr
	row.RemoteParentNativeID = &pn
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-orphan-in"},
	})
	if err == nil {
		t.Fatal("expected inbound accept on a cancelled-parent chain to be rejected, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
	if peerTx.gotReq != nil {
		t.Errorf("orphan accept must NOT dispatch settlement; got %+v", peerTx.gotReq)
	}
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-orphan-in")
	if after.Status != "ongoing" {
		t.Errorf("status after rejected orphan accept: got %q, want ongoing", after.Status)
	}
}

// TestInbound_AcceptNegotiation_OpenParent_Succeeds: when the local parent
// listing is still open, the inbound accept proceeds.
func TestInbound_AcceptNegotiation_OpenParent_Succeeds(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111
	parentChecker := &fakeParentChecker{open: map[uint64]bool{42: true}}
	h = h.WithParentChecker(parentChecker)

	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	pr := int64(111)
	pn := "42"
	row := buildRemoteNegForTest(222, "neg-open-parent", offer, string(offerJSON),
		222, "client-3", 111, "client-9")
	row.RemoteParentRouting = &pr
	row.RemoteParentNativeID = &pn
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-open-parent"},
	}); err != nil {
		t.Fatalf("open-parent accept: %v", err)
	}
	if peerTx.gotReq == nil {
		t.Error("open-parent accept must dispatch settlement")
	}
}

// TestInbound_AcceptNegotiation_Termless_ConsumedListing_Rejected: a termless bid
// (no numeric parent id — the /public-stock wire carries none) on a listing the
// seller already consumed/cancelled must be rejected by the (owner,ticker,sell)
// key guard, with NO settlement dispatched.
func TestInbound_AcceptNegotiation_Termless_ConsumedListing_Rejected(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111
	parentChecker := &fakeParentChecker{sellerOpenSet: true, sellerOpen: false}
	h = h.WithParentChecker(parentChecker)

	// WE host the seller (client-9@111); buyer on peer 222; lastModifiedBy = local
	// side so the self-accept guard passes. NO parent link (termless): the numeric
	// orphan guard is skipped, so only the (owner,ticker,sell) key guard can block.
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	row := buildRemoteNegForTest(222, "neg-termless-consumed", offer, string(offerJSON),
		222, "client-3", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-termless-consumed"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition on a consumed termless listing, got %v", err)
	}
	if peerTx.gotReq != nil {
		t.Errorf("must NOT dispatch settlement on a consumed listing; got %+v", peerTx.gotReq)
	}
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-termless-consumed")
	if after.Status != "ongoing" {
		t.Errorf("status after rejected accept: got %q, want ongoing", after.Status)
	}
}

// TestInbound_AcceptNegotiation_Termless_OpenListing_ConsumesAndSettles: an inbound
// accept on an OPEN termless listing dispatches settlement AND consumes the local
// listing (one listing → one accepted contract).
func TestInbound_AcceptNegotiation_Termless_OpenListing_ConsumesAndSettles(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111
	parentChecker := &fakeParentChecker{sellerOpenSet: true, sellerOpen: true}
	h = h.WithParentChecker(parentChecker)

	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	row := buildRemoteNegForTest(222, "neg-termless-open", offer, string(offerJSON),
		222, "client-3", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-termless-open"},
	}); err != nil {
		t.Fatalf("open-listing termless accept: %v", err)
	}
	if peerTx.gotReq == nil {
		t.Error("open-listing accept must dispatch settlement")
	}
	if len(parentChecker.consumed) != 1 || parentChecker.consumed[0] != "client:9:AAPL" {
		t.Errorf("expected the local listing consumed for client:9:AAPL, got %v", parentChecker.consumed)
	}
}

// --- HOLE 3: well-formed local seller id ---

// TestInbound_CreateNegotiation_BogusEmployeeSeller_Rejected: an employee-<garbage>
// seller (not ^employee-\d+$) must be rejected — the seller is OURS and must be a
// resolvable local participant. No row may persist.
func TestInbound_CreateNegotiation_BogusEmployeeSeller_Rejected(t *testing.T) {
	for _, bogus := range []string{"employee-abc", "employee-", "employee-1x", "garbage", ""} {
		h, db, _, _ := newPeerOtcHandler(t)
		val := &fakeLocalSellerValidator{exists: map[string]bool{"client-9": true}}
		h = h.WithSellerValidator(val)

		_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
			PeerBankCode: "222",
			BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
			SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: bogus},
			Offer:        peerLastModifiedOffer(222, "client-7"),
		})
		if err == nil {
			t.Fatalf("seller %q: expected rejection, got nil (junk row vector)", bogus)
		}
		if c := status.Code(err); c != codes.InvalidArgument && c != codes.NotFound {
			t.Errorf("seller %q: expected InvalidArgument/NotFound, got %v", bogus, err)
		}
		var n int64
		db.Table("otc_negotiations").Count(&n)
		if n != 0 {
			t.Errorf("seller %q: expected 0 rows, got %d", bogus, n)
		}
	}
}

// TestInbound_CreateNegotiation_WellFormedSellers_Accepted: well-formed sellers
// (employee-<digits>, bank, real client-<n>) are accepted. The BUYER opaque id
// (UUID / acc-42) stays verbatim and is not format-checked.
func TestInbound_CreateNegotiation_WellFormedSellers_Accepted(t *testing.T) {
	cases := []struct {
		seller string
		buyer  string // opaque buyer id — stays verbatim
	}{
		{"employee-42", "550e8400-e29b-41d4-a716-446655440000"},
		{"bank", "acc-42"},
		{"client-9", "client-7"},
	}
	for _, c := range cases {
		h, db, _, _ := newPeerOtcHandler(t)
		val := &fakeLocalSellerValidator{exists: map[string]bool{"client-9": true}}
		h = h.WithSellerValidator(val)

		resp, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
			PeerBankCode: "222",
			BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: c.buyer},
			SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: c.seller},
			Offer:        peerLastModifiedOffer(222, c.buyer),
		})
		if err != nil {
			t.Fatalf("seller %q buyer %q: %v", c.seller, c.buyer, err)
		}
		if resp.GetNegotiationId().GetId() == "" {
			t.Errorf("seller %q: expected negotiation id", c.seller)
		}
		var n int64
		db.Table("otc_negotiations").Count(&n)
		if n != 1 {
			t.Errorf("seller %q: expected 1 row, got %d", c.seller, n)
		}
		// Buyer opaque id stored verbatim. Remote rows are keyed by the PEER
		// routing (222), not our own — the returned id carries our routing only as
		// the response envelope.
		seeded, gerr := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, resp.GetNegotiationId().GetId())
		if gerr != nil {
			t.Fatalf("read seeded: %v", gerr)
		}
		if seeded.RemoteBuyerID == nil || *seeded.RemoteBuyerID != c.buyer {
			t.Errorf("buyer opaque id not stored verbatim: got %v, want %q", seeded.RemoteBuyerID, c.buyer)
		}
	}
}

// TestInbound_CreateNegotiation_PhantomClientSeller_Rejected: a client-<n> that
// does not resolve locally is still NotFound (existing guard preserved).
func TestInbound_CreateNegotiation_PhantomClientSeller_Rejected(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	val := &fakeLocalSellerValidator{exists: map[string]bool{"client-9": true}}
	h = h.WithSellerValidator(val)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-888888"},
		Offer:        peerLastModifiedOffer(222, "client-7"),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for phantom client seller, got %v", err)
	}
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 0 {
		t.Errorf("expected 0 rows, got %d", n)
	}
}

// buildRemoteNegForTest is an external-package-friendly wrapper that builds a
// remote OTCNegotiation row with the buyer/seller wired, mirroring the inbound
// handler's buildRemoteNeg via the model fields.
func buildRemoteNegForTest(
	peerRouting int64, foreignID string, offer contractsitx.OtcOffer, offerJSON string,
	buyerRouting int64, buyerID string, sellerRouting int64, sellerID string,
) *model.OTCNegotiation {
	bID := buyerID
	sID := sellerID
	bR := buyerRouting
	sR := sellerRouting
	oj := offerJSON
	fid := foreignID
	return &model.OTCNegotiation{
		RoutingNumber:             peerRouting,
		NativeID:                  &fid,
		BidderOwnerType:           model.OwnerBank,
		Quantity:                  decimal.NewFromInt(offer.Amount),
		StrikePrice:               offer.PricePerStock,
		Premium:                   offer.Premium,
		Status:                    "ongoing",
		LastActionByPrincipalType: "system",
		LastActionByOwnerType:     string(model.OwnerBank),
		RemoteOfferJSON:           &oj,
		RemoteBuyerRouting:        &bR,
		RemoteBuyerID:             &bID,
		RemoteSellerRouting:       &sR,
		RemoteSellerID:            &sID,
	}
}

// TestInbound_AcceptNegotiation_NotifiesLocalBuyer: when WE host the BUYER (the
// bidder, who last proposed) and the remote seller accepts, the local buyer must
// receive OTC_CONTRACT_CREATED. Regression — the cross-bank bidder previously got
// NO notification when their bid/counter was accepted (the inbound path notified
// only the seller). The accepting (remote) party is notified by their own bank's
// OUTBOUND acceptRemoteNegotiation.
func TestInbound_AcceptNegotiation_NotifiesLocalBuyer(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 5,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	// buyer local (111/client-9), seller remote (222/client-7); the remote seller's
	// bank (222) POSTs GET /accept to us.
	row := buildRemoteNegForTest(222, "neg-buyerlocal", offer, string(offerJSON),
		111, "client-9", 222, "client-7")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-buyerlocal"},
	}); err != nil {
		t.Fatalf("accept: %v", err)
	}

	got := notif.byType("OTC_CONTRACT_CREATED")
	if got == nil {
		t.Fatalf("expected OTC_CONTRACT_CREATED for the local buyer, got %+v", notif.snapshot())
	}
	if got.UserID != 9 {
		t.Errorf("recipient = %d, want 9 (the local buyer)", got.UserID)
	}
}

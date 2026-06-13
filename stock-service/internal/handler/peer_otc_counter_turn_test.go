package handler_test

import (
	"context"
	"encoding/json"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// setStoredLastModifiedRouting rewrites the persisted RemoteOfferJSON's
// lastModifiedBy.routingNumber for a remote negotiation row, simulating "the
// other side last acted" so a subsequent inbound counter is in-turn (§3.3).
// Used by tests that need a chain where it is the PEER'S turn without driving
// the heavier outbound counter path.
func setStoredLastModifiedRouting(t *testing.T, db *gorm.DB, peerRouting int64, nativeID string, routing int64) {
	t.Helper()
	repo := repository.NewOTCNegotiationRepository(db)
	row, err := repo.GetRemoteNegByRoutingAndNative(peerRouting, nativeID)
	if err != nil {
		t.Fatalf("load neg for LM flip: %v", err)
	}
	var offer contractsitx.OtcOffer
	if row.RemoteOfferJSON != nil {
		_ = json.Unmarshal([]byte(*row.RemoteOfferJSON), &offer)
	}
	offer.LastModifiedBy.RoutingNumber = routing
	j, err := json.Marshal(offer)
	if err != nil {
		t.Fatalf("marshal LM flip: %v", err)
	}
	if err := repo.UpdateRemoteNegOffer(peerRouting, nativeID, string(j)); err != nil {
		t.Fatalf("persist LM flip: %v", err)
	}
}

// SI-TX §3.3 ("Posting a counter-offer"): "If the receiving bank deems that it
// is its turn to make a counter-offer, rather than the [other party's] bank, or
// if negotiations are closed, a 409 Conflict response code is produced." Turn
// rule: a party may counter ONLY when the OTHER side made the last
// modification. Because we DERIVE lastModifiedBy from the authenticated sender
// (HOLE 1), the stored lastModifiedBy.routingNumber is the side that last acted:
// a peer may PUT a counter iff the stored routing == ownRouting (we last acted →
// it's the peer's turn). The gateway maps FailedPrecondition → 409
// business_rule_violation, so the handler returns FailedPrecondition for both
// the out-of-turn and the closed case.

// TestInbound_UpdateNegotiation_OutOfTurn_409: peer 222 creates a negotiation
// (stored lastModifiedBy derived = 222), then peer 222 immediately PUTs a
// counter. It is NOT 222's turn (222 last acted) → 409 (FailedPrecondition), and
// the counter must NOT be persisted (the stored offer is unchanged).
func TestInbound_UpdateNegotiation_OutOfTurn_409(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
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
	nativeID := createResp.GetNegotiationId().GetId()

	// Capture the stored offer's premium BEFORE the out-of-turn PUT so we can
	// prove no mutation persisted.
	before, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, nativeID)
	var beforeOffer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(*before.RemoteOfferJSON), &beforeOffer)

	// Peer 222 immediately PUTs a counter — but 222 last acted, so it's NOT
	// 222's turn. The counter changes the premium to 999.
	counter := peerLastModifiedOffer(222, "client-7")
	counter.Premium = "999"
	_, err = h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
		Offer:         counter,
	})
	if err == nil {
		t.Fatal("expected out-of-turn counter to be rejected, got nil (§3.3 turn rule)")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition (→409), got %v", err)
	}

	// No mutation persisted: premium unchanged, lastModifiedBy still the peer.
	after, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, nativeID)
	var afterOffer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(*after.RemoteOfferJSON), &afterOffer)
	if !afterOffer.Premium.Equal(beforeOffer.Premium) {
		t.Errorf("out-of-turn counter mutated premium: got %s, want %s (unchanged)", afterOffer.Premium, beforeOffer.Premium)
	}
	if afterOffer.LastModifiedBy.RoutingNumber != 222 {
		t.Errorf("stored lastModifiedBy routing after rejected counter: got %d, want 222 (unchanged)", afterOffer.LastModifiedBy.RoutingNumber)
	}
}

// TestInbound_UpdateNegotiation_Closed_409: a negotiation that is no longer
// ongoing (DELETE → cancelled) must reject an inbound counter with 409
// (FailedPrecondition), persisting no mutation.
func TestInbound_UpdateNegotiation_Closed_409(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
	ctx := context.Background()

	// Seed a row where it WOULD be the peer's turn (we last acted) so the ONLY
	// thing that should block is the closed state.
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
	row := buildRemoteNegForTest(222, "neg-closed", offer, string(offerJSON),
		222, "client-7", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Close it (peer cancels).
	if _, err := h.DeleteNegotiation(ctx, &stockpb.DeleteNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-closed"},
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	counter := peerLastModifiedOffer(222, "client-7")
	counter.Premium = "999"
	_, err := h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-closed"},
		Offer:         counter,
	})
	if err == nil {
		t.Fatal("expected counter on a closed negotiation to be rejected, got nil (§3.3 closed rule)")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition (→409), got %v", err)
	}

	// No mutation: premium unchanged, status still cancelled.
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-closed")
	var afterOffer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(*after.RemoteOfferJSON), &afterOffer)
	if !afterOffer.Premium.Equal(decimal.RequireFromString("20")) {
		t.Errorf("closed counter mutated premium: got %s, want 20 (unchanged)", afterOffer.Premium)
	}
	if after.Status != "cancelled" {
		t.Errorf("status after rejected closed counter: got %q, want cancelled", after.Status)
	}
}

// TestInbound_UpdateNegotiation_LegitTurn_200: when WE (ownRouting 111) last
// proposed, it IS the peer's turn — the peer's counter succeeds (200), persists,
// and the stored lastModifiedBy flips to the peer (222) per the HOLE-1 derive.
func TestInbound_UpdateNegotiation_LegitTurn_200(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
	ctx := context.Background()

	// Seed an ongoing row where WE (111) last proposed → it's the peer's turn.
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
	row := buildRemoteNegForTest(222, "neg-legit-turn", offer, string(offerJSON),
		222, "client-7", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	counter := peerLastModifiedOffer(222, "client-7")
	counter.Premium = "999"
	if _, err := h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-legit-turn"},
		Offer:         counter,
	}); err != nil {
		t.Fatalf("legit-turn counter must succeed, got: %v", err)
	}

	// Mutation persisted: premium = 999, stored lastModifiedBy flipped to 222.
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-legit-turn")
	var afterOffer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(*after.RemoteOfferJSON), &afterOffer)
	if !afterOffer.Premium.Equal(decimal.RequireFromString("999")) {
		t.Errorf("legit-turn counter did not persist: premium got %s, want 999", afterOffer.Premium)
	}
	if afterOffer.LastModifiedBy.RoutingNumber != 222 {
		t.Errorf("stored lastModifiedBy after legit counter: got %d, want 222 (the peer now last acted)", afterOffer.LastModifiedBy.RoutingNumber)
	}
}

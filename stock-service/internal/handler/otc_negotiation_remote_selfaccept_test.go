package handler

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
)

// TestAcceptRemoteNegotiation_SelfAccept_Rejected guards the cross-bank
// self-accept loophole found in the live two-stack adversarial sweep
// (2026-06-05): the bidder who proposed the current terms of a REMOTE chain was
// able to ACCEPT their own bid, forming a contract + settling the premium with
// NO agreement from the counterparty. The LOCAL accept path enforces the
// "caller must be OPPOSITE to whoever proposed the current terms" rule, but the
// REMOTE accept path skipped it entirely.
//
// Here we host the BUYER (client-9@111) and the buyer also proposed the current
// terms (lastModifiedBy = {111, client-9}). The buyer accepting must be rejected
// (PermissionDenied) and NO accept must be dispatched to the peer.
func TestAcceptRemoteNegotiation_SelfAccept_Rejected(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// Seed a remote chain where the hosted buyer (client-9) is BOTH the hosted
	// party AND the last proposer of the current terms.
	offer := contractsitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-07-01",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	row := buildRemoteNeg(
		222, "neg-self", offer, string(offerJSON),
		111, "client-9", // buyer hosted by us
		222, "client-77", // seller on peer
		nil, nil, "ongoing",
	)
	repo := repository.NewOTCNegotiationRepository(db)
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seeded, err := repo.GetRemoteNegByRoutingAndNative(222, "neg-self")
	if err != nil {
		t.Fatalf("read seeded: %v", err)
	}

	_, err = h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       seeded.ID,
		CallerOwnerType:     "client",
		CallerOwnerId:       9, // the buyer who proposed the current terms
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	})
	if err == nil {
		t.Fatal("expected self-accept to be rejected, got nil (loophole)")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
	// No accept may have been dispatched to the peer.
	for _, pc := range dispatcher.proxyCalls {
		if pc.method == "GET" && pc.subpath == "/accept" {
			t.Errorf("self-accept must NOT dispatch GET /accept; calls: %+v", dispatcher.proxyCalls)
		}
	}
	// The mirror must remain ongoing (no state change).
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-self")
	if after.Status != "ongoing" {
		t.Errorf("status after rejected self-accept: got %q, want ongoing", after.Status)
	}
}

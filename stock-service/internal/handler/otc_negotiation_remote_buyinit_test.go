package handler

import (
	"context"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestOpenNegotiation_RemoteBuyInitiated_Rejected guards the buy_initiated
// cross-bank role-inversion found in the live two-stack adversarial sweep
// (2026-06-05). openRemoteNegotiation hardcodes the bidder as the BUYER and the
// listing's poster as the SELLER — correct only for sell_initiated. On a
// buy_initiated listing the poster is the BUYER and the bidder is the SELLER, so
// the hardcoded mapping SILENTLY INVERTS the economic roles: the party who bid to
// sell ends up holding the option (buyer side) and the poster who wanted to buy
// is recorded as the seller. On exercise the wrong party would receive the shares.
//
// Until the cross-bank role model supports buy_initiated (it needs the seller's
// shares reserved on the BIDDER's bank and the inbound seller-locality flipped —
// a frozen-wire-protocol change), the safe behaviour is to REJECT a cross-bank
// bid on a buy_initiated remote listing cleanly rather than silently invert.
func TestOpenNegotiation_RemoteBuyInitiated_Rejected(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// Seed a folded-in REMOTE buy_initiated listing (peer routing 222).
	nid := "peer-buyoffer-1"
	bankCode := "222"
	posterID := "client-9"
	o := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &nid,
		InitiatorBankCode:           &bankCode,
		RemoteSellerID:              &posterID,
		InitiatorOwnerType:          model.OwnerBank,
		Direction:                   model.OTCDirectionBuyInitiated, // poster is the BUYER
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "system",
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed buy_initiated remote offer: %v", err)
	}

	_, err := h.OpenNegotiation(context.Background(), openReq(o.ID, 9, "client"))
	if err == nil {
		t.Fatal("expected cross-bank bid on a buy_initiated listing to be rejected, got nil (role-inversion loophole)")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
	// No bid may have been dispatched to the peer.
	if dispatcher.calls != 0 || len(dispatcher.proxyCalls) != 0 {
		t.Errorf("rejected buy_initiated bid must not dispatch; CreateNegotiation calls=%d proxy=%d", dispatcher.calls, len(dispatcher.proxyCalls))
	}
}

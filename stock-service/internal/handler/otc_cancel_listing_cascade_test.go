package handler

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
)

// TestCancelListing_CascadesToRemoteChildren guards the listing-cancel cascade
// gap found in the live two-stack adversarial sweep (2026-06-05): cancelling a
// cross-bank listing left its REMOTE child chains "ongoing" (the local cascade
// only touches chains keyed on the numeric parent_offer_id; remote children
// group on remote_parent_native_id). Now the cancel must flip remote children to
// cancelled and DELETE to each bidder's bank.
func TestCancelListing_CascadesToRemoteChildren(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// LOCAL listing owned by client-9 (open).
	parent := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            u64ptr(9),
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   9,
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	parentNative := strconv.FormatUint(parent.ID, 10)

	// A REMOTE child chain on this listing: WE host the seller (client-9@111),
	// the buyer is on peer 222.
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock: decimal.RequireFromString("150"), Currency: "USD",
		Premium: decimal.RequireFromString("20"), PremiumCurrency: "USD",
		SettlementDate: "2026-07-01",
		LastModifiedBy: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-3"},
	}
	offerJSON, _ := json.Marshal(offer)
	pr := int64(111)
	row := buildRemoteNeg(
		222, "neg-child", offer, string(offerJSON),
		222, "client-3", // buyer on peer
		111, "client-9", // seller hosted by us
		&pr, &parentNative, "ongoing",
	)
	repo := repository.NewOTCNegotiationRepository(db)
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	// Cancel the listing as the poster (client-9).
	_, err := h.CancelListing(context.Background(), &stockpb.CancelListingRequest{
		OfferId:             parent.ID,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
	})
	if err != nil {
		t.Fatalf("CancelListing: %v", err)
	}

	// The remote child must now be cancelled.
	child, gerr := repo.GetRemoteNegByRoutingAndNative(222, "neg-child")
	if gerr != nil {
		t.Fatalf("read child: %v", gerr)
	}
	if child.Status != "cancelled" {
		t.Errorf("remote child status: got %q, want cancelled", child.Status)
	}
	// A DELETE must have been dispatched to the bidder's bank (222).
	var sawDelete bool
	for _, pc := range dispatcher.proxyCalls {
		if pc.method == "DELETE" && pc.foreignID == "neg-child" && pc.peerBankCode == "222" {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected DELETE to bidder bank 222 for neg-child; calls: %+v", dispatcher.proxyCalls)
	}
}

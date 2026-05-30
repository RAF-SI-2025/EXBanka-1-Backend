//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// setupOfferWithBid drives a sell_initiated OTC option listing to the point
// where one buyer has opened a negotiation chain (a BID) — but NOT accepted.
// Returns the offer id, the poster (seller) client, and the bidder (buyer)
// client. Skips (not fails) when the market simulator can't seed the holding
// or the v3 OTC endpoints aren't deployed.
func setupOfferWithBid(t *testing.T, adminC *client.APIClient) (offerID int, posterC, bidderC *client.APIClient) {
	t.Helper()
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerCli, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	bAcctID, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("seeded stock has no ticker/listing")
	}

	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 10,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("could not seed seller holding (order POST %d)", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("seed buy order did not fill")
	}

	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker, "quantity": "1",
		"strike_price": "100", "premium": "5", "settlement_date": "2030-12-31",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("v3 OTC option endpoints not deployed")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("create listing: got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID = int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	bidResp, err := buyerCli.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": bAcctID, "quantity": "1", "strike_price": "100",
		"premium": "5", "settlement_date": "2030-12-31",
	})
	if err != nil {
		t.Fatalf("bid: %v", err)
	}
	if bidResp.StatusCode != 201 {
		t.Fatalf("bid: got %d body=%v", bidResp.StatusCode, bidResp.Body)
	}
	return offerID, sellerC, buyerCli
}

// The listing's poster may list all chains and read the cross-chain timeline;
// a competing bidder is forbidden from both (they retain their own-chain view
// at /me/otc/options/negotiations).
func TestOTCTimeline_PosterAllowed_BidderForbidden(t *testing.T) {
	adminC := loginAsAdmin(t)
	offerID, posterC, bidderC := setupOfferWithBid(t, adminC)

	// Poster: list all chains → 200 with a non-empty "negotiations".
	negResp, err := posterC.GET(fmt.Sprintf("/api/v3/otc/options/%d/negotiations", offerID))
	if err != nil {
		t.Fatalf("poster list negotiations: %v", err)
	}
	if negResp.StatusCode != 200 {
		t.Fatalf("poster list negotiations: want 200, got %d body=%v", negResp.StatusCode, negResp.Body)
	}
	if _, ok := negResp.Body["negotiations"].([]interface{}); !ok {
		t.Errorf("poster list: expected 'negotiations' array, got %v", negResp.Body)
	}

	// Poster: cross-chain timeline → 200 with the BID entry present.
	tlResp, err := posterC.GET(fmt.Sprintf("/api/v3/otc/options/%d/timeline", offerID))
	if err != nil {
		t.Fatalf("poster timeline: %v", err)
	}
	if tlResp.StatusCode != 200 {
		t.Fatalf("poster timeline: want 200, got %d body=%v", tlResp.StatusCode, tlResp.Body)
	}
	timeline, ok := tlResp.Body["timeline"].([]interface{})
	if !ok || len(timeline) == 0 {
		t.Fatalf("poster timeline: expected non-empty 'timeline', got %v", tlResp.Body)
	}
	first, _ := timeline[0].(map[string]interface{})
	if first["action"] != "BID" {
		t.Errorf("first timeline entry should be BID, got %v", first["action"])
	}

	// Bidder: both routes forbidden (they're a party to the chain, not the poster).
	bNeg, err := bidderC.GET(fmt.Sprintf("/api/v3/otc/options/%d/negotiations", offerID))
	if err != nil {
		t.Fatalf("bidder list negotiations: %v", err)
	}
	if bNeg.StatusCode != 403 {
		t.Errorf("bidder list negotiations: want 403, got %d body=%v", bNeg.StatusCode, bNeg.Body)
	}
	bTl, err := bidderC.GET(fmt.Sprintf("/api/v3/otc/options/%d/timeline", offerID))
	if err != nil {
		t.Fatalf("bidder timeline: %v", err)
	}
	if bTl.StatusCode != 403 {
		t.Errorf("bidder timeline: want 403, got %d body=%v", bTl.StatusCode, bTl.Body)
	}
}

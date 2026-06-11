//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_OptionExerciseTaxCycle drives the OTC option tax lifecycle end-to-end
// against the live stack (resolution-month model, SP1):
//
//	seller buys stock → writes a covered call → buyer BIDS → seller ACCEPTS the
//	chain (premium paid) → buyer EXERCISES (market > strike) → admin collects
//	tax → the buyer has a realised gain and was taxed.
//
// Uses the real intra-bank OTC negotiation flow:
//
//	POST /me/otc/options  →  POST /otc/options/:id/bid  →
//	POST /me/otc/options/:id/negotiations/:nid/accept  →
//	POST /otc/contracts/:id/exercise
func TestWF_OptionExerciseTaxCycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerC, _ := setupActivatedClient(t, adminC)
	sellerAcct, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	buyerAcct, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("seeded stock has no ticker/listing — skipping")
	}

	// Seller buys 1 share so they can write a covered call. account_id is
	// required for buy orders.
	buy, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy",
		"quantity": 1, "account_id": sellerAcct,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if buy.StatusCode != 201 {
		t.Skipf("could not seed seller holding (order POST %d body=%v) — skipping", buy.StatusCode, buy.Body)
	}
	orderID := int(helpers.GetNumberField(t, buy, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("seed buy order did not fill — skipping")
	}

	// Seller writes a call: strike 1, premium 1, qty 1 → exercise is profitable
	// for any market price > 2, so the buyer realises a positive gain.
	listing, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker, "quantity": "1",
		"account_id": sellerAcct,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if listing.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	if listing.StatusCode != 201 {
		t.Fatalf("create listing: HTTP %d body=%v", listing.StatusCode, listing.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, listing, "offer", "id"))

	// Buyer opens a negotiation chain by bidding the same terms.
	bid, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": buyerAcct, "quantity": "1", "strike_price": "1",
		"premium": "1", "settlement_date": "2030-12-31",
	})
	if err != nil {
		t.Fatalf("bid: %v", err)
	}
	helpers.RequireStatus(t, bid, 201)
	negID := int(helpers.GetNestedNumberField(t, bid, "negotiation", "id"))

	// Seller accepts the chain → contract forms, premium paid (seller premium
	// gain recorded; buyer's premium deferred to exercise per resolution-month).
	accept, err := sellerC.POST(fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/accept", offerID, negID), map[string]interface{}{
		"acceptor_account_id": sellerAcct,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	helpers.RequireStatus(t, accept, 201)
	contractID := int(helpers.GetNumberField(t, accept, "contract_id"))

	// Buyer exercises (market > strike).
	ex, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/contracts/%d/exercise", contractID), map[string]interface{}{})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	helpers.RequireStatus(t, ex, 201)

	// Admin collects tax for the current month.
	time.Sleep(2 * time.Second)
	collect, err := adminC.POST("/api/v3/tax/collect", nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	helpers.RequireStatus(t, collect, 200)

	// The buyer realised the exercise gain ((market−strike)×qty − premium) and
	// must now have a tax record.
	taxResp, err := buyerC.GET("/api/v3/me/tax")
	if err != nil {
		t.Fatalf("buyer tax: %v", err)
	}
	helpers.RequireStatus(t, taxResp, 200)
	if helpers.GetNumberField(t, taxResp, "total_count") < 1 {
		t.Fatalf("buyer should have a tax record after a profitable exercise, got total_count=%v", taxResp.Body["total_count"])
	}
}

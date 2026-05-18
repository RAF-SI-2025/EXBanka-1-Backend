//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestOTCOptions_ClientCannotUseForeignAccount asserts the resource-ownership
// gate: a client creating an OTC offer with an account_id that belongs to a
// different client is rejected with 403, before any offer is created.
func TestOTCOptions_ClientCannotUseForeignAccount(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, attackerC, _ := setupActivatedClient(t, adminC)
	victimID, _, _, _ := setupActivatedClient(t, adminC)
	victimAcctID, _ := createClientAccount(t, adminC, victimID, "RSD", 100000)
	_, ticker, _ := firstStock(t, adminC)

	resp, err := attackerC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction":       "sell_initiated",
		"ticker":          ticker,
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
		"account_id":      victimAcctID,
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 using a foreign account, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// TestOTCOptions_UnknownTickerRejected asserts an unknown ticker is rejected
// with 400 (after the account-ownership check passes).
func TestOTCOptions_UnknownTickerRejected(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)
	clientAcctID, _ := createClientAccount(t, adminC, clientID, "RSD", 100000)

	resp, err := clientC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction":       "sell_initiated",
		"ticker":          "ZZ_NOT_A_TICKER",
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
		"account_id":      clientAcctID,
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for unknown ticker, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// TestOTCOptions_ClientLifecycle drives a full client-to-client OTC option
// negotiation: client A (seller) buys a stock, lists a sell_initiated offer by
// ticker, client B (buyer) accepts it with their own account, then B exercises
// the resulting contract. Heavily skip-guarded because it depends on the
// market simulator filling A's seed buy order.
func TestOTCOptions_ClientLifecycle(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	buyerAcctID, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("seeded stock has no ticker/listing — skipping lifecycle test")
	}

	// Seed the seller's holding via a market buy. Skip if the simulator
	// doesn't fill it within the window (after-hours, etc.).
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID,
		"order_type": "market",
		"direction":  "buy",
		"quantity":   10,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("could not seed seller holding (order POST %d) — skipping", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("seed buy order did not fill — skipping lifecycle test")
	}

	// Seller creates a sell_initiated offer keyed by ticker.
	createResp, err := sellerC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction":       "sell_initiated",
		"ticker":          ticker,
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
		"account_id":      sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("expected 201 creating offer, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// Buyer accepts with their own account.
	acceptResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/offers/%d/accept", offerID), map[string]interface{}{
		"account_id": buyerAcctID,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acceptResp.StatusCode != 201 {
		t.Fatalf("expected 201 accepting offer, got %d body=%v", acceptResp.StatusCode, acceptResp.Body)
	}
	contractID := int(helpers.GetNumberField(t, acceptResp, "contract_id"))

	// Buyer exercises the contract — accounts come from the contract, so the
	// body is empty.
	exResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/contracts/%d/exercise", contractID), map[string]interface{}{})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if exResp.StatusCode != 201 {
		t.Errorf("expected 201 exercising contract, got %d body=%v", exResp.StatusCode, exResp.Body)
	}
}

// TestOTCOptions_ListMyContractsEmpty asserts the contracts endpoint
// returns an empty list for a fresh client.
func TestOTCOptions_ListMyContractsEmpty(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/otc/contracts")
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	helpers.RequireStatus(t, resp, 200)
	if raw, ok := resp.Body["contracts"]; ok && raw != nil {
		contracts, _ := raw.([]interface{})
		if len(contracts) != 0 {
			t.Errorf("expected empty contracts for fresh client, got %d", len(contracts))
		}
	}
}

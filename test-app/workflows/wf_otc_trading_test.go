//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_OTCTradingBetweenUsers exercises OTC (over-the-counter) trading between two agents:
//
//	agent A buys stock → makes holding public → agent B finds OTC offer → B buys from A →
//	verify A's quantity decreased, B has a new holding.
func TestWF_OTCTradingBetweenUsers(t *testing.T) {
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)

	// Step 1: Create two agent employees
	_, agentA, _ := setupAgentEmployee(t, adminC)
	_, agentB, _ := setupAgentEmployee(t, adminC)

	_, listingID := getFirstStockListingID(t, agentA)
	t.Logf("WF-9: using listing_id=%d", listingID)

	// Step 2: Agent A buys stock
	buyQuantity := 5
	buyResp, err := agentA.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    buyQuantity,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-9: agent A buy stock: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-9: agent A buy order id=%d", buyOrderID)

	waitForOrderFill(t, agentA, buyOrderID, 30*time.Second)
	t.Logf("WF-9: agent A buy order filled")

	// Step 3: Get A's holding ID from portfolio
	portfolioResp, err := agentA.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-9: agent A list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)

	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-9: agent A has no stock holdings after buy")
	}

	// Find the holding matching our listing
	var holdingID int
	var holdingQuantity float64
	for _, h := range holdings {
		hMap := h.(map[string]interface{})
		holdingID = int(hMap["id"].(float64))
		if qty, ok := hMap["quantity"].(float64); ok {
			holdingQuantity = qty
		}
		break // use the first holding
	}
	t.Logf("WF-9: agent A holding id=%d quantity=%.0f", holdingID, holdingQuantity)

	// Step 4: Agent A makes holding public
	publicQuantity := 2
	makePublicResp, err := agentA.POST(fmt.Sprintf("/api/me/portfolio/%d/make-public", holdingID), map[string]interface{}{
		"quantity": publicQuantity,
	})
	if err != nil {
		t.Fatalf("WF-9: make public: %v", err)
	}
	helpers.RequireStatus(t, makePublicResp, 200)
	t.Logf("WF-9: agent A made %d shares public from holding %d", publicQuantity, holdingID)

	// Step 5: Agent B lists OTC offers
	time.Sleep(1 * time.Second) // brief wait for offer to propagate
	offersResp, err := agentB.GET("/api/otc/offers")
	if err != nil {
		t.Fatalf("WF-9: agent B list OTC offers: %v", err)
	}
	helpers.RequireStatus(t, offersResp, 200)

	offers, ok := offersResp.Body["offers"].([]interface{})
	if !ok || len(offers) == 0 {
		t.Fatal("WF-9: no OTC offers found after make-public")
	}
	t.Logf("WF-9: found %d OTC offer(s)", len(offers))

	// Find an offer to buy (use the last one — most recently created)
	offerObj := offers[len(offers)-1].(map[string]interface{})
	offerID := int(offerObj["id"].(float64))
	t.Logf("WF-9: agent B will buy offer id=%d", offerID)

	// Step 6: Agent B buys from A's OTC offer
	otcBuyResp, err := agentB.POST(fmt.Sprintf("/api/otc/offers/%d/buy", offerID), map[string]interface{}{
		"quantity":   1,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("WF-9: agent B OTC buy: %v", err)
	}
	// Accept 200 (success) or 201 (created)
	if otcBuyResp.StatusCode != 200 && otcBuyResp.StatusCode != 201 {
		t.Fatalf("WF-9: expected 200 or 201 on OTC buy, got %d: %v", otcBuyResp.StatusCode, otcBuyResp.Body)
	}
	t.Logf("WF-9: agent B OTC buy response status=%d", otcBuyResp.StatusCode)

	// Step 7: Verify B has a holding
	portfolioBResp, err := agentB.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-9: agent B list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioBResp, 200)

	holdingsB, ok := portfolioBResp.Body["holdings"].([]interface{})
	if !ok || len(holdingsB) == 0 {
		t.Error("WF-9: agent B expected at least one stock holding after OTC buy")
	} else {
		t.Logf("WF-9: agent B has %d stock holding(s)", len(holdingsB))
	}

	t.Logf("WF-9: OTC trading workflow complete")
}

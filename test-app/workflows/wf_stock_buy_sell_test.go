//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_StockBuySellCycle exercises a complete stock buy-then-sell cycle:
//
//	agent buys stock via market order → waits for fill → verifies holding in portfolio →
//	sells stock via market order → waits for fill → verifies order is done.
func TestWF_StockBuySellCycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create agent employee
	_, agentC, _ := setupAgentEmployee(t, adminC)

	// Step 2: Get a stock listing
	_, listingID := getFirstStockListingID(t, agentC)
	t.Logf("WF-6: using listing_id=%d", listingID)

	// Step 3: Place market buy order
	buyResp, err := agentC.POST("/api/v1/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-6: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-6: buy order created id=%d", buyOrderID)

	// Step 4: Wait for fill
	waitForOrderFill(t, agentC, buyOrderID, 30*time.Second)
	t.Logf("WF-6: buy order filled")

	// Step 5: Assert holding exists in portfolio
	portfolioResp, err := agentC.GET("/api/v1/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-6: list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)

	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-6: expected at least one stock holding after buy, got none")
	}
	t.Logf("WF-6: portfolio has %d stock holding(s)", len(holdings))

	// Step 6: Place market sell order for the same listing
	sellResp, err := agentC.POST("/api/v1/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "sell",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-6: create sell order: %v", err)
	}
	helpers.RequireStatus(t, sellResp, 201)
	sellOrderID := int(helpers.GetNumberField(t, sellResp, "id"))
	t.Logf("WF-6: sell order created id=%d", sellOrderID)

	// Step 7: Wait for sell to fill
	waitForOrderFill(t, agentC, sellOrderID, 30*time.Second)
	t.Logf("WF-6: sell order filled")

	// Step 8: Verify the sell order is done
	orderResp, err := agentC.GET(fmt.Sprintf("/api/v1/me/orders/%d", sellOrderID))
	if err != nil {
		t.Fatalf("WF-6: get sell order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 200)
	if done, ok := orderResp.Body["is_done"].(bool); !ok || !done {
		t.Errorf("WF-6: sell order %d expected is_done=true, got %v", sellOrderID, orderResp.Body["is_done"])
	}
	t.Logf("WF-6: stock buy/sell cycle complete")
}

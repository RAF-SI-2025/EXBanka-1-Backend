//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_TaxCollectionCycle exercises the tax lifecycle:
//
//	agent buys stock → sells stock (generates capital gain) → checks own tax records →
//	supervisor triggers tax collection → verifies collection response.
func TestWF_TaxCollectionCycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create agent and buy stock
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, listingID := getFirstStockListingID(t, agentC)
	bankAcctID := getBankRSDAccountID(t, adminC)
	t.Logf("WF-10: using listing_id=%d bank_rsd_account_id=%d", listingID, bankAcctID)

	// Place market buy
	buyResp, err := agentC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    2,
		"all_or_none": false,
		"margin":      false,
		"account_id":  bankAcctID,
	})
	if err != nil {
		t.Fatalf("WF-10: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-10: buy order id=%d", buyOrderID)

	waitForOrderFill(t, agentC, buyOrderID, 30*time.Second)
	t.Logf("WF-10: buy order filled")

	// Sell stock to generate capital gain/loss
	sellResp, err := agentC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "sell",
		"order_type":  "market",
		"quantity":    2,
		"all_or_none": false,
		"margin":      false,
		"account_id":  bankAcctID,
	})
	if err != nil {
		t.Fatalf("WF-10: create sell order: %v", err)
	}
	helpers.RequireStatus(t, sellResp, 201)
	sellOrderID := int(helpers.GetNumberField(t, sellResp, "id"))
	t.Logf("WF-10: sell order id=%d", sellOrderID)

	waitForOrderFill(t, agentC, sellOrderID, 30*time.Second)
	t.Logf("WF-10: sell order filled — capital gain/loss generated")

	// Step 2: Check agent's own tax records
	taxResp, err := agentC.GET("/api/v3/me/tax")
	if err != nil {
		t.Fatalf("WF-10: get my tax records: %v", err)
	}
	helpers.RequireStatus(t, taxResp, 200)
	helpers.RequireField(t, taxResp, "records")
	helpers.RequireField(t, taxResp, "total_count")
	t.Logf("WF-10: agent tax records retrieved")

	// Step 3: Create supervisor
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	// Step 4: Supervisor triggers tax collection
	collectResp, err := supervisorC.POST("/api/v3/tax/collect", nil)
	if err != nil {
		t.Fatalf("WF-10: supervisor collect tax: %v", err)
	}
	helpers.RequireStatus(t, collectResp, 200)
	helpers.RequireField(t, collectResp, "collected_count")
	helpers.RequireField(t, collectResp, "total_collected_rsd")
	t.Logf("WF-10: tax collection response — collected_count=%v, total_collected_rsd=%v",
		collectResp.Body["collected_count"], collectResp.Body["total_collected_rsd"])

	// Step 5: Supervisor can list all tax records
	taxListResp, err := supervisorC.GET("/api/v3/tax")
	if err != nil {
		t.Fatalf("WF-10: supervisor list tax: %v", err)
	}
	helpers.RequireStatus(t, taxListResp, 200)
	helpers.RequireField(t, taxListResp, "tax_records")
	helpers.RequireField(t, taxListResp, "total_count")
	t.Logf("WF-10: tax collection cycle complete")
}

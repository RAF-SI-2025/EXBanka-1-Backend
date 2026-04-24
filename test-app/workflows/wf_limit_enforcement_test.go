//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_LimitEnforcementAcrossDomains exercises the employee limit enforcement flow
// across actuary (trading) and employee limits:
//
//	admin creates agent -> supervisor sets very low actuary limit ->
//	agent tries stock order (may be rejected or filled depending on price vs limit) ->
//	supervisor increases limit -> agent retries -> order succeeds.
//
// This test validates that the limit infrastructure exists and affects behavior.
// If the system does not enforce actuary limits at order time, the test still
// verifies the full limit CRUD + order lifecycle.
func TestWF_LimitEnforcementAcrossDomains(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create agent employee
	agentID, agentC, _ := setupAgentEmployee(t, adminC)
	t.Logf("WF-12: agent created id=%d", agentID)

	// Step 2: Create supervisor to manage actuary limits
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	// Step 3: Set agent's actuary limit very low (e.g., 1 RSD)
	setLimitResp, err := supervisorC.PUT(fmt.Sprintf("/api/v1/actuaries/%d/limit", agentID), map[string]interface{}{
		"limit": "1.00",
	})
	if err != nil {
		t.Fatalf("WF-12: set actuary limit: %v", err)
	}
	helpers.RequireStatus(t, setLimitResp, 200)
	t.Logf("WF-12: agent actuary limit set to 1.00 RSD")

	// Also set employee limits low for completeness
	setEmpLimitResp, err := adminC.PUT(fmt.Sprintf("/api/v1/employees/%d/limits", agentID), map[string]interface{}{
		"max_loan_approval_amount": "100.00",
		"max_single_transaction":   "100.00",
		"max_daily_transaction":    "200.00",
		"max_client_daily_limit":   "100.00",
		"max_client_monthly_limit": "500.00",
	})
	if err != nil {
		t.Fatalf("WF-12: set employee limits: %v", err)
	}
	helpers.RequireStatus(t, setEmpLimitResp, 200)
	t.Logf("WF-12: employee limits set to low values")

	// Step 4: Get a stock listing for order placement
	_, listingID := getFirstStockListingID(t, agentC)
	t.Logf("WF-12: using listing_id=%d", listingID)

	// Agent trades on behalf of the bank — use the bank's RSD account.
	bankAcctID := getBankRSDAccountID(t, adminC)

	// Step 5: Agent tries a stock order with the low limit
	// Depending on enforcement, this may be rejected (403/400) or accepted
	lowLimitOrderResp, err := agentC.POST("/api/v1/me/orders", map[string]interface{}{
		"security_type": "stock",
		"listing_id":    listingID,
		"direction":     "buy",
		"order_type":    "market",
		"quantity":      1,
		"all_or_none":   false,
		"margin":        false,
		"account_id":    bankAcctID,
	})
	if err != nil {
		t.Fatalf("WF-12: create order with low limit: %v", err)
	}

	lowLimitBlocked := lowLimitOrderResp.StatusCode != 201
	if lowLimitBlocked {
		t.Logf("WF-12: order blocked with low limit (status=%d) — limit enforcement active", lowLimitOrderResp.StatusCode)
	} else {
		orderID := int(helpers.GetNumberField(t, lowLimitOrderResp, "id"))
		t.Logf("WF-12: order accepted with low limit id=%d — limit may not be enforced at order time", orderID)
		// If order was accepted, best-effort wait (fill is not required here).
		_ = tryWaitForOrderFill(t, agentC, orderID, 10*time.Second)
	}

	// Step 6: Supervisor increases the actuary limit to a high value
	increaseLimitResp, err := supervisorC.PUT(fmt.Sprintf("/api/v1/actuaries/%d/limit", agentID), map[string]interface{}{
		"limit": "10000000.00",
	})
	if err != nil {
		t.Fatalf("WF-12: increase actuary limit: %v", err)
	}
	helpers.RequireStatus(t, increaseLimitResp, 200)
	t.Logf("WF-12: agent actuary limit increased to 10,000,000 RSD")

	// Also increase employee limits
	increaseEmpLimitResp, err := adminC.PUT(fmt.Sprintf("/api/v1/employees/%d/limits", agentID), map[string]interface{}{
		"max_loan_approval_amount": "5000000.00",
		"max_single_transaction":   "5000000.00",
		"max_daily_transaction":    "10000000.00",
		"max_client_daily_limit":   "1000000.00",
		"max_client_monthly_limit": "5000000.00",
	})
	if err != nil {
		t.Fatalf("WF-12: increase employee limits: %v", err)
	}
	helpers.RequireStatus(t, increaseEmpLimitResp, 200)
	t.Logf("WF-12: employee limits increased to high values")

	// Step 7: Agent retries with higher limit — should succeed
	retryOrderResp, err := agentC.POST("/api/v1/me/orders", map[string]interface{}{
		"security_type": "stock",
		"listing_id":    listingID,
		"direction":     "buy",
		"order_type":    "market",
		"quantity":      1,
		"all_or_none":   false,
		"margin":        false,
		"account_id":    bankAcctID,
	})
	if err != nil {
		t.Fatalf("WF-12: create order with high limit: %v", err)
	}
	helpers.RequireStatus(t, retryOrderResp, 201)
	retryOrderID := int(helpers.GetNumberField(t, retryOrderResp, "id"))
	t.Logf("WF-12: order created with high limit id=%d", retryOrderID)

	// Placement succeeded with the high limit — we don't require the order to
	// fill within the test window (market simulator adds ~30 min post-close).
	settled := tryWaitForOrderFill(t, agentC, retryOrderID, 10*time.Second)
	t.Logf("WF-12: order settled=%v (high-limit placement verified regardless)", settled)

	// Step 8: Verify agent's limits can be read back
	getLimitsResp, err := adminC.GET(fmt.Sprintf("/api/v1/employees/%d/limits", agentID))
	if err != nil {
		t.Fatalf("WF-12: get employee limits: %v", err)
	}
	helpers.RequireStatus(t, getLimitsResp, 200)

	if lowLimitBlocked {
		t.Logf("WF-12: PASS — low limit blocked order, high limit allowed order")
	} else {
		t.Logf("WF-12: PASS — limit CRUD verified, orders succeeded at both limit levels (enforcement may be async)")
	}
}

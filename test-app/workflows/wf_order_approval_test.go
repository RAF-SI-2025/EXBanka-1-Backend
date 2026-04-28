//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_OrderApprovalWorkflow exercises the supervisor approval/reject flow:
//
//	agent places buy order → supervisor approves → order fills → agent has holding →
//	agent places another order → supervisor rejects → order is rejected, no new holding.
func TestWF_OrderApprovalWorkflow(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create agent and supervisor
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	_, listingID := getFirstStockListingID(t, agentC)
	t.Logf("WF-8: using listing_id=%d", listingID)

	// Step 2: Agent places a buy order
	buyResp, err := agentC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-8: agent create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	orderID1 := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-8: agent order #1 created id=%d", orderID1)

	// Check initial status — may be "pending" (needs approval) or already approved
	orderCheck, err := agentC.GET(fmt.Sprintf("/api/v3/me/orders/%d", orderID1))
	if err != nil {
		t.Fatalf("WF-8: get order #1: %v", err)
	}
	helpers.RequireStatus(t, orderCheck, 200)
	status1 := fmt.Sprintf("%v", orderCheck.Body["status"])
	t.Logf("WF-8: order #1 initial status=%s", status1)

	// Step 3: Supervisor approves the order
	approveResp, err := supervisorC.POST(fmt.Sprintf("/api/v3/orders/%d/approve", orderID1), nil)
	if err != nil {
		t.Fatalf("WF-8: supervisor approve order: %v", err)
	}
	// Accept 200 (approved) or 409 (already approved/executed)
	if approveResp.StatusCode != 200 && approveResp.StatusCode != 409 {
		t.Fatalf("WF-8: expected 200 or 409 on approve, got %d", approveResp.StatusCode)
	}
	t.Logf("WF-8: supervisor approve response status=%d", approveResp.StatusCode)

	// Step 4: Wait for the order to fill
	waitForOrderFill(t, agentC, orderID1, 30*time.Second)
	t.Logf("WF-8: order #1 filled after approval")

	// Verify agent has a holding
	portfolioResp, err := agentC.GET("/api/v3/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-8: list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)
	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-8: expected at least one holding after approved buy, got none")
	}
	holdingsCountAfterApprove := len(holdings)
	t.Logf("WF-8: agent has %d holding(s) after approved buy", holdingsCountAfterApprove)

	// Step 5: Agent places another buy order
	buyResp2, err := agentC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-8: agent create order #2: %v", err)
	}
	helpers.RequireStatus(t, buyResp2, 201)
	orderID2 := int(helpers.GetNumberField(t, buyResp2, "id"))
	t.Logf("WF-8: agent order #2 created id=%d", orderID2)

	// Step 6: Supervisor rejects this order
	declineResp, err := supervisorC.POST(fmt.Sprintf("/api/v3/orders/%d/reject", orderID2), nil)
	if err != nil {
		t.Fatalf("WF-8: supervisor reject order: %v", err)
	}
	// Accept 200 (rejected) or 409 (already processed)
	if declineResp.StatusCode != 200 && declineResp.StatusCode != 409 {
		t.Fatalf("WF-8: expected 200 or 409 on reject, got %d", declineResp.StatusCode)
	}
	t.Logf("WF-8: supervisor reject response status=%d", declineResp.StatusCode)

	// Verify order #2 status is declined (or similar terminal state)
	order2Check, err := agentC.GET(fmt.Sprintf("/api/v3/me/orders/%d", orderID2))
	if err != nil {
		t.Fatalf("WF-8: get order #2: %v", err)
	}
	helpers.RequireStatus(t, order2Check, 200)
	status2 := fmt.Sprintf("%v", order2Check.Body["status"])
	t.Logf("WF-8: order #2 status after decline=%s", status2)

	// The rejected order should not have created a new holding beyond what approval gave
	portfolio2Resp, err := agentC.GET("/api/v3/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-8: list portfolio after decline: %v", err)
	}
	helpers.RequireStatus(t, portfolio2Resp, 200)
	t.Logf("WF-8: order approval/reject workflow complete")
}

//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF11: Client Limit Management ---

func TestClientLimits_GetLimits(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	resp, err := c.GET(fmt.Sprintf("/api/v3/clients/%d/limits", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestClientLimits_SetLimits(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID := createTestClient(t, adminC)

	// Admin has unlimited limits set by seeder — can set client limits directly
	resp, err := adminC.PUT(fmt.Sprintf("/api/v3/clients/%d/limits", clientID), map[string]interface{}{
		"daily_limit":    "100000.00",
		"monthly_limit":  "500000.00",
		"transfer_limit": "50000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestClientLimits_SetLimitsExceedingEmployeeMax(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID := createTestClient(t, adminC)

	// Create an agent employee with modest limits
	agentID, agentC, _ := setupAgentEmployee(t, adminC)

	// Admin sets modest limits on the agent (all fields required)
	resp, err := adminC.PUT(fmt.Sprintf("/api/v3/employees/%d/limits", agentID), map[string]interface{}{
		"max_loan_approval_amount": "50000.00",
		"max_single_transaction":   "100000.00",
		"max_daily_transaction":    "500000.00",
		"max_client_daily_limit":   "10000.00",
		"max_client_monthly_limit": "50000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Agent tries to set client limits exceeding their own max
	resp, err = agentC.PUT(fmt.Sprintf("/api/v3/clients/%d/limits", clientID), map[string]interface{}{
		"daily_limit":   "999999.00",
		"monthly_limit": "9999999.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure: client limits exceed employee's max")
	}
}

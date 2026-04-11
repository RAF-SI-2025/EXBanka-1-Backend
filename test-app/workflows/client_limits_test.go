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

	resp, err := c.GET(fmt.Sprintf("/api/clients/%d/limits", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestClientLimits_SetLimits(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	// First set admin employee limits (required for client limit enforcement)
	resp, err := c.PUT("/api/employees/1/limits", map[string]interface{}{
		"max_loan_approval_amount": "10000000.00",
		"max_single_transaction":   "1000000.00",
		"max_daily_transaction":    "5000000.00",
		"max_client_daily_limit":   "500000.00",
		"max_client_monthly_limit": "2000000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Set client limits (within employee's max)
	resp, err = c.PUT(fmt.Sprintf("/api/clients/%d/limits", clientID), map[string]interface{}{
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
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	// Set modest employee limits
	_, err := c.PUT("/api/employees/1/limits", map[string]interface{}{
		"max_client_daily_limit":   "10000.00",
		"max_client_monthly_limit": "50000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Try to set client limits exceeding employee's max
	resp, err := c.PUT(fmt.Sprintf("/api/clients/%d/limits", clientID), map[string]interface{}{
		"daily_limit":   "999999.00", // exceeds max_client_daily_limit
		"monthly_limit": "9999999.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure: client limits exceed employee's max")
	}
}

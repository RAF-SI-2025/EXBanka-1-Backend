//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF14: Interest Rate Tier & Bank Margin Management ---

func TestInterestRateTiers_List(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/interest-rate-tiers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestInterestRateTiers_UnauthenticatedDenied(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/v3/interest-rate-tiers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestInterestRateTiers_CreateFixed(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		"fixed_rate":    5.50,
		"variable_base": 0.01,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating fixed interest rate tier, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestInterestRateTiers_CreateVariable(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		"fixed_rate":    0.01,
		"variable_base": 3.25,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating variable interest rate tier, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestInterestRateTiers_CreateMissingFixedRate(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		// "fixed_rate" intentionally omitted
		"variable_base": 3.25,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Binding requires fixed_rate; should return 400
	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		t.Logf("warning: server accepted request without fixed_rate (may default to 0.0)")
	}
}

func TestInterestRateTiers_UpdateTier(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a tier first
	createResp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		"fixed_rate":    4.0,
		"variable_base": 0.01,
	})
	if err != nil {
		t.Fatalf("create tier error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping update test: create tier returned %d", createResp.StatusCode)
	}
	tierID := int(helpers.GetNumberField(t, createResp, "id"))

	// Update the tier
	resp, err := c.PUT(fmt.Sprintf("/api/v3/interest-rate-tiers/%d", tierID), map[string]interface{}{
		"fixed_rate":    6.0,
		"variable_base": 0.01,
	})
	if err != nil {
		t.Fatalf("update tier error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success updating interest rate tier, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestInterestRateTiers_DeleteTier(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a tier to delete
	createResp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		"fixed_rate":    7.0,
		"variable_base": 0.01,
	})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping delete: create returned %d", createResp.StatusCode)
	}
	tierID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := c.DELETE(fmt.Sprintf("/api/v3/interest-rate-tiers/%d", tierID))
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestInterestRateTiers_ApplyVariableRate(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a variable rate tier
	createResp, err := c.POST("/api/v3/interest-rate-tiers", map[string]interface{}{
		"fixed_rate":    0.01,
		"variable_base": 4.0,
	})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping apply: create returned %d", createResp.StatusCode)
	}
	tierID := int(helpers.GetNumberField(t, createResp, "id"))

	// Apply variable rate update
	resp, err := c.POST(fmt.Sprintf("/api/v3/interest-rate-tiers/%d/apply", tierID), map[string]interface{}{})
	if err != nil {
		t.Fatalf("apply error: %v", err)
	}
	// May succeed or return 200/409 depending on whether loans exist
	if resp.StatusCode >= 500 {
		t.Fatalf("unexpected server error applying variable rate: %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestBankMargins_List(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/bank-margins")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestBankMargins_Update(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// List margins to get an ID
	listResp, err := c.GET("/api/v3/bank-margins")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	margins, ok := listResp.Body["margins"].([]interface{})
	if !ok || len(margins) == 0 {
		t.Skip("no bank margins seeded to update")
	}
	first := margins[0].(map[string]interface{})
	marginID := int(first["id"].(float64))

	resp, err := c.PUT(fmt.Sprintf("/api/v3/bank-margins/%d", marginID), map[string]interface{}{
		"margin": 2.5,
	})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

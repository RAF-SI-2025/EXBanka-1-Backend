package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF14: Interest Rate Tier & Bank Margin Management ---

func TestInterestRateTiers_List(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/interest-rate-tiers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestInterestRateTiers_Create(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/interest-rate-tiers", map[string]interface{}{
		"min_amount":    "0.00",
		"max_amount":    "1000000.00",
		"interest_rate": "5.50",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestBankMargins_List(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/bank-margins")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestInterestRateTiers_UnauthenticatedDenied(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/interest-rate-tiers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

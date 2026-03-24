//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF12: Exchange Rates (public) ---

func TestExchangeRates_ListAll(t *testing.T) {
	c := newClient() // public route, no auth needed
	resp, err := c.GET("/api/exchange-rates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestExchangeRates_GetSpecific(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/exchange-rates/EUR/RSD")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// May be 200 (found) or 404 (no rate). Either is valid.
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Fatalf("expected 200 or 404, got %d", resp.StatusCode)
	}
}

func TestExchangeRates_GetInverse(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/exchange-rates/RSD/EUR")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Fatalf("expected 200 or 404, got %d", resp.StatusCode)
	}
}

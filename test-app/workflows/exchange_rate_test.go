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

// --- New canonical /api/exchange/rates paths ---

func TestExchangeRates_NewPath_ListAll(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/exchange/rates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestExchangeRates_NewPath_GetSpecific(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/exchange/rates/EUR/RSD")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Fatalf("expected 200 or 404, got %d", resp.StatusCode)
	}
}

// --- POST /api/exchange/calculate ---

func TestExchangeRates_Calculate(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/exchange/calculate", map[string]interface{}{
		"fromCurrency": "EUR",
		"toCurrency":   "RSD",
		"amount":       "100.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// 200 if rates are seeded, 404 if rate pair not found
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Fatalf("expected 200 or 404, got %d", resp.StatusCode)
	}
	if resp.StatusCode == 200 {
		fromCurrency := helpers.GetStringField(t, resp, "from_currency")
		if fromCurrency != "EUR" {
			t.Fatalf("expected from_currency=EUR, got %s", fromCurrency)
		}
		toCurrency := helpers.GetStringField(t, resp, "to_currency")
		if toCurrency != "RSD" {
			t.Fatalf("expected to_currency=RSD, got %s", toCurrency)
		}
	}
}

func TestExchangeRates_Calculate_MissingFields(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/exchange/calculate", map[string]interface{}{
		"fromCurrency": "EUR",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestExchangeRates_Calculate_InvalidAmount(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/exchange/calculate", map[string]interface{}{
		"fromCurrency": "EUR",
		"toCurrency":   "RSD",
		"amount":       "-100",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestExchangeRates_Calculate_UnsupportedCurrency(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/exchange/calculate", map[string]interface{}{
		"fromCurrency": "XYZ",
		"toCurrency":   "RSD",
		"amount":       "100",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

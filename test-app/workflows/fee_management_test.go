package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF13: Transfer Fee Management ---

func TestFees_ListFees(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/fees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestFees_CreatePercentageFee(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"fee_type":   "percentage",
		"value":      "0.5",
		"min_amount": "500.00",
		"max_fee":    "10000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestFees_CreateFixedFee(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"fee_type":   "fixed",
		"value":      "100.00",
		"min_amount": "0.00",
		"max_fee":    "100.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestFees_CreateWithInvalidType(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"fee_type":   "invalid",
		"value":      "100",
		"min_amount": "0",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		t.Fatal("expected failure with invalid fee type")
	}
}

func TestFees_UnauthenticatedCannotManageFees(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/fees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

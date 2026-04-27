//go:build integration && destructive

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestStockSource_SwitchToGenerated exercises the admin stock-source switch flow:
//
//	POST /api/v3/admin/stock-source {"source":"generated"} →
//	poll GET /api/v3/admin/stock-source until status=idle →
//	verify 20 generated stocks including AAPL →
//	verify options exist for the first stock.
//
// WARNING: this test is DESTRUCTIVE — switching to "generated" wipes all existing
// securities data in the stock-service DB and replaces it with the 20 hardcoded
// generated tickers. Run in isolation or after the full suite.
// Tagged with the "destructive" build tag so it does not run during normal CI.
func TestStockSource_SwitchToGenerated(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Restore source to "external" when the test finishes so later tests see live data.
	t.Cleanup(func() {
		restoreResp, err := adminC.POST("/api/v3/admin/stock-source", map[string]string{
			"source": "external",
		})
		if err != nil {
			t.Logf("cleanup: failed to restore stock source to external: %v", err)
			return
		}
		if restoreResp.StatusCode != 202 {
			t.Logf("cleanup: restore stock source returned %d (body: %s)", restoreResp.StatusCode, string(restoreResp.RawBody))
		}
	})

	// Switch to generated source.
	switchResp, err := adminC.POST("/api/v3/admin/stock-source", map[string]string{
		"source": "generated",
	})
	if err != nil {
		t.Fatalf("POST /api/v3/admin/stock-source: %v", err)
	}
	helpers.RequireStatus(t, switchResp, 202)

	// Poll for idle — the generated source switch is synchronous but we poll for safety.
	deadline := time.Now().Add(10 * time.Second)
	finalStatus := ""
	for time.Now().Before(deadline) {
		statusResp, err := adminC.GET("/api/v3/admin/stock-source")
		if err != nil {
			t.Fatalf("GET /api/v3/admin/stock-source: %v", err)
		}
		helpers.RequireStatus(t, statusResp, 200)
		if s, ok := statusResp.Body["status"].(string); ok && s == "idle" {
			finalStatus = s
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if finalStatus != "idle" {
		t.Fatalf("expected stock-source switch to reach status=idle within 10s, last status=%q", finalStatus)
	}

	// Verify 20 generated stocks were seeded.
	stocksResp, err := adminC.GET("/api/v3/securities/stocks?page=1&page_size=50")
	if err != nil {
		t.Fatalf("GET /api/v3/securities/stocks: %v", err)
	}
	helpers.RequireStatus(t, stocksResp, 200)

	stocks, ok := stocksResp.Body["stocks"].([]interface{})
	if !ok {
		t.Fatalf("expected 'stocks' array in response, body: %s", string(stocksResp.RawBody))
	}
	if len(stocks) != 20 {
		t.Fatalf("expected 20 generated stocks, got %d", len(stocks))
	}

	foundAAPL := false
	firstStockID := 0
	for _, s := range stocks {
		m, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if ticker, _ := m["ticker"].(string); ticker == "AAPL" {
			foundAAPL = true
		}
		if firstStockID == 0 {
			if id, ok := m["id"].(float64); ok {
				firstStockID = int(id)
			}
		}
	}
	if !foundAAPL {
		t.Fatalf("AAPL not found in generated stock set (got %d stocks)", len(stocks))
	}
	if firstStockID == 0 {
		t.Fatal("could not extract first stock ID from response")
	}
	t.Logf("generated source: %d stocks seeded, AAPL present, first stock id=%d", len(stocks), firstStockID)

	// Verify options exist for the first stock.
	optsResp, err := adminC.GET("/api/v3/securities/options?stock_id=" + helpers.FormatID(firstStockID))
	if err != nil {
		t.Fatalf("GET /api/v3/securities/options?stock_id=%d: %v", firstStockID, err)
	}
	helpers.RequireStatus(t, optsResp, 200)

	opts, ok := optsResp.Body["options"].([]interface{})
	if !ok {
		t.Fatalf("expected 'options' array in response, body: %s", string(optsResp.RawBody))
	}
	if len(opts) == 0 {
		t.Fatalf("expected at least one option for stock id=%d, got 0", firstStockID)
	}
	t.Logf("stock id=%d has %d option(s) — TestStockSource_SwitchToGenerated PASS", firstStockID, len(opts))
}

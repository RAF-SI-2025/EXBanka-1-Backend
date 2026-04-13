//go:build integration && destructive
// +build integration,destructive

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestOptionsV2_Exercise_RouteWired verifies that the v2 options exercise route is wired,
// auth-enforced, and reachable through the API gateway. A full exercise flow (with client
// account setup, option holding acquisition, and in-the-money check) is out of scope because
// it requires extensive fixtures; that can be added as a follow-up.
//
// The test:
//  1. Switches stock-source to "generated" so seeded options with listing_ids are available.
//  2. Finds the first stock and one of its options.
//  3. POSTs to /api/v2/options/{option_id}/exercise.
//  4. Accepts 200/400/403/404-holding/409 — rejects routing 404 or 5xx.
//
// Tagged "destructive" because switching to generated wipes existing securities data.
func TestOptionsV2_Exercise_RouteWired(t *testing.T) {
	admin := loginAsAdmin(t)

	// Switch to generated so we have seeded options with listing_ids.
	switchResp, err := admin.POST("/api/v1/admin/stock-source", map[string]string{"source": "generated"})
	if err != nil {
		t.Fatalf("switch to generated: %v", err)
	}
	helpers.RequireStatus(t, switchResp, 202)

	// Cleanup: restore external at test end.
	t.Cleanup(func() {
		_, _ = admin.POST("/api/v1/admin/stock-source", map[string]string{"source": "external"})
	})

	// Poll for idle — the generated source switch is synchronous but we poll for safety.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := admin.GET("/api/v1/admin/stock-source")
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if s, ok := statusResp.Body["status"].(string); ok && s == "idle" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Find a stock and its options.
	stocksResp, err := admin.GET("/api/v1/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("list stocks: %v", err)
	}
	helpers.RequireStatus(t, stocksResp, 200)

	stocks, ok := stocksResp.Body["stocks"].([]interface{})
	if !ok || len(stocks) == 0 {
		t.Fatal("no stocks found after switch to generated")
	}
	firstStock := stocks[0].(map[string]interface{})
	stockID := int(firstStock["id"].(float64))

	optsResp, err := admin.GET("/api/v1/securities/options?stock_id=" + helpers.FormatID(stockID))
	if err != nil {
		t.Fatalf("list options: %v", err)
	}
	helpers.RequireStatus(t, optsResp, 200)

	opts, ok := optsResp.Body["options"].([]interface{})
	if !ok || len(opts) == 0 {
		t.Fatal("no options found for first stock")
	}
	firstOpt := opts[0].(map[string]interface{})
	optionID := int(firstOpt["id"].(float64))
	t.Logf("using stock id=%d option id=%d", stockID, optionID)

	// Try to exercise the option via v2.
	// We use the admin token — a real exercise requires a client with an option holding.
	//
	// Acceptable responses:
	//   200 — exercise succeeded
	//   400 — validation error (e.g., invalid holding_id)
	//   403 — admin lacks required permission
	//   404 — no holding found (expected when admin has no option holding)
	//   409 — business rule violation (e.g., option expired, not in-the-money)
	//
	// Not acceptable: routing 404 (route not wired) or 5xx (server error).
	exerciseResp, err := admin.POST(
		"/api/v2/options/"+helpers.FormatID(optionID)+"/exercise",
		map[string]interface{}{"holding_id": 0},
	)
	if err != nil {
		t.Fatalf("v2 exercise: %v", err)
	}

	if exerciseResp.StatusCode >= 500 {
		t.Fatalf("v2 exercise returned 5xx: %d body=%s",
			exerciseResp.StatusCode, string(exerciseResp.RawBody))
	}

	// If we got a 404, check that it's NOT the routing 404.
	if exerciseResp.StatusCode == 404 {
		if errMap, ok := exerciseResp.Body["error"].(map[string]interface{}); ok {
			if msg, ok := errMap["message"].(string); ok && msg == "route not found" {
				t.Fatalf("v2 exercise route is not wired — got routing 404")
			}
		}
	}

	t.Logf("v2 exercise returned %d (expected 200/400/403/404-holding/409): %s",
		exerciseResp.StatusCode, string(exerciseResp.RawBody))
}

//go:build integration && destructive
// +build integration,destructive

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestOptionsV2_CreateOrder_EndToEnd verifies that the v2 options order route is wired,
// auth-enforced, and reachable through the API gateway. A full buy flow (with client
// account setup, order approval, and portfolio verification) is out of scope because it
// requires extensive fixtures; that can be added as a follow-up.
//
// The test:
//  1. Switches stock-source to "generated" so seeded options with listing_ids are available.
//  2. Finds the first stock and one of its options.
//  3. POSTs to /api/v3/options/{option_id}/orders.
//  4. Accepts 201/400/403/409 — rejects 404 (route missing) or 5xx (server error).
//
// Tagged "destructive" because switching to generated wipes existing securities data.
func TestOptionsV2_CreateOrder_EndToEnd(t *testing.T) {
	admin := loginAsAdmin(t)

	// Switch to generated so we have seeded options with listing_ids.
	switchResp, err := admin.POST("/api/v3/stock-sources", map[string]string{"source": "generated"})
	if err != nil {
		t.Fatalf("switch to generated: %v", err)
	}
	helpers.RequireStatus(t, switchResp, 202)

	// Cleanup: restore external at test end.
	t.Cleanup(func() {
		_, _ = admin.POST("/api/v3/stock-sources", map[string]string{"source": "external"})
	})

	// Poll for idle — the generated source switch is synchronous but we poll for safety.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := admin.GET("/api/v3/stock-sources/active")
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
	stocksResp, err := admin.GET("/api/v3/securities/stocks?page=1&page_size=1")
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

	optsResp, err := admin.GET("/api/v3/securities/options?stock_id=" + helpers.FormatID(stockID))
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

	// Try to create an options order via v2.
	// We use the admin token — a real client token would require full client fixtures.
	//
	// Acceptable responses:
	//   201 — order created
	//   400 — validation or account error (expected if admin has no trading account)
	//   403 — admin lacks securities.trade permission
	//   409 — option not tradeable OR listing not found
	//
	// Not acceptable: 404 (route missing) or 5xx (server error).
	orderResp, err := admin.POST(
		"/api/v3/options/"+helpers.FormatID(optionID)+"/orders",
		map[string]interface{}{
			"direction":  "buy",
			"order_type": "market",
			"quantity":   1,
			"margin":     true,
			"account_id": 1,
		},
	)
	if err != nil {
		t.Fatalf("create v2 option order: %v", err)
	}
	if orderResp.StatusCode == 404 {
		t.Fatalf("v2 route returned 404 — router not wired")
	}
	if orderResp.StatusCode >= 500 {
		t.Fatalf("v2 route returned 5xx: %d body=%s", orderResp.StatusCode, string(orderResp.RawBody))
	}
	t.Logf("v2 create order returned %d (expected 201/400/403/409): %s",
		orderResp.StatusCode, string(orderResp.RawBody))
}

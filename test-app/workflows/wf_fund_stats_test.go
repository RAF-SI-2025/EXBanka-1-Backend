//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FundStatistics_SurfaceAndSort verifies the SP3 fund-statistics API
// surface end-to-end: the detail endpoint returns the metrics + history fields,
// the discovery endpoint accepts metric sorting, and an invalid sort is
// rejected. (The metrics_available=true path requires months of snapshot
// history and is covered by stock-service unit tests; a fresh fund here reports
// metrics_available=false with empty history.)
func TestWF_FundStatistics_SurfaceAndSort(t *testing.T) {
	adminC := loginAsAdmin(t)

	name := fmt.Sprintf("Stats-%d", time.Now().UnixNano())
	createResp, err := adminC.POST("/api/v3/investment-funds", map[string]interface{}{
		"name":                     name,
		"description":              "SP3 stats fund",
		"minimum_contribution_rsd": "0",
	})
	if err != nil {
		t.Fatalf("create fund: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("v3 investment-funds endpoints not deployed — skipping")
	}
	helpers.RequireStatus(t, createResp, 201)
	fund := helpers.RequireField(t, createResp, "fund").(map[string]interface{})
	fundID := int(fund["id"].(float64))

	// Detail: new statistics + history fields present.
	detail, err := adminC.GET(fmt.Sprintf("/api/v3/investment-funds/%d", fundID))
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	helpers.RequireStatus(t, detail, 200)
	for _, f := range []string{"annualized_return_pct", "volatility_pct", "reward_to_variability", "max_drawdown_pct", "metrics_available", "history", "average_history"} {
		helpers.RequireField(t, detail, f)
	}
	if detail.Body["metrics_available"] != false {
		t.Errorf("fresh fund should have metrics_available=false, got %v", detail.Body["metrics_available"])
	}

	// Discovery: metric sort returns 200 and the fund carries the metric fields.
	sorted, err := adminC.GET("/api/v3/investment-funds?sort_by=annualized_return&sort_order=desc&page_size=200")
	if err != nil {
		t.Fatalf("sorted list: %v", err)
	}
	helpers.RequireStatus(t, sorted, 200)
	funds, _ := helpers.RequireField(t, sorted, "funds").([]interface{})
	var found bool
	for _, raw := range funds {
		fr, _ := raw.(map[string]interface{})
		if int(fr["id"].(float64)) == fundID {
			found = true
			// proto JSON omits false bools, but the metric strings ("0" when
			// unavailable) are non-empty and present — assert they flow through.
			if _, ok := fr["annualized_return_pct"]; !ok {
				t.Errorf("fund in sorted list missing annualized_return_pct")
			}
		}
	}
	if !found {
		t.Errorf("created fund not present in sorted discovery list")
	}

	// Invalid sort_by → 400.
	bad, err := adminC.GET("/api/v3/investment-funds?sort_by=bogus")
	if err != nil {
		t.Fatalf("bad sort: %v", err)
	}
	helpers.RequireStatus(t, bad, 400)
}

//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FundDividendMode verifies SP4: a fund can be created in reinvest mode,
// the mode is surfaced on responses, and it can be toggled via PUT. (The full
// dividend→DRIP-buy is covered by stock-service unit tests; it needs a fund
// holding + a declared dividend.)
func TestWF_FundDividendMode(t *testing.T) {
	adminC := loginAsAdmin(t)

	name := fmt.Sprintf("DRIP-%d", time.Now().UnixNano())
	createResp, err := adminC.POST("/api/v3/investment-funds", map[string]interface{}{
		"name":          name,
		"description":   "SP4 reinvest fund",
		"dividend_mode": "reinvest",
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
	if fund["dividend_mode"] != "reinvest" {
		t.Fatalf("created fund dividend_mode = %v, want reinvest", fund["dividend_mode"])
	}

	// Toggle back to payout.
	putResp, err := adminC.PUT(fmt.Sprintf("/api/v3/investment-funds/%d", fundID), map[string]interface{}{
		"dividend_mode": "payout",
	})
	if err != nil {
		t.Fatalf("update fund: %v", err)
	}
	helpers.RequireStatus(t, putResp, 200)

	detail, err := adminC.GET(fmt.Sprintf("/api/v3/investment-funds/%d", fundID))
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	helpers.RequireStatus(t, detail, 200)
	fundObj := helpers.RequireField(t, detail, "fund").(map[string]interface{})
	if fundObj["dividend_mode"] != "payout" {
		t.Fatalf("after toggle dividend_mode = %v, want payout", fundObj["dividend_mode"])
	}

	// Invalid mode → 400.
	bad, err := adminC.POST("/api/v3/investment-funds", map[string]interface{}{
		"name":          name + "-bad",
		"dividend_mode": "bogus",
	})
	if err != nil {
		t.Fatalf("bad create: %v", err)
	}
	helpers.RequireStatus(t, bad, 400)
}

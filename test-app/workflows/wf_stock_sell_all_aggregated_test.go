//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_SellAllAcrossAggregatedHolding validates the Part-A rollup end-to-end:
//
//   - A client funds two accounts (A and B).
//   - They place three buy orders (10 shares each) across those accounts.
//   - The resulting /me/portfolio should show ONE holding row with quantity=30.
//   - A single sell order for quantity=30 (with account A as proceeds
//     destination) should fill — proving the reservation lookup aggregates
//     across accounts and there's no need for holding_id.
//
// Serves as the integration backstop for the Part-A schema change and the
// gateway-side removal of `holding_id is required for sell orders`.
func TestWF_SellAllAcrossAggregatedHolding(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Setup: one client with two RSD accounts, both funded.
	clientID, acctAnum, clientC, _ := setupActivatedClient(t, adminC)

	// Add a second RSD account owned by the same client.
	acctBResp, err := adminC.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 100000,
	})
	if err != nil {
		t.Fatalf("create second account: %v", err)
	}
	helpers.RequireStatus(t, acctBResp, 201)
	acctBnum := helpers.GetStringField(t, acctBResp, "account_number")

	acctAID := getAccountIDByNumber(t, clientC, acctAnum)
	acctBID := getAccountIDByNumber(t, clientC, acctBnum)

	// Pick a stock listing to trade.
	_, listingID := getFirstStockListingID(t, clientC)
	t.Logf("sell-all: listing_id=%d, acctA=%d, acctB=%d", listingID, acctAID, acctBID)

	// Place three buy orders (10 shares each, two from acctA + one from acctB).
	type buyPlan struct {
		qty       int
		accountID uint64
	}
	for i, plan := range []buyPlan{
		{10, acctAID},
		{10, acctAID},
		{10, acctBID},
	} {
		buyResp, err := clientC.POST("/api/v1/me/orders", map[string]interface{}{
			"listing_id":  listingID,
			"direction":   "buy",
			"order_type":  "market",
			"quantity":    plan.qty,
			"account_id":  plan.accountID,
			"all_or_none": false,
			"margin":      false,
		})
		if err != nil {
			t.Fatalf("buy %d: %v", i+1, err)
		}
		if buyResp.StatusCode != 201 {
			t.Fatalf("buy %d: expected 201, got %d: %v", i+1, buyResp.StatusCode, buyResp.Body)
		}
		buyID := int(helpers.GetNumberField(t, buyResp, "id"))
		// Wait for fill before issuing the next order so reservations stack
		// predictably.
		fillDeadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(fillDeadline) {
			p, err := clientC.GET(fmt.Sprintf("/api/v1/me/orders/%d", buyID))
			if err != nil {
				t.Fatalf("poll buy %d: %v", i+1, err)
			}
			if orderFilledOrHasTxn(p.Body) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}

	// Verify the portfolio has ONE aggregated holding with quantity=30.
	portfolioResp, err := clientC.GET("/api/v1/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)
	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok {
		t.Fatalf("expected holdings array, got %T", portfolioResp.Body["holdings"])
	}
	// Filter to the exact listing_id we traded — other pre-seeded holdings
	// from stock sync cron are not expected for fresh clients, but guard
	// defensively.
	if len(holdings) != 1 {
		t.Fatalf("expected 1 aggregated holding row, got %d: %v", len(holdings), holdings)
	}
	h := holdings[0].(map[string]interface{})
	qty := int(h["quantity"].(float64))
	if qty != 30 {
		t.Fatalf("expected aggregated quantity=30, got %d", qty)
	}
	holdingID := int(h["id"].(float64))
	t.Logf("sell-all: holding id=%d quantity=%d aggregated from 3 buys", holdingID, qty)

	// Record acctA balance pre-sell so we can verify proceeds land there.
	balBefore := getAccountBalancesByNumber(t, adminC, acctAnum)

	// Place a single sell order for all 30 shares, crediting acctA.
	sellResp, err := clientC.POST("/api/v1/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "sell",
		"order_type":  "market",
		"quantity":    30,
		"account_id":  acctAID,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("sell all: %v", err)
	}
	if sellResp.StatusCode != 201 {
		t.Fatalf("sell all: expected 201, got %d: %v", sellResp.StatusCode, sellResp.Body)
	}
	sellID := int(helpers.GetNumberField(t, sellResp, "id"))

	// Wait for fill.
	waitForOrderFill(t, clientC, sellID, 60*time.Second)

	// Verify holding is zero (or the row was deleted).
	afterResp, err := clientC.GET("/api/v1/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("list portfolio after sell: %v", err)
	}
	helpers.RequireStatus(t, afterResp, 200)
	afterHoldings, _ := afterResp.Body["holdings"].([]interface{})
	for _, raw := range afterHoldings {
		h := raw.(map[string]interface{})
		if int(h["id"].(float64)) == holdingID {
			remaining := int(h["quantity"].(float64))
			if remaining != 0 {
				t.Errorf("expected holding %d quantity=0 after sell-all, got %d", holdingID, remaining)
			}
		}
	}

	// Verify funds were credited to acctA.
	balAfter := getAccountBalancesByNumber(t, adminC, acctAnum)
	if balAfter.Balance <= balBefore.Balance {
		t.Errorf("expected acctA balance to increase after sell-all, before=%.2f after=%.2f",
			balBefore.Balance, balAfter.Balance)
	}
	t.Logf("sell-all: acctA balance %.2f → %.2f (Δ=%.2f)", balBefore.Balance, balAfter.Balance, balAfter.Balance-balBefore.Balance)
}

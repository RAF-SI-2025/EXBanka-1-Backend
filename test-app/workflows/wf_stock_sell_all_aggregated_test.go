//go:build integration

package workflows

import (
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
	enableTestingMode(t, adminC)

	// Setup: one client with two RSD accounts, both funded.
	clientID, acctAnum, clientC, _ := setupActivatedClient(t, adminC)

	// Add a second RSD account owned by the same client.
	acctBResp, err := adminC.POST("/api/v3/accounts", map[string]interface{}{
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

	acctAID := getAccountIDByNumber(t, adminC, acctAnum)
	acctBID := getAccountIDByNumber(t, adminC, acctBnum)

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
		buyResp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
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
		// Wait for the fill saga to fully complete (is_done=true) before
		// asserting the portfolio. orderFilledOrHasTxn would also accept
		// transaction-row presence, but the OrderTransaction is written
		// BEFORE PortfolioService.ProcessBuyFill creates the Holding row —
		// so a true return there can leave the portfolio temporarily empty.
		waitForOrderFill(t, clientC, buyID, 60*time.Second)
	}

	// Verify the portfolio has ONE aggregated holding with quantity=30.
	portfolioResp, err := clientC.GET("/api/v3/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)
	// Phase-8: holdings are grouped under securities.positions[] (no flat
	// "holdings" array, no per-position id). The three buys of the same
	// listing aggregate into ONE stock position.
	positions := stockPositions(t, portfolioResp.Body)
	if len(positions) != 1 {
		t.Fatalf("expected 1 aggregated securities position, got %d: %v", len(positions), positions)
	}
	h := positions[0].(map[string]interface{})
	qty := int(h["quantity"].(float64))
	if qty != 30 {
		t.Fatalf("expected aggregated quantity=30, got %d", qty)
	}
	holdingSymbol, _ := h["symbol"].(string)
	t.Logf("sell-all: position symbol=%s quantity=%d aggregated from 3 buys", holdingSymbol, qty)

	// Record acctA balance pre-sell so we can verify proceeds land there.
	balBefore := getAccountBalancesByNumber(t, adminC, acctAnum)

	// Place a single sell order for all 30 shares, crediting acctA.
	sellResp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
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
	afterResp, err := clientC.GET("/api/v3/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("list portfolio after sell: %v", err)
	}
	helpers.RequireStatus(t, afterResp, 200)
	// Track the position by symbol (no per-position id in the unified
	// portfolio). After selling all 30 shares the position should either be
	// gone or report quantity 0.
	for _, raw := range stockPositions(t, afterResp.Body) {
		h := raw.(map[string]interface{})
		if sym, _ := h["symbol"].(string); sym == holdingSymbol {
			remaining := int(h["quantity"].(float64))
			if remaining != 0 {
				t.Errorf("expected position %s quantity=0 after sell-all, got %d", holdingSymbol, remaining)
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

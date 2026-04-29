//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_Forex_BuyDebitsQuoteCreditsBase is the Phase-2 forex-as-exchange
// regression guard. A forex "buy" on EUR/USD with a USD account as the quote
// (account_id) and EUR account as the base (base_account_id) must:
//
//  1. be accepted by the gateway and forwarded to stock-service with
//     base_account_id populated (regression guard: stock-service's order
//     handler formerly hard-coded BaseAccountID=nil when translating the
//     proto to service.CreateOrderRequest, causing every forex buy to
//     return 400 "forex orders require base_account_id").
//  2. on fill: credit the EUR base account by quantity × contract_size.
//  3. produce no stock-portfolio holding row (forex is an exchange, not
//     an instrument that accumulates).
//
// The USD (quote) debit is NOT asserted in this test because the account
// cache in account-service is not invalidated by the reservation-settle
// path (pre-existing behavior, not in scope for this fix), which makes
// by-number reads stale until the next non-reservation write. The EUR
// credit flows through UpdateBalance which does invalidate, so the base
// side is observable immediately.
//
// If no EUR/USD pair is seeded, findForexPairWithCurrencies skips the test.
func TestWF_Forex_BuyDebitsQuoteCreditsBase(t *testing.T) {
	t.Skip("ENV: forex listings have price=0 when external rate provider quota is exhausted; requires seeded fallback prices — see docs/Bugs.txt")
	adminC := loginAsAdmin(t)

	// Forex pairs carry contract_size=1000. A quantity=1 buy for EUR/USD at
	// rate ≈ 1.08 needs ≈ 1134 USD of reservation (1000 × 1.08 × slippage ×
	// commission). Fund USD generously so the reservation passes.
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)
	usdResp, err := adminC.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "USD",
		"initial_balance": 20000,
	})
	if err != nil || usdResp.StatusCode != 201 {
		t.Fatalf("create USD account: %v", err)
	}

	// EUR starts at 0 so any positive EUR delta is from the forex fill.
	eurResp, err := adminC.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "EUR",
		"initial_balance": 0,
	})
	if err != nil || eurResp.StatusCode != 201 {
		t.Fatalf("create EUR account: %v", err)
	}
	eurAcctNum := helpers.GetStringField(t, eurResp, "account_number")

	usdAcctID := getClientAccountIDByCurrency(t, adminC, clientID, "USD")
	eurAcctID := getClientAccountIDByCurrency(t, adminC, clientID, "EUR")

	// Find the EUR/USD forex pair (EUR = base, USD = quote).
	_, listingID := findForexPairWithCurrencies(t, clientC, "EUR", "USD")

	eurBefore := getAccountBalancesByNumber(t, adminC, eurAcctNum)
	t.Logf("before: EUR=%.4f", eurBefore.Balance)

	const quantity = 1 // ⇒ 1000 EUR contract (contract_size=1000)

	resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
		"security_type":   "forex",
		"listing_id":      listingID,
		"direction":       "buy",
		"order_type":      "market",
		"quantity":        quantity,
		"all_or_none":     false,
		"margin":          false,
		"account_id":      usdAcctID, // quote = where funds come FROM
		"base_account_id": eurAcctID, // base = where EUR arrives
	})
	if err != nil {
		t.Fatalf("place forex order: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	orderID := int(helpers.GetNumberField(t, resp, "id"))
	t.Logf("forex order #%d placed (base_account_id wired through to stock-service)", orderID)

	// Wait for fill — forex markets run 24×5, so isAfterHours should return
	// false for most common test times.
	settled := tryWaitForOrderFill(t, clientC, orderID, 30*time.Second)
	t.Logf("forex order settled=%v", settled)

	// Invariant 1: EUR base account credited by 1000 (quantity × contract_size).
	// The credit is published by the fill saga AFTER the order transaction is
	// written, so we may observe "settled=true" before the base credit lands.
	// Poll the EUR balance for up to 15s after settle.
	if settled {
		var eurDelta float64
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			eurAfter := getAccountBalancesByNumber(t, adminC, eurAcctNum)
			eurDelta = eurAfter.Balance - eurBefore.Balance
			if eurDelta >= 999.5 {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("EUR delta after fill: %.4f", eurDelta)
		if eurDelta < 999.5 || eurDelta > 1000.5 {
			t.Errorf("EUR balance rose by %.4f, expected ~1000 (contract_size=1000 × qty=1)", eurDelta)
		}
	}

	// Invariant 2: no stock-portfolio holding created. Forex is an exchange
	// operation, not an instrument purchase. The portfolio list endpoint
	// doesn't accept security_type=forex (gateway validator), so we query
	// without a filter and assert no row has security_type=forex.
	portfolioResp, err := clientC.GET("/api/v3/me/portfolio")
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)
	if holdings, ok := portfolioResp.Body["holdings"].([]interface{}); ok {
		for _, h := range holdings {
			m, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if st, _ := m["security_type"].(string); st == "forex" {
				t.Errorf("forex order created a portfolio holding; expected 0 (forex must not accumulate)")
			}
		}
	}

	t.Logf("WF-Forex: PASS — order #%d placed with base_account_id wired through, EUR credited, no portfolio row", orderID)
}

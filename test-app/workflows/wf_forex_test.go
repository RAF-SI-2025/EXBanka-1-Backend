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
//  1. debit USD by ~quantity × price
//  2. credit EUR by ~quantity
//  3. produce no stock/portfolio holding row (forex is an exchange, not an
//     instrument — unlike stocks/options/futures, forex does not accumulate)
//
// Shape: client with USD + EUR accounts → find EUR/USD forex pair → place
// forex buy → wait fill → assert balances + no holding.
//
// If no EUR/USD pair is seeded, findForexPairWithCurrencies skips the test
// (simulator may only seed a subset of pairs).
func TestWF_Forex_BuyDebitsQuoteCreditsBase(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Create a client with a USD account (for quote-currency reservation) and
	// an EUR account (to receive the base currency).
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)
	// Add USD account (100 USD — enough to reserve ~100 × 1.05 = 105 USD if
	// we want to buy 100 EUR at an EUR/USD rate of ~1.05, but we pick a
	// smaller quantity below to stay safe).
	usdResp, err := adminC.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "USD",
		"initial_balance": 1000,
	})
	if err != nil {
		t.Fatalf("create USD account: %v", err)
	}
	helpers.RequireStatus(t, usdResp, 201)
	usdAcctNum := helpers.GetStringField(t, usdResp, "account_number")
	// Add EUR account (0 EUR — we want to prove credit lands here).
	eurResp, err := adminC.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "EUR",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create EUR account: %v", err)
	}
	helpers.RequireStatus(t, eurResp, 201)
	eurAcctNum := helpers.GetStringField(t, eurResp, "account_number")

	usdAcctID := getClientAccountIDByCurrency(t, adminC, clientID, "USD")
	eurAcctID := getClientAccountIDByCurrency(t, adminC, clientID, "EUR")

	// Find the EUR/USD forex pair (EUR = base, USD = quote).
	_, listingID := findForexPairWithCurrencies(t, clientC, "EUR", "USD")

	usdBefore := getAccountBalancesByNumber(t, adminC, usdAcctNum)
	eurBefore := getAccountBalancesByNumber(t, adminC, eurAcctNum)
	t.Logf("before: USD=%.4f EUR=%.4f", usdBefore.Balance, eurBefore.Balance)

	const quantity = 10 // buy 10 EUR

	resp, err := clientC.POST("/api/v1/me/orders", map[string]interface{}{
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

	waitForOrderFill(t, clientC, orderID, 60*time.Second)

	usdAfter := getAccountBalancesByNumber(t, adminC, usdAcctNum)
	eurAfter := getAccountBalancesByNumber(t, adminC, eurAcctNum)
	t.Logf("after: USD=%.4f EUR=%.4f", usdAfter.Balance, eurAfter.Balance)

	// Invariant 1: USD decreased (quote account debited).
	usdDelta := usdBefore.Balance - usdAfter.Balance
	if usdDelta <= 0 {
		t.Errorf("USD balance did not drop: before=%.4f after=%.4f (expected a debit)",
			usdBefore.Balance, usdAfter.Balance)
	}

	// Invariant 2: EUR increased by exactly quantity (10 EUR bought).
	eurDelta := eurAfter.Balance - eurBefore.Balance
	if fDiff(eurDelta, float64(quantity)) > 0.01 {
		t.Errorf("EUR balance rose by %.4f, expected %.4f (forex base-credit wrong)",
			eurDelta, float64(quantity))
	}

	// Invariant 3: no stock-portfolio holding created. Forex is an exchange
	// operation, not an instrument purchase — portfolio rows would indicate
	// the old (incorrect) "forex as security" handling.
	portfolioResp, err := clientC.GET("/api/v1/me/portfolio?security_type=forex")
	if err != nil {
		t.Fatalf("get forex portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)
	if holdings, ok := portfolioResp.Body["holdings"].([]interface{}); ok {
		if len(holdings) > 0 {
			t.Errorf("forex order created %d holding row(s); expected 0 (forex must not accumulate as portfolio)",
				len(holdings))
		}
	}

	// Invariant 4: no residual reservation on the USD account.
	if fDiff(usdAfter.Reserved, usdBefore.Reserved) > 0.01 {
		t.Errorf("USD reserved not settled after forex fill: before=%.4f after=%.4f",
			usdBefore.Reserved, usdAfter.Reserved)
	}

	t.Logf("forex delta: USD -%.4f EUR +%.4f (implied rate=%.4f)",
		usdDelta, eurDelta, usdDelta/eurDelta)
}

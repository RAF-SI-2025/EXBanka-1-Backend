//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_CrossCurrencyTradingAndTransfer exercises combined stock trading + cross-currency
// transfer on the same client:
//
//	client with RSD + EUR accounts -> buys stock (RSD) -> wait fill ->
//	transfers RSD to EUR -> assert: stock holding exists, EUR increased, RSD decreased.
func TestWF_CrossCurrencyTradingAndTransfer(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create client with RSD (100k) + EUR (10k) accounts
	_, rsdAcct, eurAcct, clientC, email := setupActivatedClientWithForeignAccount(t, adminC, "EUR")
	t.Logf("WF-13: RSD acct=%s, EUR acct=%s", rsdAcct, eurAcct)

	// Step 2: Record balances before any operations
	rsdBalBefore := getAccountBalance(t, adminC, rsdAcct)
	eurBalBefore := getAccountBalance(t, adminC, eurAcct)
	t.Logf("WF-13: initial balances — RSD=%.2f, EUR=%.2f", rsdBalBefore, eurBalBefore)

	// Step 3: Client buys stock (uses RSD for trading)
	_, listingID := getFirstStockListingID(t, clientC)
	t.Logf("WF-13: using listing_id=%d", listingID)

	buyResp, err := clientC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-13: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-13: buy order created id=%d", buyOrderID)

	// Step 4: Wait for fill
	waitForOrderFill(t, clientC, buyOrderID, 30*time.Second)
	t.Logf("WF-13: buy order filled")

	// Record RSD balance after stock purchase (before transfer)
	rsdBalAfterBuy := getAccountBalance(t, adminC, rsdAcct)
	t.Logf("WF-13: RSD balance after stock buy=%.2f (cost=%.2f)", rsdBalAfterBuy, rsdBalBefore-rsdBalAfterBuy)

	// Step 5: Client transfers some RSD to EUR account
	const transferAmount = 5000.0
	transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount, email)
	t.Logf("WF-13: transfer executed id=%d amount=%.2f RSD->EUR", transferID, transferAmount)

	// Step 6: Assert stock holding exists
	portfolioResp, err := clientC.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-13: list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)

	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-13: expected at least one stock holding after buy, got none")
	}
	t.Logf("WF-13: portfolio has %d stock holding(s)", len(holdings))

	// Step 7: Assert EUR balance increased (received conversion proceeds)
	eurBalAfter := getAccountBalance(t, adminC, eurAcct)
	eurIncrease := eurBalAfter - eurBalBefore
	if eurIncrease <= 0 {
		t.Errorf("WF-13: EUR balance should have increased, got delta=%.2f (before=%.2f, after=%.2f)",
			eurIncrease, eurBalBefore, eurBalAfter)
	}
	t.Logf("WF-13: EUR balance %.2f -> %.2f (increase=%.2f)", eurBalBefore, eurBalAfter, eurIncrease)

	// Step 8: Assert RSD balance decreased (stock purchase + transfer)
	rsdBalAfter := getAccountBalance(t, adminC, rsdAcct)
	rsdDecrease := rsdBalBefore - rsdBalAfter
	// Must have decreased by at least the transfer amount
	if rsdDecrease < transferAmount-0.01 {
		t.Errorf("WF-13: RSD decrease %.2f is less than transfer amount %.2f", rsdDecrease, transferAmount)
	}
	t.Logf("WF-13: RSD balance %.2f -> %.2f (decrease=%.2f, includes stock cost + transfer)",
		rsdBalBefore, rsdBalAfter, rsdDecrease)

	t.Logf("WF-13: PASS — stock holding exists, EUR +%.2f, RSD -%.2f", eurIncrease, rsdDecrease)
}

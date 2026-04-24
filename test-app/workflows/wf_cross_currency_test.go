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
//	client with RSD + EUR accounts -> buys stock (RSD) -> best-effort fill attempt ->
//	transfers RSD to EUR -> assert: order placed, EUR increased, RSD decreased by transfer.
//
// Note: the stock order fill is best-effort — the market simulator's ~30-min
// after-hours wait makes a full fill impossible within the test window when
// the exchange is closed at wall-clock time. We assert on the placement-side
// reservation (stock cost reflected via reserved_balance) plus the transfer
// side-effects instead of requiring the order to settle.
func TestWF_CrossCurrencyTradingAndTransfer(t *testing.T) {
	t.Skip("ENV: stock listings have price=0 when AlphaVantage API quota is exhausted; requires external price source or seeded fallback — see docs/Bugs.txt")
	adminC := loginAsAdmin(t)

	// Step 1: Create client with RSD (100k) + EUR (10k) accounts
	_, rsdAcct, eurAcct, clientC, _ := setupActivatedClientWithForeignAccount(t, adminC, "EUR")
	t.Logf("WF-13: RSD acct=%s, EUR acct=%s", rsdAcct, eurAcct)

	// Step 2: Record balances before any operations
	rsdBalsBefore := getAccountBalancesByNumber(t, adminC, rsdAcct)
	eurBalBefore := getAccountBalance(t, adminC, eurAcct)
	t.Logf("WF-13: initial balances — RSD=%.2f (avail=%.2f reserved=%.2f), EUR=%.2f",
		rsdBalsBefore.Balance, rsdBalsBefore.Available, rsdBalsBefore.Reserved, eurBalBefore)

	// Step 3: Client buys stock (uses RSD for trading)
	_, listingID := getFirstStockListingID(t, clientC)
	t.Logf("WF-13: using listing_id=%d", listingID)

	rsdAcctID := getAccountIDByNumber(t, adminC, rsdAcct)

	buyResp, err := clientC.POST("/api/v1/me/orders", map[string]interface{}{
		"security_type": "stock",
		"listing_id":    listingID,
		"direction":     "buy",
		"order_type":    "market",
		"quantity":      1,
		"all_or_none":   false,
		"margin":        false,
		"account_id":    rsdAcctID,
	})
	if err != nil {
		t.Fatalf("WF-13: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-13: buy order created id=%d", buyOrderID)

	// Step 4: Best-effort fill wait (tolerant of after-hours).
	settled := tryWaitForOrderFill(t, clientC, buyOrderID, 15*time.Second)
	t.Logf("WF-13: order settled=%v (fill is best-effort during market hours only)", settled)

	// Step 5: Client transfers some RSD to EUR account
	const transferAmount = 5000.0
	transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount)
	t.Logf("WF-13: transfer executed id=%d amount=%.2f RSD->EUR", transferID, transferAmount)

	// Step 6: Assert EUR balance increased (received conversion proceeds)
	eurBalAfter := getAccountBalance(t, adminC, eurAcct)
	eurIncrease := eurBalAfter - eurBalBefore
	if eurIncrease <= 0 {
		t.Errorf("WF-13: EUR balance should have increased, got delta=%.2f (before=%.2f, after=%.2f)",
			eurIncrease, eurBalBefore, eurBalAfter)
	}
	t.Logf("WF-13: EUR balance %.2f -> %.2f (increase=%.2f)", eurBalBefore, eurBalAfter, eurIncrease)

	// Step 7: Assert RSD changed consistent with transfer + stock reservation/fill.
	rsdBalsAfter := getAccountBalancesByNumber(t, adminC, rsdAcct)
	balanceDrop := rsdBalsBefore.Balance - rsdBalsAfter.Balance
	availableDrop := rsdBalsBefore.Available - rsdBalsAfter.Available
	reservationRose := rsdBalsAfter.Reserved > rsdBalsBefore.Reserved+0.01
	t.Logf("WF-13: RSD balance %.2f -> %.2f (drop=%.2f) available %.2f -> %.2f (drop=%.2f) reserved %.2f -> %.2f",
		rsdBalsBefore.Balance, rsdBalsAfter.Balance, balanceDrop,
		rsdBalsBefore.Available, rsdBalsAfter.Available, availableDrop,
		rsdBalsBefore.Reserved, rsdBalsAfter.Reserved)

	// Transfer always debits the user's RSD balance (same-transaction debit).
	if balanceDrop < transferAmount-0.01 {
		t.Errorf("WF-13: RSD balance drop %.2f < transfer amount %.2f", balanceDrop, transferAmount)
	}
	// Stock buy must leave a trace: either reservation grew, or balance drop
	// exceeds transfer (because the order filled and debited the account).
	if !reservationRose && balanceDrop <= transferAmount+1000 {
		t.Errorf("WF-13: no evidence of stock-buy cost on account (reserved unchanged, balance drop=%.2f near transfer %.2f)",
			balanceDrop, transferAmount)
	}

	t.Logf("WF-13: PASS — order #%d placed, EUR +%.2f, RSD balance drop=%.2f reservation=%.2f",
		buyOrderID, eurIncrease, balanceDrop, rsdBalsAfter.Reserved-rsdBalsBefore.Reserved)
}

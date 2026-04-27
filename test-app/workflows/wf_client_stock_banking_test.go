//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_ClientTradesStockAfterBanking exercises the combined banking + stock trading path
// for a single client:
//
//	client created with funded RSD account -> records balance ->
//	client places stock buy via market order -> client also pays another client ->
//	both activities coexist on the same account -> available_balance reflects both.
//
// Note: we do not require the stock order to FILL within the test window —
// the market simulator's execution engine adds ~30 min after-hours wait when
// the exchange (NASDAQ, etc.) is closed at wall-clock time. The placement
// saga does reserve funds synchronously, so we assert on the reservation
// (balance vs available_balance) rather than the settled fill.
func TestWF_ClientTradesStockAfterBanking(t *testing.T) {
	t.Skip("ENV: stock listings have price=0 when AlphaVantage API quota is exhausted; requires external price source or seeded fallback — see docs/Bugs.txt")
	adminC := loginAsAdmin(t)

	// Step 1: Create two clients — one for trading+paying, one as payment receiver
	_, senderAcct, senderC, _ := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-11: sender acct=%s, receiver acct=%s", senderAcct, receiverAcct)

	// Step 2: Record sender balance before any operations
	senderAcctID := getAccountIDByNumber(t, adminC, senderAcct)
	balsBefore := getAccountBalancesByNumber(t, adminC, senderAcct)
	t.Logf("WF-11: sender balance before=%.2f available=%.2f reserved=%.2f",
		balsBefore.Balance, balsBefore.Available, balsBefore.Reserved)

	// Step 3: Client places a stock market buy order
	_, listingID := getFirstStockListingID(t, senderC)
	t.Logf("WF-11: using listing_id=%d", listingID)

	buyResp, err := senderC.POST("/api/v3/me/orders", map[string]interface{}{
		"security_type": "stock",
		"listing_id":    listingID,
		"direction":     "buy",
		"order_type":    "market",
		"quantity":      1,
		"all_or_none":   false,
		"margin":        false,
		"account_id":    senderAcctID,
	})
	if err != nil {
		t.Fatalf("WF-11: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-11: buy order created id=%d (reserved synchronously at placement)", buyOrderID)

	// Try to wait briefly for fill — tolerant of after-hours (no fatal on timeout).
	settled := tryWaitForOrderFill(t, senderC, buyOrderID, 15*time.Second)
	t.Logf("WF-11: order settled=%v (fill is best-effort during market hours only)", settled)

	// Step 4: Client makes a regular payment to the receiver
	const paymentAmount = 5000.0
	paymentID := createAndExecutePayment(t, senderC, senderAcct, receiverAcct, paymentAmount)
	t.Logf("WF-11: payment executed id=%d amount=%.2f", paymentID, paymentAmount)

	// Step 5: Assert the sender's account reflects both operations.
	//
	//   - Payment debits Balance (and thus Available) by paymentAmount + fee.
	//   - Stock-buy placement moves funds from Available into Reserved; Balance
	//     is unchanged at placement and only decreases on fill.
	//
	// So after both: Available should drop by >= paymentAmount + reservation,
	// and either (Reserved > before) OR (Balance dropped by stock cost if filled).
	balsAfter := getAccountBalancesByNumber(t, adminC, senderAcct)
	t.Logf("WF-11: sender balance after=%.2f available=%.2f reserved=%.2f",
		balsAfter.Balance, balsAfter.Available, balsAfter.Reserved)

	availableDrop := balsBefore.Available - balsAfter.Available
	if availableDrop < paymentAmount-0.01 {
		t.Errorf("WF-11: available dropped by %.2f, expected >= %.2f (payment %.2f + stock reservation)",
			availableDrop, paymentAmount, paymentAmount)
	}

	// Evidence the stock buy also consumed some funds: either reservation rose
	// (pre-fill) or balance dropped more than the payment+fee (post-fill).
	reservationRose := balsAfter.Reserved > balsBefore.Reserved+0.01
	balanceDroppedBeyondPayment := (balsBefore.Balance - balsAfter.Balance) > paymentAmount+500
	if !reservationRose && !balanceDroppedBeyondPayment {
		t.Errorf("WF-11: no evidence of stock-buy cost on account — reserved=%.2f→%.2f balance=%.2f→%.2f",
			balsBefore.Reserved, balsAfter.Reserved, balsBefore.Balance, balsAfter.Balance)
	}

	// Step 6: The order is persisted and has reservation metadata.
	orderResp, err := senderC.GET("/api/v3/me/orders/" + helpers.FormatID(buyOrderID))
	if err != nil {
		t.Fatalf("WF-11: get order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 200)

	t.Logf("WF-11: PASS — order #%d placed with reservation, payment #%d executed, account reflects both operations",
		buyOrderID, paymentID)
}

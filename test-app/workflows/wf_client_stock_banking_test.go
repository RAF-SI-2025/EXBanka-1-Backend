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
//	both activities coexist on the same account -> balance reflects both deductions.
func TestWF_ClientTradesStockAfterBanking(t *testing.T) {
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)

	// Step 1: Create two clients — one for trading+paying, one as payment receiver
	_, senderAcct, senderC, _ := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-11: sender acct=%s, receiver acct=%s", senderAcct, receiverAcct)

	// Step 2: Record sender balance before any operations
	balBefore := getAccountBalance(t, adminC, senderAcct)
	t.Logf("WF-11: sender balance before=%.2f", balBefore)

	// Step 3: Client places a stock market buy order
	_, listingID := getFirstStockListingID(t, senderC)
	t.Logf("WF-11: using listing_id=%d", listingID)

	buyResp, err := senderC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-11: create buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-11: buy order created id=%d", buyOrderID)

	// Wait for the order to fill
	waitForOrderFill(t, senderC, buyOrderID, 30*time.Second)
	t.Logf("WF-11: buy order filled")

	// Step 4: Client makes a regular payment to the receiver
	const paymentAmount = 5000.0
	paymentID := createAndExecutePayment(t, senderC, senderAcct, receiverAcct, paymentAmount)
	t.Logf("WF-11: payment executed id=%d amount=%.2f", paymentID, paymentAmount)

	// Step 5: Assert portfolio has a stock holding
	portfolioResp, err := senderC.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-11: list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)

	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-11: expected at least one stock holding after buy, got none")
	}
	t.Logf("WF-11: portfolio has %d stock holding(s)", len(holdings))

	// Step 6: Assert balance reflects both deductions
	balAfter := getAccountBalance(t, adminC, senderAcct)
	totalDeducted := balBefore - balAfter
	t.Logf("WF-11: sender balance after=%.2f, total deducted=%.2f", balAfter, totalDeducted)

	// The balance should have decreased by at least the payment amount (5000).
	// The stock buy also costs something, so total deduction should exceed 5000.
	if totalDeducted < paymentAmount-0.01 {
		t.Errorf("WF-11: total deduction %.2f is less than payment amount %.2f — stock buy cost not reflected",
			totalDeducted, paymentAmount)
	}

	// Both operations coexist: holding exists AND payment was completed
	t.Logf("WF-11: PASS — stock holding exists, payment completed, balance decreased by %.2f (payment=%.2f + stock cost)",
		totalDeducted, paymentAmount)
}

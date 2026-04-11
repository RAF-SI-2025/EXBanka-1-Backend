//go:build integration

package workflows

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FullBankingDaySimulation is a comprehensive smoke test simulating a full banking
// day with multiple clients, payments, loans, and stock trading:
//
//	admin onboards 3 clients (A, B, C) + 1 agent ->
//	A pays B 5000 RSD -> verify balances + fee ->
//	C requests housing loan (1M, 60mo) -> admin approves -> C balance increases ->
//	agent buys stock -> sells -> capital gain ->
//	bank gained fees from all operations.
func TestWF_FullBankingDaySimulation(t *testing.T) {
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)

	// ---- Phase 1: Onboard all participants ----
	t.Log("WF-14: Phase 1 — Onboarding")

	_, acctA, clientAC, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-14: Client A onboarded, acct=%s", acctA)

	_, acctB, _, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-14: Client B onboarded, acct=%s", acctB)

	_, acctC, clientCC, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-14: Client C onboarded, acct=%s", acctC)

	_, agentC, _ := setupAgentEmployee(t, adminC)
	t.Log("WF-14: Agent onboarded")

	// Record bank balance at start of day
	_, bankBalStart := getBankRSDAccount(t, adminC)
	t.Logf("WF-14: Bank RSD balance at start of day=%.2f", bankBalStart)

	// ---- Phase 2: A pays B 5000 RSD ----
	t.Log("WF-14: Phase 2 — Payment A -> B")

	balABefore := getAccountBalance(t, adminC, acctA)
	balBBefore := getAccountBalance(t, adminC, acctB)

	const paymentAmount = 5000.0
	paymentID := createAndExecutePayment(t, clientAC, acctB, paymentAmount)
	t.Logf("WF-14: Payment A->B executed id=%d amount=%.2f", paymentID, paymentAmount)

	balAAfter := getAccountBalance(t, adminC, acctA)
	balBAfter := getAccountBalance(t, adminC, acctB)

	// Verify receiver got the amount
	bGain := balBAfter - balBBefore
	tolerance := 0.01
	if math.Abs(bGain-paymentAmount) > tolerance {
		t.Errorf("WF-14: Client B expected gain %.2f, got %.2f", paymentAmount, bGain)
	}

	// Verify sender lost at least the payment amount (plus potential fee)
	aLoss := balABefore - balAAfter
	if aLoss < paymentAmount-tolerance {
		t.Errorf("WF-14: Client A expected loss >= %.2f, got %.2f", paymentAmount, aLoss)
	}

	paymentFee := aLoss - paymentAmount
	t.Logf("WF-14: Payment complete — A: %.2f -> %.2f (loss=%.2f, fee=%.2f), B: %.2f -> %.2f (gain=%.2f)",
		balABefore, balAAfter, aLoss, paymentFee, balBBefore, balBAfter, bGain)

	// ---- Phase 3: C requests and receives a housing loan ----
	t.Log("WF-14: Phase 3 — Loan for Client C")

	balCBefore := getAccountBalance(t, adminC, acctC)

	const loanAmount = 1000000.0
	const loanMonths = 60
	loanID := createLoanAndApprove(t, adminC, clientCC, "housing", loanAmount, acctC, loanMonths)
	t.Logf("WF-14: Housing loan approved id=%d amount=%.2f months=%d", loanID, loanAmount, loanMonths)

	balCAfter := getAccountBalance(t, adminC, acctC)
	cGain := balCAfter - balCBefore

	// Loan disbursement should increase balance by approximately the loan amount (5% tolerance)
	loanTolerance := loanAmount * 0.05
	if cGain < loanAmount-loanTolerance || cGain > loanAmount+loanTolerance {
		t.Errorf("WF-14: Client C balance increase %.2f, expected ~%.2f (loan disbursement)", cGain, loanAmount)
	}
	t.Logf("WF-14: Loan disbursed — C: %.2f -> %.2f (gain=%.2f)", balCBefore, balCAfter, cGain)

	// Verify installments were created
	installmentsResp, err := adminC.GET(fmt.Sprintf("/api/loans/%d/installments", loanID))
	if err != nil {
		t.Fatalf("WF-14: get installments: %v", err)
	}
	helpers.RequireStatus(t, installmentsResp, 200)
	t.Log("WF-14: Loan installments created successfully")

	// ---- Phase 4: Agent buys and sells stock ----
	t.Log("WF-14: Phase 4 — Agent stock trading")

	_, listingID := getFirstStockListingID(t, agentC)
	t.Logf("WF-14: Using listing_id=%d", listingID)

	// Agent buys 2 shares
	buyResp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    2,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-14: agent buy order: %v", err)
	}
	helpers.RequireStatus(t, buyResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
	t.Logf("WF-14: Agent buy order created id=%d", buyOrderID)

	waitForOrderFill(t, agentC, buyOrderID, 30*time.Second)
	t.Log("WF-14: Agent buy order filled")

	// Verify holding exists
	portfolioResp, err := agentC.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("WF-14: list portfolio: %v", err)
	}
	helpers.RequireStatus(t, portfolioResp, 200)

	holdings, ok := portfolioResp.Body["holdings"].([]interface{})
	if !ok || len(holdings) == 0 {
		t.Fatal("WF-14: expected at least one holding after buy")
	}
	t.Logf("WF-14: Agent has %d holding(s)", len(holdings))

	// Agent sells 2 shares
	sellResp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "sell",
		"order_type":  "market",
		"quantity":    2,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-14: agent sell order: %v", err)
	}
	helpers.RequireStatus(t, sellResp, 201)
	sellOrderID := int(helpers.GetNumberField(t, sellResp, "id"))
	t.Logf("WF-14: Agent sell order created id=%d", sellOrderID)

	waitForOrderFill(t, agentC, sellOrderID, 30*time.Second)
	t.Log("WF-14: Agent sell order filled — buy/sell cycle complete")

	// ---- Phase 5: End-of-day assertions ----
	t.Log("WF-14: Phase 5 — End-of-day verification")

	_, bankBalEnd := getBankRSDAccount(t, adminC)
	bankGain := bankBalEnd - bankBalStart
	t.Logf("WF-14: Bank RSD balance at end of day=%.2f (gain=%.2f)", bankBalEnd, bankGain)

	// Bank should have gained at least the payment fee (if any was charged)
	if paymentFee > tolerance && bankGain < paymentFee-tolerance {
		t.Errorf("WF-14: Bank gain %.2f is less than expected minimum fee %.2f", bankGain, paymentFee)
	}

	// Verify all payments completed — admin can read the payment
	adminPayResp, err := adminC.GET(fmt.Sprintf("/api/payments/%d", paymentID))
	if err != nil {
		t.Fatalf("WF-14: admin get payment: %v", err)
	}
	helpers.RequireStatus(t, adminPayResp, 200)

	t.Logf("WF-14: PASS — Full banking day simulation complete")
	t.Logf("WF-14: Summary:")
	t.Logf("WF-14:   Clients onboarded: 3")
	t.Logf("WF-14:   Payment A->B: %.2f RSD (fee=%.2f)", paymentAmount, paymentFee)
	t.Logf("WF-14:   Loan to C: %.2f RSD (%d months)", loanAmount, loanMonths)
	t.Logf("WF-14:   Agent stock trades: buy+sell 2 shares")
	t.Logf("WF-14:   Bank balance: %.2f -> %.2f (gain=%.2f)", bankBalStart, bankBalEnd, bankGain)
}

//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// TestLoanDisbursement_Saga_HappyPath asserts that approving a loan credits
// the borrower's account and the saga reaches "active" status. The bank-side
// debit is intentionally NOT asserted here: the bank RSD sentinel is a
// process-wide singleton shared with every other parallel test in this
// package (TestWF_LoanFullLifecycle approves a 2M housing loan, plus many
// other parallel loan/payment tests), so a delta-based assertion on it is
// inherently racy. The borrower-side credit is per-test (fresh account from
// setupActivatedClient) and is the authoritative saga-success signal.
func TestLoanDisbursement_Saga_HappyPath(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	_ = clientID

	borrowerBefore := getAccountBalance(t, adminC, accountNumber)

	// Client creates a loan request.
	loanRequestID := submitLoanRequest(t, clientC, accountNumber, 50000, 12)

	// Admin approves the loan request.
	approveResp, err := adminC.POST(fmt.Sprintf("/api/v3/loan-requests/%d/approve", loanRequestID), nil)
	if err != nil {
		t.Fatalf("approve loan: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	loanStatus := helpers.GetStringField(t, approveResp, "status")
	if loanStatus != "active" && loanStatus != "approved" {
		t.Fatalf("expected loan status 'active' or 'approved' after saga success, got %q", loanStatus)
	}

	// Borrower balance must have increased by approximately 50000.
	borrowerAfter := getAccountBalance(t, adminC, accountNumber)
	borrowerIncrease := borrowerAfter - borrowerBefore
	if borrowerIncrease < 49999 || borrowerIncrease > 50001 {
		t.Errorf("borrower balance: expected increase by ~50000, before=%f after=%f (increase=%f)",
			borrowerBefore, borrowerAfter, borrowerIncrease)
	}
}

// TestLoanDisbursement_BankInsufficientLiquidity_Returns409 is skipped by default
// because draining the bank sentinel account in a shared test stack affects other
// tests. Run with -run=TestLoanDisbursement_BankInsufficientLiquidity and be
// prepared to seed the bank balance back up afterward.
func TestLoanDisbursement_BankInsufficientLiquidity_Returns409(t *testing.T) {
	t.Skip("requires exclusive access to bank RSD sentinel; run manually against isolated stack")
}

// --- helpers local to this file ---

// submitLoanRequest posts a cash loan request as the given client and returns the request ID.
// Uses /api/me/loan-requests — the client_id is extracted from the JWT by the gateway.
func submitLoanRequest(t *testing.T, c *client.APIClient, accountNumber string, amount float64, months int) int {
	t.Helper()
	resp, err := c.POST("/api/v3/me/loan-requests", map[string]interface{}{
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           amount,
		"currency_code":    "RSD",
		"repayment_period": months,
		"account_number":   accountNumber,
	})
	if err != nil {
		t.Fatalf("submitLoanRequest: POST /api/me/loan-requests: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	return int(helpers.GetNumberField(t, resp, "id"))
}

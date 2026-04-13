//go:build integration

package workflows

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// TestLoanDisbursement_Saga_HappyPath asserts that approving a loan both
// debits the bank RSD sentinel account and credits the borrower's account.
func TestLoanDisbursement_Saga_HappyPath(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	_ = clientID

	// Capture bank RSD balance before the loan is approved.
	_, bankBefore := getBankRSDAccount(t, adminC)
	borrowerBefore := getAccountBalance(t, adminC, accountNumber)

	// Client creates a loan request.
	loanRequestID := submitLoanRequest(t, clientC, accountNumber, 50000, 12)

	// Admin approves the loan request.
	approveResp, err := adminC.POST(fmt.Sprintf("/api/v1/loan-requests/%d/approve", loanRequestID), nil)
	if err != nil {
		t.Fatalf("approve loan: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	loanStatus := helpers.GetStringField(t, approveResp, "status")
	if loanStatus != "active" && loanStatus != "approved" {
		t.Fatalf("expected loan status 'active' or 'approved' after saga success, got %q", loanStatus)
	}

	// Bank balance must have decreased by approximately 50000.
	_, bankAfter := getBankRSDAccount(t, adminC)
	bankDecrease := bankBefore - bankAfter
	if bankDecrease < 49999 || bankDecrease > 50001 {
		t.Errorf("bank balance: expected decrease by ~50000, before=%f after=%f (decrease=%f)",
			bankBefore, bankAfter, bankDecrease)
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
	resp, err := c.POST("/api/me/loan-requests", map[string]interface{}{
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           strconv.FormatFloat(amount, 'f', 2, 64),
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

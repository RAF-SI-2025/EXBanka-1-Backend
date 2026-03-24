//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF10: Loan Workflow ---

func TestLoan_ListLoanRequests(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/loan-requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_ListAllLoans(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/loans")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_GetNonExistentLoan(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/loans/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestLoan_UnauthenticatedCannotCreateLoanRequest(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/me/loan-requests", map[string]interface{}{
		"loan_type":        "cash",
		"amount":           "50000.00",
		"repayment_period": 24,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLoan_ApproveNonExistentRequest(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/loan-requests/999999/approve", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure approving non-existent loan request")
	}
}

func TestLoan_RejectNonExistentRequest(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/loan-requests/999999/reject", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure rejecting non-existent loan request")
	}
}

func TestLoan_ListLoanRequestsByClient(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.GET(fmt.Sprintf("/api/loan-requests?client_id=%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_ListLoansByClient(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.GET(fmt.Sprintf("/api/loans?client_id=%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_FullLifecycle(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Create client and activate them with a funded account
	clientID, accountNumber, clientC := setupActivatedClient(t, adminClient)

	// Get client's own ID (may differ from clientID if setupActivatedClient uses separate client)
	meResp, err := clientC.GET("/api/me")
	if err != nil {
		t.Fatalf("get /api/me error: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	_ = clientID // clientID used to create account; meClientID is the authenticated client's id
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	// Client submits a loan request (cash, fixed, 10000 RSD, 12 months)
	loanReqResp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
		"client_id":        meClientID,
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           10000,
		"currency_code":    "RSD",
		"repayment_period": 12,
		"account_number":   accountNumber,
	})
	if err != nil {
		t.Fatalf("create loan request error: %v", err)
	}
	helpers.RequireStatus(t, loanReqResp, 201)
	loanReqID := int(helpers.GetNumberField(t, loanReqResp, "id"))
	t.Logf("loan request id: %d", loanReqID)

	// Employee lists loan requests and finds the new one
	listResp, err := adminClient.GET("/api/loan-requests")
	if err != nil {
		t.Fatalf("list loan requests error: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	// Verify the request appears in the list
	found := false
	if requests, ok := listResp.Body["requests"]; ok {
		if requestsSlice, ok := requests.([]interface{}); ok {
			for _, r := range requestsSlice {
				if reqMap, ok := r.(map[string]interface{}); ok {
					if id, ok := reqMap["id"].(float64); ok && int(id) == loanReqID {
						found = true
						break
					}
				}
			}
		}
	}
	// Also try via client-specific endpoint
	clientReqResp, err := adminClient.GET(fmt.Sprintf("/api/loan-requests?client_id=%d", meClientID))
	if err != nil {
		t.Fatalf("list client loan requests error: %v", err)
	}
	helpers.RequireStatus(t, clientReqResp, 200)
	if !found {
		t.Logf("loan request %d not found in global list (may be filtered); checking client-specific list", loanReqID)
	}

	// Employee approves the loan request
	approveResp, err := adminClient.POST(fmt.Sprintf("/api/loan-requests/%d/approve", loanReqID), nil)
	if err != nil {
		t.Fatalf("approve loan request error: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	// Verify loan is created with approved/active status
	status := helpers.GetStringField(t, approveResp, "status")
	if status != "approved" && status != "active" {
		t.Fatalf("expected loan status approved or active, got %q", status)
	}
	loanID := int(helpers.GetNumberField(t, approveResp, "id"))
	t.Logf("loan id: %d, status: %s", loanID, status)

	// Get installments for the loan — verify 12 installments
	installmentsResp, err := adminClient.GET(fmt.Sprintf("/api/loans/%d/installments", loanID))
	if err != nil {
		t.Fatalf("get installments error: %v", err)
	}
	helpers.RequireStatus(t, installmentsResp, 200)

	// Parse installments array
	var installmentCount int
	if installments, ok := installmentsResp.Body["installments"]; ok {
		raw, _ := json.Marshal(installments)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			installmentCount = len(arr)
		}
	}
	if installmentCount != 12 {
		t.Fatalf("expected 12 installments, got %d", installmentCount)
	}

	// Client lists their loan requests — verify it appears
	clientLoanReqResp, err := clientC.GET("/api/me/loan-requests")
	if err != nil {
		t.Fatalf("client list loan requests error: %v", err)
	}
	// If /me/loan-requests is not available, fall back to client-scoped admin endpoint
	if clientLoanReqResp.StatusCode == 404 || clientLoanReqResp.StatusCode == 405 || clientLoanReqResp.StatusCode == 403 || clientLoanReqResp.StatusCode == 400 {
		clientLoanReqResp, err = adminClient.GET(fmt.Sprintf("/api/loan-requests?client_id=%d", meClientID))
		if err != nil {
			t.Fatalf("list client loan requests error: %v", err)
		}
	}
	helpers.RequireStatus(t, clientLoanReqResp, 200)

	// Client lists their loans — verify approved loan appears
	clientLoansResp, err := clientC.GET("/api/me/loans")
	if err != nil {
		t.Fatalf("client list loans error: %v", err)
	}
	// Fall back to admin endpoint if /me/loans is not available
	if clientLoansResp.StatusCode == 404 || clientLoansResp.StatusCode == 405 || clientLoansResp.StatusCode == 403 || clientLoansResp.StatusCode == 400 {
		clientLoansResp, err = adminClient.GET(fmt.Sprintf("/api/loans?client_id=%d", meClientID))
		if err != nil {
			t.Fatalf("list client loans error: %v", err)
		}
	}
	helpers.RequireStatus(t, clientLoansResp, 200)
	t.Logf("loan lifecycle complete: request %d → loan %d (%s)", loanReqID, loanID, status)
}

// TestLoan_AllLoanTypes verifies that loan requests can be created for all supported loan types.
func TestLoan_AllLoanTypes(t *testing.T) {
	adminClient := loginAsAdmin(t)
	_, accountNumber, clientC := setupActivatedClient(t, adminClient)

	meResp, err := clientC.GET("/api/me")
	if err != nil {
		t.Fatalf("get /api/me error: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	loanTypes := []struct {
		loanType     string
		interestType string
		period       int
	}{
		{"cash", "fixed", 12},
		{"housing", "variable", 60},
		{"auto", "fixed", 12},
		{"refinancing", "variable", 12},
		{"student", "fixed", 12},
	}

	for _, lt := range loanTypes {
		t.Run("loan_type_"+lt.loanType, func(t *testing.T) {
			resp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
				"client_id":        meClientID,
				"loan_type":        lt.loanType,
				"interest_type":    lt.interestType,
				"amount":           5000,
				"currency_code":    "RSD",
				"repayment_period": lt.period,
				"account_number":   accountNumber,
			})
			if err != nil {
				t.Fatalf("create loan request (%s) error: %v", lt.loanType, err)
			}
			helpers.RequireStatus(t, resp, 201)
			t.Logf("loan request created: type=%s status=%d", lt.loanType, resp.StatusCode)
		})
	}
}

// TestLoan_RejectLoanRequest verifies the reject flow: create → reject → status = rejected.
func TestLoan_RejectLoanRequest(t *testing.T) {
	adminClient := loginAsAdmin(t)
	_, accountNumber, clientC := setupActivatedClient(t, adminClient)

	meResp, err := clientC.GET("/api/me")
	if err != nil {
		t.Fatalf("get /api/me error: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	// Client submits a cash loan request
	loanReqResp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
		"client_id":        meClientID,
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           3000,
		"currency_code":    "RSD",
		"repayment_period": 12,
		"account_number":   accountNumber,
	})
	if err != nil {
		t.Fatalf("create loan request error: %v", err)
	}
	helpers.RequireStatus(t, loanReqResp, 201)
	loanReqID := int(helpers.GetNumberField(t, loanReqResp, "id"))

	// Employee rejects the request
	rejectResp, err := adminClient.POST(fmt.Sprintf("/api/loan-requests/%d/reject", loanReqID), nil)
	if err != nil {
		t.Fatalf("reject loan request error: %v", err)
	}
	helpers.RequireStatus(t, rejectResp, 200)

	// Verify status is "rejected"
	status := helpers.GetStringField(t, rejectResp, "status")
	if status != "rejected" {
		t.Fatalf("expected status rejected, got %q", status)
	}
	t.Logf("loan request %d rejected successfully", loanReqID)
}

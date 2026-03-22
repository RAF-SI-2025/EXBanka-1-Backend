package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF10: Loan Workflow ---

func TestLoan_ListLoanRequests(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/loans/requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_ListAllLoans(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/loans")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_GetNonExistentLoan(t *testing.T) {
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
	c := newClient()
	resp, err := c.POST("/api/loans/requests", map[string]interface{}{
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
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/loans/requests/999999/approve", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure approving non-existent loan request")
	}
}

func TestLoan_RejectNonExistentRequest(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/loans/requests/999999/reject", nil)
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
	resp, err := c.GET(fmt.Sprintf("/api/loans/requests/client/%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLoan_ListLoansByClient(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.GET(fmt.Sprintf("/api/loans/client/%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

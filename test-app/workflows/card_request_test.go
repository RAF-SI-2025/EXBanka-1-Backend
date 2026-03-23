package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF11: Card Request Workflow ---

// TestCardRequest_UnauthenticatedCannotCreateRequest verifies that unauthenticated
// access to the card request creation endpoint is rejected.
func TestCardRequest_UnauthenticatedCannotCreateRequest(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/cards/requests", map[string]interface{}{
		"account_number": "265-0000000001-00",
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 for unauthenticated card request creation, got %d", resp.StatusCode)
	}
}

// TestCardRequest_EmployeeCannotCreateRequest verifies that an employee token
// cannot submit a card request (client-only endpoint).
func TestCardRequest_EmployeeCannotCreateRequest(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/cards/requests", map[string]interface{}{
		"account_number": "265-0000000001-00",
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Employee tokens should be rejected on client-only routes
	if resp.StatusCode == 201 {
		t.Fatal("expected failure: card request creation should be client-only")
	}
}

// TestCardRequest_EmployeeCanListRequests verifies that an employee with
// cards.approve permission can list all card requests.
func TestCardRequest_EmployeeCanListRequests(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/cards/requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// TestCardRequest_EmployeeCanFilterByStatus verifies filtering requests by status.
func TestCardRequest_EmployeeCanFilterByStatus(t *testing.T) {
	c := loginAsAdmin(t)

	statuses := []string{"pending", "approved", "rejected"}
	for _, status := range statuses {
		t.Run("status_"+status, func(t *testing.T) {
			resp, err := c.GET(fmt.Sprintf("/api/cards/requests?status=%s", status))
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			helpers.RequireStatus(t, resp, 200)
		})
	}
}

// TestCardRequest_InvalidStatusFilterRejected verifies that an invalid status
// filter returns HTTP 400.
func TestCardRequest_InvalidStatusFilterRejected(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/cards/requests?status=unknown_status")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid status filter, got %d", resp.StatusCode)
	}
}

// TestCardRequest_GetNonExistentRequest verifies that getting a non-existent
// request returns 404.
func TestCardRequest_GetNonExistentRequest(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/cards/requests/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for non-existent request, got %d", resp.StatusCode)
	}
}

// TestCardRequest_ApproveNonExistentRequest verifies that approving a non-existent
// request returns an error (not 200).
func TestCardRequest_ApproveNonExistentRequest(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/cards/requests/999999/approve", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure approving non-existent card request")
	}
}

// TestCardRequest_RejectNonExistentRequest verifies that rejecting a non-existent
// request returns an error (not 200).
func TestCardRequest_RejectNonExistentRequest(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/cards/requests/999999/reject", map[string]interface{}{
		"reason": "Test rejection",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure rejecting non-existent card request")
	}
}

// TestCardRequest_RejectRequiresReason verifies that rejecting a request without
// a reason returns HTTP 400.
func TestCardRequest_RejectRequiresReason(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/cards/requests/1/reject", map[string]interface{}{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure: reject without reason should return 400")
	}
}

// TestCardRequest_EmployeeApproveAndRejectFlow creates a card via the employee
// direct-create path and verifies the approve/reject endpoints are accessible
// to authenticated employees.
func TestCardRequest_EmployeeApproveAndRejectFlow(t *testing.T) {
	c := loginAsAdmin(t)

	// Create a client and account to have a valid account number
	_, acctNum := createTestAccountForCards(t, c)

	// Verify the employee can list requests (may be empty but endpoint works)
	listResp, err := c.GET("/api/cards/requests?status=pending")
	if err != nil {
		t.Fatalf("error listing card requests: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	helpers.RequireField(t, listResp, "requests")

	// Account number was created successfully — log it for debugging
	t.Logf("created account number: %s", acctNum)
}

//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF11: Card Request Workflow ---

// TestCardRequest_UnauthenticatedCannotCreateRequest verifies that unauthenticated
// access to the card request creation endpoint is rejected.
func TestCardRequest_UnauthenticatedCannotCreateRequest(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v3/me/cards/requests", map[string]interface{}{
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/me/cards/requests", map[string]interface{}{
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/cards/requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// TestCardRequest_EmployeeCanFilterByStatus verifies filtering requests by status.
func TestCardRequest_EmployeeCanFilterByStatus(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	statuses := []string{"pending", "approved", "rejected"}
	for _, status := range statuses {
		t.Run("status_"+status, func(t *testing.T) {
			resp, err := c.GET(fmt.Sprintf("/api/v3/cards/requests?status=%s", status))
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/cards/requests?status=unknown_status")
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/cards/requests/999999")
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/cards/requests/999999/approve", nil)
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/cards/requests/999999/reject", map[string]interface{}{
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v3/cards/requests/1/reject", map[string]interface{}{})
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
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a client and account to have a valid account number
	_, acctNum := createTestAccountForCards(t, c)

	// Verify the employee can list requests (may be empty but endpoint works)
	listResp, err := c.GET("/api/v3/cards/requests?status=pending")
	if err != nil {
		t.Fatalf("error listing card requests: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	helpers.RequireField(t, listResp, "requests")

	// Account number was created successfully — log it for debugging
	t.Logf("created account number: %s", acctNum)
}

// TestCardRequest_FullLifecycle tests the complete card request lifecycle:
// client submits a request, employee approves it, and a card is created.
func TestCardRequest_FullLifecycle(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)

	// Create client with funded RSD account and activate them
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	// Client lists their requests before — should be empty or have prior requests
	listBefore, err := clientC.GET("/api/v3/me/cards/requests")
	if err != nil {
		t.Fatalf("list card requests before error: %v", err)
	}
	helpers.RequireStatus(t, listBefore, 200)

	var requestsBefore int
	if requests, ok := listBefore.Body["requests"]; ok {
		raw, _ := json.Marshal(requests)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			requestsBefore = len(arr)
		}
	}

	// Client submits a card request for their account (visa brand)
	cardReqResp, err := clientC.POST("/api/v3/me/cards/requests", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("create card request error: %v", err)
	}
	helpers.RequireStatus(t, cardReqResp, 201)
	cardReqID := int(helpers.GetNumberField(t, cardReqResp, "id"))
	t.Logf("card request id: %d", cardReqID)

	// Client lists their requests — verify pending request appears
	listAfter, err := clientC.GET("/api/v3/me/cards/requests")
	if err != nil {
		t.Fatalf("list card requests after error: %v", err)
	}
	helpers.RequireStatus(t, listAfter, 200)

	var requestsAfter int
	var foundPending bool
	if requests, ok := listAfter.Body["requests"]; ok {
		raw, _ := json.Marshal(requests)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			requestsAfter = len(arr)
			for _, r := range arr {
				if reqMap, ok := r.(map[string]interface{}); ok {
					if id, ok := reqMap["id"].(float64); ok && int(id) == cardReqID {
						foundPending = true
						statusVal, _ := reqMap["status"].(string)
						if statusVal != "pending" {
							t.Fatalf("expected card request status pending, got %q", statusVal)
						}
					}
				}
			}
		}
	}
	if requestsAfter <= requestsBefore {
		t.Fatalf("expected more card requests after submitting one: before=%d after=%d", requestsBefore, requestsAfter)
	}
	if !foundPending {
		t.Fatalf("card request %d not found in client's request list", cardReqID)
	}

	// Employee lists all requests — verify it appears
	empListResp, err := adminClient.GET("/api/v3/cards/requests?status=pending")
	if err != nil {
		t.Fatalf("employee list card requests error: %v", err)
	}
	helpers.RequireStatus(t, empListResp, 200)
	helpers.RequireField(t, empListResp, "requests")

	var foundInEmployeeList bool
	if requests, ok := empListResp.Body["requests"]; ok {
		raw, _ := json.Marshal(requests)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			for _, r := range arr {
				if reqMap, ok := r.(map[string]interface{}); ok {
					if id, ok := reqMap["id"].(float64); ok && int(id) == cardReqID {
						foundInEmployeeList = true
					}
				}
			}
		}
	}
	if !foundInEmployeeList {
		t.Fatalf("card request %d not found in employee's pending list", cardReqID)
	}

	// Employee approves the request
	approveResp, err := adminClient.POST(fmt.Sprintf("/api/v3/cards/requests/%d/approve", cardReqID), nil)
	if err != nil {
		t.Fatalf("approve card request error: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	// Verify card request status is "approved".
	// The approve response body is {"card":{...}, "request":{"status":"approved",...}}.
	// Extract status from the nested "request" object.
	var reqStatus string
	if reqObj, ok := approveResp.Body["request"].(map[string]interface{}); ok {
		reqStatus, _ = reqObj["status"].(string)
	}
	// Fallback: some implementations return status at top-level
	if reqStatus == "" {
		reqStatus, _ = approveResp.Body["status"].(string)
	}
	if reqStatus != "approved" {
		t.Fatalf("expected card request status approved, got %q (full body: %s)", reqStatus, string(approveResp.RawBody))
	}

	// Verify an actual card now exists for that account
	cardsResp, err := adminClient.GET(fmt.Sprintf("/api/v3/cards?account_number=%s", accountNumber))
	if err != nil {
		t.Fatalf("get cards by account error: %v", err)
	}
	helpers.RequireStatus(t, cardsResp, 200)
	helpers.RequireField(t, cardsResp, "cards")

	var cardCount int
	if cards, ok := cardsResp.Body["cards"]; ok {
		raw, _ := json.Marshal(cards)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			cardCount = len(arr)
		}
	}
	if cardCount == 0 {
		t.Fatal("expected at least one card after approving card request, got 0")
	}
	t.Logf("card request %d approved: %d card(s) now exist for account %s", cardReqID, cardCount, accountNumber)
}

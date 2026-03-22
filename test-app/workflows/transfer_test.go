package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF9: Transfer Workflow ---

func TestTransfer_UnauthenticatedCannotCreateTransfer(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/transfers", map[string]interface{}{
		"from_account_number": "123",
		"to_account_number":   "456",
		"amount":              "100.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestTransfer_EmployeeCanReadTransfers(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/transfers/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.Fatalf("expected read access for employee, got %d", resp.StatusCode)
	}
}

func TestTransfer_ListByClient(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.GET(fmt.Sprintf("/api/transfers/client/%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

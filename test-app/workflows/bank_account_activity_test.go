//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestBankAccountActivity_EmployeeCanView asserts an employee (admin) can list
// the bank's accounts, pick one, and read its ledger activity.
func TestBankAccountActivity_EmployeeCanView(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	listResp, err := adminC.GET("/api/v3/bank-accounts")
	if err != nil {
		t.Fatalf("list bank accounts: %v", err)
	}
	if listResp.StatusCode == 404 {
		t.Skip("bank-accounts endpoint not deployed")
	}
	helpers.RequireStatus(t, listResp, 200)
	accounts, _ := listResp.Body["accounts"].([]interface{})
	if len(accounts) == 0 {
		t.Skip("no seeded bank accounts — skipping")
	}
	first, _ := accounts[0].(map[string]interface{})
	bankAcctID := int(first["id"].(float64))

	actResp, err := adminC.GET(fmt.Sprintf("/api/v3/bank-accounts/%d/activity", bankAcctID))
	if err != nil {
		t.Fatalf("get bank account activity: %v", err)
	}
	if actResp.StatusCode == 404 {
		t.Skip("bank-account activity endpoint not deployed")
	}
	helpers.RequireStatus(t, actResp, 200)
	if _, ok := actResp.Body["entries"]; !ok {
		t.Errorf("expected an 'entries' field in the response")
	}
}

// TestBankAccountActivity_RejectsClientAccount asserts the endpoint 404s when
// pointed at a client account id (it lives in the bank-accounts namespace).
func TestBankAccountActivity_RejectsClientAccount(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, _, _ := setupActivatedClient(t, adminC)
	clientAcctID, _ := createClientAccount(t, adminC, clientID, "RSD", 5000)

	resp, err := adminC.GET(fmt.Sprintf("/api/v3/bank-accounts/%d/activity", clientAcctID))
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for a client account id, got %d", resp.StatusCode)
	}
}

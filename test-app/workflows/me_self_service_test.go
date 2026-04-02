//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Me Self-Service Endpoints ---

func TestMe_GetProfile(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "email")
}

func TestMe_ListMyAccounts(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetMyAccountByID(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)

	// List accounts to get an ID
	_ = clientID
	listResp, err := clientC.GET("/api/me/accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	accounts, ok := listResp.Body["accounts"].([]interface{})
	if !ok || len(accounts) == 0 {
		t.Skip("no accounts found for client")
	}
	first := accounts[0].(map[string]interface{})
	acctID := int(first["id"].(float64))

	resp, err := clientC.GET("/api/me/accounts/" + helpers.FormatID(acctID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyCards(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/cards")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyPayments(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/payments")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyTransfers(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/transfers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyPaymentRecipients(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/payment-recipients")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyLoanRequests(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/loan-requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyLoans(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/loans")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyOrders(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/orders")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetPortfolio(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/portfolio")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetPortfolioSummary(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/portfolio/summary")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_UnauthenticatedRejected(t *testing.T) {
	t.Parallel()
	c := newClient()

	endpoints := []string{
		"/api/me",
		"/api/me/accounts",
		"/api/me/cards",
		"/api/me/payments",
		"/api/me/transfers",
		"/api/me/loan-requests",
		"/api/me/loans",
		"/api/me/orders",
		"/api/me/portfolio",
	}

	for _, ep := range endpoints {
		resp, err := c.GET(ep)
		if err != nil {
			t.Fatalf("error on %s: %v", ep, err)
		}
		if resp.StatusCode != 401 {
			t.Errorf("%s: expected 401, got %d", ep, resp.StatusCode)
		}
	}
}

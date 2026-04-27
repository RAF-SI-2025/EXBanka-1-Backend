//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Me Self-Service Endpoints ---

func TestMe_GetProfile(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "email")
}

func TestMe_ListMyAccounts(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetMyAccountByID(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)

	// List accounts to get an ID
	_ = clientID
	listResp, err := clientC.GET("/api/v3/me/accounts")
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

	resp, err := clientC.GET("/api/v3/me/accounts/" + helpers.FormatID(acctID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyCards(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/cards")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyPayments(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/payments")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyTransfers(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/transfers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyPaymentRecipients(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/payment-recipients")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyLoanRequests(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/loan-requests")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyLoans(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/loans")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_ListMyOrders(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/orders")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetPortfolio(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/portfolio")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_GetPortfolioSummary(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/portfolio/summary")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestMe_UnauthenticatedRejected(t *testing.T) {
	t.Parallel()
	c := newClient()

	endpoints := []string{
		"/api/v3/me",
		"/api/v3/me/accounts",
		"/api/v3/me/cards",
		"/api/v3/me/payments",
		"/api/v3/me/transfers",
		"/api/v3/me/loan-requests",
		"/api/v3/me/loans",
		"/api/v3/me/orders",
		"/api/v3/me/portfolio",
		"/api/v3/me/tax",
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

//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// accountBalances captures the three balance fields exposed by the gateway for
// a single account. reserved is computed as balance - available because the
// accountpb.AccountResponse does not expose reserved_balance directly.
//
// Used by Phase 2 reservation invariant tests:
//   - place buy reserves amount   -> available drops, balance unchanged, reserved rises
//   - cancel releases reservation -> all three return to placement-time values
//   - fill settles reservation    -> reserved drops, balance drops, available unchanged
type accountBalances struct {
	Balance   float64
	Available float64
	Reserved  float64 // derived: Balance - Available
}

// getAccountBalancesByNumber fetches an account by number and returns its
// {balance, available, reserved} triple. balance and available come straight
// from the gateway response (account_handler.accountToJSON); reserved is
// derived client-side.
func getAccountBalancesByNumber(t *testing.T, c *client.APIClient, accountNumber string) accountBalances {
	t.Helper()
	resp, err := c.GET("/api/v1/accounts/by-number/" + accountNumber)
	if err != nil {
		t.Fatalf("getAccountBalancesByNumber: GET /api/v1/accounts/by-number/%s: %v", accountNumber, err)
	}
	helpers.RequireStatus(t, resp, 200)
	bal := parseJSONBalance(t, resp.Body, "balance")
	avail := parseJSONBalance(t, resp.Body, "available_balance")
	return accountBalances{
		Balance:   bal,
		Available: avail,
		Reserved:  bal - avail,
	}
}

// getAccountBalancesByID fetches an account by numeric id. Useful when the
// caller only has an account_id (e.g., from a bank-accounts list) and not a
// number. Falls back to parsing the same JSON shape as the by-number variant.
func getAccountBalancesByID(t *testing.T, c *client.APIClient, accountID uint64) accountBalances {
	t.Helper()
	resp, err := c.GET("/api/v1/accounts/" + helpers.FormatID(int(accountID)))
	if err != nil {
		t.Fatalf("getAccountBalancesByID: GET /api/v1/accounts/%d: %v", accountID, err)
	}
	helpers.RequireStatus(t, resp, 200)
	bal := parseJSONBalance(t, resp.Body, "balance")
	avail := parseJSONBalance(t, resp.Body, "available_balance")
	return accountBalances{
		Balance:   bal,
		Available: avail,
		Reserved:  bal - avail,
	}
}

// getAccountIDByNumber resolves an account_number to its numeric id via the
// by-number lookup endpoint. Used by tests that have only the account_number
// (from setupActivatedClient) but need the id to pass as account_id on
// POST /api/v1/me/orders.
func getAccountIDByNumber(t *testing.T, c *client.APIClient, accountNumber string) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/accounts/by-number/" + accountNumber)
	if err != nil {
		t.Fatalf("getAccountIDByNumber: GET /api/v1/accounts/by-number/%s: %v", accountNumber, err)
	}
	helpers.RequireStatus(t, resp, 200)
	idVal, ok := resp.Body["id"].(float64)
	if !ok {
		t.Fatalf("getAccountIDByNumber: response missing id: %v", resp.Body)
	}
	return uint64(idVal)
}

// placeOrderRaw sends POST /api/v1/me/orders with the given body and returns
// the HTTP status + parsed body without asserting. Tests can then branch on
// 201 (accepted), 400 (validation), 409 (insufficient funds), etc.
//
// Callers that want the "happy path" should prefer the specific helpers in
// helpers_test.go (buyStock, createAndExecutePayment, ...). Use placeOrderRaw
// when the expected outcome varies per test (e.g., concurrent-orders races,
// validation error cases).
func placeOrderRaw(t *testing.T, c *client.APIClient, body map[string]interface{}) (status int, parsed map[string]interface{}) {
	t.Helper()
	resp, err := c.POST("/api/v1/me/orders", body)
	if err != nil {
		t.Fatalf("placeOrderRaw: POST /api/v1/me/orders: %v", err)
	}
	return resp.StatusCode, resp.Body
}

// getFirstClientAccountID returns the numeric id of the first account owned by
// the given client_id. Used to convert the account_number returned by
// setupActivatedClient into an account_id suitable for POST /api/v1/me/orders.
func getFirstClientAccountID(t *testing.T, c *client.APIClient, clientID int) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/accounts?client_id=" + helpers.FormatID(clientID))
	if err != nil {
		t.Fatalf("getFirstClientAccountID: GET /api/v1/accounts?client_id=%d: %v", clientID, err)
	}
	helpers.RequireStatus(t, resp, 200)
	accts, ok := resp.Body["accounts"].([]interface{})
	if !ok || len(accts) == 0 {
		t.Fatalf("getFirstClientAccountID: no accounts for client %d", clientID)
	}
	m, ok := accts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("getFirstClientAccountID: unexpected accounts[0] shape: %T", accts[0])
	}
	idVal, ok := m["id"].(float64)
	if !ok {
		t.Fatalf("getFirstClientAccountID: accounts[0].id missing or not a number: %v", m["id"])
	}
	return uint64(idVal)
}

// getClientAccountIDByCurrency returns the id of the client's account with the
// given currency code (e.g., "RSD", "USD", "EUR"). Skips the test if no such
// account exists — the caller is responsible for having created it during
// setup.
func getClientAccountIDByCurrency(t *testing.T, c *client.APIClient, clientID int, currency string) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/accounts?client_id=" + helpers.FormatID(clientID))
	if err != nil {
		t.Fatalf("getClientAccountIDByCurrency: GET /api/v1/accounts?client_id=%d: %v", clientID, err)
	}
	helpers.RequireStatus(t, resp, 200)
	accts, ok := resp.Body["accounts"].([]interface{})
	if !ok {
		t.Fatalf("getClientAccountIDByCurrency: response missing accounts array")
	}
	for _, a := range accts {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if code, _ := m["currency_code"].(string); code != currency {
			continue
		}
		if idVal, ok := m["id"].(float64); ok {
			return uint64(idVal)
		}
	}
	t.Skipf("getClientAccountIDByCurrency: no %s account for client %d — skipping test", currency, clientID)
	return 0
}

// getBankRSDAccountID returns the id of the first bank-owned RSD account.
// Complements getBankRSDAccount() (in helpers_test.go) which returns the
// account_number; this variant returns the numeric id required by
// POST /api/v1/me/orders.
func getBankRSDAccountID(t *testing.T, c *client.APIClient) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/bank-accounts")
	if err != nil {
		t.Fatalf("getBankRSDAccountID: GET /api/v1/bank-accounts: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	accts, ok := resp.Body["accounts"].([]interface{})
	if !ok {
		t.Fatalf("getBankRSDAccountID: response missing accounts array")
	}
	for _, a := range accts {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if code, _ := m["currency_code"].(string); code != "RSD" {
			continue
		}
		if idVal, ok := m["id"].(float64); ok {
			return uint64(idVal)
		}
	}
	t.Fatal("getBankRSDAccountID: no bank RSD account found")
	return 0
}

// findForexPairWithCurrencies iterates the forex pair list and returns the id
// of the first pair whose base/quote currencies match the requested codes,
// along with its listing_id. Skips the test if no such pair exists (e.g.,
// if the simulator hasn't seeded it yet).
func findForexPairWithCurrencies(t *testing.T, c *client.APIClient, base, quote string) (pairID uint64, listingID uint64) {
	t.Helper()
	resp, err := c.GET("/api/v1/securities/forex?page=1&page_size=50")
	if err != nil {
		t.Fatalf("findForexPairWithCurrencies: GET /api/v1/securities/forex: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	pairs, ok := resp.Body["forex_pairs"].([]interface{})
	if !ok {
		t.Skipf("findForexPairWithCurrencies: no forex_pairs array in response — skipping")
	}
	for _, p := range pairs {
		m, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		b, _ := m["base_currency"].(string)
		q, _ := m["quote_currency"].(string)
		if b != base || q != quote {
			continue
		}
		pairID = uint64(m["id"].(float64))
		if l, ok := m["listing"].(map[string]interface{}); ok && l != nil {
			if lid, ok := l["id"].(float64); ok {
				listingID = uint64(lid)
			}
		}
		if listingID == 0 {
			t.Skipf("findForexPairWithCurrencies: %s/%s pair exists but has no listing — skipping", base, quote)
		}
		return
	}
	t.Skipf("findForexPairWithCurrencies: no %s/%s forex pair found — skipping", base, quote)
	return
}

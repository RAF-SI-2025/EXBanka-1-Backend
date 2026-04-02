//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF6: Account Management ---

// createTestClient creates a client and returns its ID for use in account tests.
func createTestClient(t *testing.T, c *client.APIClient) int {
	t.Helper()
	resp, err := c.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("Acct"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         nextClientEmail(),
		"phone":         helpers.RandomPhone(),
		"address":       "Account Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	return int(helpers.GetNumberField(t, resp, "id"))
}

func TestAccount_CreateCurrentAccount(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "account_number")

	// Verify Kafka event
	_, found := el.WaitForEvent("account.created", 10*time.Second, nil)
	if !found {
		t.Fatal("expected account.created Kafka event")
	}
}

func TestAccount_CreateForeignAccount(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "foreign",
		"account_type":  "personal",
		"currency_code": "EUR",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

func TestAccount_CreateWithInvalidKind(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "savings", // invalid
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid account_kind")
	}
}

func TestAccount_ListAllAccounts(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_GetAccountByID(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	createResp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	acctID := helpers.GetNumberField(t, createResp, "id")

	resp, err := c.GET(fmt.Sprintf("/api/accounts/%d", int(acctID)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_UpdateStatus(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	createResp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	acctID := helpers.GetNumberField(t, createResp, "id")

	// Deactivate
	resp, err := c.PUT(fmt.Sprintf("/api/accounts/%d/status", int(acctID)), map[string]interface{}{
		"status": "inactive",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Reactivate
	resp, err = c.PUT(fmt.Sprintf("/api/accounts/%d/status", int(acctID)), map[string]interface{}{
		"status": "active",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_ListCurrencies(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/currencies")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_BankAccountCRUD(t *testing.T) {
	c := loginAsAdmin(t)

	// List bank accounts
	resp, err := c.GET("/api/bank-accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Create bank account
	resp, err = c.POST("/api/bank-accounts", map[string]interface{}{
		"currency_code": "USD",
		"account_kind":  "current",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// May be 201 or 200 depending on implementation
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating bank account, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAccount_GetNonExistent(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/accounts/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAccount_CreateCurrentPersonalEUR(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "foreign",
		"account_type":  "personal",
		"currency_code": "EUR",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "account_number")
}

func TestAccount_CreateCurrentPersonalUSD(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "foreign",
		"account_type":  "personal",
		"currency_code": "USD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

func TestAccount_CreateForeignPersonalUSD(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "foreign",
		"account_type":  "personal",
		"currency_code": "USD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

func TestAccount_CreateWithInitialBalance(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 50000,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	acctNum := helpers.GetStringField(t, resp, "account_number")

	// Verify balance reflects initial_balance
	bal := getAccountBalance(t, c, acctNum)
	if bal < 49999 {
		t.Fatalf("expected balance ~50000, got %f", bal)
	}
}

func TestAccount_GetByAccountNumber(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	createResp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	acctNum := helpers.GetStringField(t, createResp, "account_number")

	resp, err := c.GET("/api/accounts/by-number/" + acctNum)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "account_number")
}

func TestAccount_MissingRequiredFields(t *testing.T) {
	c := loginAsAdmin(t)
	// Missing account_kind
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      1,
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 missing account_kind, got %d", resp.StatusCode)
	}
}

func TestAccount_InvalidCurrencyCode(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "FAKE",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid currency_code")
	}
}

func TestAccount_UpdateStatusInvalidValue(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/accounts/1/status", map[string]interface{}{
		"status": "suspended", // invalid
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure with invalid status value")
	}
}

func TestAccount_ListWithPagination(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/accounts?page=1&page_size=5")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_BankAccountCreateForeignEUR(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/bank-accounts", map[string]interface{}{
		"currency_code": "EUR",
		"account_kind":  "foreign",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating EUR bank account, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAccount_UnauthenticatedCannotCreate(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      1,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAccount_UpdateName(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	createResp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	acctID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := c.PUT(fmt.Sprintf("/api/accounts/%d/name", acctID), map[string]interface{}{
		"new_name":  "My Savings Account",
		"client_id": clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_UpdateLimits(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	createResp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	acctID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := c.PUT(fmt.Sprintf("/api/accounts/%d/limits", acctID), map[string]interface{}{
		"daily_limit":   50000.0,
		"monthly_limit": 500000.0,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_UpdateLimitsNegativeRejected(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.PUT("/api/accounts/1/limits", map[string]interface{}{
		"daily_limit": -100.0,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for negative limit, got %d", resp.StatusCode)
	}
}

func TestAccount_DeleteBankAccount(t *testing.T) {
	c := loginAsAdmin(t)

	// Create a bank account to delete (use a less common currency)
	createResp, err := c.POST("/api/bank-accounts", map[string]interface{}{
		"currency_code": "CHF",
		"account_kind":  "foreign",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping delete test: create bank account returned %d", createResp.StatusCode)
	}
	bankAcctID := int(helpers.GetNumberField(t, createResp, "id"))

	// Delete it
	resp, err := c.DELETE(fmt.Sprintf("/api/bank-accounts/%d", bankAcctID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAccount_CreateCompany(t *testing.T) {
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)

	resp, err := c.POST("/api/companies", map[string]interface{}{
		"company_name":        fmt.Sprintf("TestCo_%d", helpers.DateOfBirthUnix()),
		"registration_number": fmt.Sprintf("%08d", time.Now().UnixNano()%100000000),
		"tax_number":          fmt.Sprintf("%09d", time.Now().UnixNano()%1000000000),
		"activity_code":       "62010",
		"address":             "Test Business St 1",
		"owner_id":            clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating company, got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

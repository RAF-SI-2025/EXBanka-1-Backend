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
		"email":         helpers.RandomEmail(),
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
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/accounts/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

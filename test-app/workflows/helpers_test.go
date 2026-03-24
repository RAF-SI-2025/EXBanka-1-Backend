//go:build integration

package workflows

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	kafkalib "github.com/segmentio/kafka-go"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// clientEmailCounter is a package-level counter used to generate unique tagged
// client emails of the form base+clientN@domain. Using atomic.Int32 ensures
// parallel sub-tests do not collide on email uniqueness constraints.
var clientEmailCounter atomic.Int32

func init() {
	// Start counter at 9 so nextClientEmail() returns +client10, +client11, etc.
	// Slots 1–9 are reserved for hardcoded uses in payment_test.go and transfer_test.go.
	clientEmailCounter.Store(9)
}

// nextClientEmail returns cfg.ClientEmail(n) where n is a monotonically
// increasing integer. Each call increments the counter.
func nextClientEmail() string {
	n := int(clientEmailCounter.Add(1))
	return cfg.ClientEmail(n)
}

// getAccountBalance fetches the available_balance for a single account by account number.
// Uses GET /api/accounts/by-number/:account_number (anyAuth — employee or client token).
// The balance field is returned as a JSON string by the account service; this function
// parses it to float64 for arithmetic comparisons in tests.
func getAccountBalance(t *testing.T, c *client.APIClient, accountNumber string) float64 {
	t.Helper()
	resp, err := c.GET("/api/accounts/by-number/" + accountNumber)
	if err != nil {
		t.Fatalf("getAccountBalance: GET /api/accounts/by-number/%s: %v", accountNumber, err)
	}
	helpers.RequireStatus(t, resp, 200)
	return parseJSONBalance(t, resp.Body, "available_balance")
}

// getBankRSDAccount returns the account number and available balance of the first
// bank-owned RSD account. Uses GET /api/bank-accounts (employee auth required).
func getBankRSDAccount(t *testing.T, c *client.APIClient) (string, float64) {
	t.Helper()
	resp, err := c.GET("/api/bank-accounts")
	if err != nil {
		t.Fatalf("getBankRSDAccount: GET /api/bank-accounts: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	accts, ok := resp.Body["accounts"].([]interface{})
	if !ok {
		t.Fatalf("getBankRSDAccount: response missing 'accounts' array. Body: %s", string(resp.RawBody))
	}
	for _, a := range accts {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if m["currency_code"] != "RSD" {
			continue
		}
		acctNum, _ := m["account_number"].(string)
		bal := parseJSONBalance(t, m, "available_balance")
		return acctNum, bal
	}
	t.Fatal("getBankRSDAccount: no bank account with currency_code=RSD found")
	return "", 0
}

// scanKafkaForActivationToken reads the notification.send-email topic from the earliest
// offset and returns the most recent activation token sent to the given email address.
// Blocks up to 15 seconds waiting for messages; fails the test if no token is found.
//
// This mirrors ensureAdminActivated in auth_test.go but is parameterised by email.
func scanKafkaForActivationToken(t *testing.T, email string) string {
	t.Helper()
	// No GroupID: use direct partition reader so Kafka never redirects us to
	// the group coordinator (which advertises the internal kafka:9092 address
	// unreachable from outside Docker).
	r := kafkalib.NewReader(kafkalib.ReaderConfig{
		Brokers:     []string{cfg.KafkaBrokers},
		Topic:       "notification.send-email",
		Partition:   0,
		StartOffset: kafkalib.FirstOffset,
		MaxWait:     500 * time.Millisecond,
	})
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var latestToken string
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			break // context timeout or EOF
		}
		var body struct {
			To        string            `json:"to"`
			EmailType string            `json:"email_type"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(msg.Value, &body) != nil {
			continue
		}
		if body.To == email && body.EmailType == "ACTIVATION" {
			if token := body.Data["token"]; token != "" {
				latestToken = token // keep scanning for the latest one
			}
		}
	}

	if latestToken == "" {
		t.Fatalf("scanKafkaForActivationToken: no ACTIVATION token found for %s within 15s", email)
	}
	return latestToken
}

// setupActivatedClient creates a new bank client, creates a funded RSD account for them,
// activates their account via the Kafka activation token, and returns the client's ID,
// their account number, and an authenticated APIClient ready for use in tests.
//
// Uses a random email per call so repeated test runs don't collide on uniqueness constraints.
// The account is funded with 100 000 RSD initial balance.
func setupActivatedClient(t *testing.T, adminC *client.APIClient) (clientID int, accountNumber string, clientC *client.APIClient) {
	t.Helper()

	email := helpers.RandomEmail()
	password := helpers.RandomPassword()

	// Create client
	createResp, err := adminC.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("Cli"),
		"last_name":     helpers.RandomName("User"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "other",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Helper Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("setupActivatedClient: create client: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	clientID = int(helpers.GetNumberField(t, createResp, "id"))

	// Create funded RSD account
	acctResp, err := adminC.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 100000,
	})
	if err != nil {
		t.Fatalf("setupActivatedClient: create account: %v", err)
	}
	helpers.RequireStatus(t, acctResp, 201)
	accountNumber = helpers.GetStringField(t, acctResp, "account_number")

	// Activate via Kafka token
	token := scanKafkaForActivationToken(t, email)
	activateResp, err := newClient().ActivateAccount(token, password)
	if err != nil {
		t.Fatalf("setupActivatedClient: activate account: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	// Login as the new client
	clientC = loginAsClient(t, email, password)
	return clientID, accountNumber, clientC
}

// parseJSONBalance extracts a balance field that may be a JSON number (float64) or
// a JSON string (e.g., "50000.0000" from the decimal serialisation). Returns float64.
func parseJSONBalance(t *testing.T, m map[string]interface{}, field string) float64 {
	t.Helper()
	val, ok := m[field]
	if !ok {
		t.Fatalf("parseJSONBalance: field %q not found in map", field)
	}
	switch v := val.(type) {
	case float64:
		return v
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("parseJSONBalance: field %q value %q is not a valid float: %v", field, v, err)
		}
		return f
	default:
		t.Fatalf("parseJSONBalance: field %q has unexpected type %T: %v", field, val, val)
		return 0
	}
}

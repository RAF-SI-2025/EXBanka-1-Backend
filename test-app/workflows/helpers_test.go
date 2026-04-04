//go:build integration

package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	kafkalib "github.com/segmentio/kafka-go"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

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

// scanKafkaForVerificationCode reads the notification.send-email topic from the earliest
// offset and returns the most recent TRANSACTION_VERIFICATION code sent to the given email.
// Blocks up to 15 seconds; fails the test if no code is found.
func scanKafkaForVerificationCode(t *testing.T, email string) string {
	t.Helper()
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

	var latestCode string
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			break
		}
		var body struct {
			To        string            `json:"to"`
			EmailType string            `json:"email_type"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(msg.Value, &body) != nil {
			continue
		}
		if body.To == email && body.EmailType == "VERIFICATION_CODE" {
			if code := body.Data["code"]; code != "" {
				latestCode = code
			}
		}
	}

	if latestCode == "" {
		t.Fatalf("scanKafkaForVerificationCode: no VERIFICATION_CODE email found for %s within 15s", email)
	}
	return latestCode
}

// setupActivatedClient creates a new bank client, creates a funded RSD account for them,
// activates their account via the Kafka activation token, and returns the client's ID,
// their account number, and an authenticated APIClient ready for use in tests.
//
// Uses a random email per call so repeated test runs don't collide on uniqueness constraints.
// The account is funded with 100 000 RSD initial balance.
func setupActivatedClient(t *testing.T, adminC *client.APIClient) (clientID int, accountNumber string, clientC *client.APIClient, email string) {
	t.Helper()

	email = helpers.RandomEmail()
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
	return clientID, accountNumber, clientC, email
}

// createVerificationAndGetChallengeID creates a verification challenge for the given
// source (e.g., "payment" or "transfer") and source_id, submits the bypass code "111111"
// to verify it, and returns the challenge_id to use when executing.
func createVerificationAndGetChallengeID(t *testing.T, c *client.APIClient, sourceService string, sourceID int) int {
	t.Helper()

	// Create verification challenge
	createResp, err := c.POST("/api/verifications", map[string]interface{}{
		"source_service": sourceService,
		"source_id":      sourceID,
	})
	if err != nil {
		t.Fatalf("createVerification: POST /api/verifications: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Submit bypass code to verify
	submitResp, err := c.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": "111111",
	})
	if err != nil {
		t.Fatalf("createVerification: submit code: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)

	return challengeID
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

// createAndVerifyChallenge creates a verification challenge and verifies it
// using the Kafka email verification code. Returns the challenge ID.
func createAndVerifyChallenge(t *testing.T, c *client.APIClient, sourceService string, sourceID int, email string) int {
	t.Helper()
	challengeID, code := createChallengeOnly(t, c, sourceService, sourceID, email)
	submitVerificationCode(t, c, challengeID, code)
	return challengeID
}

// createChallengeOnly creates a verification challenge and extracts the code
// from Kafka without submitting it.
func createChallengeOnly(t *testing.T, c *client.APIClient, sourceService string, sourceID int, email string) (int, string) {
	t.Helper()
	createResp, err := c.POST("/api/verifications", map[string]interface{}{
		"source_service": sourceService,
		"source_id":      sourceID,
	})
	if err != nil {
		t.Fatalf("createChallengeOnly: POST /api/verifications: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	code := scanKafkaForVerificationCode(t, email)
	return challengeID, code
}

// submitVerificationCode submits a verification code and asserts success.
func submitVerificationCode(t *testing.T, c *client.APIClient, challengeID int, code string) {
	t.Helper()
	resp, err := c.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": code,
	})
	if err != nil {
		t.Fatalf("submitVerificationCode: POST /api/verifications/%d/code: %v", challengeID, err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// setupActivatedClientWithForeignAccount creates a client with RSD (100k) + foreign currency account (10k).
func setupActivatedClientWithForeignAccount(t *testing.T, adminC *client.APIClient, currency string) (clientID int, rsdAccountNum string, foreignAccountNum string, clientC *client.APIClient, email string) {
	t.Helper()
	clientID, rsdAccountNum, clientC, email = setupActivatedClient(t, adminC)

	acctResp, err := adminC.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   currency,
		"initial_balance": 10000,
	})
	if err != nil {
		t.Fatalf("setupActivatedClientWithForeignAccount: create foreign account: %v", err)
	}
	helpers.RequireStatus(t, acctResp, 201)
	foreignAccountNum = helpers.GetStringField(t, acctResp, "account_number")
	return
}

// setupClientWithCard creates a client with an RSD account and an approved card.
func setupClientWithCard(t *testing.T, adminC *client.APIClient, brand string) (clientID int, accountNum string, cardID int, clientC *client.APIClient, email string) {
	t.Helper()
	clientID, accountNum, clientC, email = setupActivatedClient(t, adminC)

	cardResp, err := adminC.POST("/api/cards", map[string]interface{}{
		"account_number": accountNum,
		"card_brand":     brand,
		"owner_type":     "client",
	})
	if err != nil {
		t.Fatalf("setupClientWithCard: create card: %v", err)
	}
	helpers.RequireStatus(t, cardResp, 201)
	cardID = int(helpers.GetNumberField(t, cardResp, "id"))
	return
}

// createAndExecutePayment creates a payment, verifies via challenge, and executes it.
func createAndExecutePayment(t *testing.T, fromClient *client.APIClient, toAccountNum string, amount float64, email string) int {
	t.Helper()

	createResp, err := fromClient.POST("/api/me/payments", map[string]interface{}{
		"recipient_account_number": toAccountNum,
		"amount":                   amount,
		"payment_code":             "289",
		"purpose":                  "test payment",
	})
	if err != nil {
		t.Fatalf("createAndExecutePayment: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	paymentID := int(helpers.GetNumberField(t, createResp, "id"))

	challengeID := createAndVerifyChallenge(t, fromClient, "payment", paymentID, email)

	execResp, err := fromClient.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"challenge_id": challengeID,
	})
	if err != nil {
		t.Fatalf("createAndExecutePayment: execute: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)
	return paymentID
}

// createAndExecuteTransfer creates a transfer between own accounts, verifies, and executes.
func createAndExecuteTransfer(t *testing.T, clientC *client.APIClient, fromAccountNum string, toAccountNum string, amount float64, email string) int {
	t.Helper()

	createResp, err := clientC.POST("/api/me/transfers", map[string]interface{}{
		"from_account_number": fromAccountNum,
		"to_account_number":   toAccountNum,
		"amount":              amount,
	})
	if err != nil {
		t.Fatalf("createAndExecuteTransfer: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	transferID := int(helpers.GetNumberField(t, createResp, "id"))

	challengeID := createAndVerifyChallenge(t, clientC, "transfer", transferID, email)

	execResp, err := clientC.POST(fmt.Sprintf("/api/me/transfers/%d/execute", transferID), map[string]interface{}{
		"challenge_id": challengeID,
	})
	if err != nil {
		t.Fatalf("createAndExecuteTransfer: execute: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)
	return transferID
}

// buyStock places a market buy order and waits for it to fill.
func buyStock(t *testing.T, c *client.APIClient, listingID uint64, quantity int, email string) int {
	t.Helper()

	orderResp, err := c.POST("/api/orders", map[string]interface{}{
		"listing_id": listingID,
		"order_type": "market",
		"direction":  "buy",
		"quantity":   quantity,
	})
	if err != nil {
		t.Fatalf("buyStock: create order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 201)
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))

	waitForOrderFill(t, c, orderID, 30*time.Second)
	return orderID
}

// waitForOrderFill polls until an order's is_done field is true or timeout.
func waitForOrderFill(t *testing.T, c *client.APIClient, orderID int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := c.GET(fmt.Sprintf("/api/orders/%d", orderID))
		if err != nil {
			t.Fatalf("waitForOrderFill: GET order: %v", err)
		}
		helpers.RequireStatus(t, resp, 200)
		if done, ok := resp.Body["is_done"].(bool); ok && done {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("waitForOrderFill: order %d not filled within %s", orderID, timeout)
}

// createLoanAndApprove submits a loan request and has admin approve it.
func createLoanAndApprove(t *testing.T, adminC *client.APIClient, clientC *client.APIClient, loanType string, amount float64, accountNum string, months int) int {
	t.Helper()

	reqResp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
		"loan_type":         loanType,
		"interest_type":     "fixed",
		"amount":            amount,
		"currency":          "RSD",
		"purpose":           "test loan",
		"monthly_income":    50000,
		"employment_status": "permanent",
		"employment_period": 24,
		"repayment_period":  months,
		"phone":             helpers.RandomPhone(),
		"account_number":    accountNum,
	})
	if err != nil {
		t.Fatalf("createLoanAndApprove: create request: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 201)
	requestID := int(helpers.GetNumberField(t, reqResp, "id"))

	approveResp, err := adminC.POST(fmt.Sprintf("/api/loan-requests/%d/approve", requestID), nil)
	if err != nil {
		t.Fatalf("createLoanAndApprove: approve: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)
	loanID := int(helpers.GetNumberField(t, approveResp, "loan_id"))
	return loanID
}

// assertBalanceChanged fetches current balance and asserts it changed by expectedDelta.
func assertBalanceChanged(t *testing.T, c *client.APIClient, accountNum string, before float64, expectedDelta float64) {
	t.Helper()
	after := getAccountBalance(t, c, accountNum)
	actual := after - before
	tolerance := 0.01
	diff := actual - expectedDelta
	if diff < -tolerance || diff > tolerance {
		t.Errorf("assertBalanceChanged(%s): expected delta %.2f, got %.2f (before=%.2f, after=%.2f)",
			accountNum, expectedDelta, actual, before, after)
	}
}

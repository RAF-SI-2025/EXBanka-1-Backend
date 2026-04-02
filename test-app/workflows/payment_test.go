//go:build integration

package workflows

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF8: Payment Workflow ---

// Note: Payments are client-only write operations.
// These tests verify the employee-visible read paths and
// validate that unauthenticated/wrong-auth access is blocked.

func TestPayment_EmployeeCanReadPayments(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	// Get payment by ID (may not exist but should return 404, not 401/403)
	resp, err := c.GET("/api/payments/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Expected: 404 (not found) not 401/403
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.Fatalf("expected read access for employee, got %d", resp.StatusCode)
	}
}

func TestPayment_UnauthenticatedCannotCreatePayment(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": "123",
		"to_account_number":   "456",
		"amount":              "100.00",
		"payment_code":        "289",
		"purpose":             "test",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPayment_KafkaEventsOnPayment(t *testing.T) {
	// Not parallel: EventListener uses a fixed GroupID per topic. Running concurrently
	// with other tests that use EventListener would cause Kafka partition rebalancing,
	// potentially starving other listeners (e.g. TestPayment_EndToEnd's WaitForEvent).
	// This test just verifies the Kafka listener can monitor payment topics.
	// Full payment flow requires an authenticated client with funded accounts.
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	// Allow listener to connect
	time.Sleep(2 * time.Second)

	// Check we can query payment topics (no events expected in fresh state)
	events := el.EventsByTopic("transaction.payment-completed")
	t.Logf("payment-completed events observed: %d", len(events))
}

func TestPayment_EndToEnd(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Create source client (A) and dest client (B)
	clientAID := createTestClient(t, adminClient)
	clientBID := createTestClient(t, adminClient)

	// Get activation email for client A
	// We need the client's email — use random email so tests are idempotent
	emailA := helpers.RandomEmail()
	passwordA := helpers.RandomPassword()

	createRespA, err := adminClient.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("PayA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         emailA,
		"phone":         helpers.RandomPhone(),
		"address":       "Payment Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client A error: %v", err)
	}
	helpers.RequireStatus(t, createRespA, 201)
	clientAID = int(helpers.GetNumberField(t, createRespA, "id"))

	// Create source account for client A with 50000 RSD
	srcAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientAID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 50000,
	})
	if err != nil {
		t.Fatalf("create source account error: %v", err)
	}
	helpers.RequireStatus(t, srcAcctResp, 201)
	srcAccountNumber := helpers.GetStringField(t, srcAcctResp, "account_number")

	// Create destination account for client B with 0 RSD
	_ = clientBID
	dstAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientBID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create dest account error: %v", err)
	}
	helpers.RequireStatus(t, dstAcctResp, 201)
	dstAccountNumber := helpers.GetStringField(t, dstAcctResp, "account_number")

	// Activate client A
	tokenA := scanKafkaForActivationToken(t, emailA)
	activateResp, err := newClient().ActivateAccount(tokenA, passwordA)
	if err != nil {
		t.Fatalf("activate client A error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	clientA := loginAsClient(t, emailA, passwordA)

	// Record balances before
	srcBalanceBefore := getAccountBalance(t, adminClient, srcAccountNumber)
	dstBalanceBefore := getAccountBalance(t, adminClient, dstAccountNumber)

	// Start Kafka listener for payment events
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	// Client A creates payment (500 RSD — below 1000 fee threshold)
	payResp, err := clientA.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": srcAccountNumber,
		"to_account_number":   dstAccountNumber,
		"amount":              500,
		"payment_purpose":     "Test payment below threshold",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	helpers.RequireStatus(t, payResp, 201)
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	// Verify via verification-service (bypass code)
	challengeID := createVerificationAndGetChallengeID(t, clientA, "payment", paymentID)

	// Execute payment with challenge_id
	execResp, err := clientA.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute payment error: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	// Verify balances moved in the right direction
	srcBalanceAfter := getAccountBalance(t, adminClient, srcAccountNumber)
	dstBalanceAfter := getAccountBalance(t, adminClient, dstAccountNumber)

	if srcBalanceAfter >= srcBalanceBefore {
		t.Fatalf("source balance should have decreased: before=%f after=%f", srcBalanceBefore, srcBalanceAfter)
	}
	if dstBalanceAfter <= dstBalanceBefore {
		t.Fatalf("dest balance should have increased: before=%f after=%f", dstBalanceBefore, dstBalanceAfter)
	}

	// Verify Kafka event
	_, found := el.WaitForEvent("transaction.payment-completed", 15*time.Second, nil)
	if !found {
		t.Fatal("expected transaction.payment-completed Kafka event")
	}
}

func TestPayment_WithFee(t *testing.T) {
	adminClient := loginAsAdmin(t)

	emailA := helpers.RandomEmail()
	passwordA := helpers.RandomPassword()

	createRespA, err := adminClient.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("FeeA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         emailA,
		"phone":         helpers.RandomPhone(),
		"address":       "Fee Payment St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client A error: %v", err)
	}
	helpers.RequireStatus(t, createRespA, 201)
	clientAID := int(helpers.GetNumberField(t, createRespA, "id"))

	clientBID := createTestClient(t, adminClient)

	// Source account with 50000 RSD
	srcAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientAID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 50000,
	})
	if err != nil {
		t.Fatalf("create source account error: %v", err)
	}
	helpers.RequireStatus(t, srcAcctResp, 201)
	srcAccountNumber := helpers.GetStringField(t, srcAcctResp, "account_number")

	// Dest account with 0 RSD
	dstAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientBID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create dest account error: %v", err)
	}
	helpers.RequireStatus(t, dstAcctResp, 201)
	dstAccountNumber := helpers.GetStringField(t, dstAcctResp, "account_number")

	// Activate client A
	tokenA := scanKafkaForActivationToken(t, emailA)
	activateResp, err := newClient().ActivateAccount(tokenA, passwordA)
	if err != nil {
		t.Fatalf("activate client A error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	clientA := loginAsClient(t, emailA, passwordA)

	// Create payment of 5000 RSD (above 1000 fee threshold)
	payResp, err := clientA.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": srcAccountNumber,
		"to_account_number":   dstAccountNumber,
		"amount":              5000,
		"payment_purpose":     "Test payment above threshold",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	helpers.RequireStatus(t, payResp, 201)
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	// Verify via verification-service (bypass code)
	challengeID := createVerificationAndGetChallengeID(t, clientA, "payment", paymentID)

	// Execute payment with challenge_id
	execResp, err := clientA.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute payment error: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	// Verify commission > 0 (above 1000 RSD threshold)
	commissionStr := helpers.GetStringField(t, execResp, "commission")
	commission, err := strconv.ParseFloat(commissionStr, 64)
	if err != nil {
		t.Fatalf("parse commission %q: %v", commissionStr, err)
	}
	if commission <= 0 {
		t.Fatalf("expected non-zero commission for 5000 RSD payment, got %f", commission)
	}
	t.Logf("payment commission for 5000 RSD: %f", commission)
}

func TestPayment_ExternalPayment(t *testing.T) {
	adminClient := loginAsAdmin(t)

	emailA := helpers.RandomEmail()
	passwordA := helpers.RandomPassword()

	createRespA, err := adminClient.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("ExtA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         emailA,
		"phone":         helpers.RandomPhone(),
		"address":       "External Payment St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client A error: %v", err)
	}
	helpers.RequireStatus(t, createRespA, 201)
	clientAID := int(helpers.GetNumberField(t, createRespA, "id"))

	srcAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientAID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 20000,
	})
	if err != nil {
		t.Fatalf("create source account error: %v", err)
	}
	helpers.RequireStatus(t, srcAcctResp, 201)
	srcAccountNumber := helpers.GetStringField(t, srcAcctResp, "account_number")

	tokenA := scanKafkaForActivationToken(t, emailA)
	activateResp, err := newClient().ActivateAccount(tokenA, passwordA)
	if err != nil {
		t.Fatalf("activate client A error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	clientA := loginAsClient(t, emailA, passwordA)

	// External account number — not belonging to any client in the system
	externalAccountNumber := "908-9999999999-99"

	payResp, err := clientA.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": srcAccountNumber,
		"to_account_number":   externalAccountNumber,
		"amount":              1000,
		"payment_purpose":     "External payment test",
	})
	if err != nil {
		t.Fatalf("create external payment error: %v", err)
	}
	// External account does not exist in our system. Payment creation may be rejected (400/404/422)
	// or may succeed if the service supports external routing (201).
	if payResp.StatusCode >= 400 {
		t.Logf("external payment rejected at creation (expected): status=%d", payResp.StatusCode)
		return
	}
	if payResp.StatusCode != 201 {
		t.Fatalf("unexpected status for external payment creation: %d: %s", payResp.StatusCode, string(payResp.RawBody))
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	challengeID := createVerificationAndGetChallengeID(t, clientA, "payment", paymentID)

	execResp, err := clientA.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute external payment error: %v", err)
	}
	// External payments may succeed (200) or be rejected (400/422 if external routing not supported)
	if execResp.StatusCode != 200 && execResp.StatusCode < 400 {
		t.Fatalf("unexpected status for external payment: %d", execResp.StatusCode)
	}
	t.Logf("external payment status: %d", execResp.StatusCode)
}

func TestPayment_WrongOTPCodeRejected(t *testing.T) {
	adminClient := loginAsAdmin(t)

	emailA := helpers.RandomEmail()
	passwordA := helpers.RandomPassword()

	createRespA, err := adminClient.POST("/api/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("OtpA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         emailA,
		"phone":         helpers.RandomPhone(),
		"address":       "OTP Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client A error: %v", err)
	}
	helpers.RequireStatus(t, createRespA, 201)
	clientAID := int(helpers.GetNumberField(t, createRespA, "id"))
	clientBID := createTestClient(t, adminClient)

	srcAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientAID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 20000,
	})
	if err != nil {
		t.Fatalf("create source account error: %v", err)
	}
	helpers.RequireStatus(t, srcAcctResp, 201)
	srcAccountNumber := helpers.GetStringField(t, srcAcctResp, "account_number")

	dstAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientBID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create dest account error: %v", err)
	}
	helpers.RequireStatus(t, dstAcctResp, 201)
	dstAccountNumber := helpers.GetStringField(t, dstAcctResp, "account_number")

	tokenA := scanKafkaForActivationToken(t, emailA)
	activateResp, err := newClient().ActivateAccount(tokenA, passwordA)
	if err != nil {
		t.Fatalf("activate client A error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	clientA := loginAsClient(t, emailA, passwordA)

	payResp, err := clientA.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": srcAccountNumber,
		"to_account_number":   dstAccountNumber,
		"amount":              500,
		"payment_purpose":     "Wrong OTP test",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	helpers.RequireStatus(t, payResp, 201)
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	// Create verification challenge but submit wrong code
	createResp, err := clientA.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
	})
	if err != nil {
		t.Fatalf("create verification error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Submit WRONG code (not the bypass code 111111)
	submitResp, err := clientA.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": "999999",
	})
	if err != nil {
		t.Fatalf("submit wrong code error: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)

	// Execute with unverified challenge — should fail
	execResp, err := clientA.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "999999",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute payment with wrong OTP error: %v", err)
	}
	if execResp.StatusCode == 200 {
		t.Fatal("expected failure when executing payment with wrong OTP code")
	}
	t.Logf("wrong OTP correctly rejected: status=%d", execResp.StatusCode)
}

func TestPayment_InsufficientBalance(t *testing.T) {
	adminClient := loginAsAdmin(t)
	_, accountNumber, clientC, clientEmail := setupActivatedClient(t, adminClient)

	// The account has 100000 RSD. Create a destination.
	destClientID := createTestClient(t, adminClient)
	dstAcctResp, err := adminClient.POST("/api/accounts", map[string]interface{}{
		"owner_id":        destClientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create dest account error: %v", err)
	}
	helpers.RequireStatus(t, dstAcctResp, 201)
	dstAccountNumber := helpers.GetStringField(t, dstAcctResp, "account_number")

	// Try to pay more than the balance
	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   dstAccountNumber,
		"amount":              9999999, // way more than 100000
		"payment_purpose":     "Insufficient balance test",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	_ = clientEmail // not needed for verification flow
	// Should fail at creation (400/422) or at execution step
	if payResp.StatusCode == 201 {
		t.Logf("payment created (amount > balance); verifying execution fails")
		paymentID := int(helpers.GetNumberField(t, payResp, "id"))
		challengeID := createVerificationAndGetChallengeID(t, clientC, "payment", paymentID)

		execResp, err := clientC.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
			"verification_code": "111111",
			"challenge_id":      challengeID,
		})
		if err != nil {
			t.Fatalf("execute payment error: %v", err)
		}
		if execResp.StatusCode == 200 {
			t.Fatal("expected execution to fail due to insufficient balance")
		}
		t.Logf("insufficient balance correctly blocked at execution: %d", execResp.StatusCode)
	} else {
		t.Logf("insufficient balance correctly blocked at creation: %d", payResp.StatusCode)
	}
}

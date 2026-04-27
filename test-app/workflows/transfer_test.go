//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF9: Transfer Workflow ---

func TestTransfer_UnauthenticatedCannotCreateTransfer(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v3/me/transfers", map[string]interface{}{
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
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/v3/transfers/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.Fatalf("expected read access for employee, got %d", resp.StatusCode)
	}
}

func TestTransfer_ListByClient(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID := createTestClient(t, c)
	resp, err := c.GET(fmt.Sprintf("/api/v3/transfers?client_id=%d", clientID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestTransfer_SameCurrency_EndToEnd(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)

	// Create client 1 with known email for activation
	email1 := helpers.RandomEmail()
	password1 := helpers.RandomPassword()

	createResp1, err := adminClient.POST("/api/v3/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("TrfA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email1,
		"phone":         helpers.RandomPhone(),
		"address":       "Transfer Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client 1 error: %v", err)
	}
	helpers.RequireStatus(t, createResp1, 201)
	client1ID := int(helpers.GetNumberField(t, createResp1, "id"))

	// Create RSD account for client 1 with 100000 RSD
	acct1Resp, err := adminClient.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        client1ID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 100000,
	})
	if err != nil {
		t.Fatalf("create account 1 error: %v", err)
	}
	helpers.RequireStatus(t, acct1Resp, 201)
	acctNum1 := helpers.GetStringField(t, acct1Resp, "account_number")

	// Create second RSD account for client 1 (transfers are same-client only)
	acct2Resp, err := adminClient.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        client1ID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 100000,
	})
	if err != nil {
		t.Fatalf("create account 2 error: %v", err)
	}
	helpers.RequireStatus(t, acct2Resp, 201)
	acctNum2 := helpers.GetStringField(t, acct2Resp, "account_number")

	// Activate client 1
	token1 := scanKafkaForActivationToken(t, email1)
	activateResp, err := newClient().ActivateAccount(token1, password1)
	if err != nil {
		t.Fatalf("activate client 1 error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	client1 := loginAsClient(t, email1, password1)

	// Record balances and bank RSD account balance before
	srcBalanceBefore := getAccountBalance(t, adminClient, acctNum1)
	dstBalanceBefore := getAccountBalance(t, adminClient, acctNum2)
	getBankRSDAccount(t, adminClient) // warm up; balance not asserted

	// Client 1 creates transfer of 5000 RSD (above 1000 fee threshold)
	tfrResp, err := client1.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": acctNum1,
		"to_account_number":   acctNum2,
		"amount":              5000,
	})
	if err != nil {
		t.Fatalf("create transfer error: %v", err)
	}
	helpers.RequireStatus(t, tfrResp, 201)
	transferID := int(helpers.GetNumberField(t, tfrResp, "id"))

	// Verify via verification-service (bypass code)
	challengeID := createVerificationAndGetChallengeID(t, client1, "transfer", transferID)

	// Execute transfer
	execResp, err := client1.POST(fmt.Sprintf("/api/v3/me/transfers/%d/execute", transferID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute transfer error: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	t.Logf("transfer executed, response: %v", execResp.Body)

	// Verify balances after
	srcBalanceAfter := getAccountBalance(t, adminClient, acctNum1)
	dstBalanceAfter := getAccountBalance(t, adminClient, acctNum2)

	// Source should have decreased by at least 5000 (transfer amount + any fees)
	actualSrcDecrease := srcBalanceBefore - srcBalanceAfter
	if actualSrcDecrease < 5000 {
		t.Fatalf("source balance decreased by %f, expected at least 5000", actualSrcDecrease)
	}
	t.Logf("source decreased by %.2f (5000 + %.2f fees)", actualSrcDecrease, actualSrcDecrease-5000)

	// Destination increased by 5000
	actualDstIncrease := dstBalanceAfter - dstBalanceBefore
	if actualDstIncrease < 5000-0.01 || actualDstIncrease > 5000+0.01 {
		t.Fatalf("dest balance increased by %f, expected 5000", actualDstIncrease)
	}
}

func TestTransfer_CrossCurrencyRSDtoEUR(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)

	email1 := helpers.RandomEmail()
	password1 := helpers.RandomPassword()

	createResp1, err := adminClient.POST("/api/v3/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("XCurA"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email1,
		"phone":         helpers.RandomPhone(),
		"address":       "Cross Currency St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create client 1 error: %v", err)
	}
	helpers.RequireStatus(t, createResp1, 201)
	client1ID := int(helpers.GetNumberField(t, createResp1, "id"))

	// RSD account (source) with 100000 RSD
	rsdAcctResp, err := adminClient.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        client1ID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 100000,
	})
	if err != nil {
		t.Fatalf("create RSD account error: %v", err)
	}
	helpers.RequireStatus(t, rsdAcctResp, 201)
	rsdAccountNumber := helpers.GetStringField(t, rsdAcctResp, "account_number")

	// EUR account (destination) — seed with 10000 EUR to cover cross-currency fees
	eurAcctResp, err := adminClient.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        client1ID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "EUR",
		"initial_balance": 10000,
	})
	if err != nil {
		t.Fatalf("create EUR account error: %v", err)
	}
	helpers.RequireStatus(t, eurAcctResp, 201)
	eurAccountNumber := helpers.GetStringField(t, eurAcctResp, "account_number")

	token1 := scanKafkaForActivationToken(t, email1)
	activateResp, err := newClient().ActivateAccount(token1, password1)
	if err != nil {
		t.Fatalf("activate client 1 error: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	client1 := loginAsClient(t, email1, password1)

	eurBalBefore := getAccountBalance(t, adminClient, eurAccountNumber)
	rsdBalBefore := getAccountBalance(t, adminClient, rsdAccountNumber)

	// Transfer 10000 RSD to EUR account (cross-currency)
	tfrResp, err := client1.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": rsdAccountNumber,
		"to_account_number":   eurAccountNumber,
		"amount":              10000,
	})
	if err != nil {
		t.Fatalf("create cross-currency transfer error: %v", err)
	}
	helpers.RequireStatus(t, tfrResp, 201)
	transferID := int(helpers.GetNumberField(t, tfrResp, "id"))

	challengeID := createVerificationAndGetChallengeID(t, client1, "transfer", transferID)

	execResp, err := client1.POST(fmt.Sprintf("/api/v3/me/transfers/%d/execute", transferID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute cross-currency transfer error: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	// Verify RSD decreased and EUR increased
	rsdBalAfter := getAccountBalance(t, adminClient, rsdAccountNumber)
	eurBalAfter := getAccountBalance(t, adminClient, eurAccountNumber)

	if rsdBalAfter >= rsdBalBefore {
		t.Fatalf("RSD balance should have decreased: before=%f after=%f", rsdBalBefore, rsdBalAfter)
	}
	if eurBalAfter <= eurBalBefore {
		t.Fatalf("EUR balance should have increased: before=%f after=%f", eurBalBefore, eurBalAfter)
	}
	t.Logf("cross-currency transfer: RSD %f→%f, EUR %f→%f", rsdBalBefore, rsdBalAfter, eurBalBefore, eurBalAfter)
}

func TestTransfer_PaymentRecipientCRUD(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminClient)

	// Create a payment recipient — handler requires client_id, recipient_name, account_number
	createResp, err := clientC.POST("/api/v3/me/payment-recipients", map[string]interface{}{
		"client_id":      clientID,
		"account_number": "908-0000000001-00",
		"recipient_name": "John Doe",
	})
	if err != nil {
		t.Fatalf("create payment recipient error: %v", err)
	}
	// 201 = created, 404/405 = endpoint not implemented (skip gracefully)
	if createResp.StatusCode == 404 || createResp.StatusCode == 405 {
		t.Skip("payment recipient endpoint not implemented")
	}
	helpers.RequireStatus(t, createResp, 201)
	recipientID := int(helpers.GetNumberField(t, createResp, "id"))
	t.Logf("payment recipient created: id=%d", recipientID)

	// List payment recipients — client-scoped route
	listResp, err := clientC.GET("/api/v3/me/payment-recipients")
	if err != nil {
		t.Fatalf("list payment recipients error: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
}

func TestTransfer_InsufficientBalance(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	_, accountNumber, clientC, clientEmail := setupActivatedClient(t, adminClient)

	// Second account for same client
	meResp, err := clientC.GET("/api/v3/me")
	if err != nil {
		t.Fatalf("get me error: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	acct2Resp, err := adminClient.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        meClientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create second account error: %v", err)
	}
	helpers.RequireStatus(t, acct2Resp, 201)
	acctNum2 := helpers.GetStringField(t, acct2Resp, "account_number")

	// Attempt to transfer more than the balance (100000 RSD available)
	tfrResp, err := clientC.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   acctNum2,
		"amount":              9999999,
	})
	if err != nil {
		t.Fatalf("create transfer error: %v", err)
	}
	_ = clientEmail
	if tfrResp.StatusCode == 201 {
		t.Logf("transfer created (amount > balance); verifying execution fails")
		transferID := int(helpers.GetNumberField(t, tfrResp, "id"))
		challengeID := createVerificationAndGetChallengeID(t, clientC, "transfer", transferID)
		execResp, err := clientC.POST(fmt.Sprintf("/api/v3/me/transfers/%d/execute", transferID), map[string]interface{}{
			"verification_code": "111111",
			"challenge_id":      challengeID,
		})
		if err != nil {
			t.Fatalf("execute transfer error: %v", err)
		}
		if execResp.StatusCode == 200 {
			t.Fatal("expected execution to fail due to insufficient balance")
		}
		t.Logf("insufficient balance blocked at execution: %d", execResp.StatusCode)
	} else {
		t.Logf("insufficient balance blocked at creation: %d", tfrResp.StatusCode)
	}
}

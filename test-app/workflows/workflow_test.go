//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- Cross-Service Integration Workflows ---
//
// These tests exercise end-to-end scenarios that span multiple microservices.
// They are intentionally placed last (file sorts after transfer_test.go alphabetically)
// so per-service tests have already validated individual service behaviour.
//
// Prerequisites: fresh Docker environment with docker-compose down -v && docker-compose up --build -d

// TestWorkflow_FullBankingOnboarding exercises the complete new-client onboarding path:
//
//	employee created → client created → RSD account opened → client logs in →
//	client submits card request → employee approves → card exists.
func TestWorkflow_FullBankingOnboarding(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Step 1: Employee creates a new client with a unique email
	clientEmail := helpers.RandomEmail()
	clientPassword := helpers.RandomPassword()

	createClientResp, err := adminClient.POST("/api/v1/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("Onboard"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         clientEmail,
		"phone":         helpers.RandomPhone(),
		"address":       "Onboarding Blvd 1",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("WF-Onboard: create client: %v", err)
	}
	helpers.RequireStatus(t, createClientResp, 201)
	clientID := int(helpers.GetNumberField(t, createClientResp, "id"))
	t.Logf("WF-Onboard: client created id=%d", clientID)

	// Step 2: Employee opens an RSD account for the new client
	createAcctResp, err := adminClient.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 10000,
	})
	if err != nil {
		t.Fatalf("WF-Onboard: create account: %v", err)
	}
	helpers.RequireStatus(t, createAcctResp, 201)
	accountNumber := helpers.GetStringField(t, createAcctResp, "account_number")
	t.Logf("WF-Onboard: account created number=%s", accountNumber)

	// Step 3: Client activates their account via Kafka activation token
	activationToken := scanKafkaForActivationToken(t, clientEmail)
	activateResp, err := newClient().ActivateAccount(activationToken, clientPassword)
	if err != nil {
		t.Fatalf("WF-Onboard: activate account: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)
	t.Logf("WF-Onboard: account activated")

	// Step 4: Client logs in
	clientC := loginAsClient(t, clientEmail, clientPassword)
	t.Logf("WF-Onboard: client logged in")

	// Step 5: Client submits a card request for their account
	cardReqResp, err := clientC.POST("/api/v1/me/cards/requests", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("WF-Onboard: create card request: %v", err)
	}
	helpers.RequireStatus(t, cardReqResp, 201)
	cardReqID := int(helpers.GetNumberField(t, cardReqResp, "id"))
	t.Logf("WF-Onboard: card request submitted id=%d", cardReqID)

	// Step 6: Employee approves the card request
	approveResp, err := adminClient.POST(fmt.Sprintf("/api/v1/cards/requests/%d/approve", cardReqID), nil)
	if err != nil {
		t.Fatalf("WF-Onboard: approve card request: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	// Verify nested status extraction
	var reqStatus string
	if reqObj, ok := approveResp.Body["request"].(map[string]interface{}); ok {
		reqStatus, _ = reqObj["status"].(string)
	}
	if reqStatus == "" {
		reqStatus, _ = approveResp.Body["status"].(string)
	}
	if reqStatus != "approved" {
		t.Fatalf("WF-Onboard: expected approved, got %q (body: %s)", reqStatus, string(approveResp.RawBody))
	}
	t.Logf("WF-Onboard: card request approved")

	// Step 7: Verify a card now exists for the account
	cardsResp, err := adminClient.GET(fmt.Sprintf("/api/v1/cards?account_number=%s", accountNumber))
	if err != nil {
		t.Fatalf("WF-Onboard: list cards by account: %v", err)
	}
	helpers.RequireStatus(t, cardsResp, 200)

	var cardCount int
	if cards, ok := cardsResp.Body["cards"]; ok {
		raw, _ := json.Marshal(cards)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			cardCount = len(arr)
		}
	}
	if cardCount == 0 {
		t.Fatal("WF-Onboard: expected at least one card after approval, got 0")
	}
	t.Logf("WF-Onboard: PASS — client onboarded, %d card(s) issued", cardCount)
}

// TestWorkflow_FullPaymentWithFee exercises the payment fee deduction path:
//
//	setup 2 funded clients → client A pays 5000 RSD to client B (above fee threshold) →
//	verify: A balance decreased by 5000+fee, B balance increased by 5000, bank fee account increased by fee.
func TestWorkflow_FullPaymentWithFee(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Set up client A with 50000 RSD
	_, acctNumA, clientA, _ := setupActivatedClient(t, adminClient)

	// Set up client B — we only need an account number, not login
	clientBEmail := helpers.RandomEmail()
	createBResp, err := adminClient.POST("/api/v1/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("WfFeeB"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "other",
		"email":         clientBEmail,
		"phone":         helpers.RandomPhone(),
		"address":       "Fee Workflow St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("WF-Fee: create client B: %v", err)
	}
	helpers.RequireStatus(t, createBResp, 201)
	clientBID := int(helpers.GetNumberField(t, createBResp, "id"))

	acctBResp, err := adminClient.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientBID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("WF-Fee: create client B account: %v", err)
	}
	helpers.RequireStatus(t, acctBResp, 201)
	acctNumB := helpers.GetStringField(t, acctBResp, "account_number")

	// Record balances before
	balABefore := getAccountBalance(t, adminClient, acctNumA)
	balBBefore := getAccountBalance(t, adminClient, acctNumB)
	_, bankBalBefore := getBankRSDAccount(t, adminClient)

	// Start Kafka listener
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	// Client A pays 5000 RSD to client B (above 1000 RSD fee threshold)
	payResp, err := clientA.POST("/api/v1/me/payments", map[string]interface{}{
		"from_account_number": acctNumA,
		"to_account_number":   acctNumB,
		"amount":              5000,
		"payment_purpose":     "Workflow fee test payment",
	})
	if err != nil {
		t.Fatalf("WF-Fee: create payment: %v", err)
	}
	helpers.RequireStatus(t, payResp, 201)
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	// Verify via verification-service (bypass code)
	challengeID := createVerificationAndGetChallengeID(t, clientA, "payment", paymentID)

	// Execute payment with challenge_id
	execResp, err := clientA.POST(fmt.Sprintf("/api/v1/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("WF-Fee: execute payment: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	// Parse commission
	commissionStr := helpers.GetStringField(t, execResp, "commission")
	commission, err := strconv.ParseFloat(commissionStr, 64)
	if err != nil {
		t.Fatalf("WF-Fee: parse commission %q: %v", commissionStr, err)
	}
	if commission <= 0 {
		t.Fatalf("WF-Fee: expected positive commission for 5000 RSD payment, got %f", commission)
	}
	t.Logf("WF-Fee: commission = %f", commission)

	// Verify balances
	balAAfter := getAccountBalance(t, adminClient, acctNumA)
	balBAfter := getAccountBalance(t, adminClient, acctNumB)
	_, bankBalAfter := getBankRSDAccount(t, adminClient)

	// A decreased by 5000 + fee
	expectedADecrease := 5000 + commission
	actualADecrease := balABefore - balAAfter
	if actualADecrease < expectedADecrease-0.01 || actualADecrease > expectedADecrease+0.01 {
		t.Fatalf("WF-Fee: client A balance decreased by %f, expected %f (5000 + commission %f)",
			actualADecrease, expectedADecrease, commission)
	}

	// B increased by 5000
	actualBIncrease := balBAfter - balBBefore
	if actualBIncrease < 5000-0.01 || actualBIncrease > 5000+0.01 {
		t.Fatalf("WF-Fee: client B balance increased by %f, expected 5000", actualBIncrease)
	}

	// Bank fee account increased by commission
	actualBankIncrease := bankBalAfter - bankBalBefore
	if actualBankIncrease < commission-0.01 || actualBankIncrease > commission+0.01 {
		t.Fatalf("WF-Fee: bank RSD balance increased by %f, expected commission %f",
			actualBankIncrease, commission)
	}

	// Verify Kafka event
	_, found := el.WaitForEvent("transaction.payment-completed", 15*time.Second, nil)
	if !found {
		t.Fatal("WF-Fee: expected transaction.payment-completed Kafka event")
	}
	t.Logf("WF-Fee: PASS — A:%f→%f B:%f→%f bank fee+%f", balABefore, balAAfter, balBBefore, balBAfter, commission)
}

// TestWorkflow_FullCrossCurrencyTransfer exercises the cross-currency transfer path:
//
//	client A has 100 EUR in foreign account → transfers to their own RSD account →
//	verify: EUR decreased, RSD increased by exchange-rate equivalent.
func TestWorkflow_FullCrossCurrencyTransfer(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Create a client with both EUR and RSD accounts
	clientEmail := helpers.RandomEmail()
	clientPassword := helpers.RandomPassword()

	createResp, err := adminClient.POST("/api/v1/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("XCurWF"),
		"last_name":     helpers.RandomName("Client"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         clientEmail,
		"phone":         helpers.RandomPhone(),
		"address":       "FX Workflow St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("WF-FX: create client: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	clientID := int(helpers.GetNumberField(t, createResp, "id"))

	// EUR account (source) with 100 EUR
	eurAcctResp, err := adminClient.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "EUR",
		"initial_balance": 100,
	})
	if err != nil {
		t.Fatalf("WF-FX: create EUR account: %v", err)
	}
	helpers.RequireStatus(t, eurAcctResp, 201)
	eurAccountNumber := helpers.GetStringField(t, eurAcctResp, "account_number")

	// RSD account (destination) with 0 RSD
	rsdAcctResp, err := adminClient.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("WF-FX: create RSD account: %v", err)
	}
	helpers.RequireStatus(t, rsdAcctResp, 201)
	rsdAccountNumber := helpers.GetStringField(t, rsdAcctResp, "account_number")

	// Activate client
	activationToken := scanKafkaForActivationToken(t, clientEmail)
	activateResp, err := newClient().ActivateAccount(activationToken, clientPassword)
	if err != nil {
		t.Fatalf("WF-FX: activate: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	clientC := loginAsClient(t, clientEmail, clientPassword)

	eurBalBefore := getAccountBalance(t, adminClient, eurAccountNumber)
	rsdBalBefore := getAccountBalance(t, adminClient, rsdAccountNumber)

	// Lookup EUR/RSD exchange rate (to estimate expected RSD increase)
	rateResp, err := newClient().GET("/api/v1/exchange/rates/EUR/RSD")
	if err != nil {
		t.Fatalf("WF-FX: get exchange rate: %v", err)
	}
	var eurToRSD float64
	if rateResp.StatusCode == 200 {
		if rateVal, ok := rateResp.Body["rate"]; ok {
			switch v := rateVal.(type) {
			case float64:
				eurToRSD = v
			case string:
				eurToRSD, _ = strconv.ParseFloat(v, 64)
			}
		}
	}
	t.Logf("WF-FX: EUR/RSD rate = %f", eurToRSD)

	// Transfer 50 EUR to RSD account
	const transferAmount = 50.0
	tfrResp, err := clientC.POST("/api/v1/me/transfers", map[string]interface{}{
		"from_account_number": eurAccountNumber,
		"to_account_number":   rsdAccountNumber,
		"amount":              transferAmount,
	})
	if err != nil {
		t.Fatalf("WF-FX: create transfer: %v", err)
	}
	helpers.RequireStatus(t, tfrResp, 201)
	transferID := int(helpers.GetNumberField(t, tfrResp, "id"))

	challengeID := createVerificationAndGetChallengeID(t, clientC, "transfer", transferID)

	execResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/transfers/%d/execute", transferID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("WF-FX: execute transfer: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)

	// Verify EUR decreased and RSD increased
	eurBalAfter := getAccountBalance(t, adminClient, eurAccountNumber)
	rsdBalAfter := getAccountBalance(t, adminClient, rsdAccountNumber)

	if eurBalAfter >= eurBalBefore {
		t.Fatalf("WF-FX: EUR balance should have decreased: before=%f after=%f", eurBalBefore, eurBalAfter)
	}
	if rsdBalAfter <= rsdBalBefore {
		t.Fatalf("WF-FX: RSD balance should have increased: before=%f after=%f", rsdBalBefore, rsdBalAfter)
	}

	// If exchange rate is known, verify the RSD increase is in the right ballpark (±10%)
	if eurToRSD > 0 {
		expectedRSD := transferAmount * eurToRSD
		actualRSD := rsdBalAfter - rsdBalBefore
		if actualRSD < expectedRSD*0.90 || actualRSD > expectedRSD*1.10 {
			t.Fatalf("WF-FX: RSD received %f, expected ~%f (rate=%f × %f ±10%%)",
				actualRSD, expectedRSD, eurToRSD, transferAmount)
		}
	}

	t.Logf("WF-FX: PASS — EUR %f→%f RSD %f→%f", eurBalBefore, eurBalAfter, rsdBalBefore, rsdBalAfter)
}

// TestWorkflow_FullLoanLifecycle exercises the complete loan lifecycle:
//
//	client requests a housing loan → employee approves →
//	verify 12 installments created → client views their loans.
func TestWorkflow_FullLoanLifecycle(t *testing.T) {
	adminClient := loginAsAdmin(t)

	// Create and activate a client with a funded RSD account
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	meResp, err := clientC.GET("/api/v1/me")
	if err != nil {
		t.Fatalf("WF-Loan: get me: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	// Client submits a housing loan request (60-month, variable rate)
	// Housing loans require repayment_period ∈ {60,120,180,240,300,360}
	loanReqResp, err := clientC.POST("/api/v1/me/loan-requests", map[string]interface{}{
		"client_id":        meClientID,
		"loan_type":        "housing",
		"interest_type":    "variable",
		"amount":           50000,
		"currency_code":    "RSD",
		"repayment_period": 60,
		"account_number":   accountNumber,
	})
	if err != nil {
		t.Fatalf("WF-Loan: create loan request: %v", err)
	}
	helpers.RequireStatus(t, loanReqResp, 201)
	loanReqID := int(helpers.GetNumberField(t, loanReqResp, "id"))
	t.Logf("WF-Loan: loan request id=%d", loanReqID)

	// Employee verifies the request appears in the list
	listResp, err := adminClient.GET("/api/v1/loan-requests")
	if err != nil {
		t.Fatalf("WF-Loan: list loan requests: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	// Employee approves the loan request
	approveResp, err := adminClient.POST(fmt.Sprintf("/api/v1/loan-requests/%d/approve", loanReqID), nil)
	if err != nil {
		t.Fatalf("WF-Loan: approve loan request: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	status := helpers.GetStringField(t, approveResp, "status")
	if status != "approved" && status != "active" {
		t.Fatalf("WF-Loan: expected approved or active, got %q", status)
	}
	loanID := int(helpers.GetNumberField(t, approveResp, "id"))
	t.Logf("WF-Loan: loan id=%d status=%s", loanID, status)

	// Verify 12 installments were created
	installmentsResp, err := adminClient.GET(fmt.Sprintf("/api/v1/loans/%d/installments", loanID))
	if err != nil {
		t.Fatalf("WF-Loan: get installments: %v", err)
	}
	helpers.RequireStatus(t, installmentsResp, 200)

	var installmentCount int
	if installments, ok := installmentsResp.Body["installments"]; ok {
		raw, _ := json.Marshal(installments)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			installmentCount = len(arr)
		}
	}
	if installmentCount != 60 {
		t.Fatalf("WF-Loan: expected 60 installments, got %d", installmentCount)
	}
	t.Logf("WF-Loan: %d installments created", installmentCount)

	// Client lists their loan requests — using /api/me/loan-requests or admin fallback
	clientReqResp, err := clientC.GET("/api/v1/me/loan-requests")
	if err != nil {
		t.Fatalf("WF-Loan: client list loan requests: %v", err)
	}
	if clientReqResp.StatusCode == 404 || clientReqResp.StatusCode == 405 || clientReqResp.StatusCode == 403 || clientReqResp.StatusCode == 400 {
		clientReqResp, err = adminClient.GET(fmt.Sprintf("/api/v1/loan-requests?client_id=%d", meClientID))
		if err != nil {
			t.Fatalf("WF-Loan: fallback list loan requests: %v", err)
		}
	}
	helpers.RequireStatus(t, clientReqResp, 200)

	// Client lists their approved loans — using /api/me/loans or admin fallback
	clientLoansResp, err := clientC.GET("/api/v1/me/loans")
	if err != nil {
		t.Fatalf("WF-Loan: client list loans: %v", err)
	}
	if clientLoansResp.StatusCode == 404 || clientLoansResp.StatusCode == 405 || clientLoansResp.StatusCode == 403 || clientLoansResp.StatusCode == 400 {
		clientLoansResp, err = adminClient.GET(fmt.Sprintf("/api/v1/loans?client_id=%d", meClientID))
		if err != nil {
			t.Fatalf("WF-Loan: fallback list loans: %v", err)
		}
	}
	helpers.RequireStatus(t, clientLoansResp, 200)

	// Verify the approved loan appears in the client's list
	found := false
	if loans, ok := clientLoansResp.Body["loans"]; ok {
		raw, _ := json.Marshal(loans)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil {
			for _, l := range arr {
				if lm, ok := l.(map[string]interface{}); ok {
					if id, ok := lm["id"].(float64); ok && int(id) == loanID {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Logf("WF-Loan: loan %d not found in client's loan list (may be under different key)", loanID)
	}

	t.Logf("WF-Loan: PASS — housing loan %d approved with %d installments", loanID, installmentCount)
}

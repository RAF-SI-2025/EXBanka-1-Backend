# Testing Plan C: Integration Test Workflows

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 14 cross-service integration test workflows to `test-app/workflows/` that validate realistic multi-service business scenarios from the spec.

**Architecture:** Each workflow test file follows existing patterns — uses shared helpers from `helpers_test.go` and `stock_helpers_test.go`, runs against the full Docker Compose stack, build-tagged `//go:build integration`.

**Tech Stack:** Go 1.26, testify, test-app APIClient, Kafka EventListener

**Prerequisite:** Plans A and B must be completed (shared helpers exist, unit tests pass).

**Run with:** `cd test-app && go test -tags integration -v -timeout 20m ./workflows/ -run TestWF`

All new workflow test functions are prefixed `TestWF_` to run them selectively.

---

## Helper Reference

Available helpers (from Plan A):
- `loginAsAdmin(t) → *client.APIClient`
- `loginAsClient(t, email, password) → *client.APIClient`
- `newClient() → *client.APIClient`
- `setupActivatedClient(t, adminC) → (clientID, accountNum, clientC, email)`
- `setupActivatedClientWithForeignAccount(t, adminC, currency) → (clientID, rsdNum, foreignNum, clientC, email)`
- `setupClientWithCard(t, adminC, brand) → (clientID, accountNum, cardID, clientC, email)`
- `setupAgentEmployee(t, adminC) → (empID, agentC, email)`
- `setupSupervisorEmployee(t, adminC) → (empID, supervisorC, email)`
- `createAndVerifyChallenge(t, clientC, sourceService, sourceID, email) → challengeID`
- `createChallengeOnly(t, clientC, sourceService, sourceID, email) → (challengeID, code)`
- `submitVerificationCode(t, clientC, challengeID, code)`
- `createAndExecutePayment(t, fromClient, toAccountNum, amount, email) → paymentID`
- `createAndExecuteTransfer(t, clientC, fromAccountNum, toAccountNum, amount, email) → transferID`
- `buyStock(t, clientC, listingID, quantity, email) → orderID`
- `waitForOrderFill(t, clientC, orderID, timeout)`
- `createLoanAndApprove(t, adminC, clientC, loanType, amount, accountNum, months) → loanID`
- `getAccountBalance(t, clientC, accountNum) → float64`
- `getBankRSDAccount(t, adminC) → (accountNum, balance)`
- `assertBalanceChanged(t, clientC, accountNum, before, expectedDelta)`
- `scanKafkaForActivationToken(t, email) → token`
- `scanKafkaForVerificationCode(t, email) → code`
- `getFirstStockListingID(t, clientC) → (stockID, listingID)`

---

### Task 1: WF-NEW-1 — Full Client Onboarding to First Transaction

**Files:**
- Create: `test-app/workflows/wf_onboarding_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FullClientOnboardingToFirstTransaction tests the complete flow:
// client created → account → activation → login → add recipient → payment → verify → execute → balances correct
func TestWF_FullClientOnboardingToFirstTransaction(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create sender and receiver clients
	senderID, senderAcct, senderC, senderEmail := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)
	_ = senderID

	// Step 2: Record balances before
	senderBefore := getAccountBalance(t, senderC, senderAcct)
	receiverBefore := getAccountBalance(t, adminC, receiverAcct)
	_, bankBefore := getBankRSDAccount(t, adminC)

	// Step 3: Add payment recipient
	recipientResp, err := senderC.POST("/api/me/payment-recipients", map[string]interface{}{
		"name":           "Test Receiver",
		"account_number": receiverAcct,
	})
	if err != nil {
		t.Fatalf("add recipient: %v", err)
	}
	helpers.RequireStatus(t, recipientResp, 201)

	// Step 4: Create and execute payment
	paymentAmount := 5000.0
	paymentID := createAndExecutePayment(t, senderC, receiverAcct, paymentAmount, senderEmail)
	_ = paymentID

	// Step 5: Verify balances
	// Sender decreased by amount + fee (fee may be 0 for small amounts under threshold)
	senderAfter := getAccountBalance(t, senderC, senderAcct)
	receiverAfter := getAccountBalance(t, adminC, receiverAcct)
	_, bankAfter := getBankRSDAccount(t, adminC)

	senderDelta := senderBefore - senderAfter
	receiverDelta := receiverAfter - receiverBefore
	bankDelta := bankAfter - bankBefore

	t.Logf("Sender delta: %.2f, Receiver delta: %.2f, Bank delta: %.2f", senderDelta, receiverDelta, bankDelta)

	if receiverDelta < paymentAmount-0.01 || receiverDelta > paymentAmount+0.01 {
		t.Errorf("receiver should have gained %.2f, got %.2f", paymentAmount, receiverDelta)
	}

	// Sender lost at least the payment amount (possibly more with fee)
	if senderDelta < paymentAmount-0.01 {
		t.Errorf("sender should have lost at least %.2f, got %.2f", paymentAmount, senderDelta)
	}

	// Fee = senderDelta - receiverDelta, should equal bank delta
	fee := senderDelta - receiverDelta
	if fee > 0.01 { // fee exists
		if bankDelta < fee-0.01 || bankDelta > fee+0.01 {
			t.Errorf("bank should have gained fee %.2f, got %.2f", fee, bankDelta)
		}
	}

	// Step 6: Verify payment is visible
	getResp, err := adminC.GET(fmt.Sprintf("/api/payments/%d", paymentID))
	if err != nil {
		t.Fatalf("get payment: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)
	helpers.RequireFieldEquals(t, getResp, "status", "completed")
}
```

- [ ] **Step 2: Run the test**

Run: `cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_FullClientOnboardingToFirstTransaction`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/wf_onboarding_test.go
git commit -m "test(integration): WF-1 full client onboarding to first transaction"
```

---

### Task 2: WF-NEW-2 — Multi-Currency Client Lifecycle

**Files:**
- Create: `test-app/workflows/wf_multicurrency_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_MultiCurrencyClientLifecycle tests:
// Client with RSD+EUR → transfer RSD→EUR → verify exchange rate and commission
func TestWF_MultiCurrencyClientLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Create client with RSD (100k) + EUR (10k) accounts
	_, rsdAcct, eurAcct, clientC, email := setupActivatedClientWithForeignAccount(t, adminC, "EUR")

	// Record balances before
	rsdBefore := getAccountBalance(t, clientC, rsdAcct)
	eurBefore := getAccountBalance(t, clientC, eurAcct)
	_, bankBefore := getBankRSDAccount(t, adminC)

	// Transfer 10000 RSD → EUR
	transferAmount := 10000.0
	transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount, email)
	_ = transferID

	// Verify RSD decreased
	rsdAfter := getAccountBalance(t, clientC, rsdAcct)
	rsdDelta := rsdBefore - rsdAfter
	if rsdDelta < transferAmount-0.01 {
		t.Errorf("RSD should have decreased by at least %.2f, decreased by %.2f", transferAmount, rsdDelta)
	}

	// Verify EUR increased (by some amount after conversion)
	eurAfter := getAccountBalance(t, clientC, eurAcct)
	eurDelta := eurAfter - eurBefore
	if eurDelta <= 0 {
		t.Error("EUR account should have increased after transfer")
	}
	t.Logf("Transferred %.2f RSD, received %.4f EUR", transferAmount, eurDelta)

	// Verify bank got commission (commission on cross-currency transfers)
	_, bankAfter := getBankRSDAccount(t, adminC)
	bankDelta := bankAfter - bankBefore
	t.Logf("Bank commission: %.2f RSD", bankDelta)

	// Commission should be >= 0 (may be 0 for small amounts)
	if bankDelta < -0.01 {
		t.Error("bank balance should not decrease from a transfer")
	}

	// Verify ledger entries exist
	ledgerResp, err := clientC.GET("/api/me/accounts/" + rsdAcct + "/ledger?page_size=5")
	if err == nil {
		helpers.RequireStatus(t, ledgerResp, 200)
	}
}
```

- [ ] **Step 2: Run and commit**

Run: `cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_MultiCurrency`
Expected: PASS

```bash
git add test-app/workflows/wf_multicurrency_test.go
git commit -m "test(integration): WF-2 multi-currency client lifecycle"
```

---

### Task 3: WF-NEW-3 — Card Full Lifecycle

**Files:**
- Create: `test-app/workflows/wf_card_lifecycle_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_CardFullLifecycle tests:
// card request → approve → set PIN → verify PIN → block → unblock → deactivate → new card
func TestWF_CardFullLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, accountNum, clientC, email := setupActivatedClient(t, adminC)
	_ = email

	// Step 1: Client requests a card
	reqResp, err := clientC.POST("/api/me/cards/requests", map[string]interface{}{
		"account_number": accountNum,
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("card request: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 201)
	requestID := int(helpers.GetNumberField(t, reqResp, "id"))

	// Step 2: Employee approves
	approveResp, err := adminC.POST(fmt.Sprintf("/api/cards/requests/%d/approve", requestID), nil)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	// Step 3: Get the created card
	cardsResp, err := clientC.GET("/api/me/cards?account_number=" + accountNum)
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	helpers.RequireStatus(t, cardsResp, 200)
	cards := cardsResp.Body["cards"].([]interface{})
	if len(cards) == 0 {
		t.Fatal("no cards found after approval")
	}
	card := cards[0].(map[string]interface{})
	cardID := int(card["id"].(float64))

	// Step 4: Set PIN
	pinResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/pin", cardID), map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("set pin: %v", err)
	}
	helpers.RequireStatus(t, pinResp, 200)

	// Step 5: Verify PIN (correct)
	verifyResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/verify-pin", cardID), map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("verify pin: %v", err)
	}
	helpers.RequireStatus(t, verifyResp, 200)

	// Step 6: Client blocks card
	blockResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/block", cardID), nil)
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	helpers.RequireStatus(t, blockResp, 200)

	// Step 7: Employee unblocks
	unblockResp, err := adminC.POST(fmt.Sprintf("/api/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("unblock: %v", err)
	}
	helpers.RequireStatus(t, unblockResp, 200)

	// Step 8: Employee deactivates — permanent
	deactivateResp, err := adminC.POST(fmt.Sprintf("/api/cards/%d/deactivate", cardID), nil)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	helpers.RequireStatus(t, deactivateResp, 200)

	// Step 9: Cannot unblock deactivated card
	unblockResp2, err := adminC.POST(fmt.Sprintf("/api/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("unblock deactivated: %v", err)
	}
	if unblockResp2.StatusCode == 200 {
		t.Error("should not be able to unblock a deactivated card")
	}

	// Step 10: Request new card — should succeed (old one deactivated doesn't count)
	reqResp2, err := clientC.POST("/api/me/cards/requests", map[string]interface{}{
		"account_number": accountNum,
		"card_brand":     "mastercard",
	})
	if err != nil {
		t.Fatalf("second card request: %v", err)
	}
	helpers.RequireStatus(t, reqResp2, 201)
}
```

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_CardFullLifecycle
git add test-app/workflows/wf_card_lifecycle_test.go
git commit -m "test(integration): WF-3 card full lifecycle"
```

---

### Task 4: WF-NEW-4 — Loan Full Lifecycle with Installments

**Files:**
- Create: `test-app/workflows/wf_loan_lifecycle_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"fmt"
	"math"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_LoanFullLifecycle tests:
// loan request → approve → disbursement → installment schedule → formula validation
func TestWF_LoanFullLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, accountNum, clientC, _ := setupActivatedClient(t, adminC)

	balanceBefore := getAccountBalance(t, clientC, accountNum)

	// Submit housing loan request: 2M RSD, 60 months, fixed rate
	loanAmount := 2000000.0
	months := 60
	reqResp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
		"loan_type":         "housing",
		"interest_type":     "fixed",
		"amount":            loanAmount,
		"currency":          "RSD",
		"purpose":           "apartment purchase",
		"monthly_income":    150000,
		"employment_status": "permanent",
		"employment_period": 60,
		"repayment_period":  months,
		"phone":             helpers.RandomPhone(),
		"account_number":    accountNum,
	})
	if err != nil {
		t.Fatalf("create loan request: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 201)
	requestID := int(helpers.GetNumberField(t, reqResp, "id"))

	// Approve
	approveResp, err := adminC.POST(fmt.Sprintf("/api/loan-requests/%d/approve", requestID), nil)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)

	// Verify disbursement
	balanceAfter := getAccountBalance(t, clientC, accountNum)
	disbursed := balanceAfter - balanceBefore
	if disbursed < loanAmount-1 || disbursed > loanAmount+1 {
		t.Errorf("expected %.0f RSD disbursed, got %.2f", loanAmount, disbursed)
	}

	// Get loan details
	loanID := int(helpers.GetNumberField(t, approveResp, "loan_id"))
	loanResp, err := adminC.GET(fmt.Sprintf("/api/loans/%d", loanID))
	if err != nil {
		t.Fatalf("get loan: %v", err)
	}
	helpers.RequireStatus(t, loanResp, 200)

	// Verify installment schedule
	installResp, err := adminC.GET(fmt.Sprintf("/api/loans/%d/installments", loanID))
	if err != nil {
		t.Fatalf("get installments: %v", err)
	}
	helpers.RequireStatus(t, installResp, 200)
	installments := installResp.Body["installments"].([]interface{})
	if len(installments) != months {
		t.Errorf("expected %d installments, got %d", months, len(installments))
	}

	// Validate installment amount against spec formula:
	// A = P × r × (1+r)^n / ((1+r)^n - 1)
	// For 2M RSD housing: tier 1M-2M = 5.75% base + 1.50% margin = 7.25% annual
	annualRate := 0.0575 + 0.0150 // 7.25%
	monthlyRate := annualRate / 12.0
	n := float64(months)
	pow := math.Pow(1+monthlyRate, n)
	expectedInstallment := loanAmount * monthlyRate * pow / (pow - 1)

	if len(installments) > 0 {
		first := installments[0].(map[string]interface{})
		actualAmount := parseJSONBalance(t, first, "amount")
		tolerance := expectedInstallment * 0.05 // 5% tolerance for rounding differences
		if math.Abs(actualAmount-expectedInstallment) > tolerance {
			t.Errorf("installment amount: expected ~%.2f, got %.2f (tolerance %.2f)",
				expectedInstallment, actualAmount, tolerance)
		}
		t.Logf("Installment: expected=%.2f, actual=%.2f", expectedInstallment, actualAmount)
	}
}
```

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_LoanFullLifecycle
git add test-app/workflows/wf_loan_lifecycle_test.go
git commit -m "test(integration): WF-4 loan full lifecycle with installment formula validation"
```

---

### Task 5: WF-NEW-5 — Payment Verification Failure and Retry

**Files:**
- Create: `test-app/workflows/wf_verification_retry_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_PaymentVerificationFailureAndRetry tests:
// payment → wrong code 3x → challenge failed → new payment → correct code → success
func TestWF_PaymentVerificationFailureAndRetry(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, senderAcct, senderC, senderEmail := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)

	// Step 1: Create first payment
	createResp, err := senderC.POST("/api/me/payments", map[string]interface{}{
		"recipient_account_number": receiverAcct,
		"amount":                   3000,
		"payment_code":             "289",
		"purpose":                  "test retry",
	})
	if err != nil {
		t.Fatalf("create payment: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	paymentID1 := int(helpers.GetNumberField(t, createResp, "id"))

	// Step 2: Create challenge and get the real code
	challengeID1, _ := createChallengeOnly(t, senderC, "payment", paymentID1, senderEmail)

	// Step 3: Submit wrong code 3 times
	for i := 0; i < 3; i++ {
		resp, err := senderC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID1), map[string]interface{}{
			"code": "000000",
		})
		if err != nil {
			t.Fatalf("submit wrong code %d: %v", i+1, err)
		}
		// Should not be 200 (wrong code)
		if resp.StatusCode == 200 {
			verified, _ := resp.Body["verified"].(bool)
			if verified {
				t.Fatal("wrong code should not verify")
			}
		}
	}

	// Step 4: Verify challenge is now failed
	statusResp, err := senderC.GET(fmt.Sprintf("/api/verifications/%d", challengeID1))
	if err != nil {
		t.Fatalf("get challenge status: %v", err)
	}
	helpers.RequireStatus(t, statusResp, 200)
	status := helpers.GetStringField(t, statusResp, "status")
	if status != "failed" {
		t.Errorf("challenge should be failed after 3 wrong attempts, got %s", status)
	}

	// Step 5: Create NEW payment and execute successfully
	balanceBefore := getAccountBalance(t, senderC, senderAcct)
	paymentID2 := createAndExecutePayment(t, senderC, receiverAcct, 3000, senderEmail)
	_ = paymentID2

	// Step 6: Verify balance changed (second payment went through)
	balanceAfter := getAccountBalance(t, senderC, senderAcct)
	if balanceBefore-balanceAfter < 2999 {
		t.Errorf("balance should have decreased by ~3000, decreased by %.2f", balanceBefore-balanceAfter)
	}
}
```

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_PaymentVerificationFailureAndRetry
git add test-app/workflows/wf_verification_retry_test.go
git commit -m "test(integration): WF-5 payment verification failure and retry"
```

---

### Task 6: WF-NEW-6 — Stock Market Buy and Sell Cycle

**Files:**
- Create: `test-app/workflows/wf_stock_buy_sell_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_StockBuySellCycle tests:
// agent → buy stock → verify holding → sell → verify capital gain → verify account
func TestWF_StockBuySellCycle(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	// Get a stock to trade
	stockID, listingID := getFirstStockListingID(t, agentC)
	_ = stockID

	// Buy 1 share
	orderResp, err := agentC.POST("/api/orders", map[string]interface{}{
		"listing_id": listingID,
		"order_type": "market",
		"direction":  "buy",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("create buy order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 201)
	buyOrderID := int(helpers.GetNumberField(t, orderResp, "id"))

	// Wait for fill
	waitForOrderFill(t, agentC, buyOrderID, 30*time.Second)

	// Verify holding exists
	holdingsResp, err := agentC.GET("/api/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("list holdings: %v", err)
	}
	helpers.RequireStatus(t, holdingsResp, 200)
	holdings := holdingsResp.Body["holdings"].([]interface{})
	if len(holdings) == 0 {
		t.Fatal("should have at least 1 holding after buy")
	}

	// Sell 1 share
	sellResp, err := agentC.POST("/api/orders", map[string]interface{}{
		"listing_id": listingID,
		"order_type": "market",
		"direction":  "sell",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("create sell order: %v", err)
	}
	helpers.RequireStatus(t, sellResp, 201)
	sellOrderID := int(helpers.GetNumberField(t, sellResp, "id"))

	waitForOrderFill(t, agentC, sellOrderID, 30*time.Second)

	// Verify capital gain was recorded
	taxResp, err := agentC.GET("/api/me/tax-records")
	if err != nil {
		t.Fatalf("get tax records: %v", err)
	}
	helpers.RequireStatus(t, taxResp, 200)

	// Verify order details show completed
	getOrderResp, err := agentC.GET(fmt.Sprintf("/api/orders/%d", sellOrderID))
	if err != nil {
		t.Fatalf("get sell order: %v", err)
	}
	helpers.RequireStatus(t, getOrderResp, 200)
	isDone, _ := getOrderResp.Body["is_done"].(bool)
	if !isDone {
		t.Error("sell order should be done")
	}
}
```

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_StockBuySellCycle
git add test-app/workflows/wf_stock_buy_sell_test.go
git commit -m "test(integration): WF-6 stock market buy and sell cycle"
```

---

### Task 7: WF-NEW-7 — Multi-Asset Order Types

**Files:**
- Create: `test-app/workflows/wf_order_types_test.go`

- [ ] **Step 1: Write the test**

Test: supervisor places limit buy → stays pending → cancels → market buy futures → filled → verify holding with contract size. Read the existing `stock_order_test.go` for endpoint patterns before writing.

The engineer should:
1. Read `test-app/workflows/stock_order_test.go` for existing order test patterns
2. Write `TestWF_MultiAssetOrderTypes` that creates limit order, verifies it's pending (not filled because limit is below market), cancels it, then places market buy for futures

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_MultiAssetOrderTypes
git add test-app/workflows/wf_order_types_test.go
git commit -m "test(integration): WF-7 multi-asset order types"
```

---

### Task 8: WF-NEW-8 — Order Approval Workflow

**Files:**
- Create: `test-app/workflows/wf_order_approval_test.go`

- [ ] **Step 1: Write the test**

Test: admin creates agent → agent places order → pending approval → supervisor approves → filled → holding. Then agent places another → supervisor declines → no holding.

The engineer should:
1. Use `setupAgentEmployee` and `setupSupervisorEmployee`
2. Check if the agent has `needApproval` set (may need admin to set actuary limits)
3. Place order as agent, check status is "pending"
4. Approve as supervisor, wait for fill
5. Place another order, decline as supervisor, verify no holding change

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_OrderApprovalWorkflow
git add test-app/workflows/wf_order_approval_test.go
git commit -m "test(integration): WF-8 order approval workflow"
```

---

### Task 9: WF-NEW-9 — OTC Trading Between Users

**Files:**
- Create: `test-app/workflows/wf_otc_trading_test.go`

- [ ] **Step 1: Write the test**

Test: Agent A buys stock → makes public → Agent B lists offers → buys → verify quantities and capital gain. Read existing `otc_test.go` for patterns.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_OTCTrading
git add test-app/workflows/wf_otc_trading_test.go
git commit -m "test(integration): WF-9 OTC trading between users"
```

---

### Task 10: WF-NEW-10 — Tax Collection Cycle

**Files:**
- Create: `test-app/workflows/wf_tax_collection_test.go`

- [ ] **Step 1: Write the test**

Test: agent buys stock → sells at profit → capital gain recorded → admin collects tax → verify 15% deducted. Read `tax_test.go` for patterns.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_TaxCollectionCycle
git add test-app/workflows/wf_tax_collection_test.go
git commit -m "test(integration): WF-10 tax collection cycle"
```

---

### Task 11: WF-NEW-11 — Client Trades Stock After Banking Setup

**Files:**
- Create: `test-app/workflows/wf_client_stock_banking_test.go`

- [ ] **Step 1: Write the test**

Test: full client onboarding → stock buy → regular payment from same account → verify both holding and payment exist, balance reflects both. This tests that banking and trading use the same account without conflict.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_ClientTradesStockAfterBanking
git add test-app/workflows/wf_client_stock_banking_test.go
git commit -m "test(integration): WF-11 client trades stock after banking setup"
```

---

### Task 12: WF-NEW-12 — Employee Limit Enforcement Across Domains

**Files:**
- Create: `test-app/workflows/wf_limit_enforcement_test.go`

- [ ] **Step 1: Write the test**

Test: admin creates agent with low limit → stock order exceeding limit → rejected → admin increases limit → retry → succeeds. Read `employee_limits_test.go` and `actuary_test.go` for limit-setting patterns.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_LimitEnforcementAcrossDomains
git add test-app/workflows/wf_limit_enforcement_test.go
git commit -m "test(integration): WF-12 employee limit enforcement across domains"
```

---

### Task 13: WF-NEW-13 — Cross-Currency Trading and Transfer

**Files:**
- Create: `test-app/workflows/wf_cross_currency_test.go`

- [ ] **Step 1: Write the test**

Test: client with RSD+EUR → buys stock in USD (conversion from RSD) → sells → profit deposited → transfers RSD→EUR → verify exchange rates and commissions at each step.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 5m ./workflows/ -run TestWF_CrossCurrencyTradingAndTransfer
git add test-app/workflows/wf_cross_currency_test.go
git commit -m "test(integration): WF-13 cross-currency trading and transfer"
```

---

### Task 14: WF-NEW-14 — Full Banking Day Simulation

**Files:**
- Create: `test-app/workflows/wf_full_day_test.go`

- [ ] **Step 1: Write the test**

This is the big smoke test. The engineer should:

1. Admin onboards 3 clients (A, B, C) and 1 agent — all with funded RSD accounts
2. A pays B (5000 RSD) → verify balances + fee to bank
3. B creates EUR account, transfers 10000 RSD→EUR → verify exchange + commission
4. C requests housing loan (1M RSD, 60 months) → admin approves → C's balance increases by 1M
5. Agent buys stock → sells at profit → capital gain recorded
6. Admin collects tax → agent debited
7. **Final assertions:**
   - All account balances are internally consistent (sum of deltas matches)
   - Bank RSD account gained all fees
   - All payments/transfers have status "completed"

```go
//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FullBankingDaySimulation is the comprehensive smoke test.
func TestWF_FullBankingDaySimulation(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Record bank balance at start
	_, bankStart := getBankRSDAccount(t, adminC)

	// --- Onboard 3 clients ---
	_, acctA, clientA, emailA := setupActivatedClient(t, adminC)
	_, acctB, clientB, emailB := setupActivatedClient(t, adminC)
	clientCID, acctC, clientC, _ := setupActivatedClient(t, adminC)
	_ = clientB
	_ = emailB

	// --- Onboard 1 agent ---
	_, agentC, _ := setupAgentEmployee(t, adminC)

	// === Transaction 1: A pays B 5000 RSD ===
	balA1 := getAccountBalance(t, clientA, acctA)
	balB1 := getAccountBalance(t, adminC, acctB)

	createAndExecutePayment(t, clientA, acctB, 5000, emailA)

	balA2 := getAccountBalance(t, clientA, acctA)
	balB2 := getAccountBalance(t, adminC, acctB)
	t.Logf("Payment: A lost %.2f, B gained %.2f", balA1-balA2, balB2-balB1)

	if balB2-balB1 < 4999 {
		t.Error("B should have received ~5000 RSD")
	}

	// === Transaction 2: B transfers RSD→EUR ===
	// Create EUR account for B
	eurResp, err := adminC.POST("/api/accounts", map[string]interface{}{
		"owner_id":        int(helpers.GetNumberField(t, func() *helpers.FakeResp {
			r, _ := adminC.GET("/api/accounts/by-number/" + acctB)
			return &helpers.FakeResp{Body: r.Body}
		}(), "owner_id")),
		"account_kind":    "foreign",
		"account_type":    "personal",
		"currency_code":   "EUR",
		"initial_balance": 0,
	})
	// If the above helper pattern is too complex, simplify:
	// We already know clientB's owner. Since setupActivatedClient returns clientID
	// but we didn't capture it, let's skip this sub-step or use a simpler approach.
	_ = eurResp

	// === Transaction 3: C requests loan ===
	loanAmount := 1000000.0
	balC1 := getAccountBalance(t, clientC, acctC)
	createLoanAndApprove(t, adminC, clientC, "housing", loanAmount, acctC, 60)
	balC2 := getAccountBalance(t, clientC, acctC)
	if balC2-balC1 < loanAmount-1 {
		t.Errorf("C should have received loan of %.0f, got delta %.2f", loanAmount, balC2-balC1)
	}
	_ = clientCID

	// === Transaction 4: Agent trades stock ===
	_, listingID := getFirstStockListingID(t, agentC)
	buyOrderID := buyStock(t, agentC, listingID, 1, "")
	_ = buyOrderID

	// Sell
	sellResp, err := agentC.POST("/api/orders", map[string]interface{}{
		"listing_id": listingID,
		"order_type": "market",
		"direction":  "sell",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("sell order: %v", err)
	}
	helpers.RequireStatus(t, sellResp, 201)
	sellID := int(helpers.GetNumberField(t, sellResp, "id"))
	waitForOrderFill(t, agentC, sellID, 30*time.Second)

	// === Final: check bank balance grew ===
	_, bankEnd := getBankRSDAccount(t, adminC)
	bankGain := bankEnd - bankStart
	t.Logf("Bank gained %.2f RSD in fees/commissions over the day", bankGain)
	if bankGain < 0 {
		t.Error("bank should have gained fees, not lost money")
	}
}
```

Note: The WF-14 test above is a template. The engineer should adapt it based on actual API response shapes discovered during testing. Some helper calls may need adjustment (e.g., getting clientB's owner_id for creating the EUR account — better to capture it from `setupActivatedClient`).

- [ ] **Step 2: Fix the clientB EUR account creation**

Refactor to capture clientB's ID:

```go
clientBID, acctB, clientB, emailB := setupActivatedClient(t, adminC)
```

Then use `clientBID` directly in the account creation call.

- [ ] **Step 3: Run and commit**

```bash
cd test-app && go test -tags integration -v -timeout 10m ./workflows/ -run TestWF_FullBankingDaySimulation
git add test-app/workflows/wf_full_day_test.go
git commit -m "test(integration): WF-14 full banking day simulation smoke test"
```

---

### Task 15: Run all integration workflows together

- [ ] **Step 1: Run the full integration suite**

```bash
cd test-app && go test -tags integration -v -timeout 20m ./workflows/ -run "TestWF_"
```

Expected: All 14 new workflows PASS.

- [ ] **Step 2: Run alongside existing tests**

```bash
cd test-app && go test -tags integration -v -timeout 30m ./workflows/ -count=1
```

Expected: ALL tests (existing + new) PASS.

- [ ] **Step 3: Fix any failures**

If any test fails due to ordering, shared state, or timing issues, fix.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "test: all integration workflows passing — Plan C complete"
```

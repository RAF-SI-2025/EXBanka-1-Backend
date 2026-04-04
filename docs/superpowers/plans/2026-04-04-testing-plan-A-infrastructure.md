# Testing Plan A: Shared Infrastructure & Audit

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build shared test helpers and audit/fix all existing tests so Plans B and C have a solid foundation.

**Architecture:** Two helper layers — `contract/testutil/` for unit test utilities (importable by all services), and expanded `test-app/workflows/helpers_test.go` for integration test helpers. Then audit all 72 existing test files for correctness.

**Tech Stack:** Go 1.26, testify (assert/require), gorm + SQLite in-memory, segmentio/kafka-go

---

### Task 1: Create `contract/testutil/` package — DB helper

**Files:**
- Create: `contract/testutil/db.go`
- Create: `contract/testutil/db_test.go`

- [ ] **Step 1: Write the test**

```go
// contract/testutil/db_test.go
package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sampleModel struct {
	ID   uint64 `gorm:"primaryKey"`
	Name string
}

func TestSetupTestDB_MigratesAndAllowsCRUD(t *testing.T) {
	db := SetupTestDB(t, &sampleModel{})
	require.NotNil(t, db)

	err := db.Create(&sampleModel{ID: 1, Name: "test"}).Error
	require.NoError(t, err)

	var got sampleModel
	err = db.First(&got, 1).Error
	require.NoError(t, err)
	assert.Equal(t, "test", got.Name)
}

func TestSetupTestDB_IsolatedPerCall(t *testing.T) {
	db1 := SetupTestDB(t, &sampleModel{})
	db2 := SetupTestDB(t, &sampleModel{})

	_ = db1.Create(&sampleModel{ID: 1, Name: "db1"}).Error

	var got sampleModel
	err := db2.First(&got, 1).Error
	assert.Error(t, err, "db2 should be empty — databases should be isolated")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./testutil/ -v -run TestSetupTestDB`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write implementation**

```go
// contract/testutil/db.go
package testutil

import (
	"fmt"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SetupTestDB creates an isolated in-memory SQLite database with the given
// models auto-migrated. Each call produces a separate database keyed by test name.
func SetupTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("SetupTestDB: open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("SetupTestDB: raw db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("SetupTestDB: migrate: %v", err)
	}
	return db
}
```

- [ ] **Step 4: Add `gorm.io/driver/sqlite` dependency**

Run: `cd contract && go get gorm.io/driver/sqlite && go mod tidy`

- [ ] **Step 5: Run test to verify it passes**

Run: `cd contract && go test ./testutil/ -v -run TestSetupTestDB`
Expected: PASS (2 tests)

- [ ] **Step 6: Commit**

```bash
git add contract/testutil/
git commit -m "feat(contract): add testutil package with SetupTestDB helper"
```

---

### Task 2: Create `contract/testutil/` — mock Kafka producer

**Files:**
- Create: `contract/testutil/kafka.go`
- Create: `contract/testutil/kafka_test.go`

- [ ] **Step 1: Write the test**

```go
// contract/testutil/kafka_test.go
package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockKafkaProducer_CapturesEvents(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("user.created", map[string]interface{}{"id": 1})
	p.Publish("user.updated", map[string]interface{}{"id": 1})

	require.Len(t, p.Events(), 2)
	assert.Equal(t, "user.created", p.Events()[0].Topic)
	assert.Equal(t, float64(1), p.Events()[0].Data["id"])
}

func TestMockKafkaProducer_EventsByTopic(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("user.created", map[string]interface{}{"id": 1})
	p.Publish("user.created", map[string]interface{}{"id": 2})
	p.Publish("user.updated", map[string]interface{}{"id": 1})

	created := p.EventsByTopic("user.created")
	assert.Len(t, created, 2)
}

func TestMockKafkaProducer_Clear(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("topic", map[string]interface{}{})
	p.Clear()
	assert.Empty(t, p.Events())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./testutil/ -v -run TestMockKafkaProducer`
Expected: FAIL — NewMockKafkaProducer not defined

- [ ] **Step 3: Write implementation**

```go
// contract/testutil/kafka.go
package testutil

import "sync"

// MockEvent represents a captured Kafka publish.
type MockEvent struct {
	Topic string
	Data  map[string]interface{}
}

// MockKafkaProducer captures published events for test assertions.
// Thread-safe — usable from concurrent goroutines.
type MockKafkaProducer struct {
	mu     sync.Mutex
	events []MockEvent
}

func NewMockKafkaProducer() *MockKafkaProducer {
	return &MockKafkaProducer{}
}

func (p *MockKafkaProducer) Publish(topic string, data map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, MockEvent{Topic: topic, Data: data})
}

func (p *MockKafkaProducer) Events() []MockEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]MockEvent, len(p.events))
	copy(cp, p.events)
	return cp
}

func (p *MockKafkaProducer) EventsByTopic(topic string) []MockEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result []MockEvent
	for _, e := range p.events {
		if e.Topic == topic {
			result = append(result, e)
		}
	}
	return result
}

func (p *MockKafkaProducer) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./testutil/ -v -run TestMockKafkaProducer`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add contract/testutil/kafka.go contract/testutil/kafka_test.go
git commit -m "feat(contract): add MockKafkaProducer for unit tests"
```

---

### Task 3: Create `contract/testutil/` — gRPC assertion helper

**Files:**
- Create: `contract/testutil/grpc.go`
- Create: `contract/testutil/grpc_test.go`

- [ ] **Step 1: Write the test**

```go
// contract/testutil/grpc_test.go
package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequireGRPCCode_MatchingCode(t *testing.T) {
	err := status.Errorf(codes.NotFound, "not found")
	// Should not panic/fail
	RequireGRPCCode(t, err, codes.NotFound)
}

func TestRequireGRPCCode_NilErrorFails(t *testing.T) {
	mt := &testing.T{}
	RequireGRPCCode(mt, nil, codes.NotFound)
	assert.True(t, mt.Failed())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./testutil/ -v -run TestRequireGRPCCode`
Expected: FAIL — RequireGRPCCode not defined

- [ ] **Step 3: Write implementation**

```go
// contract/testutil/grpc.go
package testutil

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireGRPCCode asserts that err is a gRPC error with the expected code.
func RequireGRPCCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("RequireGRPCCode: expected gRPC error with code %s, got nil", expected)
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("RequireGRPCCode: error is not a gRPC status: %v", err)
		return
	}
	if st.Code() != expected {
		t.Fatalf("RequireGRPCCode: expected code %s, got %s (message: %s)", expected, st.Code(), st.Message())
	}
}

// RequireNoGRPCError asserts that err is nil.
func RequireNoGRPCError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			t.Fatalf("RequireNoGRPCError: got gRPC error %s: %s", st.Code(), st.Message())
		} else {
			t.Fatalf("RequireNoGRPCError: got error: %v", err)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./testutil/ -v -run TestRequireGRPCCode`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add contract/testutil/grpc.go contract/testutil/grpc_test.go
git commit -m "feat(contract): add gRPC test assertion helpers"
```

---

### Task 4: Expand integration test helpers — verification helpers

**Files:**
- Modify: `test-app/workflows/helpers_test.go`

The existing `createVerificationAndGetChallengeID` uses a hardcoded bypass code "111111". We keep that working (it's valid — see `defaultBypassCode` in verification service), but add the richer helpers.

- [ ] **Step 1: Add new verification helpers to helpers_test.go**

Append to the end of `test-app/workflows/helpers_test.go`:

```go
// createAndVerifyChallenge creates a verification challenge and verifies it
// using the Kafka email verification code. Returns the challenge ID.
// This is the preferred helper — uses the real email+code flow, not the bypass.
func createAndVerifyChallenge(t *testing.T, c *client.APIClient, sourceService string, sourceID int, email string) int {
	t.Helper()
	challengeID, code := createChallengeOnly(t, c, sourceService, sourceID, email)
	submitVerificationCode(t, c, challengeID, code)
	return challengeID
}

// createChallengeOnly creates a verification challenge and extracts the code
// from Kafka without submitting it. Returns (challengeID, code).
// Use this when you need to test the verification step itself.
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
```

- [ ] **Step 2: Run test-app build to verify compilation**

Run: `cd test-app && go build -tags integration ./workflows/`
Expected: BUILD SUCCESS (no compilation errors)

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/helpers_test.go
git commit -m "feat(test-app): add verification helpers (createAndVerifyChallenge, createChallengeOnly, submitVerificationCode)"
```

---

### Task 5: Expand integration test helpers — setup helpers

**Files:**
- Modify: `test-app/workflows/helpers_test.go`

- [ ] **Step 1: Add setup helpers**

Append to `test-app/workflows/helpers_test.go`:

```go
// setupActivatedClientWithForeignAccount creates a client with both an RSD account
// (100k initial) and a foreign currency account (10k initial). Returns all IDs and
// an authenticated client.
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
```

- [ ] **Step 2: Verify compilation**

Run: `cd test-app && go build -tags integration ./workflows/`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/helpers_test.go
git commit -m "feat(test-app): add setup helpers (foreign account, card)"
```

---

### Task 6: Expand integration test helpers — transaction & stock helpers

**Files:**
- Modify: `test-app/workflows/helpers_test.go`

- [ ] **Step 1: Add transaction and stock helpers**

Append to `test-app/workflows/helpers_test.go`:

```go
// createAndExecutePayment creates a payment, verifies via challenge, and executes it.
// Returns the payment ID.
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
// Returns the order ID.
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

// waitForOrderFill polls until an order's is_done field is true or timeout is reached.
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
// Returns the loan ID.
func createLoanAndApprove(t *testing.T, adminC *client.APIClient, clientC *client.APIClient, loanType string, amount float64, accountNum string, months int) int {
	t.Helper()

	reqResp, err := clientC.POST("/api/me/loan-requests", map[string]interface{}{
		"loan_type":          loanType,
		"interest_type":      "fixed",
		"amount":             amount,
		"currency":           "RSD",
		"purpose":            "test loan",
		"monthly_income":     50000,
		"employment_status":  "permanent",
		"employment_period":  24,
		"repayment_period":   months,
		"phone":              helpers.RandomPhone(),
		"account_number":     accountNum,
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

	// The approve response or a follow-up call returns the loan ID
	loanID := int(helpers.GetNumberField(t, approveResp, "loan_id"))
	return loanID
}

// assertBalanceChanged fetches the current balance and asserts it changed by expectedDelta
// (within tolerance for floating point).
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
```

- [ ] **Step 2: Ensure `time` import is present in helpers_test.go**

Check the imports at the top of helpers_test.go — add `"time"` if not already imported.

- [ ] **Step 3: Verify compilation**

Run: `cd test-app && go build -tags integration ./workflows/`
Expected: BUILD SUCCESS

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/helpers_test.go
git commit -m "feat(test-app): add transaction, stock, loan, and balance assertion helpers"
```

---

### Task 7: Audit existing tests — run full unit test suite

**Files:**
- No new files — this is an audit task

- [ ] **Step 1: Run all unit tests across all services**

Run:
```bash
cd user-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-user.log
cd auth-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-auth.log
cd notification-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-notification.log
cd client-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-client.log
cd account-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-account.log
cd card-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-card.log
cd transaction-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-transaction.log
cd credit-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-credit.log
cd exchange-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-exchange.log
cd stock-service && go test ./... -v -count=1 2>&1 | tee /tmp/test-stock.log
cd contract && go test ./... -v -count=1 2>&1 | tee /tmp/test-contract.log
```

Expected: Identify which tests pass and which fail. Collect all failures.

- [ ] **Step 2: For each failing test, diagnose and fix**

For each failure:
1. Read the test code and the code under test
2. Determine if the test is wrong (testing outdated behavior) or the code has a bug
3. Fix the test to match current implementation behavior
4. If the code has a bug, fix the code

- [ ] **Step 3: Re-run all tests to confirm green**

Run: `make test`
Expected: ALL PASS

- [ ] **Step 4: Commit fixes**

```bash
git add -A
git commit -m "fix: audit and fix existing unit tests across all services"
```

---

### Task 8: Audit existing tests — verify test correctness (spot check)

**Files:**
- Various existing test files

This task verifies that passing tests are actually meaningful — not just checking status 200.

- [ ] **Step 1: Audit api-gateway handler tests**

Read `api-gateway/internal/handler/auth_handler_test.go` and `employee_handler_test.go`.
Verify each test:
- Checks specific error messages, not just status codes
- Tests validation for all required fields, not just one
- Tests edge cases (empty string vs missing field)

Fix any tests that only check status without verifying the error body.

- [ ] **Step 2: Audit user-service tests**

Read `user-service/internal/service/employee_service_test.go`.
Verify:
- `TestCreateEmployee_Valid` checks all fields were persisted (not just no-error)
- `TestSetEmployeeRoles` checks the roles were actually set (query back)
- `TestResolvePermissions` checks the correct permissions list

- [ ] **Step 3: Audit exchange-service tests**

Read `exchange-service/internal/service/exchange_service_test.go`.
Verify:
- Convert tests check exact decimal amounts (not just "no error")
- Commission tests verify the rate includes the commission spread
- Sync tests verify both forward and inverse pairs were persisted

- [ ] **Step 4: Audit transaction-service tests**

Read all test files in `transaction-service/internal/service/`.
Verify:
- Fee calculation tests check decimal precision
- Saga compensation tests verify the reversal actually happened (not just that the function returned)
- Idempotency tests verify duplicate calls return same result

- [ ] **Step 5: Audit credit-service tests**

Read all test files in `credit-service/internal/service/`.
Verify:
- Installment schedule test checks amount matches formula
- Interest rate test uses correct tier for the loan amount
- Cron saga test verifies both debit and credit occurred

- [ ] **Step 6: Fix any weak or incorrect tests found**

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "fix: strengthen existing unit tests — add body/side-effect assertions"
```

---

### Task 9: Update CI workflow for new testutil package

**Files:**
- Modify: `.github/workflows/ci.yml`

The `contract` module has tests now, and any service using `contract/testutil` needs SQLite.

- [ ] **Step 1: Add `contract` to the build and test matrices**

In `.github/workflows/ci.yml`, add `contract` to the `build` job matrix (it doesn't have a `cmd/` directory, so skip the build job — just add to unit-tests) and update the `go-mod-tidy` job to include the testutil dependency.

No change needed for the lint/gofmt jobs — they already check all Go files.

Verify `contract` is already in the `go-mod-tidy` services list (it is — see the existing Makefile `tidy` target).

- [ ] **Step 2: Install SQLite for tests that need it**

Add a step before the test run in the `unit-tests` job:

```yaml
      - name: Install SQLite (for in-memory test DBs)
        run: sudo apt-get update && sudo apt-get install -y gcc libsqlite3-dev
        if: matrix.service == 'contract' || matrix.service == 'exchange-service' || matrix.service == 'transaction-service' || matrix.service == 'account-service' || matrix.service == 'card-service' || matrix.service == 'verification-service' || matrix.service == 'stock-service'
```

Also set `CGO_ENABLED=1` for those services since SQLite requires CGO:

```yaml
      - name: Test ${{ matrix.service }}
        env:
          CGO_ENABLED: 1
        run: cd ${{ matrix.service }} && go test ./... -v -count=1
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add SQLite support and contract module to test matrix"
```

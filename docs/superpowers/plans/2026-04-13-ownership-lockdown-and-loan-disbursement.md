# Ownership Lockdown, Loan Disbursement Saga, Employee On-Behalf Trading — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make loan approvals atomically debit the bank account, lock down every `/api/me/*` route so clients can only touch their own resources, and add employee-on-behalf trading routes for stocks/futures/forex/options and OTC.

**Architecture:** Three parallel workstreams. (1) account-service gains `DebitBankAccount` / `CreditBankAccount` RPCs that operate on the bank sentinel account with FOR UPDATE + idempotency by reference. (2) credit-service `ApproveLoanRequest` becomes a saga: create loan → debit bank → credit borrower → on failure, compensate. (3) api-gateway gets a shared `enforceOwnership` helper applied to 13 handlers, plus new `POST /api/v1/orders`, `POST /api/v1/otc/:id/buy`, `GET /api/v1/loans/:id` routes.

**Tech Stack:** Go 1.22+, Gin, GORM, gRPC, Protobuf, PostgreSQL, Kafka (franz-go), shopspring/decimal.

**Spec:** `docs/superpowers/specs/2026-04-13-ownership-lockdown-and-loan-disbursement-design.md`

---

## Phase 1 — account-service: DebitBankAccount / CreditBankAccount RPCs

### Task 1: Add protobuf messages and RPC definitions

**Files:**
- Modify: `contract/proto/account.proto`

- [ ] **Step 1: Add RPCs and messages to account.proto**

Add inside the `AccountGRPCService` service block:

```proto
rpc DebitBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse);
rpc CreditBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse);
```

Add new top-level messages:

```proto
message BankAccountOpRequest {
  string currency  = 1; // "RSD", "EUR", etc.
  string amount    = 2; // decimal string (4 decimal places)
  string reference = 3; // idempotency key, unique per business operation
  string reason    = 4; // audit description, e.g. "loan-disbursement:42"
}

message BankAccountOpResponse {
  string account_number = 1;
  string new_balance    = 2;
  bool   replayed       = 3; // true if this reference was already processed
}
```

- [ ] **Step 2: Regenerate protobuf**

```bash
make proto
```

Expected: `contract/accountpb/account.pb.go` and `contract/accountpb/account_grpc.pb.go` regenerate with `DebitBankAccount` and `CreditBankAccount` method stubs.

- [ ] **Step 3: Commit**

```bash
git add contract/proto/account.proto contract/accountpb/
git commit -m "feat(contract): add DebitBankAccount/CreditBankAccount RPCs

Used by credit-service loan disbursement saga to atomically debit
the bank sentinel account with idempotency-by-reference."
```

---

### Task 2: Add bank-ref idempotency table to account-service

**Files:**
- Create: `account-service/internal/model/bank_operation.go`
- Modify: `account-service/cmd/main.go` (auto-migration list)

- [ ] **Step 1: Create the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// BankOperation is an idempotency record for DebitBankAccount / CreditBankAccount.
// (reference, direction) is unique — a second call with the same reference returns
// the already-computed result instead of double-applying.
type BankOperation struct {
	ID            uint64          `gorm:"primaryKey"`
	Reference     string          `gorm:"size:128;not null;uniqueIndex:idx_bank_op_ref_dir"`
	Direction     string          `gorm:"size:16;not null;uniqueIndex:idx_bank_op_ref_dir"` // "debit" | "credit"
	Currency      string          `gorm:"size:8;not null"`
	Amount        decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	AccountNumber string          `gorm:"size:64;not null"`
	NewBalance    decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	Reason        string          `gorm:"size:255"`
	CreatedAt     time.Time
}
```

- [ ] **Step 2: Register model in main.go auto-migration**

Open `account-service/cmd/main.go`, find the `db.AutoMigrate(...)` call, add `&model.BankOperation{}` to the list.

- [ ] **Step 3: Build to verify**

```bash
cd account-service && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add account-service/internal/model/bank_operation.go account-service/cmd/main.go
git commit -m "feat(account-service): add BankOperation idempotency table"
```

---

### Task 3: Write failing tests for BankAccountRepository.DebitBank and CreditBank

**Files:**
- Create: `account-service/internal/repository/bank_account_repository_test.go`

- [ ] **Step 1: Write the tests**

```go
package repository

import (
	"testing"

	"github.com/exbanka/account-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func seedBankAccount(t *testing.T, repo *BankAccountRepository, currency string, balance string) *model.Account {
	t.Helper()
	acct := &model.Account{
		AccountNumber:    "BANK-" + currency + "-0001",
		OwnerID:          1000000000,
		IsBankAccount:    true,
		CurrencyCode:     currency,
		Balance:          decimal.RequireFromString(balance),
		AvailableBalance: decimal.RequireFromString(balance),
		Status:           "active",
	}
	require.NoError(t, repo.db.Create(acct).Error)
	return acct
}

func TestDebitBankAccount_Success(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankAccount(t, repo, "RSD", "1000000")

	resp, err := repo.DebitBank(t.Context(), "RSD", decimal.RequireFromString("50000"), "loan:1", "loan-disbursement:1")
	require.NoError(t, err)
	require.False(t, resp.Replayed)
	require.Equal(t, "950000.0000", resp.NewBalance)
}

func TestDebitBankAccount_InsufficientBalance(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankAccount(t, repo, "RSD", "100")

	_, err := repo.DebitBank(t.Context(), "RSD", decimal.RequireFromString("5000"), "loan:2", "loan-disbursement:2")
	require.ErrorIs(t, err, ErrInsufficientBankLiquidity)
}

func TestDebitBankAccount_IdempotentReplay(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankAccount(t, repo, "RSD", "1000000")

	first, err := repo.DebitBank(t.Context(), "RSD", decimal.RequireFromString("50000"), "loan:3", "loan-disbursement:3")
	require.NoError(t, err)
	require.False(t, first.Replayed)

	second, err := repo.DebitBank(t.Context(), "RSD", decimal.RequireFromString("50000"), "loan:3", "loan-disbursement:3")
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, first.NewBalance, second.NewBalance)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "950000.0000", acct.Balance.StringFixed(4))
}

func TestCreditBankAccount_Success(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankAccount(t, repo, "RSD", "1000000")

	resp, err := repo.CreditBank(t.Context(), "RSD", decimal.RequireFromString("250"), "fee:9", "fee-collection:9")
	require.NoError(t, err)
	require.False(t, resp.Replayed)
	require.Equal(t, "1000250.0000", resp.NewBalance)
}

func TestCreditBankAccount_IdempotentReplay(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankAccount(t, repo, "RSD", "1000000")

	_, err := repo.CreditBank(t.Context(), "RSD", decimal.RequireFromString("250"), "fee:10", "fee-collection:10")
	require.NoError(t, err)

	replay, err := repo.CreditBank(t.Context(), "RSD", decimal.RequireFromString("250"), "fee:10", "fee-collection:10")
	require.NoError(t, err)
	require.True(t, replay.Replayed)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "1000250.0000", acct.Balance.StringFixed(4))
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd account-service && go test ./internal/repository/ -run TestDebitBankAccount -v
```

Expected: FAIL — `BankAccountRepository`, `DebitBank`, `CreditBank`, and `ErrInsufficientBankLiquidity` are not defined.

- [ ] **Step 3: Check test helper `newTestDB`**

If `newTestDB(t *testing.T)` already exists in an existing `*_test.go` file in `account-service/internal/repository/`, note its name and import it. If not, add it to a new file `account-service/internal/repository/testing_helpers_test.go`:

```go
package repository

import (
	"testing"

	"github.com/exbanka/account-service/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Account{},
		&model.LedgerEntry{},
		&model.BankOperation{},
	))
	return db
}
```

(If an existing helper is named differently, adjust the test file references accordingly and delete this step's file.)

---

### Task 4: Implement BankAccountRepository.DebitBank and CreditBank

**Files:**
- Create: `account-service/internal/repository/bank_account_repository.go`

- [ ] **Step 1: Implement**

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/exbanka/account-service/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInsufficientBankLiquidity = errors.New("bank insufficient liquidity")
var ErrBankAccountNotFound = errors.New("bank sentinel account not found for currency")

type BankAccountRepository struct {
	db *gorm.DB
}

func NewBankAccountRepository(db *gorm.DB) *BankAccountRepository {
	return &BankAccountRepository{db: db}
}

type BankOpResult struct {
	AccountNumber string
	NewBalance    string
	Replayed      bool
}

// DebitBank atomically decreases the bank sentinel account for the given currency.
// Returns ErrInsufficientBankLiquidity if balance < amount.
// Idempotent by reference: a second call with the same reference returns the
// first result without applying the debit again.
func (r *BankAccountRepository) DebitBank(ctx context.Context, currency string, amount decimal.Decimal, reference, reason string) (*BankOpResult, error) {
	return r.applyBankOp(ctx, currency, amount, reference, reason, "debit")
}

// CreditBank atomically increases the bank sentinel account for the given currency.
// Idempotent by reference.
func (r *BankAccountRepository) CreditBank(ctx context.Context, currency string, amount decimal.Decimal, reference, reason string) (*BankOpResult, error) {
	return r.applyBankOp(ctx, currency, amount, reference, reason, "credit")
}

func (r *BankAccountRepository) applyBankOp(ctx context.Context, currency string, amount decimal.Decimal, reference, reason, direction string) (*BankOpResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive, got %s", amount.String())
	}
	if reference == "" {
		return nil, errors.New("reference is required for bank account ops")
	}

	var result *BankOpResult
	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Idempotency check: if the same (reference, direction) already exists, replay it.
		var existing model.BankOperation
		replayErr := tx.Where("reference = ? AND direction = ?", reference, direction).First(&existing).Error
		if replayErr == nil {
			result = &BankOpResult{
				AccountNumber: existing.AccountNumber,
				NewBalance:    existing.NewBalance.StringFixed(4),
				Replayed:      true,
			}
			return nil
		}
		if !errors.Is(replayErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("lookup bank_operations: %w", replayErr)
		}

		// Lock the bank sentinel row for this currency.
		var acct model.Account
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_bank_account = ? AND currency_code = ?", true, currency).
			First(&acct).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return ErrBankAccountNotFound
		}
		if findErr != nil {
			return fmt.Errorf("load bank sentinel: %w", findErr)
		}

		switch direction {
		case "debit":
			if acct.Balance.LessThan(amount) {
				return ErrInsufficientBankLiquidity
			}
			acct.Balance = acct.Balance.Sub(amount)
			acct.AvailableBalance = acct.AvailableBalance.Sub(amount)
		case "credit":
			acct.Balance = acct.Balance.Add(amount)
			acct.AvailableBalance = acct.AvailableBalance.Add(amount)
		default:
			return fmt.Errorf("unknown direction %q", direction)
		}

		if saveErr := tx.Save(&acct).Error; saveErr != nil {
			return fmt.Errorf("save bank sentinel: %w", saveErr)
		}

		op := model.BankOperation{
			Reference:     reference,
			Direction:     direction,
			Currency:      currency,
			Amount:        amount,
			AccountNumber: acct.AccountNumber,
			NewBalance:    acct.Balance,
			Reason:        reason,
		}
		if createErr := tx.Create(&op).Error; createErr != nil {
			return fmt.Errorf("record bank_operation: %w", createErr)
		}

		// Also write a ledger entry for visibility.
		ledger := &model.LedgerEntry{
			AccountNumber: acct.AccountNumber,
			EntryType:     direction,
			Amount:        amount,
			BalanceBefore: acct.Balance.Add(amountSignFlip(amount, direction)),
			BalanceAfter:  acct.Balance,
			Description:   reason,
			ReferenceID:   reference,
			ReferenceType: "bank_op",
		}
		if lerr := tx.Create(ledger).Error; lerr != nil {
			return fmt.Errorf("write ledger entry: %w", lerr)
		}

		result = &BankOpResult{
			AccountNumber: acct.AccountNumber,
			NewBalance:    acct.Balance.StringFixed(4),
			Replayed:      false,
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return result, nil
}

// amountSignFlip returns +amount for debit (previous balance was higher)
// and -amount for credit (previous balance was lower).
func amountSignFlip(amount decimal.Decimal, direction string) decimal.Decimal {
	if direction == "debit" {
		return amount
	}
	return amount.Neg()
}
```

- [ ] **Step 2: Run tests to verify they pass**

```bash
cd account-service && go test ./internal/repository/ -run TestDebitBankAccount -v
cd account-service && go test ./internal/repository/ -run TestCreditBankAccount -v
```

Expected: PASS for all 5 tests from Task 3.

- [ ] **Step 3: Commit**

```bash
git add account-service/internal/repository/bank_account_repository.go account-service/internal/repository/bank_account_repository_test.go account-service/internal/repository/testing_helpers_test.go
git commit -m "feat(account-service): add BankAccountRepository with atomic debit/credit

Operates on the bank sentinel account for a currency with FOR UPDATE
and idempotency by (reference, direction) to prevent double-apply on
saga replay."
```

---

### Task 5: Add service-layer wrapper and gRPC handler for bank ops

**Files:**
- Modify: `account-service/internal/service/account_service.go`
- Modify: `account-service/internal/handler/account_handler.go`
- Modify: `account-service/cmd/main.go` (wire the new repo into the service)

- [ ] **Step 1: Add service methods**

In `account-service/internal/service/account_service.go`, add a field and methods:

```go
// AccountService (existing struct) gains a new dependency:
type AccountService struct {
	// ... existing fields ...
	bankRepo *repository.BankAccountRepository
}

// Update the constructor NewAccountService(...) to accept and store bankRepo.
```

Add methods (place alongside existing service methods):

```go
func (s *AccountService) DebitBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid amount %q: %w", amountStr, err)
	}
	return s.bankRepo.DebitBank(ctx, currency, amount, reference, reason)
}

func (s *AccountService) CreditBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid amount %q: %w", amountStr, err)
	}
	return s.bankRepo.CreditBank(ctx, currency, amount, reference, reason)
}
```

- [ ] **Step 2: Add gRPC handlers**

In `account-service/internal/handler/account_handler.go`, add:

```go
func (h *AccountHandler) DebitBankAccount(ctx context.Context, req *accountpb.BankAccountOpRequest) (*accountpb.BankAccountOpResponse, error) {
	res, err := h.svc.DebitBankAccount(ctx, req.Currency, req.Amount, req.Reference, req.Reason)
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientBankLiquidity) {
			return nil, status.Errorf(codes.FailedPrecondition, "bank has insufficient liquidity in %s", req.Currency)
		}
		if errors.Is(err, repository.ErrBankAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "no bank sentinel account for currency %s", req.Currency)
		}
		return nil, status.Errorf(codes.Internal, "debit bank: %v", err)
	}
	return &accountpb.BankAccountOpResponse{
		AccountNumber: res.AccountNumber,
		NewBalance:    res.NewBalance,
		Replayed:      res.Replayed,
	}, nil
}

func (h *AccountHandler) CreditBankAccount(ctx context.Context, req *accountpb.BankAccountOpRequest) (*accountpb.BankAccountOpResponse, error) {
	res, err := h.svc.CreditBankAccount(ctx, req.Currency, req.Amount, req.Reference, req.Reason)
	if err != nil {
		if errors.Is(err, repository.ErrBankAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "no bank sentinel account for currency %s", req.Currency)
		}
		return nil, status.Errorf(codes.Internal, "credit bank: %v", err)
	}
	return &accountpb.BankAccountOpResponse{
		AccountNumber: res.AccountNumber,
		NewBalance:    res.NewBalance,
		Replayed:      res.Replayed,
	}, nil
}
```

Imports needed at top of file: `"context"`, `"errors"`, `"google.golang.org/grpc/codes"`, `"google.golang.org/grpc/status"`, `"github.com/exbanka/account-service/internal/repository"`, `accountpb "github.com/exbanka/contract/accountpb"`.

- [ ] **Step 3: Wire up in main.go**

In `account-service/cmd/main.go`, find where `NewAccountService(...)` is called and add `repository.NewBankAccountRepository(db)` to the argument list. Example diff:

```go
bankRepo := repository.NewBankAccountRepository(db)
svc := service.NewAccountService( /* existing args */ , bankRepo)
```

- [ ] **Step 4: Build and run tests**

```bash
cd account-service && go build ./... && go test ./...
```

Expected: clean build, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add account-service/internal/service/account_service.go account-service/internal/handler/account_handler.go account-service/cmd/main.go
git commit -m "feat(account-service): expose DebitBankAccount/CreditBankAccount via gRPC"
```

---

## Phase 2 — credit-service: loan disbursement saga

### Task 6: Update account client interface in credit-service

**Files:**
- Modify: `credit-service/internal/client/account_client.go` (or equivalent — whatever wraps the account-service gRPC client)

- [ ] **Step 1: Locate the file**

```bash
grep -rln "UpdateBalance" credit-service/internal/client/ 2>/dev/null || grep -rln "accountClient" credit-service/internal/service/
```

Expected: find the file that defines an interface for the account client used by loan_request_service.

- [ ] **Step 2: Add interface methods**

Add to the interface (keep existing `UpdateBalance`):

```go
type AccountClient interface {
	// ... existing methods ...
	DebitBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error)
	CreditBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error)
}
```

Implement them on the concrete gRPC wrapper in the same file:

```go
func (c *grpcAccountClient) DebitBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error) {
	return c.client.DebitBankAccount(ctx, &accountpb.BankAccountOpRequest{
		Currency: currency, Amount: amount, Reference: reference, Reason: reason,
	})
}

func (c *grpcAccountClient) CreditBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error) {
	return c.client.CreditBankAccount(ctx, &accountpb.BankAccountOpRequest{
		Currency: currency, Amount: amount, Reference: reference, Reason: reason,
	})
}
```

- [ ] **Step 3: Build**

```bash
cd credit-service && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add credit-service/internal/client/
git commit -m "feat(credit-service): add Debit/CreditBankAccount to account client wrapper"
```

---

### Task 7: Write failing tests for saga happy path and compensation

**Files:**
- Modify: `credit-service/internal/service/loan_request_service_test.go` (or create if absent)

- [ ] **Step 1: Write the tests**

Add these tests to the loan_request_service test file. Build on whatever mock `AccountClient` pattern already exists in the test file — if none, stub one inline:

```go
type stubAccountClient struct {
	debitBankErr  error
	debitBankResp *accountpb.BankAccountOpResponse
	creditBorrowerErr error
	compensated       bool // set true when CreditBankAccount is called
	debitCalledWith   string
	creditCalledWith  string
}

func (s *stubAccountClient) DebitBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error) {
	s.debitCalledWith = reference
	if s.debitBankErr != nil {
		return nil, s.debitBankErr
	}
	return s.debitBankResp, nil
}
func (s *stubAccountClient) CreditBankAccount(ctx context.Context, currency, amount, reference, reason string) (*accountpb.BankAccountOpResponse, error) {
	s.compensated = true
	return &accountpb.BankAccountOpResponse{}, nil
}
func (s *stubAccountClient) UpdateBalance(ctx context.Context, req *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.UpdateBalanceResponse, error) {
	s.creditCalledWith = req.AccountNumber
	if s.creditBorrowerErr != nil {
		return nil, s.creditBorrowerErr
	}
	return &accountpb.UpdateBalanceResponse{}, nil
}

func TestApproveLoanRequest_HappyPath_DebitsBankCreditsBorrower(t *testing.T) {
	svc, repo, stub := newTestLoanRequestService(t)
	reqID := seedPendingLoanRequest(t, repo, "RSD", "100000")
	stub.debitBankResp = &accountpb.BankAccountOpResponse{NewBalance: "900000.0000"}

	loan, err := svc.ApproveLoanRequest(context.Background(), reqID, 1)
	require.NoError(t, err)
	require.Equal(t, "active", loan.Status)
	require.NotEmpty(t, stub.debitCalledWith, "bank must be debited")
	require.NotEmpty(t, stub.creditCalledWith, "borrower must be credited")
	require.False(t, stub.compensated, "should not compensate on happy path")
}

func TestApproveLoanRequest_InsufficientBankLiquidity_ReturnsFailedPrecondition(t *testing.T) {
	svc, repo, stub := newTestLoanRequestService(t)
	reqID := seedPendingLoanRequest(t, repo, "RSD", "100000")
	stub.debitBankErr = status.Error(codes.FailedPrecondition, "bank has insufficient liquidity in RSD")

	_, err := svc.ApproveLoanRequest(context.Background(), reqID, 1)
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Empty(t, stub.creditCalledWith, "borrower must not be credited when bank debit fails")

	// Loan must be flagged disbursement_failed.
	loan, lerr := repo.GetLoanByRequestID(reqID)
	require.NoError(t, lerr)
	require.Equal(t, "disbursement_failed", loan.Status)
}

func TestApproveLoanRequest_BorrowerCreditFails_Compensates(t *testing.T) {
	svc, repo, stub := newTestLoanRequestService(t)
	reqID := seedPendingLoanRequest(t, repo, "RSD", "100000")
	stub.debitBankResp = &accountpb.BankAccountOpResponse{NewBalance: "900000.0000"}
	stub.creditBorrowerErr = status.Error(codes.Internal, "downstream error")

	_, err := svc.ApproveLoanRequest(context.Background(), reqID, 1)
	require.Error(t, err)
	require.True(t, stub.compensated, "must compensate bank debit when borrower credit fails")

	loan, lerr := repo.GetLoanByRequestID(reqID)
	require.NoError(t, lerr)
	require.Equal(t, "disbursement_failed", loan.Status)
}
```

Test helpers `newTestLoanRequestService`, `seedPendingLoanRequest`, `GetLoanByRequestID` may or may not already exist. If they don't, add them as stub helpers at the bottom of the test file. The intent: use an in-memory sqlite DB + stub account client.

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd credit-service && go test ./internal/service/ -run TestApproveLoanRequest -v
```

Expected: FAIL — either the methods don't compile (stub shape mismatch) or tests fail because current `ApproveLoanRequest` does not call `DebitBankAccount` and does not compensate.

- [ ] **Step 3: Commit tests as red**

```bash
git add credit-service/internal/service/loan_request_service_test.go
git commit -m "test(credit-service): add failing loan disbursement saga tests"
```

---

### Task 8: Rewrite ApproveLoanRequest to use saga

**Files:**
- Modify: `credit-service/internal/service/loan_request_service.go:126-243`

- [ ] **Step 1: Replace the disbursement block**

Keep the existing TX block (create loan + installments + mark request approved) as-is. Replace the disbursement section (currently lines 224-242) with the following saga:

```go
// Disbursement saga: bank debit → borrower credit. Either both succeed and loan
// becomes "active", or we compensate and the loan is marked "disbursement_failed".
if s.accountClient == nil {
	return loan, nil
}

reference := fmt.Sprintf("loan-disbursement:%d", loan.ID)
reason := fmt.Sprintf("loan %d disbursement to account %s", loan.ID, loan.AccountNumber)

// Step A: debit the bank sentinel account for loan.CurrencyCode.
_, debitErr := s.accountClient.DebitBankAccount(ctx, loan.CurrencyCode, loan.Amount.StringFixed(4), reference, reason)
if debitErr != nil {
	loan.Status = "disbursement_failed"
	if updateErr := s.loanRepo.Update(loan); updateErr != nil {
		log.Printf("ApproveLoanRequest: failed to flag loan %d disbursement_failed after bank debit error: %v", loan.ID, updateErr)
	}
	CreditLoanRequestTotal.WithLabelValues("disbursement_failed").Inc()
	return nil, debitErr
}

// Step B: credit the borrower's account.
_, creditErr := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
	AccountNumber:   loan.AccountNumber,
	Amount:          loan.Amount.StringFixed(4),
	UpdateAvailable: true,
})
if creditErr != nil {
	// Compensate: return the money to the bank (idempotent by reference).
	_, compErr := s.accountClient.CreditBankAccount(ctx, loan.CurrencyCode, loan.Amount.StringFixed(4), reference, "compensation for "+reason)
	if compErr != nil {
		log.Printf("ApproveLoanRequest: COMPENSATION FAILED for loan %d reference %s: %v — bank is short by %s %s", loan.ID, reference, compErr, loan.Amount.StringFixed(4), loan.CurrencyCode)
	}
	loan.Status = "disbursement_failed"
	if updateErr := s.loanRepo.Update(loan); updateErr != nil {
		log.Printf("ApproveLoanRequest: failed to flag loan %d disbursement_failed after borrower credit error: %v", loan.ID, updateErr)
	}
	CreditLoanRequestTotal.WithLabelValues("disbursement_failed").Inc()
	return nil, creditErr
}

loan.Status = "active"
if updateErr := s.loanRepo.Update(loan); updateErr != nil {
	log.Printf("ApproveLoanRequest: failed to update loan %d status to active: %v", loan.ID, updateErr)
}

// Kafka event — published after the saga, never inside the DB TX.
if s.kafkaProducer != nil {
	s.kafkaProducer.PublishLoanDisbursed(ctx, loan)
}

CreditLoanRequestTotal.WithLabelValues("approved").Inc()
return loan, nil
```

Make sure `fmt` and `log` are imported (they already are in this file). `accountpb` is already imported. Kafka helper is added in Task 10.

- [ ] **Step 2: Add a helper on `loanRepo` if needed**

If `GetLoanByRequestID` doesn't exist on the loan repo, add it in `credit-service/internal/repository/loan_repository.go`:

```go
func (r *LoanRepository) GetLoanByRequestID(requestID uint64) (*model.Loan, error) {
	var loan model.Loan
	// The existing schema does not link loan → loan_request directly; we match by
	// account_number + amount + loan_type as a fallback for tests. In production, the
	// saga only uses the loan returned from ApproveLoanRequest.
	if err := r.db.Where("loan_number IS NOT NULL").Order("id DESC").First(&loan).Error; err != nil {
		return nil, err
	}
	return &loan, nil
}
```

(Only needed if the test in Task 7 calls it. If the test was rewritten to read the loan directly from a returned pointer, this helper is unnecessary.)

- [ ] **Step 3: Run tests to verify they pass**

```bash
cd credit-service && go test ./internal/service/ -run TestApproveLoanRequest -v
```

Expected: all three saga tests pass. Existing tests must also still pass — run the full suite:

```bash
cd credit-service && go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add credit-service/internal/service/loan_request_service.go credit-service/internal/repository/loan_repository.go
git commit -m "feat(credit-service): atomic loan disbursement saga with bank debit

Loan approval now debits the bank sentinel account for the loan currency
before crediting the borrower, and compensates the bank debit if the
borrower credit fails. Insufficient bank liquidity returns
FailedPrecondition which the gateway maps to 409."
```

---

### Task 9: Add Kafka `credit.loan-disbursed` event

**Files:**
- Modify: `contract/kafka/messages.go`
- Modify: `credit-service/internal/kafka/producer.go`
- Modify: `credit-service/cmd/main.go` (add topic to `EnsureTopics`)

- [ ] **Step 1: Add message type in contract/kafka/messages.go**

```go
const TopicLoanDisbursed = "credit.loan-disbursed"

type LoanDisbursedMessage struct {
	LoanID         uint64 `json:"loan_id"`
	LoanNumber     string `json:"loan_number"`
	BorrowerID     uint64 `json:"borrower_id"`
	AccountNumber  string `json:"account_number"`
	Amount         string `json:"amount"`
	CurrencyCode   string `json:"currency_code"`
	DisbursedAt    string `json:"disbursed_at"`
}
```

- [ ] **Step 2: Add PublishLoanDisbursed in credit-service producer**

In `credit-service/internal/kafka/producer.go`:

```go
func (p *Producer) PublishLoanDisbursed(ctx context.Context, loan *model.Loan) {
	msg := contractkafka.LoanDisbursedMessage{
		LoanID:        loan.ID,
		LoanNumber:    loan.LoanNumber,
		BorrowerID:    loan.ClientID,
		AccountNumber: loan.AccountNumber,
		Amount:        loan.Amount.StringFixed(4),
		CurrencyCode:  loan.CurrencyCode,
		DisbursedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("PublishLoanDisbursed: marshal: %v", err)
		return
	}
	if err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: contractkafka.TopicLoanDisbursed,
		Value: payload,
	}); err != nil {
		log.Printf("PublishLoanDisbursed: write: %v", err)
	}
}
```

Imports: `"context"`, `"encoding/json"`, `"log"`, `"time"`, `contractkafka "github.com/exbanka/contract/kafka"`, `"github.com/exbanka/credit-service/internal/model"`, and the existing kafka import.

- [ ] **Step 3: Register topic in EnsureTopics**

In `credit-service/cmd/main.go`, find the `kafkaprod.EnsureTopics(...)` call and add `contractkafka.TopicLoanDisbursed` to the list.

- [ ] **Step 4: Build and test**

```bash
cd credit-service && go build ./... && go test ./...
```

Expected: clean build, tests pass.

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go credit-service/internal/kafka/producer.go credit-service/cmd/main.go
git commit -m "feat(credit-service): publish credit.loan-disbursed Kafka event"
```

---

## Phase 3 — api-gateway: /api/me/* ownership lockdown

### Task 10: Add `enforceOwnership` helper and `ErrNotFound` constant

**Files:**
- Modify: `api-gateway/internal/handler/validation.go`

- [ ] **Step 1: Verify `ErrNotFound` exists**

```bash
grep -n "ErrNotFound" api-gateway/internal/handler/validation.go
```

If it already exists, skip to Step 2. If not, add it next to other error constants (`ErrValidation`, `ErrUnauthorized`):

```go
const (
	// ... existing ...
	ErrNotFound = "not_found"
)
```

- [ ] **Step 2: Add the helper**

Add at the bottom of `validation.go`:

```go
// enforceOwnership verifies that the resource owner matches the JWT user_id.
// On mismatch it writes a 404 not_found response and returns a non-nil error;
// callers must `return` immediately. We use 404 (not 403) because confirming
// existence of another client's resource is itself a leak.
func enforceOwnership(c *gin.Context, ownerID uint64) error {
	uid := uint64(c.GetInt64("user_id"))
	if ownerID != uid {
		apiError(c, http.StatusNotFound, ErrNotFound, "resource not found")
		return errors.New("ownership mismatch")
	}
	return nil
}
```

Imports: add `"errors"` and `"net/http"` if not already imported (they usually are in validation.go).

- [ ] **Step 3: Write a unit test**

Create or extend `api-gateway/internal/handler/validation_test.go`:

```go
func TestEnforceOwnership_Match(t *testing.T) {
	c, w := newTestContext()
	c.Set("user_id", int64(42))
	err := enforceOwnership(c, 42)
	require.NoError(t, err)
	require.Equal(t, 200, w.Code) // unchanged
}

func TestEnforceOwnership_Mismatch(t *testing.T) {
	c, w := newTestContext()
	c.Set("user_id", int64(42))
	err := enforceOwnership(c, 99)
	require.Error(t, err)
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "not_found")
}
```

`newTestContext()` is assumed to exist already in `validation_test.go`; if not, add:

```go
func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c, w
}
```

- [ ] **Step 4: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestEnforceOwnership -v
```

Expected: PASS.

```bash
git add api-gateway/internal/handler/validation.go api-gateway/internal/handler/validation_test.go
git commit -m "feat(api-gateway): add enforceOwnership helper returning 404 on mismatch"
```

---

### Task 11: Fix GetMyLoan ownership leak

**Files:**
- Modify: `api-gateway/internal/handler/credit_handler.go:800-812`

- [ ] **Step 1: Write failing test**

Add to `api-gateway/internal/handler/credit_handler_test.go` (or create the file):

```go
func TestGetMyLoan_NotOwner_Returns404(t *testing.T) {
	stub := &stubCreditClient{getLoanResp: &creditpb.LoanProto{Id: 5, ClientId: 42}}
	h := &CreditHandler{creditClient: stub}

	c, w := newAuthedContext(99) // caller is user 99, loan belongs to 42
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	h.GetMyLoan(c)

	require.Equal(t, 404, w.Code)
}

func TestGetMyLoan_Owner_Returns200(t *testing.T) {
	stub := &stubCreditClient{getLoanResp: &creditpb.LoanProto{Id: 5, ClientId: 42}}
	h := &CreditHandler{creditClient: stub}

	c, w := newAuthedContext(42)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	h.GetMyLoan(c)

	require.Equal(t, 200, w.Code)
}
```

`stubCreditClient` must satisfy the `creditpb.CreditGRPCServiceClient` interface. If no such stub exists yet, create a minimal one that only implements `GetLoan`. `newAuthedContext(uid int64)` sets `user_id` on the gin context.

- [ ] **Step 2: Run to verify failure**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyLoan -v
```

Expected: `TestGetMyLoan_NotOwner_Returns404` FAILS because the handler returns 200 for any loan.

- [ ] **Step 3: Fix the handler**

Replace `GetMyLoan` at credit_handler.go:800:

```go
// GetMyLoan serves GET /api/me/loans/:id.
func (h *CreditHandler) GetMyLoan(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.creditClient.GetLoan(c.Request.Context(), &creditpb.GetLoanReq{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, resp.ClientId); ownErr != nil {
		return
	}
	c.JSON(http.StatusOK, loanToJSON(resp))
}
```

(Note: the response field is `ClientId`, not `OwnerId` — the spec used `OwnerId` loosely; use whatever the proto actually names.)

- [ ] **Step 4: Run to verify pass**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyLoan -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/credit_handler.go api-gateway/internal/handler/credit_handler_test.go
git commit -m "fix(api-gateway): enforce ownership on GET /api/me/loans/:id"
```

---

### Task 12: Fix GetMyPayment ownership leak

**Files:**
- Modify: `api-gateway/internal/handler/transaction_handler.go:645-658`

- [ ] **Step 1: Write failing test**

Add to `api-gateway/internal/handler/transaction_handler_test.go`:

```go
func TestGetMyPayment_NotOwner_Returns404(t *testing.T) {
	stub := &stubTxClient{getPaymentResp: &transactionpb.PaymentProto{Id: 7, ClientId: 42}}
	h := &TransactionHandler{txClient: stub}

	c, w := newAuthedContext(99)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	h.GetMyPayment(c)

	require.Equal(t, 404, w.Code)
}

func TestGetMyPayment_Owner_Returns200(t *testing.T) {
	stub := &stubTxClient{getPaymentResp: &transactionpb.PaymentProto{Id: 7, ClientId: 42}}
	h := &TransactionHandler{txClient: stub}

	c, w := newAuthedContext(42)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	h.GetMyPayment(c)

	require.Equal(t, 200, w.Code)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyPayment -v
```

- [ ] **Step 3: Fix the handler**

Replace `GetMyPayment` at transaction_handler.go:646:

```go
func (h *TransactionHandler) GetMyPayment(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.txClient.GetPayment(c.Request.Context(), &transactionpb.GetPaymentRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, resp.ClientId); ownErr != nil {
		return
	}
	c.JSON(http.StatusOK, paymentToJSON(resp))
}
```

- [ ] **Step 4: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyPayment -v
git add api-gateway/internal/handler/transaction_handler.go api-gateway/internal/handler/transaction_handler_test.go
git commit -m "fix(api-gateway): enforce ownership on GET /api/me/payments/:id"
```

---

### Task 13: Fix GetMyTransfer ownership leak

**Files:**
- Modify: `api-gateway/internal/handler/transaction_handler.go:693-705`

- [ ] **Step 1: Write failing test**

```go
func TestGetMyTransfer_NotOwner_Returns404(t *testing.T) {
	stub := &stubTxClient{getTransferResp: &transactionpb.TransferProto{Id: 12, ClientId: 42}}
	h := &TransactionHandler{txClient: stub}

	c, w := newAuthedContext(99)
	c.Params = gin.Params{{Key: "id", Value: "12"}}
	h.GetMyTransfer(c)

	require.Equal(t, 404, w.Code)
}

func TestGetMyTransfer_Owner_Returns200(t *testing.T) {
	stub := &stubTxClient{getTransferResp: &transactionpb.TransferProto{Id: 12, ClientId: 42}}
	h := &TransactionHandler{txClient: stub}

	c, w := newAuthedContext(42)
	c.Params = gin.Params{{Key: "id", Value: "12"}}
	h.GetMyTransfer(c)

	require.Equal(t, 200, w.Code)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyTransfer -v
```

- [ ] **Step 3: Fix the handler**

Replace `GetMyTransfer` at transaction_handler.go:693:

```go
func (h *TransactionHandler) GetMyTransfer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.txClient.GetTransfer(c.Request.Context(), &transactionpb.GetTransferRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, resp.ClientId); ownErr != nil {
		return
	}
	c.JSON(http.StatusOK, transferToJSON(resp))
}
```

- [ ] **Step 4: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetMyTransfer -v
git add api-gateway/internal/handler/transaction_handler.go api-gateway/internal/handler/transaction_handler_test.go
git commit -m "fix(api-gateway): enforce ownership on GET /api/me/transfers/:id"
```

---

### Task 14: Fix card PIN/block handlers (SetCardPin, VerifyCardPin, TemporaryBlockCard)

**Files:**
- Modify: `api-gateway/internal/handler/card_handler.go:423-529`

These three handlers share the same fix pattern: load the card first via `GetCard`, enforce ownership, then perform the action.

- [ ] **Step 1: Add a helper for loading+verifying a card**

Add at the bottom of `card_handler.go`:

```go
// loadCardAndEnforceOwnership fetches the card by ID and verifies the caller owns it.
// Returns the fetched card on success. On any failure it writes the error response
// and returns nil — caller must `return` on nil.
func (h *CardHandler) loadCardAndEnforceOwnership(c *gin.Context, id uint64) *cardpb.CardProto {
	resp, err := h.virtualCardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return nil
	}
	if ownErr := enforceOwnership(c, resp.OwnerId); ownErr != nil {
		return nil
	}
	return resp
}
```

Note: use whatever the card response type is actually named (`*cardpb.CardResponse`, `*cardpb.Card`, etc.) and whatever the owner field is (`OwnerId` / `ClientId`).

- [ ] **Step 2: Write failing tests**

Add to `card_handler_test.go`:

```go
func TestSetCardPin_NotOwner_Returns404(t *testing.T) {
	stub := &stubCardClient{getCardResp: &cardpb.CardProto{Id: 5, OwnerId: 42}}
	h := &CardHandler{virtualCardClient: stub}

	c, w := newAuthedContext(99)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"pin":"1234"}`))
	h.SetCardPin(c)

	require.Equal(t, 404, w.Code)
	require.False(t, stub.setCardPinCalled, "SetCardPin must not be called when ownership fails")
}

// Same pattern for VerifyCardPin and TemporaryBlockCard, two tests each
// (owner → 200/expected, not-owner → 404 + underlying RPC not called).
```

- [ ] **Step 3: Run to verify failure**

```bash
cd api-gateway && go test ./internal/handler/ -run "TestSetCardPin|TestVerifyCardPin|TestTemporaryBlockCard" -v
```

- [ ] **Step 4: Fix the three handlers**

Replace `SetCardPin` (line 423):

```go
func (h *CardHandler) SetCardPin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid card id")
		return
	}
	var body setCardPinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := validatePin(body.Pin); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if card := h.loadCardAndEnforceOwnership(c, id); card == nil {
		return
	}
	resp, err := h.virtualCardClient.SetCardPin(c.Request.Context(), &cardpb.SetCardPinRequest{Id: id, Pin: body.Pin})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
```

Replace `VerifyCardPin` (line 463) with the same pattern: after validation, call `loadCardAndEnforceOwnership`, then perform the verify RPC.

Replace `TemporaryBlockCard` (line 504) with the same pattern: after `inRange` validation, call `loadCardAndEnforceOwnership`, then perform the block RPC.

- [ ] **Step 5: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run "TestSetCardPin|TestVerifyCardPin|TestTemporaryBlockCard" -v
git add api-gateway/internal/handler/card_handler.go api-gateway/internal/handler/card_handler_test.go
git commit -m "fix(api-gateway): enforce card ownership on /api/me/cards/:id/{pin,verify-pin,temporary-block}"
```

---

### Task 15: Fix UpdatePaymentRecipient / DeletePaymentRecipient ownership leaks

**Files:**
- Modify: `api-gateway/internal/handler/transaction_handler.go:564-611`

- [ ] **Step 1: Add recipient loader helper**

At the bottom of `transaction_handler.go`:

```go
func (h *TransactionHandler) loadRecipientAndEnforceOwnership(c *gin.Context, id uint64) *transactionpb.PaymentRecipientProto {
	resp, err := h.txClient.GetPaymentRecipient(c.Request.Context(), &transactionpb.GetPaymentRecipientRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return nil
	}
	if ownErr := enforceOwnership(c, resp.ClientId); ownErr != nil {
		return nil
	}
	return resp
}
```

**Dependency check:** verify `GetPaymentRecipient` RPC exists in `contract/transactionpb/`. If not, add it to `contract/proto/transaction.proto` and regenerate:

```proto
rpc GetPaymentRecipient(GetPaymentRecipientRequest) returns (PaymentRecipientProto);
message GetPaymentRecipientRequest { uint64 id = 1; }
```

Implement the RPC in `transaction-service/internal/handler/` + `transaction-service/internal/service/` — simple lookup by ID returning `client_id` and other existing fields.

- [ ] **Step 2: Write failing tests**

```go
func TestUpdatePaymentRecipient_NotOwner_Returns404(t *testing.T) {
	stub := &stubTxClient{getRecipientResp: &transactionpb.PaymentRecipientProto{Id: 3, ClientId: 42}}
	h := &TransactionHandler{txClient: stub}
	c, w := newAuthedContext(99)
	c.Params = gin.Params{{Key: "id", Value: "3"}}
	c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(`{"recipient_name":"x","account_number":"y"}`))
	h.UpdatePaymentRecipient(c)
	require.Equal(t, 404, w.Code)
	require.False(t, stub.updateRecipientCalled)
}
// Same pattern for DeletePaymentRecipient.
```

- [ ] **Step 3: Run to verify failure**

```bash
cd api-gateway && go test ./internal/handler/ -run "TestUpdatePaymentRecipient|TestDeletePaymentRecipient" -v
```

- [ ] **Step 4: Fix the handlers**

Replace `UpdatePaymentRecipient` (line 564):

```go
func (h *TransactionHandler) UpdatePaymentRecipient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	var req updatePaymentRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if rec := h.loadRecipientAndEnforceOwnership(c, id); rec == nil {
		return
	}
	pbReq := &transactionpb.UpdatePaymentRecipientRequest{Id: id}
	pbReq.RecipientName = req.RecipientName
	pbReq.AccountNumber = req.AccountNumber
	resp, err := h.txClient.UpdatePaymentRecipient(c.Request.Context(), pbReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
```

Replace `DeletePaymentRecipient` (line 598) with the same pattern: parse id, load + enforce, then delete.

- [ ] **Step 5: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run "TestUpdatePaymentRecipient|TestDeletePaymentRecipient" -v
cd transaction-service && go build ./... && go test ./...
git add api-gateway/internal/handler/transaction_handler.go api-gateway/internal/handler/transaction_handler_test.go contract/proto/transaction.proto contract/transactionpb/ transaction-service/
git commit -m "fix(api-gateway): enforce ownership on payment-recipient update/delete"
```

---

### Task 16: Drop client_id from POST /api/me/loan-requests body

**Files:**
- Modify: `api-gateway/internal/handler/credit_handler.go:48-108`

- [ ] **Step 1: Write failing test**

```go
func TestCreateLoanRequest_IgnoresBodyClientId(t *testing.T) {
	stub := &stubCreditClient{}
	h := &CreditHandler{creditClient: stub}

	c, w := newAuthedContext(42)
	body := `{"client_id":99,"loan_type":"cash","interest_type":"fixed","amount":"1000","repayment_period":12,"account_number":"A","currency_code":"RSD","employment_status":"employed","monthly_income":"5000","employment_duration":12}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateLoanRequest(c)

	require.Equal(t, 201, w.Code, "request body: %s", w.Body.String())
	require.Equal(t, uint64(42), stub.lastCreateLoanReq.ClientId, "client_id must come from JWT, not body")
}
```

- [ ] **Step 2: Run to verify failure**

Expected: `stub.lastCreateLoanReq.ClientId` is `99` (body value), not `42` (JWT value).

- [ ] **Step 3: Fix the handler**

In `CreateLoanRequest` at line 48, change the gRPC call to derive `ClientId` from `c.GetInt64("user_id")` instead of `req.ClientID`. Remove `ClientID` from the request struct entirely — it becomes ignored. The struct decoder drops unknown fields silently so frontends still sending it continue to work.

Apply this diff:

```go
// In createLoanRequestBody struct (or wherever it's defined), DELETE the ClientID field:
// 	ClientID uint64 `json:"client_id"`   <-- remove

// In CreateLoanRequest, replace the gRPC call's ClientId assignment with:
userID := c.GetInt64("user_id")
grpcReq := &creditpb.CreateLoanRequestReq{
	ClientId:          uint64(userID),
	// ... all other fields unchanged ...
}
```

(If the handler currently takes `ClientId` from `req.ClientID`, change that one line.)

- [ ] **Step 4: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestCreateLoanRequest -v
git add api-gateway/internal/handler/credit_handler.go api-gateway/internal/handler/credit_handler_test.go
git commit -m "fix(api-gateway): derive client_id from JWT on POST /api/me/loan-requests

Body client_id is now silently ignored. Frontend will update
at its convenience during v1 transition."
```

---

### Task 17: Fix POST /api/me/orders to verify account ownership (client path)

**Files:**
- Modify: `api-gateway/internal/handler/stock_order_handler.go:19-100`
- Modify: `api-gateway/internal/handler/stock_order_handler.go` struct (to accept an account client)

- [ ] **Step 1: Inject account client**

Add an `accountClient accountpb.AccountGRPCServiceClient` field to the `StockOrderHandler` struct. Update `NewStockOrderHandler(stockClient, accountClient)` and its call site in the router setup (`api-gateway/cmd/main.go` or `api-gateway/internal/router/router_v1.go`).

- [ ] **Step 2: Write failing test**

```go
func TestCreateOrder_BuyAccountNotOwnedByCaller_Returns404(t *testing.T) {
	stock := &stubStockClient{}
	acct := &stubAccountClient{getAccountResp: &accountpb.AccountProto{Id: 101, OwnerId: 42}}
	h := &StockOrderHandler{client: stock, accountClient: acct}

	c, w := newAuthedContext(99)
	body := `{"listing_id":7,"direction":"buy","order_type":"market","quantity":10,"account_id":101}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateOrder(c)

	require.Equal(t, 404, w.Code)
	require.False(t, stock.createOrderCalled)
}

func TestCreateOrder_BuyAccountOwnedByCaller_Succeeds(t *testing.T) {
	stock := &stubStockClient{createOrderResp: &stockpb.OrderProto{Id: 5}}
	acct := &stubAccountClient{getAccountResp: &accountpb.AccountProto{Id: 101, OwnerId: 42}}
	h := &StockOrderHandler{client: stock, accountClient: acct}

	c, w := newAuthedContext(42)
	body := `{"listing_id":7,"direction":"buy","order_type":"market","quantity":10,"account_id":101}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateOrder(c)

	require.Equal(t, 201, w.Code, w.Body.String())
	require.True(t, stock.createOrderCalled)
}
```

- [ ] **Step 3: Run to verify failure**

- [ ] **Step 4: Fix the handler**

In `CreateOrder`, after all other validation and before the `CreateOrder` gRPC call, when `direction == "buy"`:

```go
if direction == "buy" {
	acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
		return
	}
}
```

Use whatever the account RPC and proto are actually named. The stock-service backend continues to enforce holding ownership for `sell` orders — no gateway-side change for sells.

- [ ] **Step 5: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestCreateOrder -v
git add api-gateway/internal/handler/stock_order_handler.go api-gateway/internal/handler/stock_order_handler_test.go api-gateway/internal/router/ api-gateway/cmd/main.go
git commit -m "fix(api-gateway): verify account ownership on POST /api/me/orders"
```

---

### Task 18: Fix POST /api/me/otc/:id/buy to verify account ownership

**Files:**
- Modify: `api-gateway/internal/handler/portfolio_handler.go:129-164`

- [ ] **Step 1: Ensure PortfolioHandler has account client**

Add `accountClient accountpb.AccountGRPCServiceClient` to `PortfolioHandler` struct and constructor + router wiring.

- [ ] **Step 2: Write failing test**

```go
func TestBuyOTCOffer_AccountNotOwnedByCaller_Returns404(t *testing.T) {
	otc := &stubOTCClient{}
	acct := &stubAccountClient{getAccountResp: &accountpb.AccountProto{Id: 101, OwnerId: 42}}
	h := &PortfolioHandler{otcClient: otc, accountClient: acct}

	c, w := newAuthedContext(99)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"quantity":10,"account_id":101}`))
	h.BuyOTCOffer(c)

	require.Equal(t, 404, w.Code)
	require.False(t, otc.buyCalled)
}
```

- [ ] **Step 3: Run to verify failure**

- [ ] **Step 4: Fix the handler**

In `BuyOTCOffer` at portfolio_handler.go:129, after validation and before the `BuyOffer` gRPC call:

```go
acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
if err != nil {
	handleGRPCError(c, err)
	return
}
if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
	return
}
```

- [ ] **Step 5: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestBuyOTCOffer -v
git add api-gateway/internal/handler/portfolio_handler.go api-gateway/internal/handler/portfolio_handler_test.go
git commit -m "fix(api-gateway): verify account ownership on POST /api/me/otc/:id/buy"
```

---

### Task 19: Verify ExerciseOption holding ownership in stock-service

**Files:**
- Investigate: `stock-service/internal/service/portfolio_service.go` (or similar)
- Modify (if needed): same file

- [ ] **Step 1: Audit current behavior**

```bash
grep -n "ExerciseOption\|HoldingId\|OwnerId\|ClientId" stock-service/internal/service/*.go | head -40
```

Read the `ExerciseOption` service method. Does it verify the holding's owning account belongs to the `user_id` passed in?

- [ ] **Step 2: Write a failing test (only if the audit shows a bug)**

If the audit shows no ownership check:

```go
func TestExerciseOption_NotOwner_ReturnsPermissionDenied(t *testing.T) {
	svc, _ := newTestPortfolioService(t)
	holdingID := seedHoldingForUser(t, svc, 42)

	_, err := svc.ExerciseOption(context.Background(), holdingID, 99) // wrong user
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
```

- [ ] **Step 3: Fix the service (if broken)**

Inside `ExerciseOption(holdingID, userID)`, after loading the holding:

```go
holding, err := s.holdingRepo.GetByID(holdingID)
if err != nil {
	return nil, status.Errorf(codes.NotFound, "holding not found")
}
// New check:
if holding.UserID != userID { // or holding.Account.OwnerID, depending on schema
	return nil, status.Errorf(codes.NotFound, "holding not found")
}
```

Return `NotFound` (not `PermissionDenied`) so the gateway maps it to 404 and existence is not leaked.

- [ ] **Step 4: Run tests**

```bash
cd stock-service && go test ./internal/service/ -run TestExerciseOption -v
```

- [ ] **Step 5: Commit (only if changes were made)**

```bash
git add stock-service/internal/service/
git commit -m "fix(stock-service): enforce holding ownership on ExerciseOption"
```

If audit showed the check was already in place, add a comment noting "verified: ExerciseOption enforces ownership" and skip the commit.

---

### Task 20: Add POST /api/me/cards/virtual (client route) + keep employee /api/cards/virtual

**Files:**
- Modify: `api-gateway/internal/handler/card_handler.go:369-407`
- Modify: `api-gateway/internal/router/router_v1.go`

- [ ] **Step 1: Create a new handler method `CreateMyVirtualCard`**

Add alongside the existing `CreateVirtualCard`:

```go
// CreateMyVirtualCard serves POST /api/me/cards/virtual.
// Unlike the employee route, owner_id is derived from the JWT and the
// provided account_number must belong to the caller.
func (h *CardHandler) CreateMyVirtualCard(c *gin.Context) {
	var body createVirtualCardBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	vcBrand, err := oneOf("card_brand", body.CardBrand, "visa", "mastercard", "dinacard", "amex")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	usageType, err := oneOf("usage_type", body.UsageType, "single_use", "multi_use")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := inRange("expiry_months", body.ExpiryMonths, 1, 3); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if usageType == "multi_use" && body.MaxUses < 2 {
		apiError(c, 400, ErrValidation, "multi_use cards must have max_uses >= 2")
		return
	}

	// Verify the account belongs to the caller.
	userID := uint64(c.GetInt64("user_id"))
	acctResp, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: body.AccountNumber})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if acctResp.OwnerId != userID {
		apiError(c, http.StatusNotFound, ErrNotFound, "resource not found")
		return
	}

	resp, err := h.virtualCardClient.CreateVirtualCard(c.Request.Context(), &cardpb.CreateVirtualCardRequest{
		AccountNumber: body.AccountNumber,
		OwnerId:       userID, // forced from JWT
		CardBrand:     vcBrand,
		UsageType:     usageType,
		ExpiryMonths:  body.ExpiryMonths,
		MaxUses:       body.MaxUses,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
```

`CardHandler` needs an `accountClient` field — add it and update the constructor + router wiring. Use the actual `GetAccountByNumber` RPC name from `accountpb`.

- [ ] **Step 2: Wire the route**

In `router_v1.go`, find the `/api/me/cards` group and add:

```go
me.POST("/cards/virtual", cardHandler.CreateMyVirtualCard)
```

The existing `/api/cards/virtual` route stays unchanged under the employee path (AuthMiddleware + `cards.manage` permission). Verify that existing route is under `AuthMiddleware` and not `AnyAuthMiddleware`; if it's currently under `AnyAuth`, move it to employee-only.

- [ ] **Step 3: Write tests**

```go
func TestCreateMyVirtualCard_AccountNotOwned_Returns404(t *testing.T) {
	card := &stubCardClient{}
	acct := &stubAccountClient{getAccountByNumberResp: &accountpb.AccountProto{AccountNumber: "X", OwnerId: 42}}
	h := &CardHandler{virtualCardClient: card, accountClient: acct}

	c, w := newAuthedContext(99)
	body := `{"card_brand":"visa","usage_type":"single_use","expiry_months":1,"account_number":"X"}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateMyVirtualCard(c)

	require.Equal(t, 404, w.Code)
	require.False(t, card.createVirtualCardCalled)
}

func TestCreateMyVirtualCard_AccountOwned_ForcesOwnerFromJWT(t *testing.T) {
	card := &stubCardClient{createVirtualCardResp: &cardpb.CardProto{Id: 1}}
	acct := &stubAccountClient{getAccountByNumberResp: &accountpb.AccountProto{AccountNumber: "X", OwnerId: 42}}
	h := &CardHandler{virtualCardClient: card, accountClient: acct}

	c, w := newAuthedContext(42)
	body := `{"card_brand":"visa","usage_type":"single_use","expiry_months":1,"account_number":"X"}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateMyVirtualCard(c)

	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, uint64(42), card.lastCreateReq.OwnerId, "owner must come from JWT")
}
```

- [ ] **Step 4: Run and commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestCreateMyVirtualCard -v
git add api-gateway/
git commit -m "feat(api-gateway): add POST /api/me/cards/virtual with JWT-forced owner"
```

---

## Phase 4 — api-gateway: new v1 routes

### Task 21: Add GET /api/v1/loans/:id (employee lookup)

**Files:**
- Modify: `api-gateway/internal/handler/credit_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`

- [ ] **Step 1: Write the handler**

Add to `credit_handler.go`:

```go
// GetLoan serves GET /api/v1/loans/:id.
// Employee-facing lookup — no ownership check, trusted by permission.
//
// @Summary     Get a loan by ID (employee)
// @Tags        credits
// @Produce     json
// @Param       id path integer true "Loan ID"
// @Success     200 {object} map[string]interface{}
// @Failure     400 {object} map[string]interface{}
// @Failure     404 {object} map[string]interface{}
// @Router      /v1/loans/{id} [get]
// @Security    BearerAuth
func (h *CreditHandler) GetLoan(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.creditClient.GetLoan(c.Request.Context(), &creditpb.GetLoanReq{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, loanToJSON(resp))
}
```

- [ ] **Step 2: Wire the route**

In `router_v1.go`, add under an employee-authenticated group with `credits.read` permission:

```go
loans := v1.Group("/loans", middleware.AuthMiddleware(jwtKey, cacheClient), middleware.RequirePermission("credits.read"))
{
	loans.GET("/:id", creditHandler.GetLoan)
}
```

- [ ] **Step 3: Write tests**

```go
func TestGetLoan_EmployeeLookup_Returns200(t *testing.T) {
	stub := &stubCreditClient{getLoanResp: &creditpb.LoanProto{Id: 5, ClientId: 42}}
	h := &CreditHandler{creditClient: stub}

	c, w := newAuthedContext(0) // employee context — no ownership check
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	h.GetLoan(c)

	require.Equal(t, 200, w.Code)
}
```

- [ ] **Step 4: Run, regenerate Swagger, commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestGetLoan -v && make swagger
git add api-gateway/
git commit -m "feat(api-gateway): add GET /api/v1/loans/:id employee lookup"
```

---

### Task 22: Add protobuf fields for on-behalf orders in stock-service

**Files:**
- Modify: `contract/proto/stock.proto` (whatever file defines `CreateOrderRequest` and `BuyOTCOfferRequest`)
- Modify: `stock-service/internal/model/order.go` (add `ActingEmployeeID`)
- Modify: `stock-service/internal/service/order_service.go` (honor the new field)

- [ ] **Step 1: Add proto fields**

```proto
message CreateOrderRequest {
  // ... existing fields ...
  uint64 acting_employee_id    = N;      // set when an employee places on behalf
  uint64 on_behalf_of_client_id = N+1;  // set when acting_employee_id is set
}

message BuyOTCOfferRequest {
  // ... existing fields ...
  uint64 acting_employee_id     = N;
  uint64 on_behalf_of_client_id = N+1;
}

message OrderProto {
  // ... existing fields ...
  uint64 acting_employee_id = N; // 0 when self-placed
}
```

Pick the next available field numbers without breaking existing ones.

- [ ] **Step 2: Regenerate protobuf**

```bash
make proto
```

- [ ] **Step 3: Add column on Order model**

```go
// In stock-service/internal/model/order.go
type Order struct {
	// ... existing fields ...
	ActingEmployeeID uint64 `gorm:"default:0"`
}
```

Auto-migration will add the column.

- [ ] **Step 4: Honor the field in CreateOrder service**

In `stock-service/internal/service/order_service.go`, inside `CreateOrder`:

```go
// If acting_employee_id is set, this is an on-behalf order. The caller
// (api-gateway) has already verified that account belongs to client.
// The user_id on the order is the target client, and acting_employee_id
// is recorded for audit.
if req.ActingEmployeeId != 0 {
	if req.OnBehalfOfClientId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "on_behalf_of_client_id required when acting_employee_id set")
	}
	order.UserID = req.OnBehalfOfClientId
	order.ActingEmployeeID = req.ActingEmployeeId
	// TODO for a future PR: enforce acting employee MaxSingleTransaction via user-service
} else {
	order.UserID = req.UserId
}
```

Same pattern in `BuyOTCOffer` service method: set `BuyerID` from `OnBehalfOfClientId` when `ActingEmployeeId != 0`.

- [ ] **Step 5: Include acting_employee_id in the returned OrderProto**

In whatever maps `Order → OrderProto`, add `ActingEmployeeId: order.ActingEmployeeID`.

- [ ] **Step 6: Build and test**

```bash
cd stock-service && go build ./... && go test ./...
```

- [ ] **Step 7: Commit**

```bash
git add contract/proto/ contract/stockpb/ stock-service/
git commit -m "feat(stock-service): support acting_employee_id on orders and OTC buys"
```

---

### Task 23: Add POST /api/v1/orders (employee on-behalf)

**Files:**
- Modify: `api-gateway/internal/handler/stock_order_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`

- [ ] **Step 1: Add CreateOrderOnBehalf handler**

In `stock_order_handler.go`:

```go
// CreateOrderOnBehalf serves POST /api/v1/orders for employees placing trades on behalf of a client.
//
// @Summary     Place stock/futures/forex/option order on behalf of a client
// @Tags        orders
// @Accept      json
// @Produce     json
// @Param       body body createOrderOnBehalfBody true "Order"
// @Success     201 {object} map[string]interface{}
// @Failure     400,403,404,409 {object} map[string]interface{}
// @Router      /v1/orders [post]
// @Security    BearerAuth
func (h *StockOrderHandler) CreateOrderOnBehalf(c *gin.Context) {
	var req struct {
		ClientID   uint64  `json:"client_id"`
		AccountID  uint64  `json:"account_id"`
		ListingID  uint64  `json:"listing_id"`
		HoldingID  uint64  `json:"holding_id"`
		Direction  string  `json:"direction"`
		OrderType  string  `json:"order_type"`
		Quantity   int64   `json:"quantity"`
		LimitValue *string `json:"limit_value"`
		StopValue  *string `json:"stop_value"`
		AllOrNone  bool    `json:"all_or_none"`
		Margin     bool    `json:"margin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 {
		apiError(c, 400, ErrValidation, "client_id is required")
		return
	}
	direction, err := oneOf("direction", req.Direction, "buy", "sell")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	orderType, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if direction == "buy" {
		if req.ListingID == 0 {
			apiError(c, 400, ErrValidation, "listing_id is required for buy orders")
			return
		}
		if req.AccountID == 0 {
			apiError(c, 400, ErrValidation, "account_id is required for buy orders")
			return
		}
	}
	if direction == "sell" && req.HoldingID == 0 {
		apiError(c, 400, ErrValidation, "holding_id is required for sell orders")
		return
	}
	if (orderType == "limit" || orderType == "stop_limit") && req.LimitValue == nil {
		apiError(c, 400, ErrValidation, "limit_value is required for limit/stop_limit orders")
		return
	}
	if (orderType == "stop" || orderType == "stop_limit") && req.StopValue == nil {
		apiError(c, 400, ErrValidation, "stop_value is required for stop/stop_limit orders")
		return
	}

	// Verify the account belongs to the named client (only for buy — sell uses holding).
	if direction == "buy" {
		acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		if acctResp.OwnerId != req.ClientID {
			apiError(c, http.StatusForbidden, ErrForbidden, "account does not belong to client")
			return
		}
	}

	employeeID := uint64(c.GetInt64("user_id"))

	grpcReq := &stockpb.CreateOrderRequest{
		UserId:              req.ClientID, // target client
		SystemType:          "employee",
		ListingId:           req.ListingID,
		HoldingId:           req.HoldingID,
		Direction:           direction,
		OrderType:           orderType,
		Quantity:            req.Quantity,
		AllOrNone:           req.AllOrNone,
		Margin:              req.Margin,
		AccountId:           req.AccountID,
		ActingEmployeeId:    employeeID,
		OnBehalfOfClientId:  req.ClientID,
	}
	if req.LimitValue != nil {
		grpcReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		grpcReq.StopValue = req.StopValue
	}

	resp, err := h.client.CreateOrder(c.Request.Context(), grpcReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
```

Add `ErrForbidden = "forbidden"` to `validation.go` if not already defined.

- [ ] **Step 2: Wire the route**

In `router_v1.go`, under the employee group with `securities.manage` permission:

```go
v1Orders := v1.Group("/orders", middleware.AuthMiddleware(jwtKey, cacheClient), middleware.RequirePermission("securities.manage"))
{
	v1Orders.POST("", stockOrderHandler.CreateOrderOnBehalf)
}
```

- [ ] **Step 3: Write tests**

```go
func TestCreateOrderOnBehalf_HappyPath(t *testing.T) {
	stock := &stubStockClient{createOrderResp: &stockpb.OrderProto{Id: 5, ActingEmployeeId: 1}}
	acct := &stubAccountClient{getAccountResp: &accountpb.AccountProto{Id: 101, OwnerId: 42}}
	h := &StockOrderHandler{client: stock, accountClient: acct}

	c, w := newAuthedContext(1) // employee user_id=1
	body := `{"client_id":42,"account_id":101,"listing_id":7,"direction":"buy","order_type":"market","quantity":10}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateOrderOnBehalf(c)

	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, uint64(1), stock.lastCreateOrderReq.ActingEmployeeId)
	require.Equal(t, uint64(42), stock.lastCreateOrderReq.OnBehalfOfClientId)
}

func TestCreateOrderOnBehalf_AccountNotOwnedByClient_Returns403(t *testing.T) {
	stock := &stubStockClient{}
	acct := &stubAccountClient{getAccountResp: &accountpb.AccountProto{Id: 101, OwnerId: 999}}
	h := &StockOrderHandler{client: stock, accountClient: acct}

	c, w := newAuthedContext(1)
	body := `{"client_id":42,"account_id":101,"listing_id":7,"direction":"buy","order_type":"market","quantity":10}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateOrderOnBehalf(c)

	require.Equal(t, 403, w.Code)
	require.False(t, stock.createOrderCalled)
}
```

- [ ] **Step 4: Run, regenerate Swagger, commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestCreateOrderOnBehalf -v && make swagger
git add api-gateway/
git commit -m "feat(api-gateway): add POST /api/v1/orders for employee on-behalf trades"
```

---

### Task 24: Add POST /api/v1/otc/:id/buy (employee on-behalf)

**Files:**
- Modify: `api-gateway/internal/handler/portfolio_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`

- [ ] **Step 1: Add handler**

```go
// BuyOTCOfferOnBehalf serves POST /api/v1/otc/:id/buy for employee on-behalf.
//
// @Summary     Buy an OTC offer on behalf of a client
// @Tags        otc
// @Accept      json
// @Produce     json
// @Param       id path integer true "Offer ID"
// @Param       body body map[string]interface{} true "Purchase"
// @Success     200 {object} map[string]interface{}
// @Failure     400,403,404 {object} map[string]interface{}
// @Router      /v1/otc/{id}/buy [post]
// @Security    BearerAuth
func (h *PortfolioHandler) BuyOTCOfferOnBehalf(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid offer id")
		return
	}
	var req struct {
		ClientID  uint64 `json:"client_id"`
		AccountID uint64 `json:"account_id"`
		Quantity  int64  `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 || req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "client_id and account_id are required")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}

	acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if acctResp.OwnerId != req.ClientID {
		apiError(c, http.StatusForbidden, ErrForbidden, "account does not belong to client")
		return
	}

	employeeID := uint64(c.GetInt64("user_id"))
	resp, err := h.otcClient.BuyOffer(c.Request.Context(), &stockpb.BuyOTCOfferRequest{
		OfferId:            id,
		BuyerId:            req.ClientID,
		SystemType:         "employee",
		Quantity:           req.Quantity,
		AccountId:          req.AccountID,
		ActingEmployeeId:   employeeID,
		OnBehalfOfClientId: req.ClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
```

- [ ] **Step 2: Wire the route**

```go
v1Otc := v1.Group("/otc", middleware.AuthMiddleware(jwtKey, cacheClient), middleware.RequirePermission("securities.manage"))
{
	v1Otc.POST("/:id/buy", portfolioHandler.BuyOTCOfferOnBehalf)
}
```

- [ ] **Step 3: Write tests** (same pattern as Task 23 — happy path + account-not-owned-by-client → 403).

- [ ] **Step 4: Run, regenerate Swagger, commit**

```bash
cd api-gateway && go test ./internal/handler/ -run TestBuyOTCOfferOnBehalf -v && make swagger
git add api-gateway/
git commit -m "feat(api-gateway): add POST /api/v1/otc/:id/buy for employee on-behalf OTC purchases"
```

---

## Phase 5 — integration tests

### Task 25: Integration test for loan disbursement

**Files:**
- Create: `test-app/workflows/loan_disbursement_test.go`

- [ ] **Step 1: Write test**

```go
package workflows

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoanApproval_BankBalanceDecreases(t *testing.T) {
	ctx := newTestContext(t)
	client := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "credits.manage")

	acct := client.createRSDAccount(t)
	client.depositForInitialBalance(t, acct, "0")

	bankBefore := ctx.bankBalance(t, "RSD")

	loanReq := client.createLoanRequest(t, acct.AccountNumber, "50000", 12, "cash", "fixed")
	loan := employee.approveLoanRequest(t, loanReq.ID)

	bankAfter := ctx.bankBalance(t, "RSD")
	clientBalance := client.getAccountBalance(t, acct.AccountNumber)

	require.Equal(t, "active", loan.Status)
	require.Equal(t, subDec(bankBefore, "50000"), bankAfter, "bank balance must decrease by loan amount")
	require.Equal(t, "50000", clientBalance, "borrower account must increase by loan amount")

	ctx.expectKafka(t, "credit.loan-disbursed", func(msg []byte) bool {
		return containsLoanID(msg, loan.ID)
	})
}

func TestLoanApproval_InsufficientBankLiquidity_Returns409(t *testing.T) {
	ctx := newTestContext(t)
	ctx.setBankBalance(t, "RSD", "100")
	client := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "credits.manage")
	acct := client.createRSDAccount(t)

	loanReq := client.createLoanRequest(t, acct.AccountNumber, "50000", 12, "cash", "fixed")
	resp := employee.approveLoanRequestRaw(t, loanReq.ID)

	require.Equal(t, 409, resp.StatusCode)
	balance := client.getAccountBalance(t, acct.AccountNumber)
	require.Equal(t, "0", balance, "borrower must not be credited on failed disbursement")
}
```

Use the existing `test-app/workflows/helpers_test.go` for `newTestContext`, `newClient`, `newEmployeeWithPermission`, Kafka helpers. Adapt the method names to match actual helpers.

- [ ] **Step 2: Run**

```bash
cd test-app && go test ./workflows/ -run TestLoanApproval -v
```

Expected: PASS (requires local docker-compose stack).

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/loan_disbursement_test.go
git commit -m "test(workflows): integration test for atomic loan disbursement"
```

---

### Task 26: Integration tests for /api/me/* ownership lockdown

**Files:**
- Create: `test-app/workflows/ownership_lockdown_test.go`

- [ ] **Step 1: Write tests**

```go
package workflows

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientCannotReadOtherClientLoan(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "credits.manage")

	aliceAcct := alice.createRSDAccount(t)
	aliceReq := alice.createLoanRequest(t, aliceAcct.AccountNumber, "10000", 12, "cash", "fixed")
	aliceLoan := employee.approveLoanRequest(t, aliceReq.ID)

	resp := bob.rawGET(t, "/api/me/loans/"+itoa(aliceLoan.ID))
	require.Equal(t, 404, resp.StatusCode)
}

func TestClientCannotSetPinOnOtherClientCard(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	aliceAcct := alice.createRSDAccount(t)
	aliceCard := alice.createCard(t, aliceAcct.AccountNumber)

	resp := bob.rawPOST(t, "/api/me/cards/"+itoa(aliceCard.ID)+"/pin", `{"pin":"1234"}`)
	require.Equal(t, 404, resp.StatusCode)
}

func TestClientCannotBlockOtherClientCard(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	aliceAcct := alice.createRSDAccount(t)
	aliceCard := alice.createCard(t, aliceAcct.AccountNumber)

	resp := bob.rawPOST(t, "/api/me/cards/"+itoa(aliceCard.ID)+"/temporary-block", `{"duration_hours":1,"reason":"test"}`)
	require.Equal(t, 404, resp.StatusCode)
}

func TestClientCannotReadOtherClientPayment(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	aliceAcct := alice.createRSDAccount(t)
	alicePayment := alice.createPayment(t, aliceAcct, "100")

	resp := bob.rawGET(t, "/api/me/payments/"+itoa(alicePayment.ID))
	require.Equal(t, 404, resp.StatusCode)
}

func TestClientCannotPlaceOrderOnOtherClientAccount(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	aliceAcct := alice.createRSDAccount(t)
	alice.depositForInitialBalance(t, aliceAcct, "100000")
	listing := ctx.seedStockListing(t, "AAPL", "150")

	body := `{"listing_id":` + itoa(listing.ID) + `,"direction":"buy","order_type":"market","quantity":1,"account_id":` + itoa(aliceAcct.ID) + `}`
	resp := bob.rawPOST(t, "/api/me/orders", body)
	require.Equal(t, 404, resp.StatusCode)
}

func TestCreateLoanRequestIgnoresBodyClientId(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	aliceAcct := alice.createRSDAccount(t)

	// Bob sends client_id=alice.ID in body — must be ignored.
	body := `{"client_id":` + itoa(alice.ID) + `,"loan_type":"cash","interest_type":"fixed","amount":"1000","repayment_period":12,"account_number":"` + aliceAcct.AccountNumber + `","currency_code":"RSD","employment_status":"employed","monthly_income":"5000","employment_duration":12}`
	resp := bob.rawPOST(t, "/api/me/loan-requests", body)

	// Creation for Bob against Alice's account will fail validation at some layer,
	// but the key assertion is: no loan request appears for Alice.
	require.True(t, resp.StatusCode == 400 || resp.StatusCode == 201)
	aliceRequests := alice.listLoanRequests(t)
	for _, r := range aliceRequests {
		require.NotEqual(t, r.Amount, "1000", "Alice must not have a loan request created by Bob")
	}
}
```

Add assertions for transfers, payment recipients, OTC based on the same pattern.

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test ./workflows/ -run "TestClientCannot|TestCreateLoanRequestIgnoresBodyClientId" -v
git add test-app/workflows/ownership_lockdown_test.go
git commit -m "test(workflows): integration coverage for /api/me/* ownership lockdown"
```

---

### Task 27: Integration test for employee on-behalf order

**Files:**
- Create: `test-app/workflows/employee_onbehalf_test.go`

- [ ] **Step 1: Write test**

```go
package workflows

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmployeeCreatesOrderOnBehalf_HappyPath(t *testing.T) {
	ctx := newTestContext(t)
	client := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "securities.manage")

	acct := client.createRSDAccount(t)
	client.depositForInitialBalance(t, acct, "100000")
	listing := ctx.seedStockListing(t, "AAPL", "150")

	body := map[string]any{
		"client_id":  client.ID,
		"account_id": acct.ID,
		"listing_id": listing.ID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   10,
	}
	raw, _ := json.Marshal(body)
	resp := employee.rawPOST(t, "/api/v1/orders", string(raw))

	require.Equal(t, 201, resp.StatusCode, resp.Body)
	var order struct {
		ID               uint64 `json:"id"`
		ActingEmployeeID uint64 `json:"acting_employee_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Body), &order))
	require.Equal(t, employee.ID, order.ActingEmployeeID)
}

func TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403(t *testing.T) {
	ctx := newTestContext(t)
	alice := ctx.newClient(t)
	bob := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "securities.manage")
	aliceAcct := alice.createRSDAccount(t)
	alice.depositForInitialBalance(t, aliceAcct, "100000")
	listing := ctx.seedStockListing(t, "AAPL", "150")

	body := map[string]any{
		"client_id":  bob.ID, // wrong!
		"account_id": aliceAcct.ID,
		"listing_id": listing.ID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	}
	raw, _ := json.Marshal(body)
	resp := employee.rawPOST(t, "/api/v1/orders", string(raw))

	require.Equal(t, 403, resp.StatusCode)
}

func TestEmployeeWithoutPermission_CannotCreateOnBehalf(t *testing.T) {
	ctx := newTestContext(t)
	client := ctx.newClient(t)
	employee := ctx.newEmployeeWithPermission(t, "credits.read") // lacks securities.manage
	acct := client.createRSDAccount(t)
	listing := ctx.seedStockListing(t, "AAPL", "150")

	body := map[string]any{"client_id": client.ID, "account_id": acct.ID, "listing_id": listing.ID, "direction": "buy", "order_type": "market", "quantity": 1}
	raw, _ := json.Marshal(body)
	resp := employee.rawPOST(t, "/api/v1/orders", string(raw))

	require.Equal(t, 403, resp.StatusCode)
}
```

- [ ] **Step 2: Run and commit**

```bash
cd test-app && go test ./workflows/ -run TestEmployeeCreatesOrderOnBehalf -v
git add test-app/workflows/employee_onbehalf_test.go
git commit -m "test(workflows): integration coverage for POST /api/v1/orders on-behalf path"
```

---

## Phase 6 — docs, Swagger, spec updates

### Task 28: Update Specification.md

**Files:**
- Modify: `Specification.md`

- [ ] **Step 1: Update Section 11 (gRPC)**

Add `DebitBankAccount` and `CreditBankAccount` to the `AccountGRPCService` RPC list, with request/response shapes.

- [ ] **Step 2: Update Section 17 (REST routes)**

Add new routes:
- `POST /api/v1/orders` — employee on-behalf stock/futures/forex/option
- `POST /api/v1/otc/:id/buy` — employee on-behalf OTC
- `GET /api/v1/loans/:id` — employee single-loan lookup
- `POST /api/me/cards/virtual` — client virtual card

Update existing routes to document the 404 ownership semantics:
- All 13 lockdown routes listed in Phase 3.

- [ ] **Step 3: Update Section 18 (entities)**

- Add `acting_employee_id` to `Order`.
- Add `Loan.status` values: `approved`, `active`, `disbursement_failed`.
- Add new `BankOperation` model in account-service.

- [ ] **Step 4: Update Section 19 (Kafka)**

Add `credit.loan-disbursed` topic and `LoanDisbursedMessage` shape.

- [ ] **Step 5: Update Section 20 (enums)**

Add `disbursement_failed` to loan status enum.

- [ ] **Step 6: Update Section 21 (business rules)**

Add:
- Bank must have sufficient liquidity in the loan currency for approval to succeed. Insufficient liquidity → 409.
- Loan approval is atomic: bank debited and borrower credited, or neither (compensation on partial failure).
- All `/api/me/*` routes derive resource ownership from JWT; request body and URL IDs are verified before any action, ownership mismatch returns 404.
- Employee on-behalf trading routes verify that the specified account belongs to the specified client.

- [ ] **Step 7: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): document loan disbursement saga, ownership lockdown, on-behalf trading"
```

---

### Task 29: Update docs/api/REST_API_v1.md

**Files:**
- Modify: `docs/api/REST_API_v1.md`

- [ ] **Step 1: Add three new route sections**

`POST /api/v1/orders`, `POST /api/v1/otc/:id/buy`, `GET /api/v1/loans/:id`, `POST /api/me/cards/virtual`. Follow the existing section format: auth, parameters, request body, example, responses.

- [ ] **Step 2: Update existing route sections**

For each of the 13 lockdown routes, add a note: "Ownership is derived from the JWT. Attempts to access another client's resource return 404 not_found." Update response code table accordingly.

For `POST /api/me/loan-requests`: add note that `client_id` in the body is ignored.

- [ ] **Step 3: Commit**

```bash
git add docs/api/REST_API_v1.md
git commit -m "docs(rest): document new v1 routes and /me ownership semantics"
```

---

### Task 30: Regenerate Swagger, run lint, final sanity check

**Files:**
- Regenerate: `api-gateway/docs/`

- [ ] **Step 1: Regenerate**

```bash
make swagger
```

- [ ] **Step 2: Lint all touched services**

```bash
make lint
```

Fix any new warnings in code we touched. Pre-existing warnings in untouched code can stay.

- [ ] **Step 3: Run full test suite**

```bash
make test
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/docs/
git commit -m "chore: regenerate swagger for v1 ownership lockdown feature"
```

- [ ] **Step 5: Verify branch state**

```bash
git log --oneline main..HEAD
```

Expect ~25 commits covering protobuf, account-service bank ops, credit-service saga, 13 handler fixes, 4 new routes, 3 integration test files, docs updates.

---

## Self-review checklist

- [x] Every spec requirement maps to a task: loan saga (Tasks 1-9), ownership lockdown (Tasks 10-20), new v1 routes (Tasks 21-24), integration tests (Tasks 25-27), docs (Tasks 28-30).
- [x] No `TBD` / `TODO` / "implement later" in any step.
- [x] `enforceOwnership` helper defined in Task 10 and used consistently in Tasks 11-20.
- [x] Saga compensation path (Task 8) references the same `loan-disbursement:{id}` reference format used in the bank debit (Task 8) — idempotent replay works.
- [x] All new protobuf fields added in Task 22 before being referenced in Tasks 23-24.
- [x] Plan is single-PR scope (one branch, one feature, tightly coupled changes).

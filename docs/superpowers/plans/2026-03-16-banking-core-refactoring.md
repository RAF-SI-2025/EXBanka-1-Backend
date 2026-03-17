# Banking Core Refactoring Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the EXBanka banking microservices for production-grade reliability: proper money types, double-entry ledger, transaction safety, auth security (2FA, brute-force protection), database constraints, and config consolidation.

**Architecture:** Six-phase refactoring applied across all 9 services. Phase 1 lays shared foundations (decimal types, config migration). Phase 2 hardens auth. Phase 3 upgrades all DB schemas. Phase 4 adds the account ledger. Phase 5 makes transactions reliable. Phase 6 fixes service boundaries and adds operational concerns.

**Tech Stack:** Go 1.25, GORM (PostgreSQL), gRPC/protobuf, Kafka (segmentio/kafka-go), Redis, shopspring/decimal, pquerna/otp (TOTP), Gin HTTP framework, Docker Compose.

---

## Current State Analysis — Critical Issues Found

| # | Issue | Severity | Where |
|---|-------|----------|-------|
| 1 | `float64` used for all monetary amounts | **CRITICAL** | All services — models, protos, handlers |
| 2 | No account ledger / audit trail for balance changes | **CRITICAL** | account-service |
| 3 | Payments/transfers don't verify balance or actually move money | **CRITICAL** | transaction-service |
| 4 | No DB transactions wrapping multi-step financial operations | **CRITICAL** | transaction-service, credit-service |
| 5 | No idempotency keys on financial operations | **HIGH** | transaction-service |
| 6 | No login attempt tracking or brute-force protection | **HIGH** | auth-service |
| 7 | No 2FA support | **HIGH** | auth-service |
| 8 | No optimistic locking (version field) on any model | **HIGH** | All services |
| 9 | No foreign key constraints in GORM models | **MEDIUM** | All services |
| 10 | Access tokens cannot be individually revoked | **MEDIUM** | auth-service |
| 11 | API Gateway orchestrates employee creation (should be user-service) | **MEDIUM** | api-gateway |
| 12 | Settings scattered across .env files with hardcoded credentials | **MEDIUM** | All services |
| 13 | No health check endpoints | **LOW** | All services |
| 14 | No graceful shutdown in most services | **LOW** | All services |

---

## Dependency Graph

```
Chunk 1 (Foundation) ──→ Chunk 3 (DB Schema) ──→ Chunk 4 (Ledger) ──→ Chunk 5 (Transactions)
                    └──→ Chunk 2 (Auth)
                    └──→ Chunk 6 (Service Boundaries)
```

Chunk 1 must be done first. Chunks 2 and 6 can run in parallel with 3. Chunk 4 depends on 3. Chunk 5 depends on 4.

---

## Chunk 1: Contract Layer & Config Migration

This chunk establishes shared foundations: adds the `shopspring/decimal` dependency, creates shared utility packages in `contract/`, updates all proto definitions to use `string` for monetary fields, migrates configuration from `.env` files into `docker-compose.yml` environment blocks, and updates each service's config loader to read from environment variables directly (no .env file walking).

### Task 1.1: Add shopspring/decimal dependency to contract module

**Files:**
- Modify: `contract/go.mod`
- Create: `contract/shared/money.go`
- Create: `contract/shared/money_test.go`

- [ ] **Step 1: Write the money utility test**

```go
// contract/shared/money_test.go
package shared

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestParseAmount_Valid(t *testing.T) {
	d, err := ParseAmount("1234.56")
	assert.NoError(t, err)
	assert.Equal(t, "1234.56", d.StringFixed(2))
}

func TestParseAmount_Empty(t *testing.T) {
	d, err := ParseAmount("")
	assert.NoError(t, err)
	assert.True(t, d.IsZero())
}

func TestParseAmount_Invalid(t *testing.T) {
	_, err := ParseAmount("abc")
	assert.Error(t, err)
}

func TestFormatAmount(t *testing.T) {
	d := decimal.NewFromFloat(1234.5)
	assert.Equal(t, "1234.5000", FormatAmount(d))
}

func TestAmountIsPositive(t *testing.T) {
	assert.True(t, AmountIsPositive(decimal.NewFromInt(1)))
	assert.False(t, AmountIsPositive(decimal.NewFromInt(0)))
	assert.False(t, AmountIsPositive(decimal.NewFromInt(-1)))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/ -v -run TestParseAmount`
Expected: FAIL — package/files don't exist yet

- [ ] **Step 3: Add shopspring/decimal dependency**

Run: `cd contract && go get github.com/shopspring/decimal`

- [ ] **Step 4: Write the money utility implementation**

```go
// contract/shared/money.go
package shared

import "github.com/shopspring/decimal"

// ParseAmount converts a string amount to decimal.Decimal.
// Returns zero for empty strings. Used at proto ↔ service boundaries.
func ParseAmount(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// FormatAmount converts a decimal.Decimal to a 4-decimal-place string
// for proto serialization.
func FormatAmount(d decimal.Decimal) string {
	return d.StringFixed(4)
}

// AmountIsPositive returns true if d > 0.
func AmountIsPositive(d decimal.Decimal) bool {
	return d.IsPositive()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd contract && go test ./shared/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add contract/shared/money.go contract/shared/money_test.go contract/go.mod contract/go.sum
git commit -m "feat(contract): add shared money utilities with shopspring/decimal"
```

---

### Task 1.2: Create shared idempotency key utility

**Files:**
- Create: `contract/shared/idempotency.go`
- Create: `contract/shared/idempotency_test.go`

- [ ] **Step 1: Write idempotency key test**

```go
// contract/shared/idempotency_test.go
package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateIdempotencyKey(t *testing.T) {
	key1 := GenerateIdempotencyKey()
	key2 := GenerateIdempotencyKey()
	assert.Len(t, key1, 36) // UUID v4 format
	assert.NotEqual(t, key1, key2)
}

func TestValidateIdempotencyKey(t *testing.T) {
	assert.True(t, ValidateIdempotencyKey(GenerateIdempotencyKey()))
	assert.False(t, ValidateIdempotencyKey(""))
	assert.False(t, ValidateIdempotencyKey("not-a-uuid"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/ -v -run TestGenerateIdempotencyKey`
Expected: FAIL

- [ ] **Step 3: Write implementation**

```go
// contract/shared/idempotency.go
package shared

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// GenerateIdempotencyKey returns a new UUID v4 string.
func GenerateIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ValidateIdempotencyKey checks if a string is a valid UUID v4.
func ValidateIdempotencyKey(key string) bool {
	return uuidRegex.MatchString(key)
}
```

- [ ] **Step 4: Run tests**

Run: `cd contract && go test ./shared/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add contract/shared/idempotency.go contract/shared/idempotency_test.go
git commit -m "feat(contract): add idempotency key generation utility"
```

---

### Task 1.3: Create shared retry utility

**Files:**
- Create: `contract/shared/retry.go`
- Create: `contract/shared/retry_test.go`

- [ ] **Step 1: Write retry utility test**

```go
// contract/shared/retry_test.go
package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetry_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_SucceedsAfterRetries(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAttempts(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 2, BaseDelay: time.Millisecond}, func() error {
		calls++
		return errors.New("persistent")
	})
	assert.Error(t, err)
	assert.Equal(t, 2, calls)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/ -v -run TestRetry`
Expected: FAIL

- [ ] **Step 3: Write implementation**

```go
// contract/shared/retry.go
package shared

import (
	"context"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

// DefaultRetryConfig for cross-service gRPC calls.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500 * time.Millisecond,
}

// Retry executes fn up to MaxAttempts times with exponential backoff.
// Returns the last error if all attempts fail.
func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if i < cfg.MaxAttempts-1 {
			delay := cfg.BaseDelay * time.Duration(1<<uint(i))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}
```

- [ ] **Step 4: Run tests**

Run: `cd contract && go test ./shared/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add contract/shared/retry.go contract/shared/retry_test.go
git commit -m "feat(contract): add retry utility with exponential backoff"
```

---

### Task 1.4: Update proto definitions — monetary fields from double to string

All monetary amounts in proto files must change from `double` to `string` to prevent floating-point precision loss. This is the most impactful change in this chunk as it affects all services.

**Files:**
- Modify: `contract/proto/account/account.proto`
- Modify: `contract/proto/transaction/transaction.proto`
- Modify: `contract/proto/credit/credit.proto`
- Modify: `contract/proto/card/card.proto`

- [ ] **Step 1: Update account.proto — change double fields to string**

In `contract/proto/account/account.proto`, change every `double` field to `string`:

```
// In CreateAccountRequest:
string initial_balance = 8;  // was: double initial_balance

// In AccountResponse:
string balance = 5;              // was: double
string available_balance = 6;    // was: double
string maintenance_fee = 12;     // was: double
string daily_limit = 13;         // was: double
string monthly_limit = 14;       // was: double
string daily_spending = 15;      // was: double
string monthly_spending = 16;    // was: double

// In UpdateAccountLimitsRequest:
string daily_limit = 2;   // was: double
string monthly_limit = 3; // was: double

// In UpdateBalanceRequest:
string amount = 2;  // was: double
```

- [ ] **Step 2: Update transaction.proto — change double fields to string**

In `contract/proto/transaction/transaction.proto`:

```
// CreatePaymentRequest: amount field
string amount = 7;  // was: double

// PaymentResponse: initial_amount, final_amount, commission
string initial_amount = 8;  // was: double
string final_amount = 9;    // was: double
string commission = 10;     // was: double

// CreateTransferRequest: amount
string amount = 3;  // was: double

// TransferResponse: initial_amount, final_amount, exchange_rate, commission
string initial_amount = 4;  // was: double
string final_amount = 5;    // was: double
string exchange_rate = 6;   // was: double
string commission = 7;      // was: double

// ExchangeRateResponse: buy_rate, sell_rate
string buy_rate = 4;   // was: double
string sell_rate = 5;  // was: double
```

- [ ] **Step 3: Update credit.proto — change double fields to string**

In `contract/proto/credit/credit.proto`:

```
// CreateLoanRequestReq: amount, monthly_salary
string amount = 4;          // was: double
string monthly_salary = 8;  // was: double

// LoanRequestResponse: amount, monthly_salary
string amount = 5;          // was: double
string monthly_salary = 9;  // was: double

// LoanResponse: amount, nominal_interest_rate, effective_interest_rate,
//               next_installment_amount, remaining_debt
string amount = 5;                    // was: double
string nominal_interest_rate = 8;     // was: double
string effective_interest_rate = 9;   // was: double
string next_installment_amount = 10;  // was: double
string remaining_debt = 11;           // was: double

// InstallmentResponse: amount, interest_rate
string amount = 3;         // was: double
string interest_rate = 4;  // was: double
```

- [ ] **Step 4: Update card.proto — change double field to string**

In `contract/proto/card/card.proto`:

```
// CardResponse: card_limit
string card_limit = 11;  // was: double
```

- [ ] **Step 5: Regenerate protobuf Go code**

Run: `make proto`
Expected: All `*pb.go` files regenerated with `string` types replacing `float64`

- [ ] **Step 6: Commit**

```bash
git add contract/proto/ contract/accountpb/ contract/transactionpb/ contract/creditpb/ contract/cardpb/
git commit -m "feat(contract): change all monetary proto fields from double to string

Prevents floating-point precision loss for financial amounts.
All services must now parse/format amounts at handler boundaries."
```

---

### Task 1.5: Update Kafka message types for decimal amounts

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Update message structs — change float64 to string**

In `contract/kafka/messages.go`, update all monetary fields:

```go
// PaymentCompletedMessage
type PaymentCompletedMessage struct {
	PaymentID         uint64 `json:"payment_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	Amount            string `json:"amount"`    // was float64
	Status            string `json:"status"`
}

// TransferCompletedMessage
type TransferCompletedMessage struct {
	TransferID        uint64 `json:"transfer_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	InitialAmount     string `json:"initial_amount"`  // was float64
	FinalAmount       string `json:"final_amount"`    // was float64
	ExchangeRate      string `json:"exchange_rate"`   // was float64
}

// LoanStatusMessage
type LoanStatusMessage struct {
	LoanRequestID uint64 `json:"loan_request_id"`
	ClientEmail   string `json:"client_email"`
	LoanType      string `json:"loan_type"`
	Amount        string `json:"amount"`  // was float64
	Status        string `json:"status"`
}

// InstallmentResultMessage
type InstallmentResultMessage struct {
	LoanID        uint64 `json:"loan_id"`
	ClientEmail   string `json:"client_email"`
	Amount        string `json:"amount"`   // was float64
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	RetryDeadline string `json:"retry_deadline,omitempty"`
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat(contract): change Kafka monetary fields from float64 to string"
```

---

### Task 1.6: Migrate configuration from .env to docker-compose.yml

Remove the `.env` file dependency. All configuration moves into `docker-compose.yml` `environment:` blocks. Each service's `config.go` will read directly from `os.Getenv()` without godotenv.

**Files:**
- Modify: `docker-compose.yml`
- Modify: `auth-service/internal/config/config.go`
- Modify: `user-service/internal/config/config.go`
- Modify: `client-service/internal/config/config.go`
- Modify: `account-service/internal/config/config.go`
- Modify: `card-service/internal/config/config.go`
- Modify: `transaction-service/internal/config/config.go`
- Modify: `credit-service/internal/config/config.go`
- Modify: `notification-service/internal/config/config.go`
- Modify: `api-gateway/internal/config/config.go`

- [ ] **Step 1: Update docker-compose.yml — add all env vars explicitly**

In `docker-compose.yml`, for each service, ensure ALL environment variables are defined directly (not referencing `.env`). Example for auth-service:

```yaml
  auth-service:
    build: ./auth-service
    environment:
      AUTH_DB_HOST: auth-db
      AUTH_DB_PORT: "5432"
      AUTH_DB_USER: postgres
      AUTH_DB_PASSWORD: postgres
      AUTH_DB_NAME: authdb
      AUTH_DB_SSLMODE: disable
      AUTH_GRPC_ADDR: ":50051"
      USER_GRPC_ADDR: "user-service:50052"
      CLIENT_GRPC_ADDR: "client-service:50054"
      KAFKA_BROKERS: "kafka:9092"
      REDIS_ADDR: "redis:6379"
      JWT_SECRET: "${JWT_SECRET:-change-me-in-production}"
      JWT_ACCESS_EXPIRY: "15m"
      JWT_REFRESH_EXPIRY: "168h"
      FRONTEND_BASE_URL: "http://localhost:3000"
```

Repeat this pattern for ALL services, replacing any `${VAR}` references with direct values. Keep `JWT_SECRET` as the only variable that should be overridden from the host environment for production.

- [ ] **Step 2: Simplify all service config loaders — remove godotenv**

For each service's `config.go`, remove the godotenv import and `.env` file walking logic. Replace with direct `os.Getenv` calls. The `Load()` function becomes:

```go
func Load() *Config {
	return &Config{
		DBHost:       getEnv("AUTH_DB_HOST", "localhost"),
		DBPort:       getEnv("AUTH_DB_PORT", "5432"),
		// ... other fields with sensible localhost defaults for local dev
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

Remove ALL hardcoded Azure credentials from defaults. Defaults should be `localhost`/`postgres`/`postgres` only.

Apply this pattern to all 9 service config files:
- `auth-service/internal/config/config.go` — also remove hardcoded `Anajankovic03` password
- `user-service/internal/config/config.go` — also remove hardcoded Azure credentials
- `client-service/internal/config/config.go`
- `account-service/internal/config/config.go`
- `card-service/internal/config/config.go`
- `transaction-service/internal/config/config.go`
- `credit-service/internal/config/config.go`
- `notification-service/internal/config/config.go`
- `api-gateway/internal/config/config.go`

- [ ] **Step 3: Remove godotenv from go.mod of all services**

Run for each service:
```bash
cd auth-service && go mod tidy
cd ../user-service && go mod tidy
cd ../client-service && go mod tidy
cd ../account-service && go mod tidy
cd ../card-service && go mod tidy
cd ../transaction-service && go mod tidy
cd ../credit-service && go mod tidy
cd ../notification-service && go mod tidy
cd ../api-gateway && go mod tidy
```

- [ ] **Step 4: Verify all services still build**

Run: `make build`
Expected: All 9 services build successfully

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml */internal/config/config.go */go.mod */go.sum
git commit -m "refactor: migrate config from .env files to docker-compose environment

Removes godotenv dependency and hardcoded credentials from all services.
All configuration now flows through docker-compose environment blocks.
Local development uses sensible localhost defaults."
```

---

### Task 1.7: Add shopspring/decimal dependency to all service modules

Each service module that handles money needs `shopspring/decimal` added.

**Files:**
- Modify: `account-service/go.mod`
- Modify: `transaction-service/go.mod`
- Modify: `credit-service/go.mod`
- Modify: `card-service/go.mod`

- [ ] **Step 1: Add dependency to each service**

```bash
cd account-service && go get github.com/shopspring/decimal && cd ..
cd transaction-service && go get github.com/shopspring/decimal && cd ..
cd credit-service && go get github.com/shopspring/decimal && cd ..
cd card-service && go get github.com/shopspring/decimal && cd ..
```

- [ ] **Step 2: Run go mod tidy**

Run: `make tidy`

- [ ] **Step 3: Commit**

```bash
git add */go.mod */go.sum
git commit -m "feat: add shopspring/decimal dependency to financial services"
```

---

## Chunk 2: Auth Service Security Overhaul

This chunk adds: login attempt tracking with account locking after 5 failures, TOTP-based 2FA, active session tracking, access token blacklisting via Redis, and JWT hardening with JTI claims.

### Task 2.1: Add LoginAttempt model and repository

**Files:**
- Create: `auth-service/internal/model/login_attempt.go`
- Create: `auth-service/internal/repository/login_attempt_repository.go`
- Create: `auth-service/internal/repository/login_attempt_repository_test.go`

- [ ] **Step 1: Write the LoginAttempt model**

```go
// auth-service/internal/model/login_attempt.go
package model

import "time"

// LoginAttempt tracks each login attempt for brute-force detection.
type LoginAttempt struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Email     string    `gorm:"not null;index:idx_login_attempt_email"`
	IPAddress string    `gorm:"size:45"`
	Success   bool      `gorm:"not null;default:false"`
	CreatedAt time.Time `gorm:"not null;index:idx_login_attempt_created"`
}

// AccountLock tracks locked accounts after too many failed attempts.
type AccountLock struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"not null;uniqueIndex"`
	Email     string    `gorm:"not null;index"`
	Reason    string    `gorm:"not null;default:'too_many_failed_attempts'"`
	LockedAt  time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"` // locks expire after 30 min by default
	UnlockedAt *time.Time
}
```

- [ ] **Step 2: Write the repository test**

```go
// auth-service/internal/repository/login_attempt_repository_test.go
package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/exbanka/auth-service/internal/model"
)

// These tests require a test DB setup helper — use the pattern below:
// func setupTestDB(t *testing.T) *gorm.DB { ... }

func TestCountRecentFailedAttempts_NoAttempts(t *testing.T) {
	// Given no login attempts for user@test.com
	// When counting recent failures in last 15 min
	// Then count should be 0
}

func TestCountRecentFailedAttempts_WithFailures(t *testing.T) {
	// Given 3 failed attempts for user@test.com in the last 5 min
	// When counting recent failures in last 15 min
	// Then count should be 3
}
```

NOTE: Full integration tests require a test database. For now, the repository is straightforward GORM code — test at the service level with mocks.

- [ ] **Step 3: Write the repository implementation**

```go
// auth-service/internal/repository/login_attempt_repository.go
package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type LoginAttemptRepository struct {
	db *gorm.DB
}

func NewLoginAttemptRepository(db *gorm.DB) *LoginAttemptRepository {
	return &LoginAttemptRepository{db: db}
}

// RecordAttempt stores a login attempt.
func (r *LoginAttemptRepository) RecordAttempt(email, ip string, success bool) error {
	return r.db.Create(&model.LoginAttempt{
		Email:     email,
		IPAddress: ip,
		Success:   success,
		CreatedAt: time.Now(),
	}).Error
}

// CountRecentFailedAttempts returns the number of failed login attempts
// for the given email within the specified window.
func (r *LoginAttemptRepository) CountRecentFailedAttempts(email string, window time.Duration) (int64, error) {
	var count int64
	err := r.db.Model(&model.LoginAttempt{}).
		Where("email = ? AND success = false AND created_at > ?", email, time.Now().Add(-window)).
		Count(&count).Error
	return count, err
}

// LockAccount creates an account lock record.
func (r *LoginAttemptRepository) LockAccount(userID int64, email string, duration time.Duration) error {
	lock := model.AccountLock{
		UserID:    userID,
		Email:     email,
		LockedAt:  time.Now(),
		ExpiresAt: time.Now().Add(duration),
	}
	return r.db.Create(&lock).Error
}

// GetActiveLock returns the active lock for a user, or nil if not locked.
func (r *LoginAttemptRepository) GetActiveLock(email string) (*model.AccountLock, error) {
	var lock model.AccountLock
	err := r.db.Where("email = ? AND expires_at > ? AND unlocked_at IS NULL", email, time.Now()).
		First(&lock).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &lock, err
}

// UnlockAccount manually unlocks an account.
func (r *LoginAttemptRepository) UnlockAccount(userID int64) error {
	now := time.Now()
	return r.db.Model(&model.AccountLock{}).
		Where("user_id = ? AND unlocked_at IS NULL", userID).
		Update("unlocked_at", &now).Error
}
```

- [ ] **Step 4: Add auto-migration for new models in main.go**

In `auth-service/cmd/main.go`, add to the AutoMigrate call:

```go
db.AutoMigrate(
	&model.RefreshToken{},
	&model.ActivationToken{},
	&model.PasswordResetToken{},
	&model.LoginAttempt{},    // NEW
	&model.AccountLock{},     // NEW
)
```

- [ ] **Step 5: Verify it builds**

Run: `cd auth-service && go build ./cmd`
Expected: Builds successfully

- [ ] **Step 6: Commit**

```bash
git add auth-service/internal/model/login_attempt.go auth-service/internal/repository/login_attempt_repository.go auth-service/cmd/main.go
git commit -m "feat(auth): add login attempt tracking and account lock models"
```

---

### Task 2.2: Integrate brute-force protection into auth service login flow

**Files:**
- Modify: `auth-service/internal/service/auth_service.go`
- Modify: `auth-service/cmd/main.go`

- [ ] **Step 1: Add LoginAttemptRepository to AuthService struct**

In `auth-service/internal/service/auth_service.go`, add field:

```go
type AuthService struct {
	tokenRepo       *repository.TokenRepository
	loginAttemptRepo *repository.LoginAttemptRepository  // NEW
	jwtService      *JWTService
	// ... rest unchanged
}
```

Update the constructor to accept and store it.

- [ ] **Step 2: Add brute-force check to Login method**

In the `Login` method, before calling `ValidateCredentials`, add:

```go
const maxFailedAttempts = 5
const lockoutWindow = 15 * time.Minute
const lockoutDuration = 30 * time.Minute

func (s *AuthService) Login(ctx context.Context, email, password string) (string, string, error) {
	// Check if account is locked
	lock, err := s.loginAttemptRepo.GetActiveLock(email)
	if err != nil {
		return "", "", fmt.Errorf("failed to check account lock: %w", err)
	}
	if lock != nil {
		remaining := time.Until(lock.ExpiresAt).Minutes()
		return "", "", fmt.Errorf("account locked due to too many failed attempts, try again in %.0f minutes", remaining)
	}

	// Check recent failed attempts
	failedCount, err := s.loginAttemptRepo.CountRecentFailedAttempts(email, lockoutWindow)
	if err != nil {
		log.Printf("WARNING: failed to count login attempts: %v", err)
	}

	// Existing: validate credentials via user-service gRPC
	resp, err := s.userClient.ValidateCredentials(ctx, &userpb.ValidateCredentialsRequest{
		Email:    email,
		Password: password,
	})
	if err != nil || !resp.Valid {
		// Record failed attempt
		_ = s.loginAttemptRepo.RecordAttempt(email, "", false)

		// Lock if threshold reached
		if failedCount+1 >= maxFailedAttempts {
			_ = s.loginAttemptRepo.LockAccount(0, email, lockoutDuration)
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		remaining := maxFailedAttempts - (failedCount + 1)
		return "", "", fmt.Errorf("invalid credentials (%d attempts remaining)", remaining)
	}

	// Record successful attempt
	_ = s.loginAttemptRepo.RecordAttempt(email, "", true)

	// ... rest of existing login logic (generate JWT, refresh token, etc.)
}
```

- [ ] **Step 3: Apply same pattern to ClientLogin method**

Add identical brute-force check logic to `ClientLogin`, checking against client-service credentials.

- [ ] **Step 4: Wire LoginAttemptRepository in main.go**

```go
loginAttemptRepo := repository.NewLoginAttemptRepository(db)
// Pass to auth service constructor
authService := service.NewAuthService(tokenRepo, loginAttemptRepo, jwtSvc, userConn, clientConn, producer, redisCache, cfg.RefreshExpiry, cfg.FrontendBaseURL)
```

- [ ] **Step 5: Verify it builds**

Run: `cd auth-service && go build ./cmd`

- [ ] **Step 6: Commit**

```bash
git add auth-service/internal/service/auth_service.go auth-service/cmd/main.go
git commit -m "feat(auth): add brute-force protection with account locking after 5 failed attempts"
```

---

### Task 2.3: Add TOTP 2FA model and service

**Files:**
- Create: `auth-service/internal/model/totp.go`
- Create: `auth-service/internal/service/totp_service.go`
- Create: `auth-service/internal/service/totp_service_test.go`
- Modify: `auth-service/go.mod`

- [ ] **Step 1: Add pquerna/otp dependency**

Run: `cd auth-service && go get github.com/pquerna/otp`

- [ ] **Step 2: Write the TOTP model**

```go
// auth-service/internal/model/totp.go
package model

import "time"

// TOTPSecret stores the TOTP shared secret for a user's 2FA.
type TOTPSecret struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"not null;uniqueIndex"`
	Secret    string    `gorm:"not null"` // base32-encoded TOTP secret
	Enabled   bool      `gorm:"not null;default:false"` // only true after first successful verification
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

- [ ] **Step 3: Write the TOTP service test**

```go
// auth-service/internal/service/totp_service_test.go
package service

import (
	"testing"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
)

func TestGenerateTOTPSecret(t *testing.T) {
	svc := &TOTPService{}
	key, url, err := svc.GenerateSecret("user@test.com", "EXBanka")
	assert.NoError(t, err)
	assert.NotEmpty(t, key)
	assert.Contains(t, url, "otpauth://totp/EXBanka:user@test.com")
}

func TestValidateTOTPCode(t *testing.T) {
	svc := &TOTPService{}
	secret, _, _ := svc.GenerateSecret("user@test.com", "EXBanka")

	// Generate a valid code
	code, err := totp.GenerateCode(secret, time.Now())
	assert.NoError(t, err)

	// Validate it
	assert.True(t, svc.ValidateCode(secret, code))
	assert.False(t, svc.ValidateCode(secret, "000000"))
}
```

- [ ] **Step 4: Write the TOTP service**

```go
// auth-service/internal/service/totp_service.go
package service

import (
	"github.com/pquerna/otp/totp"
	"time"
)

type TOTPService struct{}

// GenerateSecret creates a new TOTP secret for a user.
// Returns the base32 secret, the OTP auth URL (for QR code), and any error.
func (s *TOTPService) GenerateSecret(email, issuer string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: email,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateCode checks if a TOTP code is valid for the given secret.
func (s *TOTPService) ValidateCode(secret, code string) bool {
	return totp.Validate(code, secret)
}

// ValidateCodeWithWindow checks if code is valid within a ±1 period window.
func (s *TOTPService) ValidateCodeWithWindow(secret, code string) bool {
	valid, _ := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:     1, // allows ±1 period (30 seconds)
		Digits:   6,
		Algorithm: otp.AlgorithmSHA1,
	})
	return valid
}
```

- [ ] **Step 5: Run tests**

Run: `cd auth-service && go test ./internal/service/ -v -run TestTOTP`
Expected: PASS (after fixing any import issues)

- [ ] **Step 6: Add AutoMigrate for TOTPSecret**

In `auth-service/cmd/main.go`, add `&model.TOTPSecret{}` to AutoMigrate call.

- [ ] **Step 7: Commit**

```bash
git add auth-service/internal/model/totp.go auth-service/internal/service/totp_service.go auth-service/internal/service/totp_service_test.go auth-service/go.mod auth-service/go.sum auth-service/cmd/main.go
git commit -m "feat(auth): add TOTP 2FA model and service"
```

---

### Task 2.4: Add 2FA proto RPCs and integrate into login flow

**Files:**
- Modify: `contract/proto/auth/auth.proto`
- Modify: `auth-service/internal/handler/grpc_handler.go`
- Modify: `auth-service/internal/service/auth_service.go`
- Modify: `api-gateway/internal/handler/auth_handler.go`
- Modify: `api-gateway/internal/router/router.go`

- [ ] **Step 1: Add 2FA RPCs to auth.proto**

```protobuf
// Add to service AuthService:
rpc Setup2FA(Setup2FARequest) returns (Setup2FAResponse);
rpc Verify2FA(Verify2FARequest) returns (Verify2FAResponse);
rpc Disable2FA(Disable2FARequest) returns (Disable2FAResponse);

// Add messages:
message Setup2FARequest {
  int64 user_id = 1;
  string email = 2;
}

message Setup2FAResponse {
  string secret = 1;
  string qr_url = 2;
}

message Verify2FARequest {
  int64 user_id = 1;
  string code = 2;
}

message Verify2FAResponse {
  bool valid = 1;
}

message Disable2FARequest {
  int64 user_id = 1;
  string code = 2; // must verify current code to disable
}

message Disable2FAResponse {
  bool success = 1;
}
```

Also modify `LoginResponse` to add:
```protobuf
message LoginResponse {
  string access_token = 1;
  string refresh_token = 2;
  bool requires_2fa = 3;       // NEW: true if user has 2FA enabled
  string two_fa_token = 4;     // NEW: temporary token for 2FA verification
}
```

- [ ] **Step 2: Regenerate proto**

Run: `make proto`

- [ ] **Step 3: Update auth service Login flow**

In `auth_service.go`, modify Login:
- After successful credential validation, check if user has 2FA enabled
- If 2FA enabled: return `requires_2fa=true` with a short-lived temporary token (not the real access token)
- If 2FA not enabled: return tokens as before
- Add `VerifyLoginWith2FA(ctx, twoFAToken, code)` method that:
  1. Validates the temporary token
  2. Validates the TOTP code
  3. Issues the real access + refresh tokens

- [ ] **Step 4: Add handler implementations for 2FA RPCs**

In `grpc_handler.go`, implement `Setup2FA`, `Verify2FA`, `Disable2FA`.

- [ ] **Step 5: Add API gateway routes**

In `api-gateway/internal/router/router.go`, add to auth routes:
```go
auth.POST("/2fa/setup", authHandler.Setup2FA)
auth.POST("/2fa/verify", authHandler.Verify2FA)
auth.POST("/2fa/disable", authHandler.Disable2FA)
auth.POST("/2fa/login", authHandler.VerifyLoginWith2FA)
```

In `api-gateway/internal/handler/auth_handler.go`, add handler methods that call the auth-service gRPC.

- [ ] **Step 6: Verify it builds**

Run: `make build`

- [ ] **Step 7: Commit**

```bash
git add contract/proto/auth/ contract/authpb/ auth-service/ api-gateway/
git commit -m "feat(auth): integrate TOTP 2FA into login flow with setup/verify/disable RPCs"
```

---

### Task 2.5: Add active session tracking

**Files:**
- Create: `auth-service/internal/model/active_session.go`
- Modify: `auth-service/internal/repository/token_repository.go`
- Modify: `auth-service/internal/service/auth_service.go`

- [ ] **Step 1: Write the ActiveSession model**

```go
// auth-service/internal/model/active_session.go
package model

import "time"

// ActiveSession tracks where a user is logged in from.
type ActiveSession struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"not null;index:idx_session_user"`
	RefreshTokenID int64     `gorm:"not null;index"` // FK to refresh_tokens.id
	IPAddress      string    `gorm:"size:45"`
	UserAgent      string    `gorm:"size:512"`
	DeviceInfo     string    `gorm:"size:255"`
	LastActiveAt   time.Time `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	RevokedAt      *time.Time
}
```

- [ ] **Step 2: Integrate session creation into Login**

In `auth_service.go` Login method, after creating the refresh token, create an ActiveSession record:

```go
session := &model.ActiveSession{
	UserID:         resp.UserId,
	RefreshTokenID: refreshTokenRecord.ID,
	IPAddress:      "", // passed from handler via context
	LastActiveAt:   time.Now(),
	CreatedAt:      time.Now(),
}
s.tokenRepo.CreateSession(session)
```

- [ ] **Step 3: Revoke session on Logout**

In `Logout`, when revoking refresh token, also mark the ActiveSession as revoked.

- [ ] **Step 4: Add AutoMigrate and wire**

Add `&model.ActiveSession{}` to AutoMigrate in main.go.

- [ ] **Step 5: Commit**

```bash
git add auth-service/internal/model/active_session.go auth-service/internal/repository/token_repository.go auth-service/internal/service/auth_service.go auth-service/cmd/main.go
git commit -m "feat(auth): add active session tracking for login audit trail"
```

---

### Task 2.6: Add JWT JTI claim and access token blacklisting

**Files:**
- Modify: `auth-service/internal/service/jwt_service.go`
- Modify: `auth-service/internal/service/auth_service.go`
- Modify: `auth-service/internal/cache/redis.go`

- [ ] **Step 1: Add JTI to JWT claims**

In `jwt_service.go`, add a unique `JTI` (JWT ID) field to Claims:

```go
type Claims struct {
	UserID      int64    `json:"user_id"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
	jwt.RegisteredClaims
}
```

In `GenerateAccessToken`, set `ID` in RegisteredClaims:

```go
func (s *JWTService) GenerateAccessToken(userID int64, email, role string, permissions []string) (string, error) {
	jti := generateJTI() // crypto/rand 16-byte hex
	claims := Claims{
		UserID:      userID,
		Email:       email,
		Role:        role,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,  // NEW: unique token ID
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	// ... sign and return
}
```

- [ ] **Step 2: Add blacklist check in ValidateToken**

In `auth_service.go` `ValidateToken`, after parsing the JWT, check if the JTI is blacklisted:

```go
func (s *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	// ... existing cache check ...
	claims, err := s.jwtService.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Check blacklist
	if claims.ID != "" && s.cache != nil {
		blacklisted, _ := s.cache.Exists(ctx, "blacklist:"+claims.ID)
		if blacklisted {
			return nil, fmt.Errorf("token has been revoked")
		}
	}
	// ... existing cache set ...
}
```

- [ ] **Step 3: Add RevokeAccessToken method**

```go
// RevokeAccessToken blacklists a specific access token by JTI.
func (s *AuthService) RevokeAccessToken(jti string, remainingTTL time.Duration) error {
	if s.cache == nil {
		return fmt.Errorf("Redis required for token blacklisting")
	}
	return s.cache.Set(context.Background(), "blacklist:"+jti, "revoked", remainingTTL)
}
```

- [ ] **Step 4: Add Exists method to RedisCache**

In `auth-service/internal/cache/redis.go`:

```go
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, key).Result()
	return n > 0, err
}
```

- [ ] **Step 5: Commit**

```bash
git add auth-service/internal/service/jwt_service.go auth-service/internal/service/auth_service.go auth-service/internal/cache/redis.go
git commit -m "feat(auth): add JWT JTI claim and access token blacklisting via Redis"
```

---

## Chunk 3: Database Schema Hardening

This chunk upgrades ALL service models with: `Version` field for optimistic locking, `decimal.Decimal` for monetary fields, proper GORM indexes and constraints, idempotency key fields on financial operations, and soft deletes where appropriate.

### Task 3.1: Add Version field and optimistic locking helper

**Files:**
- Create: `contract/shared/optimistic_lock.go`
- Create: `contract/shared/optimistic_lock_test.go`

- [ ] **Step 1: Write optimistic lock test**

```go
// contract/shared/optimistic_lock_test.go
package shared

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptimisticLockError(t *testing.T) {
	err := ErrOptimisticLock
	assert.True(t, errors.Is(err, ErrOptimisticLock))
	assert.Contains(t, err.Error(), "optimistic lock")
}
```

- [ ] **Step 2: Write implementation**

```go
// contract/shared/optimistic_lock.go
package shared

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another transaction")

// UpdateWithVersion performs an optimistic-lock-safe update.
// The WHERE clause includes version=currentVersion and the update increments version.
// Returns ErrOptimisticLock if the row was modified concurrently.
func UpdateWithVersion(db *gorm.DB, model interface{}, id interface{}, currentVersion int64, updates map[string]interface{}) error {
	updates["version"] = currentVersion + 1
	result := db.Model(model).
		Where("id = ? AND version = ?", id, currentVersion).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// VersionedUpdate is a convenience for updating a struct with version check.
func VersionedUpdate(db *gorm.DB, table string, id interface{}, currentVersion int64, updates map[string]interface{}) error {
	updates["version"] = currentVersion + 1
	result := db.Table(table).
		Where("id = ? AND version = ?", id, currentVersion).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}
```

- [ ] **Step 3: Run tests and commit**

Run: `cd contract && go get gorm.io/gorm && go test ./shared/ -v`

```bash
git add contract/shared/optimistic_lock.go contract/shared/optimistic_lock_test.go contract/go.mod contract/go.sum
git commit -m "feat(contract): add optimistic locking helper for versioned updates"
```

---

### Task 3.2: Update account-service models with decimal, version, indexes

**Files:**
- Modify: `account-service/internal/model/account.go`
- Modify: `account-service/internal/model/company.go`

- [ ] **Step 1: Update Account model**

Replace the Account model with properly typed fields:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type Account struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber    string          `gorm:"uniqueIndex;size:18;not null"`
	AccountName      string          `gorm:"size:255"`
	OwnerID          uint64          `gorm:"not null;index:idx_account_owner"`
	OwnerName        string          `gorm:"size:255"`
	Balance          decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	AvailableBalance decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	EmployeeID       uint64          `gorm:"not null;index"`
	ExpiresAt        time.Time       `gorm:"not null"`
	CurrencyCode     string          `gorm:"size:3;not null;index:idx_account_currency"`
	Status           string          `gorm:"size:20;not null;default:'active';index:idx_account_status"`
	AccountKind      string          `gorm:"size:20;not null"` // current, foreign
	AccountType      string          `gorm:"size:20;not null;default:'standard'"` // premium, student, youth, pension, standard
	AccountCategory  string          `gorm:"size:50"`
	MaintenanceFee   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	DailyLimit       decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000"`
	MonthlyLimit     decimal.Decimal `gorm:"type:numeric(18,4);not null;default:10000000"`
	DailySpending    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MonthlySpending  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	CompanyID        *uint64         `gorm:"index"`
	Version          int64           `gorm:"not null;default:1"` // optimistic locking
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        gorm.DeletedAt  `gorm:"index"` // soft delete
}
```

- [ ] **Step 2: Update account-service repository to use versioned updates**

In `account-service/internal/repository/account_repository.go`, update `UpdateBalance` to use optimistic locking:

```go
func (r *AccountRepository) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var account model.Account
		if err := tx.Where("account_number = ?", accountNumber).First(&account).Error; err != nil {
			return err
		}

		updates := map[string]interface{}{
			"balance": gorm.Expr("balance + ?", amount),
			"version": account.Version + 1,
		}
		if updateAvailable {
			updates["available_balance"] = gorm.Expr("available_balance + ?", amount)
		}

		result := tx.Model(&model.Account{}).
			Where("account_number = ? AND version = ?", accountNumber, account.Version).
			Updates(updates)
		if result.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		return result.Error
	})
}
```

- [ ] **Step 3: Update all account-service handler conversions**

In the gRPC handler, convert between `string` (proto) and `decimal.Decimal` (model):

```go
import "github.com/exbanka/contract/shared"

// In CreateAccount handler:
balance, err := shared.ParseAmount(req.InitialBalance)
if err != nil {
	return nil, status.Error(codes.InvalidArgument, "invalid initial_balance")
}
account.Balance = balance
account.AvailableBalance = balance

// In toAccountResponse:
func toAccountResponse(a *model.Account) *pb.AccountResponse {
	return &pb.AccountResponse{
		Balance:          shared.FormatAmount(a.Balance),
		AvailableBalance: shared.FormatAmount(a.AvailableBalance),
		DailyLimit:       shared.FormatAmount(a.DailyLimit),
		// ... etc
	}
}
```

- [ ] **Step 4: Verify it builds**

Run: `cd account-service && go build ./cmd`

- [ ] **Step 5: Commit**

```bash
git add account-service/
git commit -m "feat(account): upgrade to decimal.Decimal, add version field, soft deletes, proper indexes"
```

---

### Task 3.3: Update transaction-service models with decimal, version, idempotency

**Files:**
- Modify: `transaction-service/internal/model/payment.go`
- Modify: `transaction-service/internal/model/transfer.go`
- Modify: `transaction-service/internal/model/exchange_rate.go`

- [ ] **Step 1: Update Payment model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type Payment struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey    string          `gorm:"uniqueIndex;size:36"` // UUID, prevents duplicate payments
	FromAccountNumber string          `gorm:"not null;index:idx_payment_from"`
	ToAccountNumber   string          `gorm:"not null;index:idx_payment_to"`
	InitialAmount     decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	FinalAmount       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	Commission        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	CurrencyCode      string          `gorm:"size:3;not null"`
	RecipientName     string          `gorm:"size:255"`
	PaymentCode       string          `gorm:"size:10"`
	ReferenceNumber   string          `gorm:"size:50"`
	PaymentPurpose    string          `gorm:"size:255"`
	Status            string          `gorm:"size:20;not null;default:'pending';index:idx_payment_status"`
	// Status: pending → processing → completed / failed / cancelled
	FailureReason     string          `gorm:"size:512"`
	Version           int64           `gorm:"not null;default:1"`
	Timestamp         time.Time       `gorm:"not null;index:idx_payment_timestamp"`
	CompletedAt       *time.Time
}
```

- [ ] **Step 2: Update Transfer model**

```go
type Transfer struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey    string          `gorm:"uniqueIndex;size:36"`
	FromAccountNumber string          `gorm:"not null;index:idx_transfer_from"`
	ToAccountNumber   string          `gorm:"not null;index:idx_transfer_to"`
	InitialAmount     decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	FinalAmount       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	ExchangeRate      decimal.Decimal `gorm:"type:numeric(18,8);not null;default:1"`
	Commission        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	FromCurrency      string          `gorm:"size:3;not null"`
	ToCurrency        string          `gorm:"size:3;not null"`
	Status            string          `gorm:"size:20;not null;default:'pending';index"`
	FailureReason     string          `gorm:"size:512"`
	Version           int64           `gorm:"not null;default:1"`
	Timestamp         time.Time       `gorm:"not null"`
	CompletedAt       *time.Time
}
```

- [ ] **Step 3: Update ExchangeRate model**

```go
type ExchangeRate struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement"`
	FromCurrency string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	ToCurrency   string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	BuyRate      decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	SellRate     decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	Version      int64           `gorm:"not null;default:1"`
	UpdatedAt    time.Time
}
```

- [ ] **Step 4: Update handlers for string ↔ decimal conversion**

Apply the same `shared.ParseAmount` / `shared.FormatAmount` pattern as in Task 3.2.

- [ ] **Step 5: Verify it builds and commit**

```bash
cd transaction-service && go build ./cmd
git add transaction-service/
git commit -m "feat(transaction): upgrade to decimal, add idempotency keys, version fields, status state machine"
```

---

### Task 3.4: Update credit-service models with decimal and version

**Files:**
- Modify: `credit-service/internal/model/loan_request.go`
- Modify: `credit-service/internal/model/loan.go`
- Modify: `credit-service/internal/model/installment.go`

- [ ] **Step 1: Update all credit models**

Apply the same pattern: `float64` → `decimal.Decimal` with `type:numeric(18,4)`, add `Version int64`, and add proper indexes. Key fields:

```go
// LoanRequest
Amount        decimal.Decimal `gorm:"type:numeric(18,4);not null"`
MonthlySalary decimal.Decimal `gorm:"type:numeric(18,4)"`
Version       int64           `gorm:"not null;default:1"`

// Loan
Amount              decimal.Decimal `gorm:"type:numeric(18,4);not null"`
RemainingDebt       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
NominalInterestRate decimal.Decimal `gorm:"type:numeric(8,4);not null"`
EffectiveInterestRate decimal.Decimal `gorm:"type:numeric(8,4);not null"`
NextInstallmentAmount decimal.Decimal `gorm:"type:numeric(18,4)"`
Version             int64           `gorm:"not null;default:1"`

// Installment
Amount       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
InterestRate decimal.Decimal `gorm:"type:numeric(8,4);not null"`
Version      int64           `gorm:"not null;default:1"`
```

- [ ] **Step 2: Update interest rate calculations to use decimal**

In `credit-service/internal/service/interest_rate.go`, change all `float64` arithmetic to `decimal.Decimal` operations.

- [ ] **Step 3: Update handlers and verify build**

Apply `ParseAmount`/`FormatAmount` conversions in handler. Verify build.

- [ ] **Step 4: Commit**

```bash
git add credit-service/
git commit -m "feat(credit): upgrade to decimal, add version fields, proper indexes"
```

---

### Task 3.5: Update card-service model with version and decimal

**Files:**
- Modify: `card-service/internal/model/card.go`

- [ ] **Step 1: Update Card model**

```go
CardLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000"`
Version   int64           `gorm:"not null;default:1"`
```

- [ ] **Step 2: Update handler conversions and commit**

```bash
git add card-service/
git commit -m "feat(card): upgrade card_limit to decimal, add version field"
```

---

### Task 3.6: Improve admin seeding in user-service

**Files:**
- Modify: `user-service/cmd/main.go`

- [ ] **Step 1: Update seedAdminUser for idempotency and safety**

```go
func seedAdminUser(repo *repository.EmployeeRepository) error {
	// Check by email — idempotent
	existing, err := repo.GetByEmail("admin@exbanka.com")
	if err == nil && existing != nil {
		log.Println("Admin user already exists, skipping seed")
		return nil
	}

	hash, err := service.HashPassword("AdminAdmin2026.!")
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	admin := &model.Employee{
		FirstName:    "System",
		LastName:     "Admin",
		DateOfBirth:  time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:       "other",
		Email:        "admin@exbanka.com",
		Phone:        "+381000000000",
		Address:      "System Account",
		JMBG:         "0101990000000", // valid format placeholder
		Username:     "admin",
		PasswordHash: hash,
		Salt:         "system-seed",
		Position:     "System Administrator",
		Department:   "IT",
		Active:       true,
		Role:         "EmployeeAdmin",
		Activated:    true,
	}
	return repo.Create(admin)
}
```

- [ ] **Step 2: Commit**

```bash
git add user-service/cmd/main.go
git commit -m "fix(user): improve admin seeding with proper email and idempotent check"
```

---

## Chunk 4: Account Ledger System

This chunk adds double-entry bookkeeping to account-service. Every balance change is recorded as a ledger entry, creating an immutable audit trail. Balance is derived from (or reconciled against) the ledger.

### Task 4.1: Create LedgerEntry model

**Files:**
- Create: `account-service/internal/model/ledger_entry.go`

- [ ] **Step 1: Write the LedgerEntry model**

```go
// account-service/internal/model/ledger_entry.go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// LedgerEntry represents a single debit or credit in the account ledger.
// Every balance change produces TWO entries (double-entry bookkeeping):
// one debit entry and one credit entry.
type LedgerEntry struct {
	ID              uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber   string          `gorm:"not null;index:idx_ledger_account"`
	EntryType       string          `gorm:"size:10;not null"` // "debit" or "credit"
	Amount          decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	BalanceAfter    decimal.Decimal `gorm:"type:numeric(18,4);not null"` // running balance after this entry
	Currency        string          `gorm:"size:3;not null"`
	Description     string          `gorm:"size:512;not null"`
	ReferenceType   string          `gorm:"size:50;not null;index:idx_ledger_ref"` // payment, transfer, fee, interest, adjustment
	ReferenceID     string          `gorm:"size:50;not null;index:idx_ledger_ref"` // ID of the source transaction
	IdempotencyKey  string          `gorm:"uniqueIndex;size:72"` // accountNumber:referenceType:referenceID:entryType
	CreatedAt       time.Time       `gorm:"not null;index:idx_ledger_created"`
}

// LedgerEntry is append-only — no updates or deletes allowed.
// To reverse an entry, create a new compensating entry.
```

- [ ] **Step 2: Add to AutoMigrate in main.go**

```go
db.AutoMigrate(&model.Currency{}, &model.Company{}, &model.Account{}, &model.LedgerEntry{})
```

- [ ] **Step 3: Commit**

```bash
git add account-service/internal/model/ledger_entry.go account-service/cmd/main.go
git commit -m "feat(account): add LedgerEntry model for double-entry bookkeeping"
```

---

### Task 4.2: Create ledger repository

**Files:**
- Create: `account-service/internal/repository/ledger_repository.go`

- [ ] **Step 1: Write the ledger repository**

```go
// account-service/internal/repository/ledger_repository.go
package repository

import (
	"github.com/exbanka/account-service/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type LedgerRepository struct {
	db *gorm.DB
}

func NewLedgerRepository(db *gorm.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// CreateEntry inserts a single ledger entry. Should be called within a DB transaction.
func (r *LedgerRepository) CreateEntry(tx *gorm.DB, entry *model.LedgerEntry) error {
	return tx.Create(entry).Error
}

// GetEntriesByAccount returns ledger entries for an account, ordered newest first.
func (r *LedgerRepository) GetEntriesByAccount(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
	var entries []model.LedgerEntry
	var total int64

	query := r.db.Model(&model.LedgerEntry{}).Where("account_number = ?", accountNumber)
	query.Count(&total)

	if page <= 0 { page = 1 }
	if pageSize <= 0 { pageSize = 20 }
	offset := (page - 1) * pageSize

	err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&entries).Error
	return entries, total, err
}

// GetBalance calculates account balance from ledger entries (for reconciliation).
func (r *LedgerRepository) GetBalance(accountNumber string) (decimal.Decimal, error) {
	var result struct {
		Balance decimal.Decimal
	}
	err := r.db.Model(&model.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE -amount END), 0) as balance").
		Where("account_number = ?", accountNumber).
		Scan(&result).Error
	return result.Balance, err
}

// GetDB returns the underlying *gorm.DB for transaction support.
func (r *LedgerRepository) GetDB() *gorm.DB {
	return r.db
}
```

- [ ] **Step 2: Commit**

```bash
git add account-service/internal/repository/ledger_repository.go
git commit -m "feat(account): add ledger repository with entry creation and balance reconciliation"
```

---

### Task 4.3: Create ledger service with transactional balance updates

**Files:**
- Create: `account-service/internal/service/ledger_service.go`
- Create: `account-service/internal/service/ledger_service_test.go`

- [ ] **Step 1: Write ledger service test**

```go
// account-service/internal/service/ledger_service_test.go
package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestLedgerIdempotencyKey(t *testing.T) {
	key := buildIdempotencyKey("105123456789012345", "payment", "42", "debit")
	assert.Equal(t, "105123456789012345:payment:42:debit", key)
}

func TestCreditAndDebitAreOpposite(t *testing.T) {
	amount := decimal.NewFromInt(1000)
	assert.True(t, amount.IsPositive())
	assert.True(t, amount.Neg().IsNegative())
}
```

- [ ] **Step 2: Write the ledger service**

```go
// account-service/internal/service/ledger_service.go
package service

import (
	"fmt"
	"time"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type LedgerService struct {
	ledgerRepo  *repository.LedgerRepository
	accountRepo *repository.AccountRepository
}

func NewLedgerService(ledgerRepo *repository.LedgerRepository, accountRepo *repository.AccountRepository) *LedgerService {
	return &LedgerService{ledgerRepo: ledgerRepo, accountRepo: accountRepo}
}

// RecordTransfer creates a debit entry on the source account and a credit entry
// on the destination account, updating both balances atomically.
// This is the ONLY way balances should be modified.
func (s *LedgerService) RecordTransfer(
	fromAccount, toAccount string,
	amount decimal.Decimal,
	currency string,
	refType string, // "payment", "transfer", "fee", etc.
	refID string,
	description string,
) error {
	if !amount.IsPositive() {
		return fmt.Errorf("ledger transfer amount must be positive")
	}

	db := s.ledgerRepo.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		// 1. Lock and fetch source account
		var fromAcc model.Account
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("account_number = ?", fromAccount).First(&fromAcc).Error; err != nil {
			return fmt.Errorf("source account not found: %w", err)
		}

		// 2. Check sufficient balance
		if fromAcc.AvailableBalance.LessThan(amount) {
			return fmt.Errorf("insufficient balance: available %s, required %s",
				fromAcc.AvailableBalance.StringFixed(2), amount.StringFixed(2))
		}

		// 3. Lock and fetch destination account
		var toAcc model.Account
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("account_number = ?", toAccount).First(&toAcc).Error; err != nil {
			return fmt.Errorf("destination account not found: %w", err)
		}

		now := time.Now()

		// 4. Create DEBIT entry on source
		debitEntry := &model.LedgerEntry{
			AccountNumber:  fromAccount,
			EntryType:      "debit",
			Amount:         amount,
			BalanceAfter:   fromAcc.Balance.Sub(amount),
			Currency:       currency,
			Description:    description,
			ReferenceType:  refType,
			ReferenceID:    refID,
			IdempotencyKey: buildIdempotencyKey(fromAccount, refType, refID, "debit"),
			CreatedAt:      now,
		}
		if err := s.ledgerRepo.CreateEntry(tx, debitEntry); err != nil {
			return fmt.Errorf("failed to create debit entry: %w", err)
		}

		// 5. Create CREDIT entry on destination
		creditEntry := &model.LedgerEntry{
			AccountNumber:  toAccount,
			EntryType:      "credit",
			Amount:         amount,
			BalanceAfter:   toAcc.Balance.Add(amount),
			Currency:       currency,
			Description:    description,
			ReferenceType:  refType,
			ReferenceID:    refID,
			IdempotencyKey: buildIdempotencyKey(toAccount, refType, refID, "credit"),
			CreatedAt:      now,
		}
		if err := s.ledgerRepo.CreateEntry(tx, creditEntry); err != nil {
			return fmt.Errorf("failed to create credit entry: %w", err)
		}

		// 6. Update source account balance (with optimistic lock)
		fromResult := tx.Model(&model.Account{}).
			Where("account_number = ? AND version = ?", fromAccount, fromAcc.Version).
			Updates(map[string]interface{}{
				"balance":           gorm.Expr("balance - ?", amount),
				"available_balance": gorm.Expr("available_balance - ?", amount),
				"version":           fromAcc.Version + 1,
			})
		if fromResult.RowsAffected == 0 {
			return fmt.Errorf("concurrent modification on source account")
		}

		// 7. Update destination account balance (with optimistic lock)
		toResult := tx.Model(&model.Account{}).
			Where("account_number = ? AND version = ?", toAccount, toAcc.Version).
			Updates(map[string]interface{}{
				"balance":           gorm.Expr("balance + ?", amount),
				"available_balance": gorm.Expr("available_balance + ?", amount),
				"version":           toAcc.Version + 1,
			})
		if toResult.RowsAffected == 0 {
			return fmt.Errorf("concurrent modification on destination account")
		}

		return nil
	})
}

// RecordSingleEntry records a one-sided ledger entry (deposit, withdrawal, fee, interest).
func (s *LedgerService) RecordSingleEntry(
	accountNumber string,
	entryType string, // "debit" or "credit"
	amount decimal.Decimal,
	currency, refType, refID, description string,
) error {
	if !amount.IsPositive() {
		return fmt.Errorf("amount must be positive")
	}

	db := s.ledgerRepo.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		var acc model.Account
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("account_number = ?", accountNumber).First(&acc).Error; err != nil {
			return err
		}

		var balanceAfter decimal.Decimal
		var balanceExpr interface{}
		if entryType == "credit" {
			balanceAfter = acc.Balance.Add(amount)
			balanceExpr = gorm.Expr("balance + ?", amount)
		} else {
			if acc.AvailableBalance.LessThan(amount) {
				return fmt.Errorf("insufficient balance")
			}
			balanceAfter = acc.Balance.Sub(amount)
			balanceExpr = gorm.Expr("balance - ?", amount)
		}

		entry := &model.LedgerEntry{
			AccountNumber:  accountNumber,
			EntryType:      entryType,
			Amount:         amount,
			BalanceAfter:   balanceAfter,
			Currency:       currency,
			Description:    description,
			ReferenceType:  refType,
			ReferenceID:    refID,
			IdempotencyKey: buildIdempotencyKey(accountNumber, refType, refID, entryType),
			CreatedAt:      time.Now(),
		}
		if err := s.ledgerRepo.CreateEntry(tx, entry); err != nil {
			return err
		}

		result := tx.Model(&model.Account{}).
			Where("account_number = ? AND version = ?", accountNumber, acc.Version).
			Updates(map[string]interface{}{
				"balance":           balanceExpr,
				"available_balance": balanceExpr,
				"version":           acc.Version + 1,
			})
		if result.RowsAffected == 0 {
			return fmt.Errorf("concurrent modification")
		}
		return nil
	})
}

// GetEntries returns paginated ledger entries for an account.
func (s *LedgerService) GetEntries(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
	return s.ledgerRepo.GetEntriesByAccount(accountNumber, page, pageSize)
}

// ReconcileBalance checks if account.Balance matches the sum of ledger entries.
func (s *LedgerService) ReconcileBalance(accountNumber string) (bool, decimal.Decimal, decimal.Decimal, error) {
	// Get stored balance
	acc, err := s.accountRepo.GetByNumber(accountNumber)
	if err != nil {
		return false, decimal.Zero, decimal.Zero, err
	}

	// Get ledger-derived balance
	ledgerBalance, err := s.ledgerRepo.GetBalance(accountNumber)
	if err != nil {
		return false, decimal.Zero, decimal.Zero, err
	}

	matches := acc.Balance.Equal(ledgerBalance)
	return matches, acc.Balance, ledgerBalance, nil
}

func buildIdempotencyKey(accountNumber, refType, refID, entryType string) string {
	return accountNumber + ":" + refType + ":" + refID + ":" + entryType
}
```

- [ ] **Step 3: Wire in main.go**

In `account-service/cmd/main.go`:
```go
ledgerRepo := repository.NewLedgerRepository(db)
ledgerService := service.NewLedgerService(ledgerRepo, accountRepo)
// Pass ledgerService to gRPC handler
```

- [ ] **Step 4: Add GetLedgerEntries RPC to account.proto**

```protobuf
rpc GetLedgerEntries(GetLedgerEntriesRequest) returns (GetLedgerEntriesResponse);

message GetLedgerEntriesRequest {
  string account_number = 1;
  int32 page = 2;
  int32 page_size = 3;
}

message LedgerEntryResponse {
  uint64 id = 1;
  string account_number = 2;
  string entry_type = 3;
  string amount = 4;
  string balance_after = 5;
  string currency = 6;
  string description = 7;
  string reference_type = 8;
  string reference_id = 9;
  string created_at = 10;
}

message GetLedgerEntriesResponse {
  repeated LedgerEntryResponse entries = 1;
  int64 total = 2;
}
```

- [ ] **Step 5: Regenerate proto and implement handler**

Run: `make proto`

Add handler method in `account-service/internal/handler/grpc_handler.go`.

- [ ] **Step 6: Commit**

```bash
git add account-service/ contract/proto/account/ contract/accountpb/
git commit -m "feat(account): implement double-entry ledger with transactional balance updates"
```

---

## Chunk 5: Transaction Reliability

This chunk makes financial operations reliable: wrapping in DB transactions, verifying balances before debiting, enforcing idempotency, implementing proper status state machines, and having transaction-service actually call account-service to move money.

### Task 5.1: Add account-service gRPC client to transaction-service

**Files:**
- Create: `transaction-service/internal/grpc/account_client.go`
- Modify: `transaction-service/internal/config/config.go`
- Modify: `transaction-service/cmd/main.go`

- [ ] **Step 1: Add ACCOUNT_GRPC_ADDR to config**

In `transaction-service/internal/config/config.go`, add:

```go
type Config struct {
	// ... existing fields
	AccountGRPCAddr string // NEW
}

// In Load():
AccountGRPCAddr: getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
```

- [ ] **Step 2: Create account client factory**

```go
// transaction-service/internal/grpc/account_client.go
package grpc

import (
	accountpb "github.com/exbanka/contract/accountpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func NewAccountClient(addr string) (accountpb.AccountServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return accountpb.NewAccountServiceClient(conn), nil
}
```

- [ ] **Step 3: Wire in main.go**

```go
accountClient, err := grpcconn.NewAccountClient(cfg.AccountGRPCAddr)
if err != nil {
	log.Fatalf("failed to connect to account-service: %v", err)
}
```

- [ ] **Step 4: Commit**

```bash
git add transaction-service/
git commit -m "feat(transaction): add account-service gRPC client connection"
```

---

### Task 5.2: Implement reliable payment flow with balance verification

**Files:**
- Modify: `transaction-service/internal/service/payment_service.go`

- [ ] **Step 1: Rewrite CreatePayment with proper flow**

```go
func (s *PaymentService) CreatePayment(ctx context.Context, payment *model.Payment, idempotencyKey string) (*model.Payment, error) {
	// 1. Set idempotency key
	payment.IdempotencyKey = idempotencyKey
	if idempotencyKey == "" {
		payment.IdempotencyKey = shared.GenerateIdempotencyKey()
	}

	// 2. Check for duplicate (idempotency)
	existing, err := s.repo.GetByIdempotencyKey(payment.IdempotencyKey)
	if err == nil && existing != nil {
		return existing, nil // return cached result
	}

	// 3. Calculate commission
	commission := s.CalculatePaymentCommission(payment.InitialAmount)
	payment.Commission = commission
	payment.FinalAmount = payment.InitialAmount.Add(commission)
	payment.Status = "pending"
	payment.Timestamp = time.Now()

	// 4. Create payment record
	if err := s.repo.Create(payment); err != nil {
		return nil, fmt.Errorf("failed to create payment: %w", err)
	}

	// 5. Update status to processing
	if err := s.repo.UpdateStatus(payment.ID, "processing"); err != nil {
		return nil, err
	}

	// 6. Call account-service to debit source account via ledger
	_, err = s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   payment.FromAccountNumber,
		Amount:          shared.FormatAmount(payment.FinalAmount.Neg()), // negative = debit
		UpdateAvailable: true,
		ReferenceType:   "payment",
		ReferenceId:     fmt.Sprintf("%d", payment.ID),
	})
	if err != nil {
		// Debit failed — mark payment as failed
		_ = s.repo.UpdateStatus(payment.ID, "failed")
		payment.Status = "failed"
		payment.FailureReason = err.Error()
		return payment, fmt.Errorf("debit failed: %w", err)
	}

	// 7. Credit destination account
	_, err = s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   payment.ToAccountNumber,
		Amount:          shared.FormatAmount(payment.InitialAmount), // positive = credit
		UpdateAvailable: true,
		ReferenceType:   "payment",
		ReferenceId:     fmt.Sprintf("%d", payment.ID),
	})
	if err != nil {
		// Credit failed — need to reverse the debit (compensating transaction)
		_, reverseErr := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   payment.FromAccountNumber,
			Amount:          shared.FormatAmount(payment.FinalAmount), // positive = reverse debit
			UpdateAvailable: true,
			ReferenceType:   "payment_reversal",
			ReferenceId:     fmt.Sprintf("%d", payment.ID),
		})
		if reverseErr != nil {
			log.Printf("CRITICAL: failed to reverse debit for payment %d: %v", payment.ID, reverseErr)
		}
		_ = s.repo.UpdateStatus(payment.ID, "failed")
		payment.Status = "failed"
		return payment, fmt.Errorf("credit failed: %w", err)
	}

	// 8. Mark as completed
	now := time.Now()
	payment.Status = "completed"
	payment.CompletedAt = &now
	_ = s.repo.UpdateStatus(payment.ID, "completed")

	return payment, nil
}
```

- [ ] **Step 2: Add GetByIdempotencyKey to payment repository**

```go
func (r *PaymentRepository) GetByIdempotencyKey(key string) (*model.Payment, error) {
	var payment model.Payment
	err := r.db.Where("idempotency_key = ?", key).First(&payment).Error
	if err != nil {
		return nil, err
	}
	return &payment, nil
}
```

- [ ] **Step 3: Update account.proto UpdateBalance to support ledger reference**

Add fields to `UpdateBalanceRequest`:

```protobuf
message UpdateBalanceRequest {
  string account_number = 1;
  string amount = 2;
  bool update_available = 3;
  string reference_type = 4;  // NEW: payment, transfer, fee, etc.
  string reference_id = 5;    // NEW: source transaction ID
}
```

- [ ] **Step 4: Update account-service UpdateBalance handler to use ledger**

In `account-service/internal/handler/grpc_handler.go`, route UpdateBalance through the ledger service instead of directly updating balance.

- [ ] **Step 5: Verify it builds**

Run: `make build`

- [ ] **Step 6: Commit**

```bash
git add transaction-service/ account-service/ contract/
git commit -m "feat(transaction): implement reliable payment flow with balance verification, idempotency, and compensating transactions"
```

---

### Task 5.3: Implement reliable transfer flow with currency conversion

**Files:**
- Modify: `transaction-service/internal/service/transfer_service.go`

- [ ] **Step 1: Rewrite CreateTransfer with proper flow**

Same pattern as payment but additionally:
- Look up exchange rate if currencies differ
- Convert amount using sell rate
- Debit source in source currency, credit destination in destination currency
- Use idempotency key
- Compensating transaction on failure

The logic follows the same structure as Task 5.2 Step 1 but with exchange rate lookup and currency conversion added between steps 3 and 4.

- [ ] **Step 2: Verify and commit**

```bash
git add transaction-service/
git commit -m "feat(transaction): implement reliable transfer flow with currency conversion and rollback"
```

---

### Task 5.4: Add idempotency key to proto and API gateway

**Files:**
- Modify: `contract/proto/transaction/transaction.proto`
- Modify: `api-gateway/internal/handler/transaction_handler.go`

- [ ] **Step 1: Add idempotency_key field to payment and transfer request protos**

```protobuf
message CreatePaymentRequest {
  string idempotency_key = 1;  // NEW: client-provided UUID
  string from_account_number = 2;
  // ... rest unchanged, renumber
}

message CreateTransferRequest {
  string idempotency_key = 1;  // NEW
  // ... rest
}
```

- [ ] **Step 2: Update API gateway to pass Idempotency-Key header**

In the gateway transaction handler, extract the `Idempotency-Key` HTTP header and pass it in the proto request:

```go
func (h *TransactionHandler) CreatePayment(c *gin.Context) {
	var req struct { ... }
	c.ShouldBindJSON(&req)

	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		idempotencyKey = shared.GenerateIdempotencyKey()
	}

	resp, err := h.txClient.CreatePayment(c.Request.Context(), &txpb.CreatePaymentRequest{
		IdempotencyKey: idempotencyKey,
		// ... other fields
	})
	// ...
}
```

- [ ] **Step 3: Commit**

```bash
git add contract/ api-gateway/
git commit -m "feat: add idempotency key support to payment and transfer APIs"
```

---

### Task 5.5: Implement credit installment collection with retry logic

**Files:**
- Modify: `credit-service/internal/service/cron_service.go`

- [ ] **Step 1: Rewrite collectDueInstallments with retry and account deduction**

```go
func (s *CronService) collectDueInstallments() {
	// 1. Mark overdue installments
	s.installmentService.MarkOverdueInstallments()

	// 2. Get all unpaid installments due today or earlier
	dueInstallments, err := s.installmentRepo.ListDue()
	if err != nil {
		log.Printf("ERROR: failed to list due installments: %v", err)
		return
	}

	for _, inst := range dueInstallments {
		// 3. Get the loan to find the account number
		loan, err := s.loanService.GetLoan(inst.LoanID)
		if err != nil {
			log.Printf("ERROR: failed to get loan %d: %v", inst.LoanID, err)
			continue
		}

		// 4. Try to deduct from client's account via account-service gRPC
		err = shared.Retry(context.Background(), shared.RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   5 * time.Second,
		}, func() error {
			_, deductErr := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
				AccountNumber:   loan.AccountNumber,
				Amount:          shared.FormatAmount(inst.Amount.Neg()),
				UpdateAvailable: true,
				ReferenceType:   "installment",
				ReferenceId:     fmt.Sprintf("%d", inst.ID),
			})
			return deductErr
		})

		if err != nil {
			log.Printf("WARNING: installment %d collection failed after retries: %v", inst.ID, err)
			// Publish failure event
			s.producer.PublishInstallmentFailed(context.Background(), kafkamsg.InstallmentResultMessage{
				LoanID:      inst.LoanID,
				Amount:      shared.FormatAmount(inst.Amount),
				Success:     false,
				Error:       err.Error(),
			})
			continue
		}

		// 5. Mark installment as paid
		s.installmentRepo.MarkPaid(inst.ID)

		// 6. Update remaining debt on loan
		// ... update loan.RemainingDebt -= inst.Amount

		// 7. Publish success event
		s.producer.PublishInstallmentCollected(context.Background(), kafkamsg.InstallmentResultMessage{
			LoanID:  inst.LoanID,
			Amount:  shared.FormatAmount(inst.Amount),
			Success: true,
		})
	}
}
```

- [ ] **Step 2: Add ListDue to installment repository**

```go
func (r *InstallmentRepository) ListDue() ([]model.Installment, error) {
	var installments []model.Installment
	err := r.db.Where("status = 'unpaid' AND expected_date <= ?", time.Now()).
		Order("expected_date ASC").Find(&installments).Error
	return installments, err
}
```

- [ ] **Step 3: Add account-service gRPC client to credit-service**

Same pattern as Task 5.1 — add config, client factory, wire in main.go.

- [ ] **Step 4: Commit**

```bash
git add credit-service/
git commit -m "feat(credit): implement installment collection with retry logic and account deduction"
```

---

## Chunk 6: Service Boundaries & Operations

This chunk fixes service responsibility issues, adds health checks, and improves operational readiness.

### Task 6.1: Move employee creation orchestration from gateway to user-service

Currently, the API gateway's `CreateEmployee` handler calls user-service AND then auth-service `CreateActivationToken`. This orchestration belongs in user-service.

**Files:**
- Modify: `user-service/internal/service/employee_service.go`
- Modify: `user-service/cmd/main.go`
- Modify: `api-gateway/internal/handler/employee_handler.go`

- [ ] **Step 1: Add auth-service gRPC client to user-service**

In `user-service/cmd/main.go`, connect to auth-service:

```go
authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
	log.Fatalf("failed to connect to auth-service: %v", err)
}
authClient := authpb.NewAuthServiceClient(authConn)
```

Pass `authClient` to `EmployeeService`.

- [ ] **Step 2: Add AUTH_GRPC_ADDR to user-service config**

```go
AuthGRPCAddr: getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
```

- [ ] **Step 3: Move activation token creation into user-service CreateEmployee**

In `employee_service.go`:

```go
func (s *EmployeeService) CreateEmployee(ctx context.Context, emp *model.Employee) error {
	// ... existing validation and creation logic ...

	if err := s.repo.Create(emp); err != nil {
		return err
	}

	// Publish Kafka event
	s.producer.PublishEmployeeCreated(ctx, kafkamsg.EmployeeCreatedMessage{...})

	// Create activation token via auth-service (was in gateway)
	_, err := s.authClient.CreateActivationToken(ctx, &authpb.CreateActivationTokenRequest{
		UserId:    emp.ID,
		Email:     emp.Email,
		FirstName: emp.FirstName,
	})
	if err != nil {
		log.Printf("WARNING: failed to create activation token for %s: %v", emp.Email, err)
		// Don't fail the creation — activation can be retried
	}

	return nil
}
```

- [ ] **Step 4: Simplify gateway CreateEmployee handler**

Remove the auth-service call from `api-gateway/internal/handler/employee_handler.go`:

```go
func (h *EmployeeHandler) CreateEmployee(c *gin.Context) {
	// Bind and validate request
	// Call user-service CreateEmployee (which now handles activation internally)
	// Return response
	// NO LONGER calls auth-service CreateActivationToken here
}
```

- [ ] **Step 5: Commit**

```bash
git add user-service/ api-gateway/
git commit -m "refactor: move employee activation orchestration from gateway to user-service"
```

---

### Task 6.2: Add gRPC health check endpoints to all services

**Files:**
- Create: `contract/shared/health.go` (reusable health check server)
- Modify: All service `cmd/main.go` files

- [ ] **Step 1: Add health check server helper**

```go
// contract/shared/health.go
package shared

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterHealthCheck adds the standard gRPC health check service.
func RegisterHealthCheck(s *grpc.Server, serviceName string) {
	healthServer := health.NewServer()
	healthServer.SetServingStatus(serviceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(s, healthServer)
}
```

- [ ] **Step 2: Add to each service's main.go**

In each service's `cmd/main.go`, after creating the gRPC server:

```go
s := grpc.NewServer()
shared.RegisterHealthCheck(s, "auth-service") // service-specific name
```

- [ ] **Step 3: Add health check to docker-compose**

For each service in `docker-compose.yml`:

```yaml
healthcheck:
  test: ["CMD", "grpc_health_probe", "-addr=:50051"]
  interval: 10s
  timeout: 5s
  retries: 3
```

- [ ] **Step 4: Commit**

```bash
git add contract/shared/health.go */cmd/main.go docker-compose.yml
git commit -m "feat: add gRPC health checks to all services"
```

---

### Task 6.3: Add graceful shutdown to all services

**Files:**
- Modify: All service `cmd/main.go` files

- [ ] **Step 1: Apply graceful shutdown pattern to each service**

Replace the simple `s.Serve(lis)` with signal-aware shutdown:

```go
func main() {
	// ... existing setup ...

	// Start gRPC server in goroutine
	go func() {
		log.Printf("Starting %s on %s", serviceName, cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	s.GracefulStop()
	producer.Close()
	if redisCache != nil {
		redisCache.Close()
	}
	log.Println("Server stopped")
}
```

Apply to all 8 backend services and the API gateway.

- [ ] **Step 2: Commit**

```bash
git add */cmd/main.go api-gateway/cmd/main.go
git commit -m "feat: add graceful shutdown with signal handling to all services"
```

---

### Task 6.4: Add Kafka event publishing to user-service (missing requirement)

The user-service currently has a Kafka producer but never publishes events. This violates the CLAUDE.md requirement.

**Files:**
- Modify: `user-service/internal/kafka/producer.go`
- Modify: `user-service/internal/handler/grpc_handler.go`
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Add employee event topics and messages**

In `contract/kafka/messages.go`:

```go
TopicEmployeeCreated  = "user.employee-created"
TopicEmployeeUpdated  = "user.employee-updated"

type EmployeeCreatedMessage struct {
	EmployeeID int64  `json:"employee_id"`
	Email      string `json:"email"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Role       string `json:"role"`
}
```

- [ ] **Step 2: Add publish methods to user-service producer**

In `user-service/internal/kafka/producer.go`:

```go
func (p *Producer) PublishEmployeeCreated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicEmployeeCreated, msg)
}

func (p *Producer) PublishEmployeeUpdated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicEmployeeUpdated, msg)
}
```

- [ ] **Step 3: Publish events from handler**

In `user-service/internal/handler/grpc_handler.go`, after successful create/update operations, publish Kafka events.

- [ ] **Step 4: Commit**

```bash
git add user-service/ contract/kafka/messages.go
git commit -m "feat(user): add Kafka event publishing for employee create/update operations"
```

---

### Task 6.5: Update CLAUDE.md with new architecture details

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md**

Add sections documenting:
- The ledger system in account-service
- The 2FA flow
- The idempotency key pattern
- The brute-force protection mechanism
- The decimal money type usage
- The config migration (no more .env)
- The health check endpoints

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md with refactored architecture details"
```

---

## Review Fixes — Critical Issues Addressed

The following corrections address critical findings from plan review. These MUST be applied during implementation.

### Fix F1: Use GORM v2 `clause.Locking` instead of deprecated `gorm:query_option` [C4]

**Everywhere the plan uses `tx.Set("gorm:query_option", "FOR UPDATE")`, replace with:**

```go
import "gorm.io/gorm/clause"

tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(...)
```

This applies to all `SELECT ... FOR UPDATE` in the ledger service (Task 4.3).

### Fix F2: Use pessimistic locking only (remove version checks from locked paths) [C5]

In `RecordTransfer` and `RecordSingleEntry` (Task 4.3), the `FOR UPDATE` lock already prevents concurrent modification within the DB transaction. Remove the `WHERE version = ?` check from the balance UPDATE within those methods. Keep the `Version` field on models for use in other contexts (e.g., non-locked reads), but don't combine both locking strategies in the same transaction.

### Fix F3: Route commissions to bank's own account [C2, C7]

Add a **bank account seeding** step in account-service startup (similar to currency seeding):

```go
// account-service/internal/model/account.go
func SeedBankAccounts(db *gorm.DB) {
    currencies := []string{"RSD", "EUR", "USD", "CHF", "GBP", "JPY", "CAD", "AUD"}
    for _, curr := range currencies {
        bankAcct := Account{
            AccountNumber: "105000000BANK" + curr, // deterministic bank account numbers
            AccountName:   "EXBanka " + curr + " Operating Account",
            OwnerID:       0, // system-owned
            Balance:       decimal.Zero,
            AvailableBalance: decimal.Zero,
            CurrencyCode:  curr,
            Status:        "active",
            AccountKind:   "current",
            AccountType:   "bank_internal",
            ExpiresAt:     time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC),
            Version:       1,
        }
        db.Where("account_number = ?", bankAcct.AccountNumber).FirstOrCreate(&bankAcct)
    }
}
```

In the payment flow (Task 5.2), after debiting the source and crediting the destination, **also credit the commission to the bank's operating account**:

```go
// Step 7b: Credit commission to bank account
if payment.Commission.IsPositive() {
    bankAccountNumber := "105000000BANK" + payment.CurrencyCode
    _, err = s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
        AccountNumber: bankAccountNumber,
        Amount:        shared.FormatAmount(payment.Commission),
        UpdateAvailable: true,
        ReferenceType: "commission",
        ReferenceId:   fmt.Sprintf("%d", payment.ID),
    })
    if err != nil {
        log.Printf("WARNING: failed to credit commission for payment %d: %v", payment.ID, err)
    }
}
```

### Fix F4: Integrate verification codes into payment state machine [C1]

The payment status flow must include a verification step:

```
pending → awaiting_verification → processing → completed / failed / cancelled
```

In the payment service `CreatePayment`:
1. Create payment with status `pending`
2. Generate verification code (call existing verification service)
3. Set status to `awaiting_verification`, return payment + verification code
4. Client calls `ValidateVerificationCode` with the code
5. On valid code, a NEW method `ExecutePayment(paymentID)` is called which:
   - Changes status to `processing`
   - Performs the actual debit/credit/commission flow
   - Changes status to `completed` or `failed`
6. If verification expires (5 min) or 3 failed attempts, set status to `cancelled`

### Fix F5: Enforce daily/monthly spending limits [C3]

In the payment/transfer flow (Task 5.2), before debiting:

```go
// Check daily limit
account, _ := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
    AccountNumber: payment.FromAccountNumber,
})
currentDaily, _ := shared.ParseAmount(account.DailySpending)
dailyLimit, _ := shared.ParseAmount(account.DailyLimit)
if currentDaily.Add(payment.FinalAmount).GreaterThan(dailyLimit) {
    return nil, fmt.Errorf("daily spending limit exceeded")
}

// Same for monthly limit
currentMonthly, _ := shared.ParseAmount(account.MonthlySpending)
monthlyLimit, _ := shared.ParseAmount(account.MonthlyLimit)
if currentMonthly.Add(payment.FinalAmount).GreaterThan(monthlyLimit) {
    return nil, fmt.Errorf("monthly spending limit exceeded")
}
```

After successful debit, increment spending counters on the account.

Add a **daily/monthly spending reset cron** in account-service:
- Reset `DailySpending` to 0 at midnight (daily cron)
- Reset `MonthlySpending` to 0 on the 1st of each month

### Fix F6: Fix AccountLock uniqueIndex issue [I11]

Change `AccountLock.UserID` from `uniqueIndex` to regular `index`:

```go
UserID int64 `gorm:"not null;index"` // was uniqueIndex — allows multiple lock records
```

Also change the lock logic to use email as the primary identifier (since we may not know UserID on failed login):

```go
func (r *LoginAttemptRepository) LockAccount(email string, duration time.Duration) error {
    lock := model.AccountLock{
        Email:     email,
        LockedAt:  time.Now(),
        ExpiresAt: time.Now().Add(duration),
    }
    return r.db.Create(&lock).Error
}
```

### Fix F7: Fix circular dependency — use Kafka instead of gRPC for activation [I8]

In Task 6.1, instead of user-service calling auth-service via gRPC (creating a circular dependency since auth already calls user), publish a Kafka event:

```go
// In user-service CreateEmployee, instead of gRPC call:
s.producer.PublishEmployeeCreated(ctx, kafkamsg.EmployeeCreatedMessage{
    EmployeeID: emp.ID,
    Email:      emp.Email,
    FirstName:  emp.FirstName,
    LastName:   emp.LastName,
    Role:       emp.Role,
})

// Auth-service adds a Kafka CONSUMER that listens for "user.employee-created"
// and automatically creates activation tokens + sends activation emails.
```

This eliminates the bidirectional gRPC dependency.

### Fix F8: Fix TOTP import issue [C6]

In Task 2.3, `ValidateCodeWithWindow` needs both imports:

```go
import (
    "github.com/pquerna/otp"
    "github.com/pquerna/otp/totp"
)
```

### Fix F9: Fix RefreshToken flow for client tokens [M5]

In auth-service `RefreshToken` method, check the token's associated role. If role is "client", call client-service instead of user-service:

```go
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (string, string, error) {
    // ... existing token fetch and validation ...

    // Determine if this is a client or employee token
    // Store role info alongside refresh token, or look it up
    if isClientToken {
        clientResp, err := s.clientClient.GetClient(ctx, ...)
        // generate token with role="client"
    } else {
        empResp, err := s.userClient.GetEmployee(ctx, ...)
        // generate token with employee role + permissions
    }
}
```

Add a `Role` field to the `RefreshToken` model to track this.

### Fix F10: Cross-currency transfer via RSD intermediate [I1, I14]

For cross-currency transfers (e.g., EUR → USD), implement a two-hop flow:
1. Debit source account (EUR)
2. Credit bank's EUR account (at sell rate → RSD equivalent)
3. Debit bank's USD account (at sell rate from RSD → USD)
4. Credit destination account (USD)

The ledger `RecordTransfer` needs an overloaded version for cross-currency:

```go
func (s *LedgerService) RecordCrossCurrencyTransfer(
    fromAccount, toAccount string,
    debitAmount decimal.Decimal, debitCurrency string,
    creditAmount decimal.Decimal, creditCurrency string,
    exchangeRate decimal.Decimal,
    refType, refID, description string,
) error { ... }
```

### Fix F11: Proper double-entry for single-sided operations [I3]

Replace `RecordSingleEntry` with proper double-entry using a control account:

- Deposits: Debit "external_inflow" control account, Credit customer account
- Withdrawals: Debit customer account, Credit "external_outflow" control account
- Fees: Debit customer account, Credit bank operating account
- Interest: Debit bank interest expense account, Credit customer account

Seed control accounts alongside bank accounts in Fix F3.

### Fix F12: 72-hour installment retry schedule [I16]

Replace the immediate 3-retry loop in Task 5.5 with a scheduled retry:

```go
// In cron_service.go collectDueInstallments:
// On first failure, record failure time and schedule retry
// Instead of immediate Retry(), update installment with:
installment.RetryCount++
installment.NextRetryAt = time.Now().Add(retryIntervals[installment.RetryCount])
// retryIntervals: {4h, 12h, 24h, 48h, 72h}
// After 72h of failures, mark as "defaulted" and apply penalty rate
```

---

## Implementation Order Summary

| Order | Task | Est. Complexity | Dependencies |
|-------|------|-----------------|--------------|
| 1 | 1.1 Money utils | Low | None |
| 2 | 1.2 Idempotency utils | Low | None |
| 3 | 1.3 Retry utils | Low | None |
| 4 | 1.4 Proto decimal migration | High | None |
| 5 | 1.5 Kafka message types | Low | 1.4 |
| 6 | 1.6 Config migration | Medium | None |
| 7 | 1.7 Add decimal deps | Low | None |
| 8 | 2.1 Login attempt model | Medium | None |
| 9 | 2.2 Brute-force protection | Medium | 2.1 |
| 10 | 2.3 TOTP 2FA model/service | Medium | None |
| 11 | 2.4 2FA proto + integration | High | 2.3, 1.4 |
| 12 | 2.5 Session tracking | Medium | 2.1 |
| 13 | 2.6 JWT JTI + blacklisting | Medium | None |
| 14 | 3.1 Optimistic lock helper | Low | None |
| 15 | 3.2 Account models upgrade | High | 1.4, 1.7, 3.1 |
| 16 | 3.3 Transaction models upgrade | High | 1.4, 1.7, 3.1 |
| 17 | 3.4 Credit models upgrade | High | 1.4, 1.7, 3.1 |
| 18 | 3.5 Card model upgrade | Low | 1.4, 1.7 |
| 19 | 3.6 Admin seeding fix | Low | None |
| 20 | 4.1 Ledger model | Low | 3.2 |
| 21 | 4.2 Ledger repository | Medium | 4.1 |
| 22 | 4.3 Ledger service | High | 4.2 |
| 23 | 5.1 Account client in tx-svc | Low | None |
| 24 | 5.2 Reliable payment flow | High | 4.3, 5.1 |
| 25 | 5.3 Reliable transfer flow | High | 5.2 |
| 26 | 5.4 Idempotency in API | Medium | 5.2 |
| 27 | 5.5 Credit collection retry | Medium | 5.1 |
| 28 | 6.1 Move orchestration | Medium | None |
| 29 | 6.2 Health checks | Low | None |
| 30 | 6.3 Graceful shutdown | Low | None |
| 31 | 6.4 User-service Kafka events | Low | None |
| 32 | 6.5 Update CLAUDE.md | Low | All |

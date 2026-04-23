# Bank-Safe Settlement — Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix bug #4 (all seven sub-issues 4a–4h in `docs/Bugs.txt`): introduce a fund-reservation mechanism on accounts, a saga-log pattern in stock-service for cross-service fill orchestration, proper cross-currency conversion, forex-as-exchange settlement (no holding; debits quote account, credits base account), commission as an explicit saga step, Kafka-after-saga-commit, and recovery reconciliation on startup.

**Prerequisites:** Phase 1 (`2026-04-22-unblock-order-flow.md`) must be complete — orders must run end-to-end before we rewrite the fill path.

**Architecture:** Add `reserved_balance` column + `account_reservations` / `account_reservation_settlements` tables on account-service; expose four new RPCs (`ReserveFunds`, `ReleaseReservation`, `PartialSettleReservation`, `GetReservation`). Add `saga_logs` + sell-side `holding_reservations` tables on stock-service; introduce a `SagaExecutor` helper mirroring `transaction-service/internal/service/saga_helper.go`. Rewrite `order_service.CreateOrder` to run a placement saga and `portfolio_service.ProcessBuyFill` / `ProcessSellFill` (plus new forex fill path) to run a fill saga with explicit compensating transactions. All cross-service steps are idempotent on `order_id` or `order_transaction_id`.

**Tech Stack:** Go 1.22, GORM with `BeforeUpdate` optimistic-lock hooks, PostgreSQL `SELECT FOR UPDATE`, gRPC, `shopspring/decimal`, UUID v4 for saga IDs, existing Kafka producer/consumer wiring.

**Design spec:** `docs/superpowers/specs/2026-04-22-securities-bank-safety-design.md` — §4 defines the account-service reservation system, §5 the saga log + fill paths, §6 placement + holding reservations, §7 currency conversion, §8 doc updates.

**Do not commit the plan file itself.** The plan includes normal TDD commit steps for the engineer executing it — those commits happen when the plan is *executed*, not when it is written.

---

## Context

The full surface area affected:

- **account-service**
  - `internal/model/account.go` — add `ReservedBalance decimal.Decimal`
  - `internal/model/account_reservation.go` — NEW
  - `internal/model/account_reservation_settlement.go` — NEW
  - `internal/repository/account_reservation_repository.go` — NEW
  - `internal/service/reservation_service.go` — NEW
  - `internal/handler/reservation_handler.go` — NEW (or extend existing account handler)
  - `cmd/main.go` — register new migrations + gRPC handlers

- **contract/proto/account/account.proto** — add four RPCs; regenerate `contract/accountpb/`
- **stock-service**
  - `internal/model/saga_log.go` — NEW (mirror `transaction-service/internal/model/saga_log.go`)
  - `internal/model/holding.go` — add `ReservedQuantity int64`
  - `internal/model/holding_reservation.go` — NEW
  - `internal/model/holding_reservation_settlement.go` — NEW
  - `internal/model/order.go` — add reservation metadata fields
  - `internal/model/order_transaction.go` — add native/converted/fx_rate fields
  - `internal/repository/saga_log_repository.go` — NEW
  - `internal/repository/holding_reservation_repository.go` — NEW
  - `internal/service/saga_helper.go` — NEW
  - `internal/service/order_service.go` — rewrite `CreateOrder` with placement saga
  - `internal/service/portfolio_service.go` — rewrite `ProcessBuyFill` / `ProcessSellFill`
  - `internal/service/forex_fill_service.go` — NEW (forex fill path)
  - `internal/service/order_execution.go` — route forex to forex-fill; publish Kafka synchronously after saga step
  - `internal/service/saga_recovery.go` — NEW (startup reconciler)
  - `internal/grpc/account_client.go` — add methods for the new RPCs
  - `internal/grpc/exchange_client.go` — wire `Convert` RPC if not already wired
  - `cmd/main.go` — migrate new tables, wire SagaExecutor, wire recovery
- **api-gateway**
  - `internal/handler/order_handler.go` (or wherever `POST /api/v1/me/orders` is) — validate `base_account_id` for forex, reject forex sell
  - `internal/handler/validation.go` — add helpers if needed
  - Swagger annotations + regen
- **docs**
  - `docs/Specification.md` — §17 / §18 / §19 / §20 / §21 / §11 per design spec §8.1
  - `docs/api/REST_API_v1.md` — per design spec §8.2
- **test-app/workflows** — new integration tests per design spec §5.8 / §6.x / §7.x

Key existing references:
- Saga pattern reference: `transaction-service/internal/model/saga_log.go`, `transaction-service/internal/service/saga_helper.go`
- Reservation-lock pattern: `account-service/internal/repository/ledger_repository.go` (`DebitWithLock`, `CreditWithLock`)
- Exchange convert: `exchange-service/internal/service/exchange_service.go:155` (`Convert(from, to, amount)`)

This is a large plan. It is organized bottom-up: account-service first (new RPCs), then stock-service saga + placement + fill rewrites, then gateway + spec + integration tests.

---

## File Structure

### account-service (new & modified)

- `account-service/internal/model/account.go` — ADD `ReservedBalance`
- `account-service/internal/model/account_reservation.go` — NEW
- `account-service/internal/model/account_reservation_settlement.go` — NEW
- `account-service/internal/repository/account_reservation_repository.go` — NEW
- `account-service/internal/repository/account_reservation_repository_test.go` — NEW
- `account-service/internal/service/reservation_service.go` — NEW
- `account-service/internal/service/reservation_service_test.go` — NEW
- `account-service/internal/handler/reservation_handler.go` — NEW
- `account-service/internal/handler/reservation_handler_test.go` — NEW
- `account-service/cmd/main.go` — migrate + register
- `contract/proto/account/account.proto` — ADD four RPCs + messages

### stock-service (new & modified)

- `stock-service/internal/model/saga_log.go` — NEW
- `stock-service/internal/model/holding.go` — ADD `ReservedQuantity`
- `stock-service/internal/model/holding_reservation.go` — NEW
- `stock-service/internal/model/holding_reservation_settlement.go` — NEW
- `stock-service/internal/model/order.go` — ADD reservation metadata
- `stock-service/internal/model/order_transaction.go` — ADD native/converted/fx_rate
- `stock-service/internal/repository/saga_log_repository.go` — NEW
- `stock-service/internal/repository/saga_log_repository_test.go` — NEW
- `stock-service/internal/repository/holding_reservation_repository.go` — NEW
- `stock-service/internal/repository/holding_reservation_repository_test.go` — NEW
- `stock-service/internal/service/saga_helper.go` — NEW
- `stock-service/internal/service/saga_helper_test.go` — NEW
- `stock-service/internal/service/order_service.go` — rewrite
- `stock-service/internal/service/order_service_test.go` — update
- `stock-service/internal/service/portfolio_service.go` — rewrite
- `stock-service/internal/service/portfolio_service_test.go` — update
- `stock-service/internal/service/forex_fill_service.go` — NEW
- `stock-service/internal/service/forex_fill_service_test.go` — NEW
- `stock-service/internal/service/order_execution.go` — route by security_type, publish sync
- `stock-service/internal/service/saga_recovery.go` — NEW
- `stock-service/internal/service/saga_recovery_test.go` — NEW
- `stock-service/internal/grpc/account_client.go` — add methods
- `stock-service/cmd/main.go` — wire everything

### api-gateway (modified)

- `api-gateway/internal/handler/me_order_handler.go` — add `base_account_id` validation; reject forex sell
- `api-gateway/internal/handler/validation.go` — helper additions if needed
- `api-gateway/docs/` — regenerate Swagger

### docs (modified)

- `docs/Specification.md`
- `docs/api/REST_API_v1.md`
- `docs/Bugs.txt` — mark bug #4 fixed

### test-app (new)

- `test-app/workflows/wf_stock_reservation_test.go`
- `test-app/workflows/wf_stock_concurrent_orders_test.go`
- `test-app/workflows/wf_stock_cross_currency_test.go`
- `test-app/workflows/wf_stock_fill_failure_test.go`
- `test-app/workflows/wf_stock_commission_failure_test.go`
- `test-app/workflows/wf_forex_test.go`
- `test-app/workflows/wf_forex_validation_test.go`
- `test-app/workflows/wf_stop_limit_refund_test.go`
- `test-app/workflows/helpers_orders_test.go` — helpers (getOrder, assertLedgerHas, etc.)

---

## Task 1: account-service — proto additions

**Files:**
- Modify: `contract/proto/account/account.proto`
- Regenerate: `contract/accountpb/*.pb.go`

- [ ] **Step 1: Write the proto additions**

Append the following to `contract/proto/account/account.proto` (inside the `service AccountService` block and at the bottom of the file for messages):

```protobuf
service AccountService {
    // ... existing RPCs remain unchanged ...

    // Reservation lifecycle for order placement → fill → release flow (bank-safe settlement).
    rpc ReserveFunds(ReserveFundsRequest) returns (ReserveFundsResponse);
    rpc ReleaseReservation(ReleaseReservationRequest) returns (ReleaseReservationResponse);
    rpc PartialSettleReservation(PartialSettleReservationRequest) returns (PartialSettleReservationResponse);
    rpc GetReservation(GetReservationRequest) returns (GetReservationResponse);
}

message ReserveFundsRequest {
    uint64 account_id = 1;
    uint64 order_id = 2;
    string amount = 3;         // decimal string
    string currency_code = 4;  // must equal account currency
}
message ReserveFundsResponse {
    uint64 reservation_id = 1;
    string reserved_balance = 2;
    string available_balance = 3;
}

message ReleaseReservationRequest {
    uint64 order_id = 1;
}
message ReleaseReservationResponse {
    string released_amount = 1;
    string reserved_balance = 2;
}

message PartialSettleReservationRequest {
    uint64 order_id = 1;
    uint64 order_transaction_id = 2;   // idempotency key
    string amount = 3;
    string memo = 4;                    // written to ledger_entries
}
message PartialSettleReservationResponse {
    string settled_amount = 1;
    string remaining_reserved = 2;
    string balance_after = 3;
    uint64 ledger_entry_id = 4;
}

message GetReservationRequest {
    uint64 order_id = 1;
}
message GetReservationResponse {
    bool exists = 1;
    string status = 2;           // active|released|settled
    string amount = 3;
    string settled_total = 4;
    repeated uint64 settled_transaction_ids = 5;
}
```

- [ ] **Step 2: Regenerate the generated Go files**

Run: `make proto`

Expected: `contract/accountpb/account.pb.go` and `contract/accountpb/account_grpc.pb.go` regenerate with no errors.

- [ ] **Step 3: Confirm the new types exist**

Run: `cd contract && grep -l "ReserveFundsRequest\|PartialSettleReservationRequest" accountpb/*.go`

Expected: both file names listed.

- [ ] **Step 4: Commit**

```bash
git add contract/proto/account/account.proto contract/accountpb/
git commit -m "feat(contract): add reservation RPCs to AccountService proto

Part of Phase 2 (bank-safe settlement). Adds ReserveFunds,
ReleaseReservation, PartialSettleReservation, GetReservation. Regenerate
Go bindings via make proto."
```

---

## Task 2: account-service — ReservedBalance column + migration

**Files:**
- Modify: `account-service/internal/model/account.go`
- Modify: `account-service/cmd/main.go` (the `AutoMigrate` call)

- [ ] **Step 1: Add ReservedBalance to the Account model**

Open `account-service/internal/model/account.go`. Locate the `Account` struct. Add the following field (just below `AvailableBalance` if that field exists; otherwise next to `Balance`):

```go
// ReservedBalance is the running total of active reservations on this
// account. Maintained by the reservation service inside the same DB
// transaction as each reserve/release/partial-settle. Never negative.
// AvailableBalance = Balance - ReservedBalance (computed, never stored).
ReservedBalance decimal.Decimal `gorm:"type:decimal(20,4);not null;default:0" json:"reserved_balance"`
```

If there is a `Computed` helper (e.g., a method `func (a *Account) AvailableBalance() decimal.Decimal`), leave it — it will keep working as `a.Balance.Sub(a.ReservedBalance)`. If `AvailableBalance` is a stored column, keep it as a cached column but derive its updates from `Balance - ReservedBalance` inside the reservation service.

- [ ] **Step 2: Inspect the existing AvailableBalance handling**

Run: `cd account-service && grep -rn "AvailableBalance" internal/`

Note where AvailableBalance is written (which service methods). If there are places that set it directly from `Balance`, those must now subtract `ReservedBalance` as well. Keep existing callers working by updating them in Task 5 when we write the reservation service.

- [ ] **Step 3: Build the service**

Run: `cd account-service && go build ./...`

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add account-service/internal/model/account.go
git commit -m "feat(account-service): add ReservedBalance column to Account

Part of Phase 2 (bank-safe settlement). Running total of active
reservations; AvailableBalance = Balance - ReservedBalance."
```

---

## Task 3: account-service — reservation models

**Files:**
- Create: `account-service/internal/model/account_reservation.go`
- Create: `account-service/internal/model/account_reservation_settlement.go`

- [ ] **Step 1: Create the reservation model**

Create `account-service/internal/model/account_reservation.go`:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Reservation statuses.
const (
	ReservationStatusActive   = "active"
	ReservationStatusReleased = "released"
	ReservationStatusSettled  = "settled"
)

// AccountReservation is the idempotency + state ledger for a single order's
// hold on an account. `amount` is immutable after insert; only `status` (and
// timestamps/version) transition. Running totals live on the account row; this
// table is the source of truth for recovery.
type AccountReservation struct {
	ID           uint64          `gorm:"primaryKey" json:"id"`
	AccountID    uint64          `gorm:"not null;index" json:"account_id"`
	OrderID      uint64          `gorm:"not null;uniqueIndex" json:"order_id"` // idempotency key
	Amount       decimal.Decimal `gorm:"type:decimal(20,4);not null" json:"amount"`
	CurrencyCode string          `gorm:"size:3;not null" json:"currency_code"`
	Status       string          `gorm:"size:16;not null;index:idx_account_reservation_status" json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Version      int64           `gorm:"not null;default:0" json:"version"`
}

// TableName overrides the default pluralisation.
func (AccountReservation) TableName() string { return "account_reservations" }

// BeforeUpdate enforces optimistic locking via the Version column (CLAUDE.md
// §Concurrency). Caller must load the row, mutate it, then call db.Save.
func (r *AccountReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}
```

- [ ] **Step 2: Create the settlement model**

Create `account-service/internal/model/account_reservation_settlement.go`:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// AccountReservationSettlement is one row per partial-settle call. Immutable
// once inserted. The OrderTransactionID is an idempotency key from
// stock-service's OrderTransaction.ID; stock-service never reuses these IDs
// because they are serial PKs in its own DB.
type AccountReservationSettlement struct {
	ID                  uint64          `gorm:"primaryKey" json:"id"`
	ReservationID       uint64          `gorm:"not null;index" json:"reservation_id"`
	OrderTransactionID  uint64          `gorm:"not null;uniqueIndex" json:"order_transaction_id"` // idempotency key
	Amount              decimal.Decimal `gorm:"type:decimal(20,4);not null" json:"amount"`
	CreatedAt           time.Time       `json:"created_at"`
}

func (AccountReservationSettlement) TableName() string { return "account_reservation_settlements" }
```

- [ ] **Step 3: Build**

Run: `cd account-service && go build ./...`

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add account-service/internal/model/account_reservation.go \
        account-service/internal/model/account_reservation_settlement.go
git commit -m "feat(account-service): add AccountReservation and settlement models

Part of Phase 2. Reservation row is immutable except status/version;
settlement rows are append-only. order_id and order_transaction_id are
idempotency keys for ReserveFunds and PartialSettleReservation RPCs."
```

---

## Task 4: account-service — repository

**Files:**
- Create: `account-service/internal/repository/account_reservation_repository.go`
- Create: `account-service/internal/repository/account_reservation_repository_test.go`

- [ ] **Step 1: Write the failing test**

Create `account-service/internal/repository/account_reservation_repository_test.go`:

```go
package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
)

func TestReservationRepo_InsertAndGetByOrderID(t *testing.T) {
	db := newTestDB(t) // existing helper in testing_helpers_test.go
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID:    1,
		OrderID:      1001,
		Amount:       decimal.NewFromInt(500),
		CurrencyCode: "RSD",
		Status:       model.ReservationStatusActive,
	}
	if err := repo.Create(r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.GetByOrderID(1001)
	if err != nil {
		t.Fatalf("GetByOrderID: %v", err)
	}
	if got.ID != r.ID {
		t.Fatalf("ID mismatch: got %d want %d", got.ID, r.ID)
	}
	if !got.Amount.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("amount: got %s", got.Amount)
	}
}

func TestReservationRepo_SumSettlements(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 2001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	if err := repo.Create(r); err != nil {
		t.Fatal(err)
	}

	s1 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9001, Amount: decimal.NewFromInt(300),
	}
	s2 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9002, Amount: decimal.NewFromInt(200),
	}
	if err := repo.CreateSettlement(s1); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSettlement(s2); err != nil {
		t.Fatal(err)
	}

	total, err := repo.SumSettlements(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !total.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("expected 500, got %s", total)
	}
}

func TestReservationRepo_CreateSettlement_UniqueOnOrderTransactionID(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 3001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	_ = repo.Create(r)

	s := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 7001, Amount: decimal.NewFromInt(100),
	}
	if err := repo.CreateSettlement(s); err != nil {
		t.Fatal(err)
	}
	// Second insert with same OrderTransactionID must violate unique index.
	s2 := &model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 7001, Amount: decimal.NewFromInt(100),
	}
	if err := repo.CreateSettlement(s2); err == nil {
		t.Fatal("expected unique violation, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd account-service && go test ./internal/repository/ -run TestReservationRepo -v`

Expected: FAIL — repository doesn't exist yet.

- [ ] **Step 3: Create the repository**

Create `account-service/internal/repository/account_reservation_repository.go`:

```go
package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
)

type AccountReservationRepository struct {
	db *gorm.DB
}

func NewAccountReservationRepository(db *gorm.DB) *AccountReservationRepository {
	return &AccountReservationRepository{db: db}
}

// WithTx returns a repository bound to the given transaction handle. Lets
// callers run the reservation flow inside an outer transaction that also
// updates the account row under SELECT FOR UPDATE.
func (r *AccountReservationRepository) WithTx(tx *gorm.DB) *AccountReservationRepository {
	return &AccountReservationRepository{db: tx}
}

func (r *AccountReservationRepository) Create(res *model.AccountReservation) error {
	return r.db.Create(res).Error
}

// InsertIfAbsent inserts the reservation unless a row with the same OrderID
// already exists. Returns (inserted, existing, error). If inserted=true the
// caller must update the account's reserved_balance in the same TX. If
// inserted=false the caller returns the existing state (idempotent retry).
func (r *AccountReservationRepository) InsertIfAbsent(res *model.AccountReservation) (bool, *model.AccountReservation, error) {
	result := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "order_id"}},
		DoNothing: true,
	}).Create(res)
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected == 1 {
		return true, res, nil
	}
	existing, err := r.GetByOrderID(res.OrderID)
	if err != nil {
		return false, nil, err
	}
	return false, existing, nil
}

func (r *AccountReservationRepository) GetByOrderID(orderID uint64) (*model.AccountReservation, error) {
	var res model.AccountReservation
	err := r.db.Where("order_id = ?", orderID).First(&res).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, err
	}
	return &res, nil
}

// GetByOrderIDForUpdate loads the reservation with SELECT FOR UPDATE; caller
// must already be inside a DB transaction.
func (r *AccountReservationRepository) GetByOrderIDForUpdate(orderID uint64) (*model.AccountReservation, error) {
	var res model.AccountReservation
	err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_id = ?", orderID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *AccountReservationRepository) UpdateStatus(res *model.AccountReservation) error {
	result := r.db.Save(res)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *AccountReservationRepository) CreateSettlement(s *model.AccountReservationSettlement) error {
	return r.db.Create(s).Error
}

func (r *AccountReservationRepository) ListSettlements(reservationID uint64) ([]model.AccountReservationSettlement, error) {
	var out []model.AccountReservationSettlement
	if err := r.db.Where("reservation_id = ?", reservationID).Order("id ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *AccountReservationRepository) SumSettlements(reservationID uint64) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.Model(&model.AccountReservationSettlement{}).
		Where("reservation_id = ?", reservationID).
		Select("COALESCE(SUM(amount), 0)").Scan(&total).Error
	if err != nil {
		return decimal.Zero, err
	}
	return total, nil
}
```

If `ErrOptimisticLock` doesn't exist in this package, copy the one from `stock-service/internal/repository/listing_repository.go` (or wherever account-service defines it — search for `ErrOptimisticLock`).

- [ ] **Step 4: Run tests**

Run: `cd account-service && go test ./internal/repository/ -run TestReservationRepo -v`

Expected: PASS.

- [ ] **Step 5: Register the tables in AutoMigrate**

Open `account-service/cmd/main.go`. Find the `db.AutoMigrate(...)` call. Add the two new models:

```go
if err := db.AutoMigrate(
    &model.Account{},
    // ... existing models ...
    &model.AccountReservation{},
    &model.AccountReservationSettlement{},
); err != nil {
    log.Fatalf("auto-migrate: %v", err)
}
```

- [ ] **Step 6: Run the repo test again with migrations firing**

Run: `cd account-service && go test ./internal/repository/ -run TestReservationRepo -v`

Expected: PASS.

- [ ] **Step 7: Lint**

Run: `cd account-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 8: Commit**

```bash
git add account-service/internal/repository/account_reservation_repository.go \
        account-service/internal/repository/account_reservation_repository_test.go \
        account-service/cmd/main.go
git commit -m "feat(account-service): add AccountReservationRepository with AutoMigrate

Part of Phase 2. Implements InsertIfAbsent (ON CONFLICT DO NOTHING for
idempotency), GetByOrderIDForUpdate (SELECT FOR UPDATE), CreateSettlement
(unique on order_transaction_id), and SumSettlements."
```

---

## Task 5: account-service — reservation service

**Files:**
- Create: `account-service/internal/service/reservation_service.go`
- Create: `account-service/internal/service/reservation_service_test.go`

- [ ] **Step 1: Write the failing tests**

Create `account-service/internal/service/reservation_service_test.go` with these scenarios (one function per scenario):

```go
package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

func newReservationFixture(t *testing.T) (*ReservationService, *repository.AccountRepository, *repository.LedgerRepository, uint64) {
	t.Helper()
	db := newTestDB(t) // existing helper
	accountRepo := repository.NewAccountRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	resRepo := repository.NewAccountReservationRepository(db)
	svc := NewReservationService(db, accountRepo, resRepo, ledgerRepo)

	acc := &model.Account{
		OwnerID:      10,
		Balance:      decimal.NewFromInt(1000),
		CurrencyCode: "RSD",
		Status:       "active",
	}
	if err := accountRepo.Create(acc); err != nil {
		t.Fatal(err)
	}
	return svc, accountRepo, ledgerRepo, acc.ID
}

func TestReserveFunds_HappyPath(t *testing.T) {
	svc, accountRepo, _, accountID := newReservationFixture(t)

	resp, err := svc.ReserveFunds(context.Background(), accountID, 500, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatalf("ReserveFunds: %v", err)
	}
	if resp.ReservationID == 0 {
		t.Fatalf("expected non-zero reservation id")
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("reserved_balance: got %s, want 400", acc.ReservedBalance)
	}
	// balance unchanged at reservation time
	if !acc.Balance.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("balance: got %s, want 1000", acc.Balance)
	}
}

func TestReserveFunds_Idempotent(t *testing.T) {
	svc, accountRepo, _, accountID := newReservationFixture(t)
	ctx := context.Background()

	r1, err := svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatal(err)
	}
	if r1.ReservationID != r2.ReservationID {
		t.Errorf("retry produced different reservation id")
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("retry double-counted reserved_balance: %s", acc.ReservedBalance)
	}
}

func TestReserveFunds_InsufficientAvailable(t *testing.T) {
	svc, _, _, accountID := newReservationFixture(t)
	_, err := svc.ReserveFunds(context.Background(), accountID, 500, decimal.NewFromInt(5000), "RSD")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestReserveFunds_CurrencyMismatch(t *testing.T) {
	svc, _, _, accountID := newReservationFixture(t)
	_, err := svc.ReserveFunds(context.Background(), accountID, 500, decimal.NewFromInt(100), "USD")
	if err == nil {
		t.Fatal("expected currency-mismatch error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestPartialSettle_HappyPath_LedgerEntryWritten(t *testing.T) {
	svc, accountRepo, ledgerRepo, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")

	resp, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), "order 500 fill 9001")
	if err != nil {
		t.Fatalf("PartialSettle: %v", err)
	}
	if !resp.SettledAmount.Equal(decimal.NewFromInt(150)) {
		t.Errorf("settled amount: got %s", resp.SettledAmount)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.Balance.Equal(decimal.NewFromInt(850)) {
		t.Errorf("balance after settle: got %s want 850", acc.Balance)
	}
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(250)) {
		t.Errorf("reserved after settle: got %s want 250", acc.ReservedBalance)
	}
	entries, _ := ledgerRepo.ListByAccount(accountID)
	if len(entries) == 0 {
		t.Fatal("expected at least one ledger entry")
	}
}

func TestPartialSettle_IdempotentOnTxnID(t *testing.T) {
	svc, accountRepo, _, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")

	if _, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), ""); err != nil {
		t.Fatal("expected idempotent no-op, got error:", err)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.Balance.Equal(decimal.NewFromInt(850)) {
		t.Errorf("retry double-debited: balance %s", acc.Balance)
	}
}

func TestPartialSettle_OverReservation(t *testing.T) {
	svc, _, _, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")

	_, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(500), "")
	if err == nil {
		t.Fatal("expected over-reservation error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestRelease_ActiveReservation(t *testing.T) {
	svc, accountRepo, _, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")

	resp, err := svc.ReleaseReservation(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.Equal(decimal.NewFromInt(400)) {
		t.Errorf("released: got %s want 400", resp.ReleasedAmount)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.ReservedBalance.IsZero() {
		t.Errorf("reserved after release: %s", acc.ReservedBalance)
	}
}

func TestRelease_AfterFullSettle_NoOp(t *testing.T) {
	svc, _, _, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")
	_, _ = svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(400), "")

	resp, err := svc.ReleaseReservation(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.IsZero() {
		t.Errorf("expected 0 released, got %s", resp.ReleasedAmount)
	}
}

func TestRelease_Idempotent(t *testing.T) {
	svc, _, _, accountID := newReservationFixture(t)
	ctx := context.Background()
	_, _ = svc.ReserveFunds(ctx, accountID, 500, decimal.NewFromInt(400), "RSD")
	if _, err := svc.ReleaseReservation(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseReservation(ctx, 500); err != nil {
		t.Fatal("second release should be no-op")
	}
}

func TestRelease_NonexistentOrder_NoOp(t *testing.T) {
	svc, _, _, _ := newReservationFixture(t)
	resp, err := svc.ReleaseReservation(context.Background(), 99999)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.IsZero() {
		t.Errorf("expected 0 released, got %s", resp.ReleasedAmount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd account-service && go test ./internal/service/ -run TestReserveFunds -v -run TestPartialSettle -v -run TestRelease -v`

Expected: FAIL — service doesn't exist yet.

- [ ] **Step 3: Implement the reservation service**

Create `account-service/internal/service/reservation_service.go`:

```go
package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

type ReservationService struct {
	db          *gorm.DB
	accountRepo *repository.AccountRepository
	resRepo     *repository.AccountReservationRepository
	ledgerRepo  *repository.LedgerRepository
}

func NewReservationService(
	db *gorm.DB,
	accountRepo *repository.AccountRepository,
	resRepo *repository.AccountReservationRepository,
	ledgerRepo *repository.LedgerRepository,
) *ReservationService {
	return &ReservationService{db: db, accountRepo: accountRepo, resRepo: resRepo, ledgerRepo: ledgerRepo}
}

type ReserveFundsResult struct {
	ReservationID    uint64
	ReservedBalance  decimal.Decimal
	AvailableBalance decimal.Decimal
}

type ReleaseResult struct {
	ReleasedAmount  decimal.Decimal
	ReservedBalance decimal.Decimal
}

type PartialSettleResult struct {
	SettledAmount     decimal.Decimal
	RemainingReserved decimal.Decimal
	BalanceAfter      decimal.Decimal
	LedgerEntryID     uint64
}

// ReserveFunds holds `amount` on account `accountID` for `orderID`. Idempotent
// on orderID. FailedPrecondition if balance-reserved < amount or currency
// mismatch.
func (s *ReservationService) ReserveFunds(ctx context.Context, orderID, accountID uint64, amount decimal.Decimal, currencyCode string) (*ReserveFundsResult, error) {
	if amount.Sign() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	var out *ReserveFundsResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "account not found")
			}
			return err
		}
		if acc.CurrencyCode != currencyCode {
			return status.Errorf(codes.FailedPrecondition, "currency mismatch: account=%s reserve=%s", acc.CurrencyCode, currencyCode)
		}
		available := acc.Balance.Sub(acc.ReservedBalance)
		if available.LessThan(amount) {
			return status.Errorf(codes.FailedPrecondition, "insufficient available balance: have %s, need %s", available, amount)
		}
		res := &model.AccountReservation{
			AccountID:    acc.ID,
			OrderID:      orderID,
			Amount:       amount,
			CurrencyCode: currencyCode,
			Status:       model.ReservationStatusActive,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			out = &ReserveFundsResult{
				ReservationID:    existing.ID,
				ReservedBalance:  acc.ReservedBalance,
				AvailableBalance: acc.Balance.Sub(acc.ReservedBalance),
			}
			return nil
		}
		acc.ReservedBalance = acc.ReservedBalance.Add(amount)
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}
		out = &ReserveFundsResult{
			ReservationID:    res.ID,
			ReservedBalance:  acc.ReservedBalance,
			AvailableBalance: acc.Balance.Sub(acc.ReservedBalance),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ReservationService) ReleaseReservation(ctx context.Context, orderID uint64) (*ReleaseResult, error) {
	var out *ReleaseResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// No reservation → successful no-op.
				out = &ReleaseResult{ReleasedAmount: decimal.Zero, ReservedBalance: decimal.Zero}
				return nil
			}
			return err
		}
		if res.Status != model.ReservationStatusActive {
			out = &ReleaseResult{ReleasedAmount: decimal.Zero, ReservedBalance: decimal.Zero}
			return nil
		}
		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		remaining := res.Amount.Sub(settled)

		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, res.AccountID).Error; err != nil {
			return err
		}
		acc.ReservedBalance = acc.ReservedBalance.Sub(remaining)
		if acc.ReservedBalance.Sign() < 0 {
			acc.ReservedBalance = decimal.Zero
		}
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}

		res.Status = model.ReservationStatusReleased
		res.UpdatedAt = time.Now()
		if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
			return err
		}

		out = &ReleaseResult{ReleasedAmount: remaining, ReservedBalance: acc.ReservedBalance}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PartialSettleReservation debits `amount` from both reserved_balance and
// balance. Idempotent on orderTransactionID. Writes a ledger entry with the
// supplied memo. FailedPrecondition if reservation missing/inactive or
// over-settlement would occur.
func (s *ReservationService) PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*PartialSettleResult, error) {
	if amount.Sign() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	var out *PartialSettleResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "reservation not found")
			}
			return err
		}
		if res.Status != model.ReservationStatusActive {
			return status.Errorf(codes.FailedPrecondition, "reservation status=%s", res.Status)
		}
		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, res.AccountID).Error; err != nil {
			return err
		}

		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		if settled.Add(amount).GreaterThan(res.Amount) {
			return status.Errorf(codes.FailedPrecondition, "settlement %s would exceed reservation %s", settled.Add(amount), res.Amount)
		}

		settlement := &model.AccountReservationSettlement{
			ReservationID:      res.ID,
			OrderTransactionID: orderTransactionID,
			Amount:             amount,
			CreatedAt:          time.Now(),
		}
		// ON CONFLICT DO NOTHING for idempotency.
		createErr := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "order_transaction_id"}},
			DoNothing: true,
		}).Create(settlement).Error
		if createErr != nil {
			return createErr
		}
		if settlement.ID == 0 {
			// Idempotent replay — return current state without mutating anything.
			out = &PartialSettleResult{
				SettledAmount:     amount,
				RemainingReserved: acc.ReservedBalance,
				BalanceAfter:      acc.Balance,
				LedgerEntryID:     0,
			}
			return nil
		}

		// First-time settle path: move money and write ledger entry.
		acc.ReservedBalance = acc.ReservedBalance.Sub(amount)
		acc.Balance = acc.Balance.Sub(amount)
		if acc.ReservedBalance.Sign() < 0 {
			acc.ReservedBalance = decimal.Zero
		}
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}

		// Write the ledger entry. Existing LedgerRepository method expected to be
		// available; adapt name/signature if different in this codebase.
		entry := &model.LedgerEntry{
			AccountID:   acc.ID,
			Direction:   "debit",
			Amount:      amount,
			Currency:    acc.CurrencyCode,
			Memo:        memo,
			OccurredAt:  time.Now(),
		}
		if err := s.ledgerRepo.CreateInTx(tx, entry); err != nil {
			return err
		}

		if settled.Add(amount).Equal(res.Amount) {
			res.Status = model.ReservationStatusSettled
			res.UpdatedAt = time.Now()
			if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
				return err
			}
		}

		out = &PartialSettleResult{
			SettledAmount:     amount,
			RemainingReserved: acc.ReservedBalance,
			BalanceAfter:      acc.Balance,
			LedgerEntryID:     entry.ID,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetReservation is read-only; used by stock-service to reconcile after a crash.
func (s *ReservationService) GetReservation(ctx context.Context, orderID uint64) (status string, amount, settled decimal.Decimal, settledTxnIDs []uint64, exists bool, err error) {
	res, repoErr := s.resRepo.GetByOrderID(orderID)
	if repoErr != nil {
		if errors.Is(repoErr, gorm.ErrRecordNotFound) {
			return "", decimal.Zero, decimal.Zero, nil, false, nil
		}
		return "", decimal.Zero, decimal.Zero, nil, false, repoErr
	}
	children, err := s.resRepo.ListSettlements(res.ID)
	if err != nil {
		return "", decimal.Zero, decimal.Zero, nil, false, err
	}
	total := decimal.Zero
	ids := make([]uint64, len(children))
	for i, c := range children {
		total = total.Add(c.Amount)
		ids[i] = c.OrderTransactionID
	}
	return res.Status, res.Amount, total, ids, true, nil
}
```

Adapt `ledgerRepo.CreateInTx` to the actual helper exposed by `LedgerRepository` — if the current API is `DebitWithLock`, call that here instead with the appropriate arguments. The essential contract: **the settle and the ledger entry commit in the same DB transaction as the balance update**.

- [ ] **Step 4: Run tests**

Run: `cd account-service && go test ./internal/service/ -run TestReserveFunds -run TestPartialSettle -run TestRelease -v`

Expected: all PASS.

- [ ] **Step 5: Lint**

Run: `cd account-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 6: Commit**

```bash
git add account-service/internal/service/reservation_service.go \
        account-service/internal/service/reservation_service_test.go
git commit -m "feat(account-service): ReservationService with reserve/release/settle

Part of Phase 2. Implements the four operations described in the design
spec §4.4: ReserveFunds, ReleaseReservation, PartialSettleReservation,
GetReservation. All run inside db.Transaction with SELECT FOR UPDATE on
the account row. Reserve is idempotent on order_id; settle is idempotent
on order_transaction_id. Settlement writes a ledger entry so fills appear
in account transaction history."
```

---

## Task 6: account-service — gRPC handler

**Files:**
- Create: `account-service/internal/handler/reservation_handler.go`
- Modify: `account-service/cmd/main.go` (register new RPCs on the account gRPC server)

- [ ] **Step 1: Implement the handler**

Create `account-service/internal/handler/reservation_handler.go`:

```go
package handler

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/account-service/internal/service"
)

// ReservationHandler implements the ReserveFunds/ReleaseReservation/
// PartialSettleReservation/GetReservation RPCs on AccountService.
type ReservationHandler struct {
	svc *service.ReservationService
}

func NewReservationHandler(svc *service.ReservationService) *ReservationHandler {
	return &ReservationHandler{svc: svc}
}

func (h *ReservationHandler) ReserveFunds(ctx context.Context, req *pb.ReserveFundsRequest) (*pb.ReserveFundsResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid amount")
	}
	res, err := h.svc.ReserveFunds(ctx, req.GetOrderId(), req.GetAccountId(), amount, req.GetCurrencyCode())
	if err != nil {
		return nil, err
	}
	return &pb.ReserveFundsResponse{
		ReservationId:    res.ReservationID,
		ReservedBalance:  res.ReservedBalance.String(),
		AvailableBalance: res.AvailableBalance.String(),
	}, nil
}

func (h *ReservationHandler) ReleaseReservation(ctx context.Context, req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	res, err := h.svc.ReleaseReservation(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}
	return &pb.ReleaseReservationResponse{
		ReleasedAmount:  res.ReleasedAmount.String(),
		ReservedBalance: res.ReservedBalance.String(),
	}, nil
}

func (h *ReservationHandler) PartialSettleReservation(ctx context.Context, req *pb.PartialSettleReservationRequest) (*pb.PartialSettleReservationResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid amount")
	}
	res, err := h.svc.PartialSettleReservation(ctx, req.GetOrderId(), req.GetOrderTransactionId(), amount, req.GetMemo())
	if err != nil {
		return nil, err
	}
	return &pb.PartialSettleReservationResponse{
		SettledAmount:     res.SettledAmount.String(),
		RemainingReserved: res.RemainingReserved.String(),
		BalanceAfter:      res.BalanceAfter.String(),
		LedgerEntryId:     res.LedgerEntryID,
	}, nil
}

func (h *ReservationHandler) GetReservation(ctx context.Context, req *pb.GetReservationRequest) (*pb.GetReservationResponse, error) {
	st, amount, settled, ids, exists, err := h.svc.GetReservation(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}
	return &pb.GetReservationResponse{
		Exists:                 exists,
		Status:                 st,
		Amount:                 amount.String(),
		SettledTotal:           settled.String(),
		SettledTransactionIds:  ids,
	}, nil
}
```

If account-service exposes a single `AccountServiceServer` struct that embeds all its handlers (common pattern), extend that struct or wrap this handler behind it so the four new methods are part of the same gRPC server registration.

- [ ] **Step 2: Wire into main.go**

In `account-service/cmd/main.go`, find where the gRPC handlers are registered. Ensure the four new methods are exposed — either by adding this handler to the same server struct, or by returning a composite handler.

Minimal integration: make the existing account server embed the reservation methods by promoting `*ReservationHandler` fields. Exact wiring depends on the existing layout; read `cmd/main.go` and integrate conservatively.

- [ ] **Step 3: Run proto-binding test (verify all four RPCs dispatch)**

Create a small handler smoke test, `account-service/internal/handler/reservation_handler_test.go`:

```go
package handler

import (
	"context"
	"testing"

	pb "github.com/exbanka/contract/accountpb"
)

func TestReservationHandler_ReserveAndRelease(t *testing.T) {
	h, cleanup := newReservationHandlerFixture(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := h.ReserveFunds(ctx, &pb.ReserveFundsRequest{
		AccountId:    h.testAccountID,
		OrderId:      1001,
		Amount:       "100",
		CurrencyCode: "RSD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReservationId == 0 {
		t.Fatal("no reservation id")
	}

	relResp, err := h.ReleaseReservation(ctx, &pb.ReleaseReservationRequest{OrderId: 1001})
	if err != nil {
		t.Fatal(err)
	}
	if relResp.ReleasedAmount != "100" {
		t.Errorf("released: got %s want 100", relResp.ReleasedAmount)
	}
}
```

Provide a `newReservationHandlerFixture` in the same file that stitches together an in-memory DB and returns `(*ReservationHandler & testAccountID, cleanup)`.

- [ ] **Step 4: Run the handler test**

Run: `cd account-service && go test ./internal/handler/ -run TestReservationHandler -v`

Expected: PASS.

- [ ] **Step 5: Run the full account-service test suite**

Run: `cd account-service && go test ./... && golangci-lint run ./...`

Expected: PASS; no new lint warnings.

- [ ] **Step 6: Commit**

```bash
git add account-service/internal/handler/reservation_handler.go \
        account-service/internal/handler/reservation_handler_test.go \
        account-service/cmd/main.go
git commit -m "feat(account-service): expose reservation RPCs over gRPC"
```

---

## Task 7: stock-service — account client wrapper

**Files:**
- Modify: `stock-service/internal/grpc/account_client.go` (or equivalent)

- [ ] **Step 1: Add client methods**

Find the existing account-service gRPC client wrapper in `stock-service/internal/grpc/`. Add four methods that map to the new RPCs:

```go
func (c *AccountClient) ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode string) (*pb.ReserveFundsResponse, error) {
	return c.stub.ReserveFunds(ctx, &pb.ReserveFundsRequest{
		AccountId:    accountID,
		OrderId:      orderID,
		Amount:       amount.String(),
		CurrencyCode: currencyCode,
	})
}

func (c *AccountClient) ReleaseReservation(ctx context.Context, orderID uint64) (*pb.ReleaseReservationResponse, error) {
	return c.stub.ReleaseReservation(ctx, &pb.ReleaseReservationRequest{OrderId: orderID})
}

func (c *AccountClient) PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*pb.PartialSettleReservationResponse, error) {
	return c.stub.PartialSettleReservation(ctx, &pb.PartialSettleReservationRequest{
		OrderId:            orderID,
		OrderTransactionId: orderTransactionID,
		Amount:             amount.String(),
		Memo:               memo,
	})
}

func (c *AccountClient) GetReservation(ctx context.Context, orderID uint64) (*pb.GetReservationResponse, error) {
	return c.stub.GetReservation(ctx, &pb.GetReservationRequest{OrderId: orderID})
}
```

- [ ] **Step 2: Also ensure there is a CreditWithLock (or equivalent) method for reverse-compensation and forex base credit**

Search: `cd stock-service && grep -rn "CreditWithLock\|UpdateBalance" internal/grpc/`

If the credit path is exposed, keep it. If not, add a thin wrapper that calls the existing account-service credit RPC.

- [ ] **Step 3: Build**

Run: `cd stock-service && go build ./...`

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/grpc/account_client.go
git commit -m "feat(stock-service): account client methods for reservation RPCs"
```

---

## Task 8: stock-service — saga_log model + repository

**Files:**
- Create: `stock-service/internal/model/saga_log.go`
- Create: `stock-service/internal/repository/saga_log_repository.go`
- Create: `stock-service/internal/repository/saga_log_repository_test.go`
- Modify: `stock-service/cmd/main.go` (AutoMigrate)

- [ ] **Step 1: Create the model**

Create `stock-service/internal/model/saga_log.go`. Mirror the transaction-service implementation:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/datatypes"
)

const (
	SagaStatusPending      = "pending"
	SagaStatusCompleted    = "completed"
	SagaStatusFailed       = "failed"
	SagaStatusCompensating = "compensating"
	SagaStatusCompensated  = "compensated"
)

type SagaLog struct {
	ID                  uint64           `gorm:"primaryKey" json:"id"`
	SagaID              string           `gorm:"size:36;not null;index" json:"saga_id"`
	OrderID             uint64           `gorm:"not null;index" json:"order_id"`
	OrderTransactionID  *uint64          `gorm:"index" json:"order_transaction_id,omitempty"`
	StepNumber          int              `gorm:"not null" json:"step_number"`
	StepName            string           `gorm:"size:64;not null" json:"step_name"`
	Status              string           `gorm:"size:16;not null;index" json:"status"`
	IsCompensation     bool             `gorm:"not null;default:false" json:"is_compensation"`
	CompensationOf     *uint64          `json:"compensation_of,omitempty"`
	Amount             *decimal.Decimal `gorm:"type:decimal(20,4)" json:"amount,omitempty"`
	CurrencyCode      string           `gorm:"size:3" json:"currency_code,omitempty"`
	Payload           datatypes.JSON   `json:"payload,omitempty"`
	ErrorMessage      string           `gorm:"type:text" json:"error_message,omitempty"`
	RetryCount        int              `gorm:"not null;default:0" json:"retry_count"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	Version           int64            `gorm:"not null;default:0" json:"version"`
}

func (SagaLog) TableName() string { return "saga_logs" }

func (l *SagaLog) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", l.Version)
	l.Version++
	return nil
}
```

- [ ] **Step 2: Write the failing repo test**

Create `stock-service/internal/repository/saga_log_repository_test.go`:

```go
package repository

import (
	"testing"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

func TestSagaLogRepo_RecordAndUpdate(t *testing.T) {
	db := newTestDB(t) // existing helper
	repo := NewSagaLogRepository(db)

	log := &model.SagaLog{
		SagaID: "abc-123", OrderID: 42, StepNumber: 1,
		StepName: "reserve_funds", Status: model.SagaStatusPending,
	}
	if err := repo.RecordStep(log); err != nil {
		t.Fatal(err)
	}
	if log.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if err := repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompleted, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(log.ID)
	if got.Status != model.SagaStatusCompleted {
		t.Errorf("status: got %s want completed", got.Status)
	}
}

func TestSagaLogRepo_ListStuckSagas(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)

	past := time.Now().Add(-5 * time.Minute)
	for _, st := range []string{model.SagaStatusPending, model.SagaStatusCompensating, model.SagaStatusCompleted} {
		log := &model.SagaLog{
			SagaID: "x", OrderID: 1, StepNumber: 1, StepName: "x", Status: st,
			CreatedAt: past, UpdatedAt: past,
		}
		_ = repo.RecordStep(log)
	}
	stuck, err := repo.ListStuckSagas(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(stuck) != 2 {
		t.Errorf("expected 2 stuck, got %d", len(stuck))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd stock-service && go test ./internal/repository/ -run TestSagaLogRepo -v`

Expected: FAIL (missing types/repo).

- [ ] **Step 4: Implement the repo**

Create `stock-service/internal/repository/saga_log_repository.go`:

```go
package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type SagaLogRepository struct {
	db *gorm.DB
}

func NewSagaLogRepository(db *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: db}
}

func (r *SagaLogRepository) WithTx(tx *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: tx}
}

func (r *SagaLogRepository) RecordStep(log *model.SagaLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	log.UpdatedAt = log.CreatedAt
	return r.db.Create(log).Error
}

func (r *SagaLogRepository) GetByID(id uint64) (*model.SagaLog, error) {
	var log model.SagaLog
	if err := r.db.First(&log, id).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

func (r *SagaLogRepository) ListPendingForOrder(orderID uint64) ([]model.SagaLog, error) {
	var out []model.SagaLog
	err := r.db.Where("order_id = ? AND status IN ?", orderID,
		[]string{model.SagaStatusPending, model.SagaStatusCompensating}).Find(&out).Error
	return out, err
}

func (r *SagaLogRepository) GetByStepName(orderID uint64, stepName string) (*model.SagaLog, error) {
	var log model.SagaLog
	err := r.db.Where("order_id = ? AND step_name = ?", orderID, stepName).
		Order("step_number DESC").First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

func (r *SagaLogRepository) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	result := r.db.Model(&model.SagaLog{}).
		Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{
			"status":        newStatus,
			"error_message": errMsg,
			"updated_at":    time.Now(),
			"version":       version + 1,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListStuckSagas returns saga steps in pending/compensating status older than
// the given age. Used by the startup recovery reconciler.
func (r *SagaLogRepository) ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error) {
	cutoff := time.Now().Add(-olderThan)
	var out []model.SagaLog
	err := r.db.Where("status IN ? AND updated_at < ?",
		[]string{model.SagaStatusPending, model.SagaStatusCompensating}, cutoff).
		Order("id ASC").Find(&out).Error
	return out, err
}
```

- [ ] **Step 5: Register in AutoMigrate**

Edit `stock-service/cmd/main.go`. Find the existing `db.AutoMigrate(...)` call and add `&model.SagaLog{}` to the argument list.

- [ ] **Step 6: Run tests**

Run: `cd stock-service && go test ./internal/repository/ -run TestSagaLogRepo -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/model/saga_log.go \
        stock-service/internal/repository/saga_log_repository.go \
        stock-service/internal/repository/saga_log_repository_test.go \
        stock-service/cmd/main.go
git commit -m "feat(stock-service): SagaLog model + repository

Mirrors transaction-service/internal/model/saga_log.go. Steps record
pending → completed/failed transitions; ListStuckSagas drives recovery
at startup."
```

---

## Task 9: stock-service — SagaExecutor helper

**Files:**
- Create: `stock-service/internal/service/saga_helper.go`
- Create: `stock-service/internal/service/saga_helper_test.go`

- [ ] **Step 1: Write the failing tests**

Create `stock-service/internal/service/saga_helper_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/google/uuid"

	"github.com/exbanka/stock-service/internal/model"
)

func TestSagaExecutor_RunStep_Success(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	err := exec.RunStep(context.Background(), "reserve_funds",
		decimal.NewFromInt(100), "RSD", nil,
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	logs := repo.allLogs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Status != model.SagaStatusCompleted {
		t.Errorf("final status: got %s want completed", logs[0].Status)
	}
}

func TestSagaExecutor_RunStep_Error(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	err := exec.RunStep(context.Background(), "reserve_funds",
		decimal.NewFromInt(100), "RSD", nil,
		func() error { return errors.New("boom") })
	if err == nil {
		t.Fatal("expected error")
	}
	logs := repo.allLogs()
	if logs[0].Status != model.SagaStatusFailed {
		t.Errorf("got %s want failed", logs[0].Status)
	}
	if logs[0].ErrorMessage == "" {
		t.Error("expected error message")
	}
}

func TestSagaExecutor_RunCompensation_RecordsCompensationOf(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	_ = exec.RunStep(context.Background(), "settle", decimal.Zero, "", nil, func() error { return nil })
	forwardID := repo.allLogs()[0].ID
	err := exec.RunCompensation(context.Background(), forwardID, "compensate_settle", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if logs := repo.allLogs(); len(logs) != 2 || !logs[1].IsCompensation || *logs[1].CompensationOf != forwardID {
		t.Errorf("compensation not wired: %+v", logs)
	}
}
```

The `newFakeSagaRepo` is a lightweight in-memory stub implementing the SagaLogRepo interface needed by the helper. Define it in the test file.

- [ ] **Step 2: Verify tests fail**

Run: `cd stock-service && go test ./internal/service/ -run TestSagaExecutor -v`

Expected: FAIL (type doesn't exist).

- [ ] **Step 3: Implement SagaExecutor**

Create `stock-service/internal/service/saga_helper.go`:

```go
package service

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"encoding/json"

	"github.com/exbanka/stock-service/internal/model"
)

// SagaLogRepo is the minimum interface SagaExecutor needs.
type SagaLogRepo interface {
	RecordStep(log *model.SagaLog) error
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
}

type SagaExecutor struct {
	repo      SagaLogRepo
	sagaID    string
	orderID   uint64
	txnID     *uint64
	nextStep  int
}

func NewSagaExecutor(repo SagaLogRepo, sagaID string, orderID uint64, txnID *uint64) *SagaExecutor {
	return &SagaExecutor{
		repo:    repo,
		sagaID:  sagaID,
		orderID: orderID,
		txnID:   txnID,
		nextStep: 1,
	}
}

// RunStep records the step as pending → runs fn → records completed/failed.
// On error, returns the original error after recording failure.
func (e *SagaExecutor) RunStep(ctx context.Context, name string, amount decimal.Decimal, currency string, payload map[string]any, fn func() error) error {
	log := &model.SagaLog{
		SagaID:             e.sagaID,
		OrderID:            e.orderID,
		OrderTransactionID: e.txnID,
		StepNumber:         e.nextStep,
		StepName:           name,
		Status:             model.SagaStatusPending,
		CurrencyCode:       currency,
	}
	if !amount.IsZero() {
		a := amount
		log.Amount = &a
	}
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			log.Payload = datatypes.JSON(b)
		}
	}
	if err := e.repo.RecordStep(log); err != nil {
		return err
	}
	e.nextStep++

	if err := fn(); err != nil {
		_ = e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusFailed, err.Error())
		return err
	}
	return e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompleted, "")
}

// RunCompensation records a compensation step linked to the forward step ID.
func (e *SagaExecutor) RunCompensation(ctx context.Context, forwardStepID uint64, name string, fn func() error) error {
	log := &model.SagaLog{
		SagaID:             e.sagaID,
		OrderID:            e.orderID,
		OrderTransactionID: e.txnID,
		StepNumber:         e.nextStep,
		StepName:           name,
		Status:             model.SagaStatusCompensating,
		IsCompensation:     true,
		CompensationOf:     &forwardStepID,
	}
	if err := e.repo.RecordStep(log); err != nil {
		return err
	}
	e.nextStep++
	if err := fn(); err != nil {
		_ = e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusFailed, err.Error())
		return err
	}
	return e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompensated, "")
}
```

- [ ] **Step 4: Run tests**

Run: `cd stock-service && go test ./internal/service/ -run TestSagaExecutor -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/saga_helper.go \
        stock-service/internal/service/saga_helper_test.go
git commit -m "feat(stock-service): SagaExecutor helper (RunStep + RunCompensation)"
```

---

## Task 10: stock-service — order + order_transaction schema additions

**Files:**
- Modify: `stock-service/internal/model/order.go`
- Modify: `stock-service/internal/model/order_transaction.go`
- Modify: `stock-service/internal/model/holding.go`
- Create: `stock-service/internal/model/holding_reservation.go`
- Create: `stock-service/internal/model/holding_reservation_settlement.go`
- Modify: `stock-service/cmd/main.go` (AutoMigrate)

- [ ] **Step 1: Extend Order with reservation metadata**

Open `stock-service/internal/model/order.go`. Add these fields to the `Order` struct:

```go
// Reservation metadata (populated on placement; read on cancellation/recovery).
ReservationAmount    *decimal.Decimal `gorm:"type:decimal(20,4)" json:"reservation_amount,omitempty"`
ReservationCurrency  string           `gorm:"size:3" json:"reservation_currency,omitempty"`
ReservationAccountID *uint64          `json:"reservation_account_id,omitempty"`
BaseAccountID        *uint64          `json:"base_account_id,omitempty"`       // forex only
PlacementRate        *decimal.Decimal `gorm:"type:decimal(20,8)" json:"placement_rate,omitempty"` // audit
SagaID               string           `gorm:"size:36;index" json:"saga_id,omitempty"`
```

- [ ] **Step 2: Extend OrderTransaction**

Open `stock-service/internal/model/order_transaction.go`. Add:

```go
NativeAmount    *decimal.Decimal `gorm:"type:decimal(20,4)" json:"native_amount,omitempty"`
NativeCurrency  string           `gorm:"size:3" json:"native_currency,omitempty"`
ConvertedAmount *decimal.Decimal `gorm:"type:decimal(20,4)" json:"converted_amount,omitempty"`
AccountCurrency string           `gorm:"size:3" json:"account_currency,omitempty"`
FxRate          *decimal.Decimal `gorm:"type:decimal(20,8)" json:"fx_rate,omitempty"`
```

Existing `TotalPrice` stays and is the native total (same value as `NativeAmount` when both are set; keep both for backwards compatibility with any existing reader).

- [ ] **Step 3: Extend Holding with ReservedQuantity**

Open `stock-service/internal/model/holding.go`. Add:

```go
ReservedQuantity int64 `gorm:"not null;default:0" json:"reserved_quantity"`
```

- [ ] **Step 4: Create HoldingReservation**

Create `stock-service/internal/model/holding_reservation.go`:

```go
package model

import (
	"time"

	"gorm.io/gorm"
)

type HoldingReservation struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	HoldingID uint64    `gorm:"not null;index" json:"holding_id"`
	OrderID   uint64    `gorm:"not null;uniqueIndex" json:"order_id"` // idempotency
	Quantity  int64     `gorm:"not null" json:"quantity"`              // IMMUTABLE
	Status    string    `gorm:"size:16;not null;index" json:"status"` // active|released|settled
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"version"`
}

func (HoldingReservation) TableName() string { return "holding_reservations" }

func (h *HoldingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}
```

- [ ] **Step 5: Create HoldingReservationSettlement**

Create `stock-service/internal/model/holding_reservation_settlement.go`:

```go
package model

import "time"

type HoldingReservationSettlement struct {
	ID                    uint64    `gorm:"primaryKey" json:"id"`
	HoldingReservationID  uint64    `gorm:"not null;index" json:"holding_reservation_id"`
	OrderTransactionID    uint64    `gorm:"not null;uniqueIndex" json:"order_transaction_id"`
	Quantity              int64     `gorm:"not null" json:"quantity"`
	CreatedAt             time.Time `json:"created_at"`
}

func (HoldingReservationSettlement) TableName() string { return "holding_reservation_settlements" }
```

- [ ] **Step 6: Register in AutoMigrate**

In `stock-service/cmd/main.go`, add `&model.HoldingReservation{}, &model.HoldingReservationSettlement{}` to the AutoMigrate call.

- [ ] **Step 7: Build**

Run: `cd stock-service && go build ./...`

Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add stock-service/internal/model/ stock-service/cmd/main.go
git commit -m "feat(stock-service): order/tx/holding schema + holding_reservation tables

Adds reservation metadata to Order; native/converted/fx_rate columns to
OrderTransaction; ReservedQuantity on Holding; HoldingReservation and
HoldingReservationSettlement tables."
```

---

## Task 11: stock-service — holding reservation repository

**Files:**
- Create: `stock-service/internal/repository/holding_reservation_repository.go`
- Create: `stock-service/internal/repository/holding_reservation_repository_test.go`

Same shape as Task 4 (account-service reservation repo) but for holdings. Implement:

- `Create(hr *HoldingReservation) error`
- `InsertIfAbsent(hr *HoldingReservation) (inserted bool, existing *HoldingReservation, err error)` — `ON CONFLICT(order_id) DO NOTHING`
- `GetByOrderID(orderID uint64) (*HoldingReservation, error)`
- `GetByOrderIDForUpdate(orderID uint64) (*HoldingReservation, error)`
- `UpdateStatus(hr *HoldingReservation) error`
- `CreateSettlement(s *HoldingReservationSettlement) error`
- `ListSettlements(holdingReservationID uint64) ([]HoldingReservationSettlement, error)`
- `SumSettlements(holdingReservationID uint64) (int64, error)`

Write tests analogous to the account-service ones. Commit with:

```bash
git commit -m "feat(stock-service): HoldingReservationRepository (sell-side reservations)"
```

---

## Task 12: stock-service — placement saga (rewrite order_service.CreateOrder)

**Files:**
- Modify: `stock-service/internal/service/order_service.go`
- Modify: `stock-service/internal/service/order_service_test.go`

- [ ] **Step 1: Write failing test for placement reservation (happy-path buy stock)**

Append to `stock-service/internal/service/order_service_test.go`:

```go
func TestCreateOrder_Buy_Stock_ReservesFunds(t *testing.T) {
	fx := newOrderServiceFixture(t)
	// Stub account-service to verify ReserveFunds is called with converted amount.
	fx.accountClient.expectReserve(accountReserveExpectation{
		accountID: 77, currency: "RSD",
		// stock is USD; account is RSD; conversion-rate 100 RSD per USD.
		amountApprox: decimal.NewFromInt(100 * 10 * 1_050 / 1000), // qty=10, limit=100, slippage+commission => ~1050 RSD
	})
	// Stub exchange-service to return a known rate.
	fx.exchangeClient.setRate("USD", "RSD", decimal.NewFromInt(100))

	req := CreateOrderRequest{
		UserID: 5, AccountID: 77, ListingID: 11, Direction: "buy",
		Quantity: 10, OrderType: "limit", LimitValue: ptrDec(100),
	}
	order, err := fx.svc.CreateOrder(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if order.ReservationAccountID == nil || *order.ReservationAccountID != 77 {
		t.Errorf("reservation_account_id: %v", order.ReservationAccountID)
	}
	if order.SagaID == "" {
		t.Error("saga_id should be populated")
	}
	if !fx.accountClient.reserveCalled {
		t.Error("ReserveFunds not called")
	}
}

func TestCreateOrder_InsufficientBalance_RollsBack(t *testing.T) {
	fx := newOrderServiceFixture(t)
	fx.accountClient.forceReserveError(codes.FailedPrecondition, "insufficient")

	req := CreateOrderRequest{
		UserID: 5, AccountID: 77, ListingID: 11, Direction: "buy",
		Quantity: 10, OrderType: "limit", LimitValue: ptrDec(100),
	}
	_, err := fx.svc.CreateOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("code: got %s", st.Code())
	}
	// No order row persisted.
	if fx.orderRepo.created != 0 {
		t.Error("order should not be persisted on reservation failure")
	}
}

func TestCreateOrder_Forex_MissingBaseAccount_Rejected(t *testing.T) {
	fx := newOrderServiceFixture(t)
	fx.listingRepo.stubForex(11, "EUR", "USD")

	req := CreateOrderRequest{
		UserID: 5, AccountID: 77, ListingID: 11, Direction: "buy",
		Quantity: 10, OrderType: "market",
		// BaseAccountID intentionally missing
	}
	_, err := fx.svc.CreateOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code: got %s", st.Code())
	}
}

func TestCreateOrder_Forex_SellRejected(t *testing.T) {
	fx := newOrderServiceFixture(t)
	fx.listingRepo.stubForex(11, "EUR", "USD")
	req := CreateOrderRequest{
		UserID: 5, AccountID: 77, BaseAccountID: ptrU64(88),
		ListingID: 11, Direction: "sell",
		Quantity: 10, OrderType: "market",
	}
	_, err := fx.svc.CreateOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code: got %s", st.Code())
	}
}
```

The fixture helpers (`newOrderServiceFixture`, `ptrDec`, `ptrU64`, `accountClient`, `listingRepo`, `exchangeClient`, etc.) are the same kind of test doubles already used in `order_service_test.go`. Add them to the same test file or a shared `order_service_testfixture.go`.

- [ ] **Step 2: Run test to verify FAIL**

Run: `cd stock-service && go test ./internal/service/ -run TestCreateOrder -v`

Expected: FAIL on compile/logic — current CreateOrder doesn't call ReserveFunds.

- [ ] **Step 3: Rewrite CreateOrder with placement saga**

Replace `order_service.go`'s `CreateOrder` method with (full replacement):

```go
func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*model.Order, error) {
	sagaID := uuid.New().String()
	exec := NewSagaExecutor(s.sagaRepo, sagaID, 0 /* orderID not yet known */, nil)

	var listing *model.Listing
	if err := exec.RunStep(ctx, "validate_listing", decimal.Zero, "", nil, func() error {
		l, err := s.listingRepo.GetByID(req.ListingID)
		if err != nil {
			return status.Error(codes.NotFound, "listing not found")
		}
		listing = l
		return nil
	}); err != nil {
		return nil, err
	}

	// Validate direction/forex constraints early.
	if listing.SecurityType == "forex" {
		if req.Direction != "buy" {
			return nil, status.Error(codes.InvalidArgument, "forex sells are not supported")
		}
		if req.BaseAccountID == nil {
			return nil, status.Error(codes.InvalidArgument, "forex orders require base_account_id")
		}
		// Currency validations happen in validate_account step below.
	}

	// --- validate_account + compute_reservation_amount + convert_currency + reserve_funds ---
	var reserveAmount decimal.Decimal
	var reserveCurrency string
	var placementRate *decimal.Decimal

	if err := exec.RunStep(ctx, "compute_reservation_amount", decimal.Zero, "", nil, func() error {
		native, err := s.computeNativeReservation(req, listing)
		if err != nil {
			return err
		}
		// For forex, settle on the quote account. Native is already in quote ccy.
		// For stocks/futures/options, native is in listing.Exchange.Currency.
		nativeCcy := listing.Exchange.Currency
		if listing.SecurityType == "forex" {
			nativeCcy = forexQuoteCurrency(listing) // helper that reads fp.QuoteCurrency
		}

		accountCcy, err := s.accountCurrency(ctx, req.AccountID)
		if err != nil {
			return err
		}

		if listing.SecurityType == "forex" {
			// Forex: quote-account currency MUST equal quote ccy; no conversion.
			if accountCcy != nativeCcy {
				return status.Errorf(codes.InvalidArgument,
					"forex quote account currency mismatch: account=%s pair_quote=%s",
					accountCcy, nativeCcy)
			}
			baseCcy, err := s.accountCurrency(ctx, *req.BaseAccountID)
			if err != nil {
				return err
			}
			if baseCcy != forexBaseCurrency(listing) {
				return status.Errorf(codes.InvalidArgument,
					"forex base account currency mismatch: account=%s pair_base=%s",
					baseCcy, forexBaseCurrency(listing))
			}
			reserveAmount = native
			reserveCurrency = nativeCcy
			return nil
		}

		// Stocks/futures/options: convert if needed.
		if accountCcy == nativeCcy {
			reserveAmount = native
			reserveCurrency = accountCcy
			return nil
		}
		conv, rate, convErr := s.exchangeClient.Convert(ctx, nativeCcy, accountCcy, native)
		if convErr != nil {
			return convErr
		}
		reserveAmount = conv
		reserveCurrency = accountCcy
		placementRate = &rate
		return nil
	}); err != nil {
		return nil, err
	}

	// --- reserve_funds ---
	// We have not yet persisted the order, so use a placeholder orderID and
	// patch it after order insert. Alternative: persist order first in pending,
	// then reserve, then flip to approved. We pick the latter — cleaner because
	// ReserveFunds is keyed on order_id.

	order := &model.Order{
		UserID: req.UserID, AccountID: req.AccountID,
		ListingID: req.ListingID, Direction: req.Direction,
		Quantity: req.Quantity, RemainingPortions: req.Quantity,
		OrderType: req.OrderType, LimitValue: req.LimitValue, StopValue: req.StopValue,
		AllOrNone: req.AllOrNone, AfterHours: req.AfterHours,
		ContractSize: listing.ContractSizeForOrder(),
		Status: "pending", IsDone: false,
		SecurityType: listing.SecurityType, Ticker: listing.Ticker,
		ReservationAmount: &reserveAmount, ReservationCurrency: reserveCurrency,
		ReservationAccountID: &req.AccountID,
		BaseAccountID: req.BaseAccountID,
		PlacementRate: placementRate,
		SagaID: sagaID,
	}
	if err := exec.RunStep(ctx, "persist_order_pending", decimal.Zero, "", nil, func() error {
		return s.orderRepo.Create(order)
	}); err != nil {
		return nil, err
	}
	// Rewire the exec with the real order ID so subsequent steps carry it.
	exec.orderID = order.ID

	if err := exec.RunStep(ctx, "reserve_funds", reserveAmount, reserveCurrency, nil, func() error {
		_, rerr := s.accountClient.ReserveFunds(ctx, req.AccountID, order.ID, reserveAmount, reserveCurrency)
		return rerr
	}); err != nil {
		// Compensation: delete the pending order since no money was held.
		_ = exec.RunCompensation(ctx, 0, "delete_pending_order", func() error {
			return s.orderRepo.Delete(order.ID)
		})
		return nil, err
	}

	// For sells, also reserve the holding quantity.
	if req.Direction == "sell" && listing.SecurityType != "forex" {
		if err := exec.RunStep(ctx, "reserve_holding", decimal.Zero, "", nil, func() error {
			return s.holdingReservationSvc.Reserve(ctx, req.UserID, listing.SecurityType, listing.SecurityID, order.ID, req.Quantity)
		}); err != nil {
			// Compensation: release funds, delete order.
			_ = exec.RunCompensation(ctx, 0, "release_funds", func() error {
				_, rerr := s.accountClient.ReleaseReservation(ctx, order.ID)
				return rerr
			})
			_ = exec.RunCompensation(ctx, 0, "delete_pending_order", func() error {
				return s.orderRepo.Delete(order.ID)
			})
			return nil, err
		}
	}

	// Flip order to approved.
	order.Status = "approved"
	if err := exec.RunStep(ctx, "approve_order", decimal.Zero, "", nil, func() error {
		return s.orderRepo.Update(order)
	}); err != nil {
		return nil, err
	}

	s.execEngine.StartOrderExecution(ctx, order.ID) // uses baseCtx internally (Phase 1 fix)
	return order, nil
}

// computeNativeReservation returns the native-currency amount to reserve,
// including slippage buffer and commission estimate.
func (s *OrderService) computeNativeReservation(req CreateOrderRequest, listing *model.Listing) (decimal.Decimal, error) {
	qty := decimal.NewFromInt(req.Quantity)
	contractSize := decimal.NewFromInt(listing.ContractSizeForOrder())
	var unit decimal.Decimal
	switch req.OrderType {
	case "limit", "stop_limit":
		if req.LimitValue == nil {
			return decimal.Zero, status.Error(codes.InvalidArgument, "limit order requires limit_value")
		}
		unit = *req.LimitValue
	case "market", "stop":
		unit = listing.High // worst-case ask
	default:
		return decimal.Zero, status.Errorf(codes.InvalidArgument, "unknown order_type %q", req.OrderType)
	}
	base := qty.Mul(unit).Mul(contractSize)
	slippagePct := s.settings.MarketSlippagePct()
	commissionPct := s.settings.CommissionRate()
	if req.OrderType == "market" || req.OrderType == "stop" {
		base = base.Mul(decimal.NewFromInt(1).Add(slippagePct))
	}
	return base.Mul(decimal.NewFromInt(1).Add(commissionPct)), nil
}

// forexQuoteCurrency / forexBaseCurrency — helpers that extract from the
// underlying ForexPair model. Implementation depends on how Listing ↔ ForexPair
// is wired; if listing has a nested ForexPair preload, reach through that.
```

Also add the helpers `forexQuoteCurrency(listing *model.Listing) string` and `forexBaseCurrency(listing *model.Listing) string` — they read `listing.ForexPair.QuoteCurrency` / `.BaseCurrency`. If the listing preload isn't wired, fetch the forex pair by ID here.

- [ ] **Step 4: Run tests**

Run: `cd stock-service && go test ./internal/service/ -run TestCreateOrder -v`

Expected: all four PASS.

- [ ] **Step 5: Run broader service test suite**

Run: `cd stock-service && go test ./internal/service/...`

Expected: PASS. Adjust any existing order tests that no longer match the new flow.

- [ ] **Step 6: Lint**

Run: `cd stock-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/service/order_service.go \
        stock-service/internal/service/order_service_test.go
git commit -m "feat(stock-service): placement saga with ReserveFunds + holding reservation

Rewrites CreateOrder with SagaExecutor steps: validate_listing →
compute_reservation_amount (incl currency conversion or forex validation)
→ persist_order_pending → reserve_funds → reserve_holding (sell) →
approve_order. Compensations: on reservation failure, delete pending
order; on holding reservation failure, release funds + delete order.
Adds forex-direction-sell rejection and forex base/quote currency
validation."
```

---

## Task 13: stock-service — buy fill saga rewrite

**Files:**
- Modify: `stock-service/internal/service/portfolio_service.go`
- Modify: `stock-service/internal/service/portfolio_service_test.go`

- [ ] **Step 1: Write failing tests**

Append to `stock-service/internal/service/portfolio_service_test.go`:

```go
func TestProcessBuyFill_SameCurrency_HappyPath(t *testing.T) {
	fx := newPortfolioFixture(t)
	order := fx.newApprovedBuyOrder(77, "RSD", "RSD", 10, decimal.NewFromInt(100))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 3,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(300),
	}
	if err := fx.svc.ProcessBuyFill(order, txn); err != nil {
		t.Fatal(err)
	}
	if !fx.accountClient.lastPartialSettle.amount.Equal(decimal.NewFromInt(300)) {
		t.Errorf("settle amount: got %s", fx.accountClient.lastPartialSettle.amount)
	}
	if fx.holdingRepo.lastUpsertQty != 3 {
		t.Errorf("holding upsert qty: got %d", fx.holdingRepo.lastUpsertQty)
	}
	if fx.accountClient.lastCommission.amount.Sign() <= 0 {
		t.Error("commission not credited to bank")
	}
}

func TestProcessBuyFill_CrossCurrency_ConvertsAmount(t *testing.T) {
	fx := newPortfolioFixture(t)
	fx.exchangeClient.setRate("USD", "RSD", decimal.NewFromInt(100))
	order := fx.newApprovedBuyOrder(77, "RSD", "USD", 10, decimal.NewFromInt(100))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 3,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(300), // USD
	}
	if err := fx.svc.ProcessBuyFill(order, txn); err != nil {
		t.Fatal(err)
	}
	if !fx.accountClient.lastPartialSettle.amount.Equal(decimal.NewFromInt(30_000)) {
		t.Errorf("converted settle amount: got %s want 30000", fx.accountClient.lastPartialSettle.amount)
	}
	if !txn.ConvertedAmount.Equal(decimal.NewFromInt(30_000)) {
		t.Errorf("txn.ConvertedAmount not recorded: %s", txn.ConvertedAmount)
	}
	if !txn.FxRate.Equal(decimal.NewFromInt(100)) {
		t.Errorf("txn.FxRate not recorded: %s", txn.FxRate)
	}
}

func TestProcessBuyFill_HoldingFails_RollsBackSettlement(t *testing.T) {
	fx := newPortfolioFixture(t)
	order := fx.newApprovedBuyOrder(77, "RSD", "RSD", 10, decimal.NewFromInt(100))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 3,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(300),
	}
	fx.holdingRepo.forceUpsertError(errors.New("disk full"))

	err := fx.svc.ProcessBuyFill(order, txn)
	if err == nil {
		t.Fatal("expected error")
	}
	if !fx.accountClient.compensateCredit.amount.Equal(decimal.NewFromInt(300)) {
		t.Errorf("compensation credit: got %s", fx.accountClient.compensateCredit.amount)
	}
}

func TestProcessBuyFill_CommissionFails_DoesNotRollBackTrade(t *testing.T) {
	fx := newPortfolioFixture(t)
	order := fx.newApprovedBuyOrder(77, "RSD", "RSD", 10, decimal.NewFromInt(100))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 3,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(300),
	}
	fx.accountClient.forceCommissionError(errors.New("down"))

	err := fx.svc.ProcessBuyFill(order, txn)
	// Commission failure is logged/retried; main trade is valid. Expect no error.
	if err != nil {
		t.Fatalf("commission failure should not fail the trade: %v", err)
	}
	if !fx.accountClient.lastPartialSettle.amount.Equal(decimal.NewFromInt(300)) {
		t.Error("main debit still recorded")
	}
	if fx.holdingRepo.lastUpsertQty != 3 {
		t.Error("holding still updated")
	}
}
```

- [ ] **Step 2: Run test to verify FAIL**

Run: `cd stock-service && go test ./internal/service/ -run TestProcessBuyFill -v`

Expected: FAIL.

- [ ] **Step 3: Rewrite ProcessBuyFill**

Replace `portfolio_service.ProcessBuyFill` with (complete function):

```go
func (s *PortfolioService) ProcessBuyFill(order *model.Order, txn *model.OrderTransaction) error {
	if order.SecurityType == "forex" {
		return s.forexFillSvc.ProcessForexBuy(context.Background(), order, txn)
	}

	ctx := context.Background()
	exec := NewSagaExecutor(s.sagaRepo, order.SagaID, order.ID, ptrU64(txn.ID))

	// 1. record_transaction — already done by caller. No-op step that just records success.
	// (Caller committed txn in OrderExecutionEngine before calling us; this step is
	// documented as completed in the saga log for recovery visibility.)
	if err := exec.RunStep(ctx, "record_transaction", txn.TotalPrice, order.ReservationCurrency, nil, func() error { return nil }); err != nil {
		return err
	}

	// 2. convert_amount
	var convertedAmount decimal.Decimal
	var accountCcy string
	if err := exec.RunStep(ctx, "convert_amount", txn.TotalPrice, "", nil, func() error {
		accCcy, err := s.accountCurrency(ctx, order.AccountID)
		if err != nil {
			return err
		}
		accountCcy = accCcy
		listingCcy, err := s.listingCurrency(order.ListingID)
		if err != nil {
			return err
		}
		txn.NativeAmount = &txn.TotalPrice
		txn.NativeCurrency = listingCcy
		txn.AccountCurrency = accCcy
		if listingCcy == accCcy {
			convertedAmount = txn.TotalPrice
			return nil
		}
		conv, rate, convErr := s.exchangeClient.Convert(ctx, listingCcy, accCcy, txn.TotalPrice)
		if convErr != nil {
			return convErr
		}
		convertedAmount = conv
		txn.ConvertedAmount = &conv
		txn.FxRate = &rate
		return nil
	}); err != nil {
		return err
	}
	// Persist the updated txn columns.
	if err := s.txRepo.Update(txn); err != nil {
		return err
	}

	// 3. settle_reservation
	var settleStepID uint64
	if err := exec.RunStep(ctx, "settle_reservation", convertedAmount, accountCcy,
		map[string]any{"txn_id": txn.ID}, func() error {
			memo := fmt.Sprintf("Order #%d partial fill (txn #%d)", order.ID, txn.ID)
			_, err := s.accountClient.PartialSettleReservation(ctx, order.ID, txn.ID, convertedAmount, memo)
			return err
		}); err != nil {
		return err
	}
	// Record the ID of the settle step for compensation linking.
	if stepLog, err := s.sagaRepo.GetByStepName(order.ID, "settle_reservation"); err == nil {
		settleStepID = stepLog.ID
	}

	// 4. update_holding
	if err := exec.RunStep(ctx, "update_holding", decimal.Zero, "", nil, func() error {
		return s.holdingRepo.Upsert(&model.Holding{
			UserID: order.UserID, SecurityType: order.SecurityType,
			SecurityID: s.resolveSecurityID(order), Quantity: txn.Quantity,
		})
	}); err != nil {
		// Compensation: credit back the settle.
		_ = exec.RunCompensation(ctx, settleStepID, "compensate_settle_via_credit", func() error {
			memo := fmt.Sprintf("Compensating order #%d fill #%d", order.ID, txn.ID)
			_, cerr := s.accountClient.CreditWithLock(ctx, order.AccountID, convertedAmount, memo)
			return cerr
		})
		return err
	}

	// 5. credit_commission — best-effort; do not fail the trade on commission error.
	commissionAmount := s.computeCommission(convertedAmount)
	if err := exec.RunStep(ctx, "credit_commission", commissionAmount, accountCcy,
		map[string]any{"memo_key": fmt.Sprintf("commission-%d", txn.ID)}, func() error {
			memo := fmt.Sprintf("Commission for order #%d fill #%d", order.ID, txn.ID)
			_, err := s.accountClient.CreditWithLock(ctx, s.bankCommissionAccountID(), commissionAmount, memo)
			return err
		}); err != nil {
		log.Printf("WARN: commission credit failed for order %d fill %d: %v (recovery will retry)", order.ID, txn.ID, err)
		// Deliberately do not return — the trade is valid.
	}

	return nil
}

func (s *PortfolioService) computeCommission(tradeValue decimal.Decimal) decimal.Decimal {
	return tradeValue.Mul(s.settings.CommissionRate())
}
```

Implement `resolveSecurityID(order)` (reads listing.SecurityID from the DB), `bankCommissionAccountID()` (reads from settings), and `listingCurrency(listingID)` (joins listings → exchange).

- [ ] **Step 4: Run tests**

Run: `cd stock-service && go test ./internal/service/ -run TestProcessBuyFill -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/portfolio_service.go \
        stock-service/internal/service/portfolio_service_test.go
git commit -m "feat(stock-service): buy-fill saga with compensation + cross-currency

Rewrites ProcessBuyFill as a saga: record_transaction → convert_amount →
settle_reservation → update_holding → credit_commission. On holding
failure, compensates with CreditWithLock reverse. Commission failure is
logged + left for recovery, not propagated. Routes forex orders to
forex_fill_service."
```

---

## Task 14: stock-service — sell fill saga rewrite

**Files:**
- Modify: `stock-service/internal/service/portfolio_service.go`
- Modify: `stock-service/internal/service/portfolio_service_test.go`

Write analogous failing tests and the `ProcessSellFill` rewrite following the same saga pattern as Task 13, but with:

- Step 3 is `credit_proceeds` (CreditWithLock against the user's account)
- Step 4 is `decrement_holding` (PartialSettleHoldingReservation — use the holding reservation flow)
- Compensation on step-4 failure: DebitWithLock reverse of the credit

Commit:

```bash
git commit -m "feat(stock-service): sell-fill saga rewrite

Credits proceeds first, decrements the holding via
PartialSettleHoldingReservation after. Compensation on holding failure
reverses the credit with DebitWithLock."
```

---

## Task 15: stock-service — forex fill service

**Files:**
- Create: `stock-service/internal/service/forex_fill_service.go`
- Create: `stock-service/internal/service/forex_fill_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestProcessForexBuy_DebitsQuoteCreditsBase_NoHolding(t *testing.T) {
	fx := newForexFillFixture(t)
	order := fx.newApprovedForexBuy(77 /* USD quote acc */, 88 /* EUR base acc */, 100 /* qty */, decimal.NewFromFloat(1.05))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 100,
		PricePerUnit: decimal.NewFromFloat(1.05),
		TotalPrice:   decimal.NewFromFloat(105), // 100 * 1.05 USD
	}
	err := fx.svc.ProcessForexBuy(context.Background(), order, txn)
	if err != nil {
		t.Fatal(err)
	}
	if !fx.accountClient.lastPartialSettle.amount.Equal(decimal.NewFromFloat(105)) {
		t.Errorf("quote debit: got %s", fx.accountClient.lastPartialSettle.amount)
	}
	if fx.accountClient.lastPartialSettle.accountID != 77 {
		t.Errorf("quote account: got %d", fx.accountClient.lastPartialSettle.accountID)
	}
	if !fx.accountClient.lastCredit.amount.Equal(decimal.NewFromInt(100)) {
		t.Errorf("base credit: got %s want 100 EUR", fx.accountClient.lastCredit.amount)
	}
	if fx.accountClient.lastCredit.accountID != 88 {
		t.Errorf("base credit account: got %d want 88", fx.accountClient.lastCredit.accountID)
	}
	// No holding created.
	if fx.holdingRepo.upsertCalled {
		t.Error("forex should not touch holdings")
	}
	// No exchange-service call.
	if fx.exchangeClient.convertCalled {
		t.Error("forex should not call exchange-service")
	}
}

func TestProcessForexBuy_BaseCreditFails_CompensatesQuoteDebit(t *testing.T) {
	fx := newForexFillFixture(t)
	order := fx.newApprovedForexBuy(77, 88, 100, decimal.NewFromFloat(1.05))
	txn := &model.OrderTransaction{
		ID: 900, OrderID: order.ID, Quantity: 100,
		PricePerUnit: decimal.NewFromFloat(1.05),
		TotalPrice:   decimal.NewFromFloat(105),
	}
	fx.accountClient.forceCreditErrorOnAccount(88, errors.New("down"))

	err := fx.svc.ProcessForexBuy(context.Background(), order, txn)
	if err == nil {
		t.Fatal("expected error")
	}
	if !fx.accountClient.compensateCredit.amount.Equal(decimal.NewFromFloat(105)) {
		t.Errorf("compensation credit to quote account: got %s", fx.accountClient.compensateCredit.amount)
	}
}
```

- [ ] **Step 2: Implement the service**

Create `stock-service/internal/service/forex_fill_service.go`:

```go
package service

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type ForexFillService struct {
	sagaRepo       SagaLogRepo
	accountClient  AccountClient
	txRepo         OrderTransactionRepo
	settings       SettingsProvider
}

func NewForexFillService(sagaRepo SagaLogRepo, accountClient AccountClient, txRepo OrderTransactionRepo, settings SettingsProvider) *ForexFillService {
	return &ForexFillService{sagaRepo: sagaRepo, accountClient: accountClient, txRepo: txRepo, settings: settings}
}

func (s *ForexFillService) ProcessForexBuy(ctx context.Context, order *model.Order, txn *model.OrderTransaction) error {
	if order.BaseAccountID == nil {
		return fmt.Errorf("forex order %d missing base_account_id", order.ID)
	}
	exec := NewSagaExecutor(s.sagaRepo, order.SagaID, order.ID, ptrU64(txn.ID))

	quoteAmount := txn.TotalPrice       // native quote-ccy already
	baseAmount := decimal.NewFromInt(txn.Quantity) // 1 pair unit = 1 base unit

	// 1. record_transaction (already persisted; step is for audit trail)
	if err := exec.RunStep(ctx, "record_transaction", quoteAmount, order.ReservationCurrency, nil, func() error { return nil }); err != nil {
		return err
	}

	// 2. settle_reservation_quote
	if err := exec.RunStep(ctx, "settle_reservation_quote", quoteAmount, order.ReservationCurrency,
		map[string]any{"account_id": order.AccountID}, func() error {
			memo := fmt.Sprintf("Forex buy order #%d fill #%d — debit quote", order.ID, txn.ID)
			_, err := s.accountClient.PartialSettleReservation(ctx, order.ID, txn.ID, quoteAmount, memo)
			return err
		}); err != nil {
		return err
	}
	quoteStep, _ := s.sagaRepo.GetByStepName(order.ID, "settle_reservation_quote")

	// 3. credit_base
	if err := exec.RunStep(ctx, "credit_base", baseAmount, "",
		map[string]any{"base_account_id": *order.BaseAccountID}, func() error {
			memo := fmt.Sprintf("Forex buy order #%d fill #%d — credit base", order.ID, txn.ID)
			_, err := s.accountClient.CreditWithLock(ctx, *order.BaseAccountID, baseAmount, memo)
			return err
		}); err != nil {
		// Compensation: credit back the quote side.
		_ = exec.RunCompensation(ctx, quoteStep.ID, "compensate_quote_settle", func() error {
			memo := fmt.Sprintf("Compensating forex order #%d fill #%d — refund quote", order.ID, txn.ID)
			_, cerr := s.accountClient.CreditWithLock(ctx, order.AccountID, quoteAmount, memo)
			return cerr
		})
		return err
	}

	// 4. credit_commission (optional, per settings)
	commission := s.settings.CommissionRate().Mul(quoteAmount)
	if commission.Sign() > 0 {
		_ = exec.RunStep(ctx, "credit_commission", commission, order.ReservationCurrency,
			map[string]any{"memo_key": fmt.Sprintf("commission-%d", txn.ID)}, func() error {
				memo := fmt.Sprintf("Commission for forex order #%d fill #%d", order.ID, txn.ID)
				_, err := s.accountClient.CreditWithLock(ctx, s.settings.BankCommissionAccountID(), commission, memo)
				return err
			})
	}
	return nil
}
```

- [ ] **Step 3: Run tests**

Run: `cd stock-service && go test ./internal/service/ -run TestProcessForexBuy -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/forex_fill_service.go \
        stock-service/internal/service/forex_fill_service_test.go
git commit -m "feat(stock-service): ForexFillService (debit quote, credit base, no holding)

Forex-specific fill path. Uses the forex listing's own price; does not
call exchange-service. Two ledger entries per fill (debit on quote
account, credit on base account). Compensates with a reverse credit on
the quote account if base credit fails."
```

---

## Task 16: stock-service — Kafka publish after saga step; route forex in execution engine

**Files:**
- Modify: `stock-service/internal/service/order_execution.go`

- [ ] **Step 1: Move Kafka publish inside the saga + sync**

In `order_execution.go` around line 196, replace the detached-goroutine publish with a synchronous step. The change looks like:

```go
// Publish fill event to Kafka — synchronous, AFTER the fill saga completed.
if e.producer != nil {
	key := fmt.Sprintf("order-fill-%d", txn.ID)
	_ = e.producer.PublishOrderFilled(e.baseCtx, map[string]any{
		"saga_id":          order.SagaID,
		"order_id":         orderID,
		"order_txn_id":     txn.ID,
		"user_id":          order.UserID,
		"direction":        order.Direction,
		"security_type":    order.SecurityType,
		"ticker":           order.Ticker,
		"filled_qty":       portionSize,
		"remaining_qty":    order.RemainingPortions,
		"native_amount":    txn.TotalPrice.StringFixed(4),
		"native_currency":  txn.NativeCurrency,
		"converted_amount": decString(txn.ConvertedAmount),
		"account_currency": txn.AccountCurrency,
		"fx_rate":          decString(txn.FxRate),
		"is_done":          order.IsDone,
		"kafka_key":        key,
		"timestamp":        time.Now().Unix(),
	})
}
```

Add a small `decString` helper that returns empty string for nil decimal pointers.

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/order_execution.go
git commit -m "fix(stock-service): publish order_filled synchronously after saga completes

Closes bug #4h from docs/Bugs.txt. Event no longer fires before the fill
saga has fully committed. Adds saga_id, native/converted amounts, and
fx_rate to the payload for downstream consumers."
```

---

## Task 17: stock-service — saga recovery on startup

**Files:**
- Create: `stock-service/internal/service/saga_recovery.go`
- Create: `stock-service/internal/service/saga_recovery_test.go`
- Modify: `stock-service/cmd/main.go`

Implement per §5.7 of the design spec. Core loop:

```go
func (r *SagaRecovery) Reconcile(ctx context.Context) error {
	stuck, err := r.sagaRepo.ListStuckSagas(30 * time.Second)
	if err != nil {
		return err
	}
	for _, step := range stuck {
		if err := r.reconcileStep(ctx, step); err != nil {
			log.Printf("WARN: recovery step %d: %v", step.ID, err)
		}
	}
	return nil
}

func (r *SagaRecovery) reconcileStep(ctx context.Context, step model.SagaLog) error {
	switch step.StepName {
	case "settle_reservation", "settle_reservation_quote":
		return r.reconcileSettle(ctx, step)
	case "update_holding", "decrement_holding":
		return r.reconcileHolding(ctx, step)
	case "credit_commission", "credit_base":
		return r.retryIdempotent(ctx, step)
	case "publish_kafka":
		return r.retryIdempotent(ctx, step)
	}
	return nil
}
```

`reconcileSettle` calls `accountClient.GetReservation(step.OrderID)`; if `step.OrderTransactionID` appears in `SettledTransactionIds`, mark the step completed; else retry the settle (idempotent on txn_id).

Write at least two tests: one for the "already-settled" branch, one for the "retry" branch.

In `cmd/main.go`, call `sagaRecovery.Reconcile(ctx)` immediately after `execEngine.Start(ctx)`:

```go
go func() {
	if err := sagaRecovery.Reconcile(ctx); err != nil {
		log.Printf("WARN: initial saga recovery: %v", err)
	}
	// Then run periodically.
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sagaRecovery.Reconcile(ctx); err != nil {
				log.Printf("WARN: periodic saga recovery: %v", err)
			}
		}
	}
}()
```

Commit:

```bash
git commit -m "feat(stock-service): saga recovery reconciler (startup + periodic)"
```

---

## Task 18: api-gateway — POST /api/v1/me/orders validation

**Files:**
- Modify: `api-gateway/internal/handler/me_order_handler.go` (or the file that handles this route — use `grep -rn "/me/orders" api-gateway/` if unsure)
- Modify: `api-gateway/internal/handler/validation.go`

- [ ] **Step 1: Identify the current handler**

Run: `cd api-gateway && grep -rn 'me/orders' internal/`

- [ ] **Step 2: Add validation for forex-only fields**

Inside the POST-/me/orders handler (after parsing the body, before the gRPC call):

```go
if req.SecurityType == "forex" {
	if req.Direction != "buy" {
		apiError(c, http.StatusBadRequest, "validation_error", "forex orders can only be buy")
		return
	}
	if req.BaseAccountID == nil {
		apiError(c, http.StatusBadRequest, "validation_error", "forex orders require base_account_id")
		return
	}
	if req.BaseAccountID != nil && *req.BaseAccountID == req.AccountID {
		apiError(c, http.StatusBadRequest, "validation_error", "base_account_id must differ from account_id")
		return
	}
}
```

Currency-match validation is performed in stock-service (defense in depth per CLAUDE.md §Input Validation). The gateway's job is to catch obvious misuse.

- [ ] **Step 3: Update Swagger annotations**

Document the new optional `base_account_id` param; list the new 400 and 409 response conditions.

- [ ] **Step 4: Regenerate Swagger**

Run: `make swagger`

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/ api-gateway/docs/
git commit -m "feat(api-gateway): validate forex-only fields on POST /api/v1/me/orders

Rejects forex sell orders, missing base_account_id, and base=account at
the gateway. Currency-match validation happens in stock-service per the
defense-in-depth guideline."
```

---

## Task 19: docs — Specification.md and REST_API_v1.md

**Files:**
- Modify: `docs/Specification.md`
- Modify: `docs/api/REST_API_v1.md`

Follow design spec §8.1 / §8.2 exactly:

**§17:** POST `/api/v1/me/orders` — add `base_account_id`; document 409 insufficient available balance, 400 forex mismatches. GET `/api/v1/me/orders/{id}` — document new response fields. GET `/api/v1/me/accounts/{id}` — add `reserved_balance`, `available_balance`.

**§18:** new entities AccountReservation, AccountReservationSettlement, HoldingReservation, HoldingReservationSettlement, SagaLog; modify Account, OrderTransaction, Holding.

**§19:** `stock.order-filled` payload adds `saga_id`, `converted_amount`, `account_currency`, `fx_rate`; new topic `stock.order-failed`.

**§20:** `reservation_status`: `active|released|settled`. `saga_step_status`: `pending|completed|failed|compensating|compensated`.

**§21:** business rules per design spec.

**§11:** document `ReserveFunds`, `ReleaseReservation`, `PartialSettleReservation`, `GetReservation`.

In `docs/api/REST_API_v1.md`, mirror the §17 changes.

Remove the Phase-1 warning banner added in the Phase 1 plan's Task 6 — Phase 2 replaces it with actual bank-safety.

Commit:

```bash
git commit -m "docs(spec): reservation system, forex-as-exchange, saga-log fill path"
```

---

## Task 20: integration tests — core workflows

**Files:**
- Create: `test-app/workflows/helpers_orders_test.go` (shared helpers)
- Create: `test-app/workflows/wf_stock_reservation_test.go`
- Create: `test-app/workflows/wf_stock_concurrent_orders_test.go`
- Create: `test-app/workflows/wf_stock_cross_currency_test.go`
- Create: `test-app/workflows/wf_stock_fill_failure_test.go`
- Create: `test-app/workflows/wf_stock_commission_failure_test.go`
- Create: `test-app/workflows/wf_forex_test.go`
- Create: `test-app/workflows/wf_forex_validation_test.go`
- Create: `test-app/workflows/wf_stop_limit_refund_test.go`

Each test follows the existing `test-app/workflows/` idioms. Core shape:

- Spin up the stack (or connect to a running one) using existing helpers.
- Create a client, log them in, fund their account.
- Place the relevant order(s).
- Assert the user-visible consequence: ledger entries, balances, reserved_balance, holding quantities, order status.

**Exactly one assertion per critical bug-4 scenario:**

- `wf_stock_reservation_test.go`: place buy, cancel, assert balance unchanged AND reserved_balance=0.
- `wf_stock_concurrent_orders_test.go`: 20 goroutines place orders summing > balance; assert count(approved) × amount ≤ balance.
- `wf_stock_cross_currency_test.go`: USD stock + RSD account; verify RSD ledger entries equal native × placement_rate within rounding.
- `wf_stock_fill_failure_test.go`: stub account-service to fail PartialSettle on the *first* call only (use a gRPC interceptor); assert the trade eventually completes via recovery AND no double-debit.
- `wf_stock_commission_failure_test.go`: stub commission credit to fail; assert main trade committed, saga_logs shows commission in pending, recovery eventually marks it completed.
- `wf_forex_test.go`: EUR/USD buy with USD + EUR accounts; verify USD balance decreased by ~quantity × price, EUR balance increased by quantity; no `holdings` row; no `Convert` call in exchange-service logs.
- `wf_forex_validation_test.go`: missing base_account_id → 400; account currency mismatch → 400; sell → 400.
- `wf_stop_limit_refund_test.go`: place stop-limit whose trigger never fires, invoke expiry (wait or call an admin endpoint), assert reservation released.

For each test, commit as a separate commit with a descriptive message.

---

## Task 21: Update docs/Bugs.txt

**Files:**
- Modify: `docs/Bugs.txt`

- [ ] **Step 1: Mark bugs #1, #2, #3, #4 all resolved**

Prepend each bug section with `[FIXED 2026-04-22 — Phase 2]` (or `Phase 1` for bugs 1/2/3 if that marker isn't already in place). Keep the text for historical reference.

- [ ] **Step 2: Commit**

```bash
git add docs/Bugs.txt
git commit -m "docs(bugs): mark all four critical bugs fixed after Phase 2"
```

---

## Task 22: Final verification

- [ ] **Step 1: Full unit test sweep**

Run: `make test`

Expected: PASS across all touched services.

- [ ] **Step 2: Full lint sweep**

Run: `make lint`

Expected: zero new warnings in `account-service`, `stock-service`, `api-gateway`.

- [ ] **Step 3: Docker smoke test**

Run: `make docker-up`

In another terminal, run the integration suite end-to-end:

```bash
cd test-app && go test ./workflows/ -v -timeout 15m
```

Expected: all PASS.

Specifically confirm the four big assertions:
- Placing a buy reserves the correct amount on the account.
- Fills debit the correct converted amount and write ledger entries.
- Forex orders move money between two user accounts; no holding row.
- Killing and restarting stock-service mid-fill recovers cleanly.

Bring the stack down:

```bash
make docker-down
```

- [ ] **Step 4: Update Spec's Phase-1 warning banner**

In `docs/Specification.md`, remove the "⚠ Phase-1 note" banner added in the Phase 1 plan (Task 6 there). Securities settlement is now bank-safe.

```bash
git add docs/Specification.md
git commit -m "docs(spec): remove Phase-1 best-effort warning — fill path is bank-safe"
```

Phase 2 complete. All four critical bugs from `docs/Bugs.txt` are fixed. Money moves correctly, reservations enforce available balance at placement, saga compensations keep state consistent on partial failure, forex trades as an internal FX between two user accounts, commissions are durable via recovery, and Kafka events only publish after committed fill sagas.

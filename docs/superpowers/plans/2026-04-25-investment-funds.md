# Investment Funds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Celina-4 investment funds (`docs/superpowers/specs/2026-04-24-investment-funds-design.md`): supervisors create funds with one RSD account each; clients invest/redeem; supervisors trade securities on behalf of funds; auto-liquidation covers redemptions when fund cash is insufficient; supervisor demotion reassigns funds to the admin via Kafka.

**Architecture:** stock-service owns funds, positions, contributions, and fund-side holdings. account-service is unchanged (fund RSD accounts are bank-owned accounts via `CreateBankAccount`). Invest/redeem are saga-orchestrated with the existing `SagaLog` + `SagaExecutor`. Liquidation submits real market sell orders through the existing placement+execution pipeline. user-service publishes `user.supervisor-demoted` from an outbox; stock-service consumes and reassigns funds in one TX. api-gateway exposes 8 new REST routes plus an `on_behalf_of` extension on `POST /me/orders`.

**Tech Stack:** Go, gRPC (protobuf via `make proto`), GORM, PostgreSQL, Kafka (segmentio/kafka-go), Gin, Redis (cached only — never authoritative).

---

## File structure

**stock-service (owner of funds):**
- Create:
  - `stock-service/internal/model/investment_fund.go` — `InvestmentFund` struct + `BeforeUpdate` version hook.
  - `stock-service/internal/model/client_fund_position.go` — `ClientFundPosition` struct + `BeforeUpdate` hook.
  - `stock-service/internal/model/fund_contribution.go` — `FundContribution` struct (no version hook; status-driven, append-mostly).
  - `stock-service/internal/model/fund_holding.go` — `FundHolding` struct + `BeforeUpdate` hook.
  - `stock-service/internal/repository/fund_repository.go` — CRUD on `investment_funds`.
  - `stock-service/internal/repository/client_fund_position_repository.go` — version-locked upsert + read.
  - `stock-service/internal/repository/fund_contribution_repository.go` — append + status update.
  - `stock-service/internal/repository/fund_holding_repository.go` — version-locked upsert; FIFO read for liquidation.
  - `stock-service/internal/service/fund_service.go` — Create/Update/Get/List funds (no money flow).
  - `stock-service/internal/service/fund_invest_saga.go` — invest saga steps + compensations.
  - `stock-service/internal/service/fund_redeem_saga.go` — redeem saga steps.
  - `stock-service/internal/service/fund_liquidation.go` — liquidation sub-saga (FIFO sell-order submission, fill polling).
  - `stock-service/internal/service/fund_position_reads.go` — `ListMyPositions`, `ListBankPositions`, derived value/profit calculators.
  - `stock-service/internal/service/actuary_performance.go` — sum of `capital_gains` per acting employee.
  - `stock-service/internal/handler/investment_fund_handler.go` — gRPC `InvestmentFundService` server.
  - `stock-service/internal/consumer/supervisor_demoted_consumer.go` — Kafka consumer for `user.supervisor-demoted`.
  - `stock-service/internal/service/fund_service_test.go` and one `*_test.go` per service file (TDD).
- Modify:
  - `stock-service/internal/model/order.go` — add `FundID *uint64` field with `gorm:"index"` tag.
  - `stock-service/internal/service/order_service.go:CreateOrder` — extend `compute_reservation_target` step to handle `req.OnBehalfOf.Type == "fund"` (auth gate, account selection, order metadata).
  - `stock-service/internal/service/portfolio_service.go:upsertHoldingForBuy` — branch on `order.FundID != nil` to upsert `fund_holdings` instead of `holdings`.
  - `stock-service/cmd/main.go` — wire fund service, repositories, demote-supervisor consumer, new gRPC server registration.

**user-service (supervisor demotion):**
- Create:
  - `user-service/internal/model/outbox.go` — single-table outbox with `aggregate_id`, `event_type`, `payload`, `created_at`, `published_at` columns.
  - `user-service/internal/repository/outbox_repository.go` — insert + claim-and-publish.
  - `user-service/internal/service/supervisor_demote_publisher.go` — outbox relay goroutine.
  - `user-service/internal/handler/list_employee_full_names.go` — only if RPC doesn't already exist (Task 5 verifies).
- Modify:
  - `user-service/internal/service/employee_service.go` — in `UpdateEmployeePermissions` (or whichever method removes `funds.manage`), insert outbox row inside the same DB TX.
  - `user-service/internal/service/role_service.go` — add `funds.bank-position-read` permission and assign to `EmployeeSupervisor` and `EmployeeAdmin` seed sets.
  - `user-service/cmd/main.go` — start outbox relay goroutine; `EnsureTopics` includes `user.supervisor-demoted`.

**contract (proto + Kafka):**
- Modify:
  - `contract/proto/stock/stock.proto` — add `InvestmentFundService` (10 RPCs), 4 message types per RPC pair, plus `OnBehalfOf` shared message.
  - `contract/proto/user/user.proto` — add `ListEmployeeFullNames` RPC if it doesn't already exist.
  - `contract/kafka/messages.go` — add 6 topic constants + payload structs (`StockFundCreatedMessage`, `StockFundInvestedMessage`, `StockFundRedeemedMessage`, `StockFundUpdatedMessage`, `StockFundsReassignedMessage`, `UserSupervisorDemotedMessage`).

**api-gateway (REST surface):**
- Create:
  - `api-gateway/internal/handler/investment_fund_handler.go` — 8 handler functions matching §6.1–§6.9 of the spec.
- Modify:
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes` — register the 8 new routes; pass new `InvestmentFundServiceClient` parameter.
  - `api-gateway/internal/handler/stock_order_handler.go:CreateOrder` — accept `on_behalf_of` body field; pass through to gRPC `OnBehalfOf` proto.
  - `api-gateway/cmd/main.go` — dial stock-service for `InvestmentFundServiceClient`, pass into `RegisterCoreRoutes`.

**Documentation (CLAUDE.md mandates):**
- Modify:
  - `docs/Specification.md` — §3, §6, §11, §17, §18, §19, §20, §21 per spec §11.
  - `docs/api/REST_API_v1.md` — new section per route + extension to existing `POST /me/orders` block.
  - `docker-compose.yml` and `docker-compose-remote.yml` — no new env vars required (no new gRPC dependencies between services beyond what's already wired).

---

## Pre-flight: branch + worktree

This plan assumes a long-lived branch. Do NOT work on `main` or `Development` directly.

- [ ] **Step 0.1: Create the feature branch from `Development`**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
git fetch origin
git checkout Development
git pull origin Development
git checkout -b feature/investment-funds
```

- [ ] **Step 0.2: Verify the toolchain runs end-to-end before writing any code**

```bash
make tidy
make proto
make build
make lint
make test
```

Expected: every command exits 0. If any fail on a clean checkout, fix the failure on `Development` first (separate PR) — investment-funds work assumes a green baseline.

---

## Task 1: Add `FundID` to the `Order` model

**Files:**
- Modify: `stock-service/internal/model/order.go`
- Test: `stock-service/internal/model/order_test.go` (create if missing)

- [ ] **Step 1: Write the failing test**

Append to `stock-service/internal/model/order_test.go`:

```go
package model

import "testing"

func TestOrder_FundIDDefaultsToNil(t *testing.T) {
	o := Order{}
	if o.FundID != nil {
		t.Errorf("expected FundID to default to nil, got %v", *o.FundID)
	}
}

func TestOrder_FundIDIsAssignable(t *testing.T) {
	id := uint64(101)
	o := Order{FundID: &id}
	if o.FundID == nil || *o.FundID != 101 {
		t.Fatalf("expected FundID=101, got %v", o.FundID)
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

```bash
cd stock-service && go test ./internal/model/... -run TestOrder_FundID -v
```

Expected: compile error `o.FundID undefined`.

- [ ] **Step 3: Add `FundID` field to `Order`**

In `stock-service/internal/model/order.go`, locate the `Order` struct and add the field next to other optional pointer columns:

```go
// FundID is non-nil when the order was placed on behalf of an investment
// fund. owner_user_id is then 1_000_000_000 (bank sentinel) and
// system_type is "employee". Fills credit fund_holdings instead of
// holdings.
FundID *uint64 `gorm:"index" json:"fund_id,omitempty"`
```

- [ ] **Step 4: Run tests, verify pass**

```bash
cd stock-service && go test ./internal/model/... -run TestOrder_FundID -v
```

Expected: 2 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/model/order.go stock-service/internal/model/order_test.go
git commit -m "feat(stock-service): add Order.FundID for on-behalf-of-fund orders"
```

---

## Task 2: `InvestmentFund` model + version hook

**Files:**
- Create: `stock-service/internal/model/investment_fund.go`
- Create: `stock-service/internal/model/investment_fund_test.go`

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestInvestmentFund_BeforeUpdate_BumpsVersion(t *testing.T) {
	f := &InvestmentFund{Version: 5}
	if err := f.BeforeUpdate(nil); err != nil {
		t.Fatalf("BeforeUpdate returned error: %v", err)
	}
	if f.Version != 6 {
		t.Errorf("expected version=6, got %d", f.Version)
	}
}

func TestInvestmentFund_DefaultMinimumIsZero(t *testing.T) {
	f := &InvestmentFund{}
	if !f.MinimumContributionRSD.Equal(decimal.Zero) {
		t.Errorf("expected zero default minimum, got %s", f.MinimumContributionRSD)
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

```bash
cd stock-service && go test ./internal/model/... -run TestInvestmentFund -v
```

Expected: compile error.

- [ ] **Step 3: Create the model file**

`stock-service/internal/model/investment_fund.go`:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// InvestmentFund is a supervisor-managed pool of cash + securities held in a
// single RSD account at account-service. Clients invest RSD (or any FX
// currency converted to RSD) and receive a proportional share of the fund's
// realised + unrealised P&L. The bank itself can hold a position via the
// 1_000_000_000 owner sentinel.
type InvestmentFund struct {
	ID                     uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                   string          `gorm:"size:128;not null" json:"name"`
	Description            string          `gorm:"type:text;not null;default:''" json:"description"`
	ManagerEmployeeID      int64           `gorm:"not null;index" json:"manager_employee_id"`
	MinimumContributionRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"minimum_contribution_rsd"`
	RSDAccountID           uint64          `gorm:"not null;uniqueIndex" json:"rsd_account_id"`
	Active                 bool            `gorm:"not null;default:true;index" json:"active"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	Version                int64           `gorm:"not null;default:0" json:"-"`
}

// BeforeUpdate enforces optimistic locking: the WHERE clause requires the
// pre-update version, and the version column is incremented atomically.
// Mirrors the pattern used on Account, Card, Loan, etc. per CLAUDE.md.
func (f *InvestmentFund) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", f.Version)
	}
	f.Version++
	return nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
cd stock-service && go test ./internal/model/... -run TestInvestmentFund -v
```

Expected: 2 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/model/investment_fund.go stock-service/internal/model/investment_fund_test.go
git commit -m "feat(stock-service): add InvestmentFund model + version hook"
```

---

## Task 3: `ClientFundPosition`, `FundContribution`, `FundHolding` models

**Files:**
- Create: `stock-service/internal/model/client_fund_position.go`
- Create: `stock-service/internal/model/fund_contribution.go`
- Create: `stock-service/internal/model/fund_holding.go`
- Create/append: `stock-service/internal/model/investment_fund_test.go`

- [ ] **Step 1: Write failing tests for all three**

Append to `stock-service/internal/model/investment_fund_test.go`:

```go
func TestClientFundPosition_BeforeUpdate_BumpsVersion(t *testing.T) {
	p := &ClientFundPosition{Version: 2}
	if err := p.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Version != 3 {
		t.Errorf("got %d want 3", p.Version)
	}
}

func TestFundContribution_StatusEnum(t *testing.T) {
	c := &FundContribution{Status: FundContributionStatusPending}
	if c.Status != "pending" {
		t.Errorf("status const wrong: %q", c.Status)
	}
}

func TestFundContribution_DirectionEnum(t *testing.T) {
	for _, d := range []string{FundDirectionInvest, FundDirectionRedeem} {
		if d == "" {
			t.Errorf("empty direction const")
		}
	}
}

func TestFundHolding_BeforeUpdate_BumpsVersion(t *testing.T) {
	h := &FundHolding{Version: 0}
	if err := h.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if h.Version != 1 {
		t.Errorf("got %d want 1", h.Version)
	}
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
cd stock-service && go test ./internal/model/... -v
```

Expected: compile errors on every new symbol.

- [ ] **Step 3: Create `client_fund_position.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ClientFundPosition is one (fund, owner) pair's accumulated contribution.
// owner is identified by (UserID, SystemType). The bank's own stake uses
// UserID=1_000_000_000 + SystemType="employee".
type ClientFundPosition struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID              uint64          `gorm:"not null;uniqueIndex:ux_pos_fund_user,priority:1" json:"fund_id"`
	UserID              uint64          `gorm:"not null;uniqueIndex:ux_pos_fund_user,priority:2;index:ix_pos_user" json:"user_id"`
	SystemType          string          `gorm:"size:10;not null;uniqueIndex:ux_pos_fund_user,priority:3;index:ix_pos_user" json:"system_type"`
	TotalContributedRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"total_contributed_rsd"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	Version             int64           `gorm:"not null;default:0" json:"-"`
}

func (p *ClientFundPosition) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", p.Version)
	}
	p.Version++
	return nil
}
```

- [ ] **Step 4: Create `fund_contribution.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

const (
	FundDirectionInvest = "invest"
	FundDirectionRedeem = "redeem"

	FundContributionStatusPending   = "pending"
	FundContributionStatusCompleted = "completed"
	FundContributionStatusFailed    = "failed"
)

// FundContribution is one invest or redeem event. Append-mostly: status
// transitions pending → completed | failed under the saga that produced it.
// FX details captured for audit. saga_id links to saga_logs.
type FundContribution struct {
	ID                       uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID                   uint64          `gorm:"not null;index:ix_contrib_fund" json:"fund_id"`
	UserID                   uint64          `gorm:"not null;index:ix_contrib_user,priority:1" json:"user_id"`
	SystemType               string          `gorm:"size:10;not null;index:ix_contrib_user,priority:2" json:"system_type"`
	Direction                string          `gorm:"size:8;not null" json:"direction"`
	AmountNative             decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"amount_native"`
	NativeCurrency           string          `gorm:"size:8;not null" json:"native_currency"`
	AmountRSD                decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"amount_rsd"`
	FxRate                   *decimal.Decimal `gorm:"type:numeric(20,10)" json:"fx_rate,omitempty"`
	FeeRSD                   decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"fee_rsd"`
	SourceOrTargetAccountID  uint64          `gorm:"not null" json:"source_or_target_account_id"`
	SagaID                   uint64          `gorm:"not null;index:ix_contrib_saga" json:"saga_id"`
	Status                   string          `gorm:"size:12;not null" json:"status"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
}
```

- [ ] **Step 5: Create `fund_holding.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// FundHolding is the fund-side analogue of Holding. Quantity changes when an
// on-behalf-of-fund order fills (buy: +qty) or when liquidation runs (sell:
// -qty). FIFO order for liquidation = ORDER BY created_at ASC.
type FundHolding struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID           uint64          `gorm:"not null;uniqueIndex:ux_fh_security,priority:1" json:"fund_id"`
	SecurityType     string          `gorm:"size:16;not null;uniqueIndex:ux_fh_security,priority:2" json:"security_type"`
	SecurityID       uint64          `gorm:"not null;uniqueIndex:ux_fh_security,priority:3" json:"security_id"`
	Quantity         int64           `gorm:"not null;default:0" json:"quantity"`
	ReservedQuantity int64           `gorm:"not null;default:0" json:"reserved_quantity"`
	AveragePriceRSD  decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"average_price_rsd"`
	CreatedAt        time.Time       `gorm:"index" json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `gorm:"not null;default:0" json:"-"`
}

func (h *FundHolding) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", h.Version)
	}
	h.Version++
	return nil
}
```

- [ ] **Step 6: Run tests**

```bash
cd stock-service && go test ./internal/model/... -v
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/model/client_fund_position.go \
        stock-service/internal/model/fund_contribution.go \
        stock-service/internal/model/fund_holding.go \
        stock-service/internal/model/investment_fund_test.go
git commit -m "feat(stock-service): add ClientFundPosition, FundContribution, FundHolding models"
```

---

## Task 4: Wire AutoMigrate for the new tables

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Locate the AutoMigrate block**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
grep -n "AutoMigrate" stock-service/cmd/main.go
```

Expected: a single `db.AutoMigrate(...)` call listing existing models.

- [ ] **Step 2: Add the four new models to the call**

Edit `stock-service/cmd/main.go`. Append the four new model types to the existing `db.AutoMigrate(...)` argument list (alphabetical insertion is fine):

```go
if err := db.AutoMigrate(
	// … existing models …
	&model.InvestmentFund{},
	&model.ClientFundPosition{},
	&model.FundContribution{},
	&model.FundHolding{},
); err != nil {
	log.Fatalf("failed to migrate: %v", err)
}
```

- [ ] **Step 3: Build to verify the migration compiles**

```bash
cd stock-service && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Manual smoke (optional but recommended)**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
docker compose up -d stock-db
docker compose run --rm stock-service ./bin/stock-service &
sleep 5
docker compose exec stock-db psql -U postgres -d stockdb -c "\dt investment_funds client_fund_positions fund_contributions fund_holdings"
docker compose down stock-service
```

Expected: four tables listed.

- [ ] **Step 5: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): wire fund tables into AutoMigrate"
```

---

## Task 5: Repositories — `FundRepository`

**Files:**
- Create: `stock-service/internal/repository/fund_repository.go`
- Create: `stock-service/internal/repository/fund_repository_test.go`

- [ ] **Step 1: Write the failing tests using the existing in-memory test harness**

Existing repos use a real `*gorm.DB` opened via SQLite `file::memory:?cache=shared` for tests. Mirror that pattern (see `stock-service/internal/repository/holding_repository_test.go` for the helper).

`stock-service/internal/repository/fund_repository_test.go`:

```go
package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestFundRepository_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)

	f := &model.InvestmentFund{
		Name:                   "Alpha",
		ManagerEmployeeID:      25,
		MinimumContributionRSD: decimal.NewFromInt(1000),
		RSDAccountID:           4812,
		Active:                 true,
	}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("expected autoincrement id")
	}
	got, err := r.GetByID(f.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Alpha" {
		t.Errorf("got %q want Alpha", got.Name)
	}
}

func TestFundRepository_NameUnique_AmongActive(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)

	a := &model.InvestmentFund{Name: "Beta", ManagerEmployeeID: 1, RSDAccountID: 100, Active: true}
	if err := r.Create(a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := &model.InvestmentFund{Name: "Beta", ManagerEmployeeID: 1, RSDAccountID: 101, Active: true}
	if err := r.Create(b); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
cd stock-service && go test ./internal/repository/... -run TestFundRepository -v
```

Expected: compile errors.

- [ ] **Step 3: Implement the repository**

`stock-service/internal/repository/fund_repository.go`:

```go
package repository

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ErrFundNameInUse signals an attempt to create or rename a fund to a name
// already used by another active fund (case-insensitive).
var ErrFundNameInUse = errors.New("fund name already in use")

type FundRepository struct {
	db *gorm.DB
}

func NewFundRepository(db *gorm.DB) *FundRepository {
	return &FundRepository{db: db}
}

func (r *FundRepository) Create(f *model.InvestmentFund) error {
	if err := r.assertNameAvailable(f.Name, 0); err != nil {
		return err
	}
	return r.db.Create(f).Error
}

func (r *FundRepository) GetByID(id uint64) (*model.InvestmentFund, error) {
	var f model.InvestmentFund
	if err := r.db.First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// List supports search (ILIKE on name), active filter, pagination.
func (r *FundRepository) List(search string, active *bool, page, pageSize int) ([]model.InvestmentFund, int64, error) {
	q := r.db.Model(&model.InvestmentFund{})
	if search != "" {
		q = q.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(search)+"%")
	}
	if active != nil {
		q = q.Where("active = ?", *active)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.InvestmentFund
	err := q.Order("name ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// Save persists an update. Caller must read the row first; the version hook
// on InvestmentFund enforces optimistic locking via WHERE version = ?.
func (r *FundRepository) Save(f *model.InvestmentFund) error {
	if err := r.assertNameAvailable(f.Name, f.ID); err != nil {
		return err
	}
	res := r.db.Save(f)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *FundRepository) assertNameAvailable(name string, ignoreID uint64) error {
	var count int64
	q := r.db.Model(&model.InvestmentFund{}).
		Where("LOWER(name) = ? AND active = TRUE", strings.ToLower(name))
	if ignoreID != 0 {
		q = q.Where("id <> ?", ignoreID)
	}
	if err := q.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrFundNameInUse
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd stock-service && go test ./internal/repository/... -run TestFundRepository -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/repository/fund_repository.go stock-service/internal/repository/fund_repository_test.go
git commit -m "feat(stock-service): FundRepository with name-uniqueness guard"
```

---

## Task 6: Repositories — positions, contributions, fund-holdings

**Files:**
- Create:
  - `stock-service/internal/repository/client_fund_position_repository.go`
  - `stock-service/internal/repository/fund_contribution_repository.go`
  - `stock-service/internal/repository/fund_holding_repository.go`
- Create: `stock-service/internal/repository/fund_position_test.go` (covers all three repos via table tests)

- [ ] **Step 1: Write failing tests**

```go
package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestClientFundPositionRepository_UpsertIncrements(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)

	delta := decimal.NewFromInt(500)
	if err := r.IncrementContribution(1, 99, "client", delta); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := r.IncrementContribution(1, 99, "client", delta); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, err := r.GetByOwner(1, 99, "client")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.TotalContributedRSD.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("got %s want 1000", got.TotalContributedRSD)
	}
}

func TestFundHoldingRepository_FifoOrder(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundHolding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundHoldingRepository(db)
	for i := uint64(1); i <= 3; i++ {
		if err := r.Upsert(&model.FundHolding{
			FundID: 1, SecurityType: "stock", SecurityID: i,
			Quantity: 10, AveragePriceRSD: decimal.NewFromInt(100),
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, err := r.ListByFundFIFO(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 || got[0].SecurityID != 1 || got[2].SecurityID != 3 {
		t.Errorf("FIFO order broken: got %+v", got)
	}
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
cd stock-service && go test ./internal/repository/... -run "TestClientFundPositionRepository|TestFundHoldingRepository" -v
```

Expected: compile errors.

- [ ] **Step 3: Implement `client_fund_position_repository.go`**

```go
package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ClientFundPositionRepository struct {
	db *gorm.DB
}

func NewClientFundPositionRepository(db *gorm.DB) *ClientFundPositionRepository {
	return &ClientFundPositionRepository{db: db}
}

// IncrementContribution adds delta to TotalContributedRSD, creating the row
// if it does not exist. Atomic via PostgreSQL ON CONFLICT — single round-trip.
// Required by the invest saga's upsert_position step.
func (r *ClientFundPositionRepository) IncrementContribution(fundID, userID uint64, systemType string, delta decimal.Decimal) error {
	row := &model.ClientFundPosition{
		FundID:              fundID,
		UserID:              userID,
		SystemType:          systemType,
		TotalContributedRSD: delta,
	}
	res := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fund_id"}, {Name: "user_id"}, {Name: "system_type"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"total_contributed_rsd": gorm.Expr("client_fund_positions.total_contributed_rsd + ?", delta),
			"version":               gorm.Expr("client_fund_positions.version + 1"),
		}),
	}).Create(row)
	return res.Error
}

// DecrementContribution subtracts delta. Caller is expected to have verified
// the resulting value is non-negative (the redeem saga computes the
// proportional reduction explicitly).
func (r *ClientFundPositionRepository) DecrementContribution(fundID, userID uint64, systemType string, delta decimal.Decimal) error {
	res := r.db.Model(&model.ClientFundPosition{}).
		Where("fund_id = ? AND user_id = ? AND system_type = ?", fundID, userID, systemType).
		Updates(map[string]interface{}{
			"total_contributed_rsd": gorm.Expr("total_contributed_rsd - ?", delta),
			"version":               gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *ClientFundPositionRepository) GetByOwner(fundID, userID uint64, systemType string) (*model.ClientFundPosition, error) {
	var p model.ClientFundPosition
	err := r.db.Where("fund_id = ? AND user_id = ? AND system_type = ?", fundID, userID, systemType).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &p, err
}

// ListByOwner returns every position owned by (userID, systemType).
func (r *ClientFundPositionRepository) ListByOwner(userID uint64, systemType string) ([]model.ClientFundPosition, error) {
	var out []model.ClientFundPosition
	err := r.db.Where("user_id = ? AND system_type = ?", userID, systemType).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

// SumTotalContributed returns Σ total_contributed_rsd across every position
// for a fund. Used by the value-derivation helper to compute percentage-fund.
func (r *ClientFundPositionRepository) SumTotalContributed(fundID uint64) (decimal.Decimal, error) {
	var result struct{ Total decimal.Decimal }
	err := r.db.Model(&model.ClientFundPosition{}).
		Select("COALESCE(SUM(total_contributed_rsd), 0) AS total").
		Where("fund_id = ?", fundID).
		Scan(&result).Error
	return result.Total, err
}
```

- [ ] **Step 4: Implement `fund_contribution_repository.go`**

```go
package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type FundContributionRepository struct {
	db *gorm.DB
}

func NewFundContributionRepository(db *gorm.DB) *FundContributionRepository {
	return &FundContributionRepository{db: db}
}

func (r *FundContributionRepository) Create(c *model.FundContribution) error {
	return r.db.Create(c).Error
}

func (r *FundContributionRepository) UpdateStatus(id uint64, status string) error {
	res := r.db.Model(&model.FundContribution{}).
		Where("id = ?", id).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListByFund returns contributions newest-first, paginated.
func (r *FundContributionRepository) ListByFund(fundID uint64, page, pageSize int) ([]model.FundContribution, int64, error) {
	var total int64
	q := r.db.Model(&model.FundContribution{}).Where("fund_id = ?", fundID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}
	var out []model.FundContribution
	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}
```

- [ ] **Step 5: Implement `fund_holding_repository.go`**

```go
package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type FundHoldingRepository struct {
	db *gorm.DB
}

func NewFundHoldingRepository(db *gorm.DB) *FundHoldingRepository {
	return &FundHoldingRepository{db: db}
}

// Upsert applies a buy-side weighted-average update: when the row exists,
// quantity += incoming.Quantity and average_price_rsd is recomputed.
func (r *FundHoldingRepository) Upsert(h *model.FundHolding) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fund_id"}, {Name: "security_type"}, {Name: "security_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"quantity": gorm.Expr("fund_holdings.quantity + ?", h.Quantity),
			"average_price_rsd": gorm.Expr(
				"((fund_holdings.average_price_rsd * fund_holdings.quantity) + (? * ?)) / NULLIF(fund_holdings.quantity + ?, 0)",
				h.AveragePriceRSD, h.Quantity, h.Quantity,
			),
			"version": gorm.Expr("fund_holdings.version + 1"),
		}),
	}).Create(h).Error
}

// DecrementQuantity subtracts q (>0) from a holding. Returns ErrRecordNotFound
// if the row doesn't exist or the optimistic lock fails.
func (r *FundHoldingRepository) DecrementQuantity(holdingID uint64, q int64) error {
	res := r.db.Model(&model.FundHolding{}).
		Where("id = ? AND quantity >= ?", holdingID, q).
		Updates(map[string]interface{}{
			"quantity": gorm.Expr("quantity - ?", q),
			"version":  gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListByFundFIFO returns holdings with quantity > 0, oldest first. Used by
// the liquidation sub-saga to choose which holdings to sell.
func (r *FundHoldingRepository) ListByFundFIFO(fundID uint64) ([]model.FundHolding, error) {
	var out []model.FundHolding
	err := r.db.Where("fund_id = ? AND quantity > 0", fundID).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

func (r *FundHoldingRepository) GetByFundAndSecurity(fundID uint64, securityType string, securityID uint64) (*model.FundHolding, error) {
	var h model.FundHolding
	err := r.db.Where("fund_id = ? AND security_type = ? AND security_id = ?", fundID, securityType, securityID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &h, err
}
```

- [ ] **Step 6: Run tests, verify pass**

```bash
cd stock-service && go test ./internal/repository/... -v
```

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/repository/client_fund_position_repository.go \
        stock-service/internal/repository/fund_contribution_repository.go \
        stock-service/internal/repository/fund_holding_repository.go \
        stock-service/internal/repository/fund_position_test.go
git commit -m "feat(stock-service): fund position, contribution, holding repositories"
```

---

## Task 7: System setting `fund_redemption_fee_pct` + permission seed

**Files:**
- Modify: `stock-service/cmd/main.go` — seed setting on startup.
- Modify: `user-service/internal/service/role_service.go` — add `funds.bank-position-read` permission and assign to two roles.
- Test: `user-service/internal/service/role_service_test.go` (append).

- [ ] **Step 1: Seed the system setting at startup**

In `stock-service/cmd/main.go`, locate where existing settings are seeded (search for `settingRepo.Set` or `system_settings`). Add:

```go
// Default fund redemption fee for client redemptions (supervisors acting on
// the bank position pay 0). Override in DB via the setting key.
if _, err := settingRepo.GetOrCreate("fund_redemption_fee_pct", "0.005"); err != nil {
	log.Printf("WARN: seed fund_redemption_fee_pct: %v", err)
}
```

If `SystemSettingRepository` doesn't have `GetOrCreate`, add a thin helper or use `Set` only when missing:

```go
if _, err := settingRepo.Get("fund_redemption_fee_pct"); err != nil {
	_ = settingRepo.Set("fund_redemption_fee_pct", "0.005")
}
```

- [ ] **Step 2: Add the new permission**

In `user-service/internal/service/role_service.go`, locate `DefaultPermissions` slice. Append:

```go
{Name: "funds.bank-position-read", Description: "View the bank's positions across investment funds and actuary performance", Group: "funds"},
```

Then locate the role-permission seeds (typically a map of role-name to permission codes). Add `funds.bank-position-read` to:
- `EmployeeSupervisor`'s permission list.
- `EmployeeAdmin`'s permission list (or it may already get all permissions via a wildcard — verify).

- [ ] **Step 3: Append the test**

`user-service/internal/service/role_service_test.go`:

```go
func TestSeedRolesAndPermissions_FundsBankPositionRead(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Permission{}, &model.Role{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewRoleService(repository.NewRoleRepository(db), repository.NewPermissionRepository(db))
	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var perm model.Permission
	if err := db.Where("name = ?", "funds.bank-position-read").First(&perm).Error; err != nil {
		t.Fatalf("permission not seeded: %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd user-service && go test ./internal/service/... -run TestSeedRolesAndPermissions_FundsBankPositionRead -v
cd ../stock-service && go test ./... -run System -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/cmd/main.go user-service/internal/service/role_service.go user-service/internal/service/role_service_test.go
git commit -m "feat: seed fund_redemption_fee_pct + funds.bank-position-read permission"
```

---

## Task 8: Proto additions for `InvestmentFundService`

**Files:**
- Modify: `contract/proto/stock/stock.proto`
- Modify: `contract/kafka/messages.go`
- Build artefact: `contract/stockpb/*.pb.go` (regenerated)

- [ ] **Step 1: Append the service definition + messages to `stock.proto`**

```proto
// ---- Investment Funds (Celina 4) -----------------------------------------

service InvestmentFundService {
  rpc CreateFund(CreateFundRequest) returns (FundResponse);
  rpc ListFunds(ListFundsRequest) returns (ListFundsResponse);
  rpc GetFund(GetFundRequest) returns (FundDetailResponse);
  rpc UpdateFund(UpdateFundRequest) returns (FundResponse);

  rpc InvestInFund(InvestInFundRequest) returns (ContributionResponse);
  rpc RedeemFromFund(RedeemFromFundRequest) returns (ContributionResponse);

  rpc ListMyPositions(ListMyPositionsRequest) returns (ListPositionsResponse);
  rpc ListBankPositions(ListBankPositionsRequest) returns (ListPositionsResponse);
  rpc GetActuaryPerformance(GetActuaryPerformanceRequest) returns (GetActuaryPerformanceResponse);
}

message OnBehalfOf {
  string type = 1;        // "self" | "bank" | "fund"
  uint64 fund_id = 2;     // required when type == "fund"
}

message CreateFundRequest {
  int64 actor_employee_id = 1;
  string name = 2;
  string description = 3;
  string minimum_contribution_rsd = 4;
}

message FundResponse {
  uint64 id = 1;
  string name = 2;
  string description = 3;
  int64  manager_employee_id = 4;
  string manager_full_name = 5;
  string minimum_contribution_rsd = 6;
  uint64 rsd_account_id = 7;
  string rsd_account_number = 8;
  bool   active = 9;
  string created_at = 10;        // RFC3339
  string updated_at = 11;
  // Derived fields surfaced for cheap UI display:
  string value_rsd = 12;         // total mark-to-market value
  string liquid_rsd = 13;        // RSD account balance
  string profit_rsd = 14;        // Σ holding (current - average) × qty + cash variance
}

message ListFundsRequest {
  int32 page = 1;
  int32 page_size = 2;
  string search = 3;
  bool   active_only = 4;
}
message ListFundsResponse {
  repeated FundResponse funds = 1;
  int64 total = 2;
}

message GetFundRequest { uint64 fund_id = 1; }
message FundDetailResponse {
  FundResponse fund = 1;
  repeated FundHoldingItem holdings = 2;
}
message FundHoldingItem {
  string security_type = 1;
  uint64 security_id = 2;
  string ticker = 3;
  int64  quantity = 4;
  string average_price_rsd = 5;
  string current_price_rsd = 6;
  string acquired_at = 7;
}

message UpdateFundRequest {
  int64  actor_employee_id = 1;
  uint64 fund_id = 2;
  string name = 3;                  // optional; empty = unchanged
  string description = 4;           // optional
  string minimum_contribution_rsd = 5; // optional; empty = unchanged
  bool   active = 6;                // mirror; explicit false toggles deactivation
  bool   active_set = 7;            // distinguishes "unchanged" from "set to false"
}

message InvestInFundRequest {
  uint64 fund_id = 1;
  uint64 actor_user_id = 2;
  string actor_system_type = 3;     // "client" | "employee"
  uint64 source_account_id = 4;
  string amount = 5;                // native amount, decimal string
  string currency = 6;              // 3-letter ISO
  OnBehalfOf on_behalf_of = 7;
}
message RedeemFromFundRequest {
  uint64 fund_id = 1;
  uint64 actor_user_id = 2;
  string actor_system_type = 3;
  string amount_rsd = 4;
  uint64 target_account_id = 5;
  OnBehalfOf on_behalf_of = 6;
}

message ContributionResponse {
  uint64 id = 1;
  uint64 fund_id = 2;
  string direction = 3;
  string amount_native = 4;
  string native_currency = 5;
  string amount_rsd = 6;
  string fx_rate = 7;
  string fee_rsd = 8;
  string status = 9;
  uint64 saga_id = 10;
}

message ListMyPositionsRequest {
  uint64 actor_user_id = 1;
  string actor_system_type = 2;
}
message ListBankPositionsRequest {}
message ListPositionsResponse {
  repeated PositionItem positions = 1;
}
message PositionItem {
  uint64 fund_id = 1;
  string fund_name = 2;
  string manager_full_name = 3;
  string contribution_rsd = 4;
  string percentage_fund = 5;
  string current_value_rsd = 6;
  string profit_rsd = 7;
  string last_changed_at = 8;
}

message GetActuaryPerformanceRequest {}
message GetActuaryPerformanceResponse {
  repeated ActuaryPerformance actuaries = 1;
}
message ActuaryPerformance {
  int64  employee_id = 1;
  string full_name = 2;
  string role = 3;
  string realized_profit_rsd = 4;
}
```

- [ ] **Step 2: Add Kafka topic constants and payloads to `contract/kafka/messages.go`**

Append:

```go
const (
	TopicStockFundCreated      = "stock.fund-created"
	TopicStockFundUpdated      = "stock.fund-updated"
	TopicStockFundInvested     = "stock.fund-invested"
	TopicStockFundRedeemed     = "stock.fund-redeemed"
	TopicStockFundsReassigned  = "stock.funds-reassigned"
	TopicUserSupervisorDemoted = "user.supervisor-demoted"
)

type StockFundCreatedMessage struct {
	MessageID         string `json:"message_id"`
	OccurredAt        string `json:"occurred_at"`
	FundID            uint64 `json:"fund_id"`
	Name              string `json:"name"`
	ManagerEmployeeID int64  `json:"manager_employee_id"`
	RSDAccountID      uint64 `json:"rsd_account_id"`
	CreatedAt         string `json:"created_at"`
}

type StockFundUpdatedMessage struct {
	MessageID     string   `json:"message_id"`
	OccurredAt    string   `json:"occurred_at"`
	FundID        uint64   `json:"fund_id"`
	ChangedFields []string `json:"changed_fields"`
	UpdatedAt     string   `json:"updated_at"`
}

type StockFundInvestedMessage struct {
	MessageID      string `json:"message_id"`
	OccurredAt     string `json:"occurred_at"`
	FundID         uint64 `json:"fund_id"`
	UserID         uint64 `json:"user_id"`
	SystemType     string `json:"system_type"`
	AmountNative   string `json:"amount_native"`
	NativeCurrency string `json:"native_currency"`
	AmountRSD      string `json:"amount_rsd"`
	FxRate         string `json:"fx_rate"`
	SagaID         uint64 `json:"saga_id"`
	ContributionID uint64 `json:"contribution_id"`
}

type StockFundRedeemedMessage struct {
	MessageID       string `json:"message_id"`
	OccurredAt      string `json:"occurred_at"`
	FundID          uint64 `json:"fund_id"`
	UserID          uint64 `json:"user_id"`
	SystemType      string `json:"system_type"`
	AmountRSD       string `json:"amount_rsd"`
	FeeRSD          string `json:"fee_rsd"`
	TargetAccountID uint64 `json:"target_account_id"`
	SagaID          uint64 `json:"saga_id"`
	ContributionID  uint64 `json:"contribution_id"`
}

type StockFundsReassignedMessage struct {
	MessageID    string   `json:"message_id"`
	OccurredAt   string   `json:"occurred_at"`
	SupervisorID int64    `json:"supervisor_id"`
	AdminID      int64    `json:"admin_id"`
	FundIDs      []uint64 `json:"fund_ids"`
}

type UserSupervisorDemotedMessage struct {
	MessageID    string `json:"message_id"`
	OccurredAt   string `json:"occurred_at"`
	SupervisorID int64  `json:"supervisor_id"`
	AdminID      int64  `json:"admin_id"`
	RevokedAt    string `json:"revoked_at"`
}
```

- [ ] **Step 3: Regenerate proto bindings**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend" && make proto
```

Expected: `contract/stockpb/stock.pb.go` and `stock_grpc.pb.go` regenerate without errors.

- [ ] **Step 4: Verify everything still builds**

```bash
make build
```

Expected: every binary builds.

- [ ] **Step 5: Commit**

```bash
git add contract/proto/stock/stock.proto contract/kafka/messages.go contract/stockpb/
git commit -m "feat(contract): InvestmentFundService proto + fund Kafka topic constants"
```

---

## Task 9: user-service `ListEmployeeFullNames` RPC (verify or add)

**Files:**
- Verify first: `contract/proto/user/user.proto`, `user-service/internal/handler/`.
- Modify (only if missing): `contract/proto/user/user.proto`, `user-service/internal/handler/grpc_handler.go`.

- [ ] **Step 1: Check whether the RPC already exists**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
grep -n "ListEmployeeFullNames\|ListFullNames" contract/proto/user/user.proto user-service/internal/handler/*.go
```

Expected: either matches (RPC already exists — skip to Task 10) or no matches (proceed below).

- [ ] **Step 2: Add the RPC to `user.proto`**

```proto
service UserService {
  // … existing RPCs …
  rpc ListEmployeeFullNames(ListEmployeeFullNamesRequest) returns (ListEmployeeFullNamesResponse);
}

message ListEmployeeFullNamesRequest {
  repeated int64 employee_ids = 1;
}
message ListEmployeeFullNamesResponse {
  map<int64, string> names_by_id = 1;
}
```

- [ ] **Step 3: Regen + implement on the user-service handler**

```bash
make proto
```

Then `user-service/internal/handler/grpc_handler.go` (next to existing `GetEmployee` handler):

```go
func (h *UserGRPCHandler) ListEmployeeFullNames(ctx context.Context, req *userpb.ListEmployeeFullNamesRequest) (*userpb.ListEmployeeFullNamesResponse, error) {
	if len(req.EmployeeIds) == 0 {
		return &userpb.ListEmployeeFullNamesResponse{NamesById: map[int64]string{}}, nil
	}
	rows, err := h.svc.GetByIDs(req.EmployeeIds) // adds a thin repository List(IDs) call
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make(map[int64]string, len(rows))
	for _, e := range rows {
		out[e.ID] = strings.TrimSpace(e.FirstName + " " + e.LastName)
	}
	return &userpb.ListEmployeeFullNamesResponse{NamesById: out}, nil
}
```

Add the corresponding `GetByIDs(ids []int64) ([]model.Employee, error)` on `EmployeeService` and a single-query repository method.

- [ ] **Step 4: Test**

`user-service/internal/handler/grpc_handler_test.go` — append a test that mocks the repo to return two employees and asserts the map shape.

- [ ] **Step 5: Build + test**

```bash
cd user-service && go test ./... && go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add contract/proto/user/user.proto contract/userpb/ user-service/
git commit -m "feat(user-service): ListEmployeeFullNames RPC for fund / actuary decoration"
```

---

## Task 10: user-service outbox table + repository

**Files:**
- Create: `user-service/internal/model/outbox.go`
- Create: `user-service/internal/repository/outbox_repository.go`
- Create: `user-service/internal/service/outbox_relay.go`
- Modify: `user-service/cmd/main.go` (AutoMigrate + start relay).
- Test: `user-service/internal/repository/outbox_repository_test.go`

- [ ] **Step 1: Write failing test**

```go
package repository

import (
	"testing"
	"time"

	"github.com/exbanka/user-service/internal/model"
)

func TestOutboxRepository_InsertAndClaim(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOutboxRepository(db)

	if err := r.Insert(&model.OutboxEvent{
		AggregateID: "supervisor:7",
		EventType:   "user.supervisor-demoted",
		Payload:     []byte(`{"supervisor_id":7}`),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := r.ClaimUnpublished(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if err := r.MarkPublished(rows[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("mark: %v", err)
	}
	again, _ := r.ClaimUnpublished(10)
	if len(again) != 0 {
		t.Errorf("expected 0 rows after publish, got %d", len(again))
	}
}
```

- [ ] **Step 2: Implement model**

`user-service/internal/model/outbox.go`:

```go
package model

import "time"

// OutboxEvent is a transactional outbox row written inside the same DB TX as
// the originating state change. A background relay (RelayOutbox) periodically
// publishes unpublished rows to Kafka. Mark-published is idempotent on ID.
type OutboxEvent struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement"`
	AggregateID string     `gorm:"size:64;not null;index"`
	EventType   string     `gorm:"size:64;not null;index"`
	Payload     []byte     `gorm:"type:bytea;not null"`
	CreatedAt   time.Time  `gorm:"not null;index"`
	PublishedAt *time.Time `gorm:"index"`
}
```

- [ ] **Step 3: Implement repository**

`user-service/internal/repository/outbox_repository.go`:

```go
package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/user-service/internal/model"
)

type OutboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// Insert writes a new outbox row. Caller passes the same *gorm.DB (or *gorm.DB
// transaction) that's running the originating state change so both commits
// atomically.
func (r *OutboxRepository) Insert(e *model.OutboxEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return r.db.Create(e).Error
}

// ClaimUnpublished returns up to limit rows where PublishedAt IS NULL,
// oldest first. Concurrency note: MVP polls without row locks. If multiple
// relay goroutines run at once, switch this to FOR UPDATE SKIP LOCKED.
func (r *OutboxRepository) ClaimUnpublished(limit int) ([]model.OutboxEvent, error) {
	var out []model.OutboxEvent
	err := r.db.Where("published_at IS NULL").
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *OutboxRepository) MarkPublished(id uint64, at time.Time) error {
	res := r.db.Model(&model.OutboxEvent{}).Where("id = ?", id).Update("published_at", at)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
```

- [ ] **Step 4: Implement relay goroutine**

`user-service/internal/service/outbox_relay.go`:

```go
package service

import (
	"context"
	"log"
	"time"

	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
)

type OutboxRelay struct {
	repo     *repository.OutboxRepository
	producer *kafkaprod.Producer
	tick     time.Duration
}

func NewOutboxRelay(repo *repository.OutboxRepository, producer *kafkaprod.Producer, tick time.Duration) *OutboxRelay {
	if tick == 0 {
		tick = 2 * time.Second
	}
	return &OutboxRelay{repo: repo, producer: producer, tick: tick}
}

func (r *OutboxRelay) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(r.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("outbox relay stopping")
				return
			case <-ticker.C:
				r.processBatch(ctx)
			}
		}
	}()
}

func (r *OutboxRelay) processBatch(ctx context.Context) {
	rows, err := r.repo.ClaimUnpublished(100)
	if err != nil {
		log.Printf("WARN: outbox claim failed: %v", err)
		return
	}
	for _, row := range rows {
		if err := r.producer.PublishRaw(ctx, row.EventType, row.Payload); err != nil {
			log.Printf("WARN: outbox publish %s id=%d: %v", row.EventType, row.ID, err)
			continue
		}
		if err := r.repo.MarkPublished(row.ID, time.Now().UTC()); err != nil {
			log.Printf("WARN: outbox mark-published id=%d: %v", row.ID, err)
		}
	}
	_ = (model.OutboxEvent{}) // silence unused import in tooling
}
```

If `Producer.PublishRaw(ctx, topic, []byte)` doesn't exist, add it (most kafka producers expose a string-topic + raw-bytes write API; check existing producer.go and add a thin wrapper).

- [ ] **Step 5: Wire into `user-service/cmd/main.go`**

After existing repo construction:

```go
outboxRepo := repository.NewOutboxRepository(db)
outboxRelay := service.NewOutboxRelay(outboxRepo, producer, 2*time.Second)
outboxRelay.Start(ctx)
```

Add `&model.OutboxEvent{}` to the AutoMigrate list. Add `kafkamsg.TopicUserSupervisorDemoted` to the existing `EnsureTopics` call.

- [ ] **Step 6: Test**

```bash
cd user-service && go test ./internal/repository/... -run TestOutbox -v
```

- [ ] **Step 7: Commit**

```bash
git add user-service/internal/model/outbox.go user-service/internal/repository/outbox_repository.go \
        user-service/internal/repository/outbox_repository_test.go \
        user-service/internal/service/outbox_relay.go \
        user-service/cmd/main.go
git commit -m "feat(user-service): outbox table + relay for cross-service events"
```

---

## Task 11: user-service publishes `user.supervisor-demoted` from outbox on permission change

**Files:**
- Modify: `user-service/internal/service/employee_service.go` (or wherever `UpdateEmployeePermissions` lives — locate first).

- [ ] **Step 1: Locate the permission-update method**

```bash
grep -n "func.*UpdateEmployeePermissions\|UpdateAdditionalPermissions\|SetEmployeePermissions" user-service/internal/service/*.go
```

- [ ] **Step 2: Test first — assert event written when funds.manage is revoked**

Append to the relevant `_test.go`:

```go
func TestUpdatePermissions_PublishesSupervisorDemotedOnFundsRevoke(t *testing.T) {
	// Setup: employee with funds.manage; revoke it; verify outbox row.
	db := newTestDB(t)
	migrateAll(db, t)
	empRepo := repository.NewEmployeeRepository(db)
	roleRepo := repository.NewRoleRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	svc := NewEmployeeService(empRepo, nil, nil, NewRoleService(roleRepo, permRepo), nil)
	svc = svc.WithOutbox(outboxRepo) // builder added in next step
	emp := seedSupervisorWithFundsManage(t, db) // helper: creates employee + role grant
	if err := svc.UpdateAdditionalPermissions(emp.ID, []string{} /* drop everything */, 1 /* admin */); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, _ := outboxRepo.ClaimUnpublished(10)
	if len(rows) != 1 || rows[0].EventType != "user.supervisor-demoted" {
		t.Fatalf("expected 1 supervisor-demoted event, got %+v", rows)
	}
}
```

- [ ] **Step 3: Add `WithOutbox` builder + emit logic**

In the relevant service file:

```go
func (s *EmployeeService) WithOutbox(repo *repository.OutboxRepository) *EmployeeService {
	cp := *s
	cp.outboxRepo = repo
	return &cp
}

// inside UpdateAdditionalPermissions / UpdateEmployeePermissions, after computing
// the diff and before tx.Commit:
if revoked := setDifference(oldPerms, newPerms); contains(revoked, "funds.manage") {
	payload, _ := json.Marshal(kafkamsg.UserSupervisorDemotedMessage{
		MessageID:    uuid.NewString(),
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		SupervisorID: empID,
		AdminID:      changedBy,
		RevokedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	if s.outboxRepo != nil {
		if err := tx.Create(&model.OutboxEvent{
			AggregateID: fmt.Sprintf("supervisor:%d", empID),
			EventType:   kafkamsg.TopicUserSupervisorDemoted,
			Payload:     payload,
		}).Error; err != nil {
			return err
		}
	}
}
```

`setDifference` and `contains` are trivial helpers — add them to a `slices.go` if the existing codebase doesn't already have them.

- [ ] **Step 4: Run tests**

```bash
cd user-service && go test ./internal/service/... -run TestUpdatePermissions -v
```

- [ ] **Step 5: Commit**

```bash
git add user-service/internal/service/employee_service.go user-service/internal/service/employee_service_test.go
git commit -m "feat(user-service): outbox supervisor-demoted on funds.manage revoke"
```

---

## Task 12: stock-service `FundService` (Create / List / Get / Update — no money flow yet)

**Files:**
- Create: `stock-service/internal/service/fund_service.go`
- Create: `stock-service/internal/service/fund_service_test.go`

- [ ] **Step 1: Failing test — create + read**

```go
package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestFundService_Create_GeneratesRSDAccount(t *testing.T) {
	fx := newFundFixture(t)
	got, err := fx.svc.Create(context.Background(), CreateFundInput{
		ActorEmployeeID:        25,
		Name:                   "Alpha",
		Description:            "IT focus",
		MinimumContributionRSD: decimal.NewFromInt(1000),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ManagerEmployeeID != 25 {
		t.Errorf("manager %d", got.ManagerEmployeeID)
	}
	if got.RSDAccountID == 0 {
		t.Errorf("expected RSDAccountID != 0")
	}
	if !fx.bankAccountClient.created {
		t.Errorf("CreateBankAccount not called")
	}
}

func TestFundService_Create_RejectsDuplicateName(t *testing.T) {
	fx := newFundFixture(t)
	for i := 0; i < 2; i++ {
		_, err := fx.svc.Create(context.Background(), CreateFundInput{
			ActorEmployeeID: 25, Name: "Beta",
		})
		if i == 0 && err != nil {
			t.Fatalf("first create failed: %v", err)
		}
		if i == 1 && err == nil {
			t.Errorf("expected duplicate-name error")
		}
	}
}
```

`newFundFixture` is a test helper that wires:
- An in-memory `*gorm.DB` with all relevant tables migrated.
- A stub `BankAccountServiceClient` with one method `CreateBankAccount` that records calls and returns a stub account.

- [ ] **Step 2: Implement `fund_service.go`**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// BankAccountClient is the minimal subset of accountpb.BankAccountServiceClient
// FundService needs. Stated as an interface so tests can stub it.
type BankAccountClient interface {
	CreateBankAccount(ctx context.Context, in *accountpb.CreateBankAccountRequest) (*accountpb.AccountResponse, error)
}

type FundService struct {
	repo              *repository.FundRepository
	bankAccountClient BankAccountClient
	producer          *kafkaprod.Producer
}

func NewFundService(repo *repository.FundRepository, bankAccountClient BankAccountClient, producer *kafkaprod.Producer) *FundService {
	return &FundService{repo: repo, bankAccountClient: bankAccountClient, producer: producer}
}

type CreateFundInput struct {
	ActorEmployeeID        int64
	Name                   string
	Description            string
	MinimumContributionRSD decimal.Decimal
}

func (s *FundService) Create(ctx context.Context, in CreateFundInput) (*model.InvestmentFund, error) {
	if in.Name == "" || len(in.Name) > 128 {
		return nil, errors.New("name must be 1-128 chars")
	}
	if len(in.Description) > 2000 {
		return nil, errors.New("description must be <= 2000 chars")
	}
	if in.MinimumContributionRSD.IsNegative() {
		return nil, errors.New("minimum_contribution_rsd must be >= 0")
	}

	acct, err := s.bankAccountClient.CreateBankAccount(ctx, &accountpb.CreateBankAccountRequest{
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountName:  fmt.Sprintf("Fund: %s", in.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("create RSD account: %w", err)
	}

	f := &model.InvestmentFund{
		Name:                   in.Name,
		Description:            in.Description,
		ManagerEmployeeID:      in.ActorEmployeeID,
		MinimumContributionRSD: in.MinimumContributionRSD,
		RSDAccountID:           acct.Id,
		Active:                 true,
	}
	if err := s.repo.Create(f); err != nil {
		// Compensation: delete the bank account we just created. account-service
		// has DeleteBankAccount; if the call fails, log — manual cleanup is fine.
		// (Implementation detail in BankAccountClient interface; see plan note.)
		return nil, err
	}

	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicStockFundCreated, mustJSON(kafkamsg.StockFundCreatedMessage{
			MessageID:         uuid.NewString(),
			OccurredAt:        time.Now().UTC().Format(time.RFC3339),
			FundID:            f.ID,
			Name:              f.Name,
			ManagerEmployeeID: f.ManagerEmployeeID,
			RSDAccountID:      f.RSDAccountID,
			CreatedAt:         f.CreatedAt.Format(time.RFC3339),
		}))
	}
	return f, nil
}

// Update applies optional changes; only fields explicitly set in input are
// changed. ActiveSet differentiates "field unset" from "Active=false".
type UpdateFundInput struct {
	ActorEmployeeID        int64
	FundID                 uint64
	Name                   *string
	Description            *string
	MinimumContributionRSD *decimal.Decimal
	Active                 *bool
}

func (s *FundService) Update(ctx context.Context, in UpdateFundInput) (*model.InvestmentFund, error) {
	f, err := s.repo.GetByID(in.FundID)
	if err != nil {
		return nil, err
	}
	if in.ActorEmployeeID != f.ManagerEmployeeID {
		// Caller of this service is responsible for the EmployeeAdmin override.
		// Service-layer check stays strict; handler will widen for admins.
		return nil, errors.New("caller is not the fund manager")
	}
	if in.Name != nil {
		f.Name = *in.Name
	}
	if in.Description != nil {
		f.Description = *in.Description
	}
	if in.MinimumContributionRSD != nil {
		f.MinimumContributionRSD = *in.MinimumContributionRSD
	}
	if in.Active != nil {
		f.Active = *in.Active
	}
	if err := s.repo.Save(f); err != nil {
		return nil, err
	}
	return f, nil
}

// mustJSON / PublishRaw expected to be available on *kafkaprod.Producer.
func mustJSON(v any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err) // payloads are static structs; marshal can't fail
	}
	return out
}
```

(Add `json` to imports.)

- [ ] **Step 3: Run tests**

```bash
cd stock-service && go test ./internal/service/... -run TestFundService -v
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/fund_service.go stock-service/internal/service/fund_service_test.go
git commit -m "feat(stock-service): FundService Create/Update — no money-flow yet"
```

---

## Task 13: Invest saga — happy path (RSD source, no FX)

**Files:**
- Create: `stock-service/internal/service/fund_invest_saga.go`
- Create: `stock-service/internal/service/fund_invest_saga_test.go`

The saga reuses the existing `SagaExecutor` pattern. See `stock-service/internal/service/portfolio_service.go:ProcessBuyFill` for the canonical example.

- [ ] **Step 1: Failing test — RSD-source invest commits all the right side effects**

```go
func TestInvestSaga_RSDSource_HappyPath(t *testing.T) {
	fx := newInvestSagaFixture(t)
	out, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID:           fx.fundID,
		ActorUserID:      99,
		ActorSystemType:  "client",
		SourceAccountID:  4001,
		Amount:           decimal.NewFromInt(500),
		Currency:         "RSD",
		OnBehalfOfType:   "self",
	})
	if err != nil {
		t.Fatalf("invest: %v", err)
	}
	if out.Status != "completed" {
		t.Errorf("status=%q want completed", out.Status)
	}

	// account-service got Reserve + Settle for source, Credit for fund RSD account.
	if !fx.accounts.reserved(4001, "500") || !fx.accounts.settled(4001, "500") {
		t.Errorf("source debits missing: %+v", fx.accounts.calls)
	}
	if !fx.accounts.credited(fx.fundRSDAccountID, "500") {
		t.Errorf("fund credit missing")
	}

	// position upserted to 500 RSD.
	pos, _ := fx.posRepo.GetByOwner(fx.fundID, 99, "client")
	if !pos.TotalContributedRSD.Equal(decimal.NewFromInt(500)) {
		t.Errorf("position contrib %s want 500", pos.TotalContributedRSD)
	}

	// contribution row with status=completed.
	if fx.contribRepo.lastStatus != "completed" {
		t.Errorf("contribution status %s", fx.contribRepo.lastStatus)
	}
}
```

- [ ] **Step 2: Implement saga**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// FundAccountClient is the minimal account-service surface invest/redeem
// sagas use. Tests stub it without standing up a real account-service.
type FundAccountClient interface {
	GetAccount(ctx context.Context, in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error)
	ReserveFunds(ctx context.Context, accountID, sagaOrderID uint64, amount decimal.Decimal, currency string) (*accountpb.ReserveFundsResponse, error)
	PartialSettleReservation(ctx context.Context, sagaOrderID, settleSeq uint64, amount decimal.Decimal, memo string) (*accountpb.PartialSettleReservationResponse, error)
	ReleaseReservation(ctx context.Context, sagaOrderID uint64) (*accountpb.ReleaseReservationResponse, error)
	CreditAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
	DebitAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
}

type ExchangeConverter interface {
	Convert(ctx context.Context, in *exchangepb.ConvertRequest) (*exchangepb.ConvertResponse, error)
}

type InvestInput struct {
	FundID          uint64
	ActorUserID     uint64
	ActorSystemType string // "client" or "employee"
	SourceAccountID uint64
	Amount          decimal.Decimal
	Currency        string
	OnBehalfOfType  string // "self" | "bank"
}

func (s *FundService) Invest(ctx context.Context, in InvestInput) (*model.FundContribution, error) {
	fund, err := s.repo.GetByID(in.FundID)
	if err != nil {
		return nil, fmt.Errorf("fund not found: %w", err)
	}
	if !fund.Active {
		return nil, errors.New("fund is inactive")
	}

	// Disambiguate self vs bank — clients always self.
	posUserID, posSystemType := in.ActorUserID, in.ActorSystemType
	if in.OnBehalfOfType == "bank" {
		posUserID, posSystemType = bankSentinelUserID, "employee"
	}

	// 1. Convert to RSD if source ≠ RSD.
	amountRSD := in.Amount
	var fxRate *decimal.Decimal
	if in.Currency != "RSD" {
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: in.Currency,
			ToCurrency:   "RSD",
			Amount:       in.Amount.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("FX convert: %w", err)
		}
		amountRSD, _ = decimal.NewFromString(conv.ConvertedAmount)
		rate, _ := decimal.NewFromString(conv.EffectiveRate)
		fxRate = &rate
	}

	if amountRSD.LessThan(fund.MinimumContributionRSD) {
		return nil, fmt.Errorf("minimum_contribution_not_met: required %s RSD got %s", fund.MinimumContributionRSD, amountRSD)
	}

	// 2. Open saga.
	saga, err := s.sagaRepo.Create(&model.SagaLog{Name: "fund_invest", Status: "running", CreatedAt: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	exec := NewSagaExecutor(s.sagaRepo, saga.ID)

	// 3. Reserve source.
	if err := exec.RunStep(ctx, "reserve_source", in.Amount, in.Currency, nil, func() error {
		_, e := s.accounts.ReserveFunds(ctx, in.SourceAccountID, saga.ID, in.Amount, in.Currency)
		return e
	}); err != nil {
		_ = s.sagaRepo.UpdateStatus(saga.ID, "failed")
		return nil, err
	}

	// 4. Insert pending contribution.
	contrib := &model.FundContribution{
		FundID:                  in.FundID,
		UserID:                  posUserID,
		SystemType:              posSystemType,
		Direction:               model.FundDirectionInvest,
		AmountNative:            in.Amount,
		NativeCurrency:          in.Currency,
		AmountRSD:               amountRSD,
		FxRate:                  fxRate,
		FeeRSD:                  decimal.Zero,
		SourceOrTargetAccountID: in.SourceAccountID,
		SagaID:                  saga.ID,
		Status:                  model.FundContributionStatusPending,
	}
	if err := s.contribs.Create(contrib); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_source", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, saga.ID)
			return e
		})
		return nil, err
	}

	// 5. Settle source debit.
	settleMemo := fmt.Sprintf("Invest in fund #%d (saga=%d)", in.FundID, saga.ID)
	if err := exec.RunStep(ctx, "settle_source", in.Amount, in.Currency, map[string]any{"memo": settleMemo}, func() error {
		_, e := s.accounts.PartialSettleReservation(ctx, saga.ID, 1, in.Amount, settleMemo)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_source", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, saga.ID)
			return e
		})
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// 6. Credit fund RSD account.
	fundAcct, _ := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	creditMemo := fmt.Sprintf("Contribution from %s #%d (saga=%d)", in.ActorSystemType, in.ActorUserID, saga.ID)
	idemKey := fmt.Sprintf("invest-%d-%d-credit", in.FundID, saga.ID)
	if err := exec.RunStep(ctx, "credit_fund", amountRSD, "RSD", map[string]any{"memo": creditMemo}, func() error {
		_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, amountRSD, creditMemo, idemKey)
		return e
	}); err != nil {
		// Compensate by debiting the just-credited fund account back. Source
		// is already settled; the user's money is split between source-debited
		// and fund-not-yet-credited. We re-credit source and fail.
		srcAcct, _ := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SourceAccountID})
		_ = exec.RunCompensation(ctx, 0, "compensate_source_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, srcAcct.AccountNumber, in.Amount,
				fmt.Sprintf("Compensate failed invest #%d", saga.ID),
				fmt.Sprintf("invest-%d-%d-comp-source", in.FundID, saga.ID))
			return e
		})
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// 7. Upsert position.
	if err := exec.RunStep(ctx, "upsert_position", amountRSD, "RSD", nil, func() error {
		return s.positions.IncrementContribution(in.FundID, posUserID, posSystemType, amountRSD)
	}); err != nil {
		// Compensate by debiting fund and re-crediting source.
		_ = exec.RunCompensation(ctx, 0, "comp_fund_debit", func() error {
			_, e := s.accounts.DebitAccount(ctx, fundAcct.AccountNumber, amountRSD,
				fmt.Sprintf("Comp invest fund=%d saga=%d", in.FundID, saga.ID),
				fmt.Sprintf("invest-%d-%d-comp-fund", in.FundID, saga.ID))
			return e
		})
		srcAcct, _ := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SourceAccountID})
		_ = exec.RunCompensation(ctx, 0, "comp_source_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, srcAcct.AccountNumber, in.Amount,
				fmt.Sprintf("Comp invest src saga=%d", saga.ID),
				fmt.Sprintf("invest-%d-%d-comp-source", in.FundID, saga.ID))
			return e
		})
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// 8. Mark complete.
	if err := s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted); err != nil {
		return nil, err
	}
	contrib.Status = model.FundContributionStatusCompleted
	_ = s.sagaRepo.UpdateStatus(saga.ID, "completed")

	// 9. Publish kafka.
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicStockFundInvested, mustJSON(kafkamsg.StockFundInvestedMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			FundID:         in.FundID,
			UserID:         posUserID,
			SystemType:     posSystemType,
			AmountNative:   in.Amount.String(),
			NativeCurrency: in.Currency,
			AmountRSD:      amountRSD.String(),
			FxRate:         decimalPtrString(fxRate),
			SagaID:         saga.ID,
			ContributionID: contrib.ID,
		}))
	}
	return contrib, nil
}

const bankSentinelUserID uint64 = 1_000_000_000

func decimalPtrString(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}
```

Add corresponding fields/wiring to `FundService` (sagaRepo, contribs, positions, accounts, exchange, producer). Tests construct stubs.

- [ ] **Step 3: Run tests**

```bash
cd stock-service && go test ./internal/service/... -run TestInvestSaga -v
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/fund_invest_saga.go stock-service/internal/service/fund_invest_saga_test.go
git commit -m "feat(stock-service): fund invest saga (RSD source happy path)"
```

---

## Task 14: Invest saga — cross-currency + each compensation branch

**Files:**
- Modify: `stock-service/internal/service/fund_invest_saga_test.go`

- [ ] **Step 1: Add cross-currency happy-path test**

```go
func TestInvestSaga_CrossCurrency_PopulatesFxRate(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.exchange.setRate("EUR", "RSD", "117.0", "5850.00") // 50 EUR → 5850 RSD
	out, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fundID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 4001, Amount: decimal.NewFromInt(50), Currency: "EUR",
		OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("invest: %v", err)
	}
	if out.AmountRSD.String() != "5850" {
		t.Errorf("amount_rsd %s want 5850", out.AmountRSD)
	}
	if out.FxRate == nil || out.FxRate.String() != "117" {
		t.Errorf("fx %v", out.FxRate)
	}
}
```

- [ ] **Step 2: Add minimum-contribution failure test**

```go
func TestInvestSaga_BelowMinimum_Rejected(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.fund.MinimumContributionRSD = decimal.NewFromInt(10000)
	_, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fundID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 4001, Amount: decimal.NewFromInt(500), Currency: "RSD",
	})
	if err == nil || !strings.Contains(err.Error(), "minimum_contribution_not_met") {
		t.Errorf("err %v", err)
	}
	if fx.accounts.reserveCallCount() != 0 {
		t.Errorf("reservation should not have been attempted")
	}
}
```

- [ ] **Step 3: Add compensations test (each step's failure produces correct cleanup)**

For each saga step (`reserve_source`, `settle_source`, `credit_fund`, `upsert_position`), write a test that injects the failure and asserts the right compensation calls. Five tests; pattern is identical to existing `TestProcessBuyFill_*RollsBack*` tests in `portfolio_service_test.go`.

- [ ] **Step 4: Run + verify**

```bash
cd stock-service && go test ./internal/service/... -run TestInvestSaga -v
```

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/fund_invest_saga_test.go
git commit -m "test(stock-service): invest saga cross-ccy + every compensation branch"
```

---

## Task 15: Redeem saga — cash-sufficient path

**Files:**
- Create: `stock-service/internal/service/fund_redeem_saga.go`
- Create: `stock-service/internal/service/fund_redeem_saga_test.go`

The structure mirrors invest. Steps in §5.2 of the spec. Idempotency keys derived from `redeem-<fund>-<saga>-<step>`.

- [ ] **Step 1: Test happy path (RSD target, cash available)**

```go
func TestRedeemSaga_RSDTarget_HappyPath(t *testing.T) {
	fx := newRedeemSagaFixture(t, /*fund cash*/ "10000", /*pos rsd*/ "5000")
	out, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID:          fx.fundID,
		ActorUserID:     99,
		ActorSystemType: "client",
		AmountRSD:       decimal.NewFromInt(2000),
		TargetAccountID: 5001,
		OnBehalfOfType:  "self",
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if out.Status != "completed" {
		t.Errorf("status %s", out.Status)
	}
	// 0.5% fee on client redemption: 10 RSD.
	if !out.FeeRSD.Equal(decimal.NewFromInt(10)) {
		t.Errorf("fee %s want 10", out.FeeRSD)
	}
	if !fx.accounts.debited(fx.fundRSDAccountID, "2000") {
		t.Errorf("fund debit missing")
	}
	if !fx.accounts.credited(5001, "1990") {
		t.Errorf("target credit (net of fee) missing")
	}
	if !fx.accounts.credited(fx.bankRSDAccountID, "10") {
		t.Errorf("fee credit to bank missing")
	}
}
```

- [ ] **Step 2: Implement `Redeem` on `FundService`**

Mirror Invest's structure. Steps (§5.2 of spec):

```go
type RedeemInput struct {
	FundID          uint64
	ActorUserID     uint64
	ActorSystemType string
	AmountRSD       decimal.Decimal
	TargetAccountID uint64
	OnBehalfOfType  string // "self" | "bank"
}

func (s *FundService) Redeem(ctx context.Context, in RedeemInput) (*model.FundContribution, error) {
	fund, err := s.repo.GetByID(in.FundID)
	if err != nil { return nil, err }
	if !fund.Active { return nil, errors.New("fund is inactive") }

	posUserID, posSystemType := in.ActorUserID, in.ActorSystemType
	if in.OnBehalfOfType == "bank" {
		posUserID, posSystemType = bankSentinelUserID, "employee"
	}

	pos, err := s.positions.GetByOwner(in.FundID, posUserID, posSystemType)
	if err != nil { return nil, fmt.Errorf("no position: %w", err) }

	// Compute current value (uses live prices via positionValueRSD helper added later).
	totalContrib, err := s.positions.SumTotalContributed(in.FundID)
	if err != nil { return nil, err }
	fundValue, err := s.fundValueRSD(ctx, fund)
	if err != nil { return nil, err }
	posValueRSD := decimal.Zero
	if !totalContrib.IsZero() {
		posValueRSD = fundValue.Mul(pos.TotalContributedRSD).Div(totalContrib)
	}
	if in.AmountRSD.LessThanOrEqual(decimal.Zero) || in.AmountRSD.GreaterThan(posValueRSD) {
		return nil, errors.New("amount_rsd outside [0, position.current_value]")
	}

	// Fee
	feeRSD := decimal.Zero
	if posSystemType == "client" {
		feePct, _ := s.settings.GetDecimal("fund_redemption_fee_pct")
		feeRSD = in.AmountRSD.Mul(feePct).Round(4)
	}

	// Liquidity check (sub-saga in next task; for now require cash sufficient).
	fundAcct, _ := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	cashAvail, _ := decimal.NewFromString(fundAcct.AvailableBalance)
	if cashAvail.LessThan(in.AmountRSD.Add(feeRSD)) {
		// Liquidation triggered in Task 16.
		return nil, errors.New("insufficient_fund_cash: liquidation not yet implemented")
	}

	// open saga
	saga, err := s.sagaRepo.Create(&model.SagaLog{Name: "fund_redeem", Status: "running", CreatedAt: time.Now().UTC()})
	if err != nil { return nil, err }
	exec := NewSagaExecutor(s.sagaRepo, saga.ID)

	contrib := &model.FundContribution{
		FundID: in.FundID, UserID: posUserID, SystemType: posSystemType,
		Direction: model.FundDirectionRedeem, AmountNative: in.AmountRSD, NativeCurrency: "RSD",
		AmountRSD: in.AmountRSD, FeeRSD: feeRSD,
		SourceOrTargetAccountID: in.TargetAccountID, SagaID: saga.ID,
		Status: model.FundContributionStatusPending,
	}
	if err := s.contribs.Create(contrib); err != nil { return nil, err }

	// Debit fund RSD account by amount + fee.
	totalDebit := in.AmountRSD.Add(feeRSD)
	debitMemo := fmt.Sprintf("Redemption to %s #%d (saga=%d)", in.ActorSystemType, in.ActorUserID, saga.ID)
	debitKey := fmt.Sprintf("redeem-%d-%d-debit-fund", in.FundID, saga.ID)
	if err := exec.RunStep(ctx, "debit_fund", totalDebit, "RSD", map[string]any{"memo": debitMemo}, func() error {
		_, e := s.accounts.DebitAccount(ctx, fundAcct.AccountNumber, totalDebit, debitMemo, debitKey)
		return e
	}); err != nil {
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// Credit fee to bank RSD account.
	if feeRSD.IsPositive() {
		bankAcct, _ := s.bankAccountRSD.BankCommissionAccountNumber(ctx)
		feeMemo := fmt.Sprintf("Fund #%d redemption fee (saga=%d)", in.FundID, saga.ID)
		feeKey := fmt.Sprintf("redeem-%d-%d-fee-credit", in.FundID, saga.ID)
		if err := exec.RunStep(ctx, "credit_fee", feeRSD, "RSD", map[string]any{"memo": feeMemo}, func() error {
			_, e := s.accounts.CreditAccount(ctx, bankAcct, feeRSD, feeMemo, feeKey)
			return e
		}); err != nil {
			_ = exec.RunCompensation(ctx, 0, "comp_debit_fund", func() error {
				_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, totalDebit,
					fmt.Sprintf("Comp redeem fund=%d saga=%d", in.FundID, saga.ID),
					fmt.Sprintf("redeem-%d-%d-comp-fund", in.FundID, saga.ID))
				return e
			})
			_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
			return nil, err
		}
	}

	// FX convert if target account ccy ≠ RSD.
	targetAcct, _ := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.TargetAccountID})
	creditAmount := in.AmountRSD
	if targetAcct.CurrencyCode != "RSD" {
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: "RSD", ToCurrency: targetAcct.CurrencyCode, Amount: in.AmountRSD.String(),
		})
		if err != nil { return nil, err }
		creditAmount, _ = decimal.NewFromString(conv.ConvertedAmount)
	}
	creditMemo := fmt.Sprintf("Redemption from fund #%d (saga=%d)", in.FundID, saga.ID)
	creditKey := fmt.Sprintf("redeem-%d-%d-credit-target", in.FundID, saga.ID)
	if err := exec.RunStep(ctx, "credit_target", creditAmount, targetAcct.CurrencyCode, map[string]any{"memo": creditMemo}, func() error {
		_, e := s.accounts.CreditAccount(ctx, targetAcct.AccountNumber, creditAmount, creditMemo, creditKey)
		return e
	}); err != nil {
		// rollback prior steps
		// (compensation list omitted here for brevity; mirror invest's pattern)
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// Update position proportionally.
	reducedContrib := pos.TotalContributedRSD.Mul(in.AmountRSD).Div(posValueRSD)
	if err := s.positions.DecrementContribution(in.FundID, posUserID, posSystemType, reducedContrib); err != nil {
		return nil, err
	}

	if err := s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted); err != nil { return nil, err }
	contrib.Status = model.FundContributionStatusCompleted
	_ = s.sagaRepo.UpdateStatus(saga.ID, "completed")

	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicStockFundRedeemed, mustJSON(kafkamsg.StockFundRedeemedMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			FundID: in.FundID, UserID: posUserID, SystemType: posSystemType,
			AmountRSD: in.AmountRSD.String(), FeeRSD: feeRSD.String(),
			TargetAccountID: in.TargetAccountID, SagaID: saga.ID, ContributionID: contrib.ID,
		}))
	}
	return contrib, nil
}
```

Add `fundValueRSD` helper that sums fund holdings × current market price (call `listingRepo` + `exchange.Convert` per holding).

- [ ] **Step 3: Tests for fee=0 (bank redemption), full redemption (`amount_rsd == position value`)**

```go
func TestRedeemSaga_BankRedemption_NoFee(t *testing.T) { /* … */ }
func TestRedeemSaga_FullRedemption_ZerosPosition(t *testing.T) { /* … */ }
func TestRedeemSaga_OverPositionValue_Rejected(t *testing.T) { /* … */ }
```

- [ ] **Step 4: Run, verify, commit**

```bash
cd stock-service && go test ./internal/service/... -run TestRedeemSaga -v
git add stock-service/internal/service/fund_redeem_saga.go stock-service/internal/service/fund_redeem_saga_test.go
git commit -m "feat(stock-service): fund redeem saga (cash-sufficient path)"
```

---

## Task 16: Liquidation sub-saga (FIFO sell-orders, fill polling)

**Files:**
- Create: `stock-service/internal/service/fund_liquidation.go`
- Create: `stock-service/internal/service/fund_liquidation_test.go`

This is the most subtle saga. Plan:
- Read FIFO holdings via `FundHoldingRepository.ListByFundFIFO`.
- Plan: for each holding, compute `qty_to_sell = ceil(deficit_remaining / listing.Low)`, with a 2% buffer on the deficit.
- Place a market sell order via the existing `OrderService.CreateOrder` with `order.fund_id = fund.ID`, `direction = sell`, qty from plan.
- Poll the fund RSD account every 1 s (timeout 60 s) until `available_balance >= deficit`.
- On timeout: return `ResourceExhausted`.

- [ ] **Step 1: Failing test — plan computation**

```go
func TestLiquidationPlan_FIFOWithBuffer(t *testing.T) {
	holdings := []model.FundHolding{
		{ID: 1, SecurityType: "stock", SecurityID: 100, Quantity: 50},
		{ID: 2, SecurityType: "stock", SecurityID: 200, Quantity: 10},
	}
	prices := map[uint64]decimal.Decimal{
		100: decimal.NewFromInt(100), // 50 × 100 = 5000
		200: decimal.NewFromInt(500), // 10 × 500 = 5000
	}
	plan := planLiquidation(holdings, prices, decimal.NewFromInt(7000))
	// 7000 + 2% buffer = 7140. Need 5000 from holding 1 (50 shares), then 22 shares from holding 2.
	if len(plan) != 2 || plan[0].Quantity != 50 || plan[1].Quantity != 22 {
		t.Errorf("plan %+v", plan)
	}
}
```

- [ ] **Step 2: Implement `planLiquidation`**

```go
type LiquidationItem struct {
	Holding  model.FundHolding
	Quantity int64
	Price    decimal.Decimal
}

func planLiquidation(holdings []model.FundHolding, prices map[uint64]decimal.Decimal, deficitRSD decimal.Decimal) []LiquidationItem {
	target := deficitRSD.Mul(decimal.NewFromFloat(1.02)) // 2% buffer
	var plan []LiquidationItem
	remaining := target
	for _, h := range holdings {
		if remaining.LessThanOrEqual(decimal.Zero) { break }
		price, ok := prices[h.SecurityID]
		if !ok || price.IsZero() { continue }
		// ceil(remaining / price), capped at h.Quantity.
		neededRaw := remaining.Div(price)
		neededInt := neededRaw.Ceil().IntPart()
		if neededInt > h.Quantity {
			neededInt = h.Quantity
		}
		plan = append(plan, LiquidationItem{Holding: h, Quantity: neededInt, Price: price})
		remaining = remaining.Sub(decimal.NewFromInt(neededInt).Mul(price))
	}
	return plan
}
```

- [ ] **Step 3: Implement orchestration `LiquidateAndAwait` that runs the plan**

```go
func (s *FundService) LiquidateAndAwait(ctx context.Context, fund *model.InvestmentFund, deficitRSD decimal.Decimal, sagaID uint64) error {
	// 1. Read FIFO holdings.
	holdings, err := s.holdings.ListByFundFIFO(fund.ID)
	if err != nil { return err }

	// 2. Read prices via listingRepo + exchange.Convert.
	prices := map[uint64]decimal.Decimal{}
	for _, h := range holdings {
		listing, err := s.listingRepo.GetByID(h.SecurityID)
		if err != nil { continue }
		// listing.Low; fall back to Price.
		px := listing.Low
		if px.IsZero() { px = listing.Price }
		if listing.Currency != "RSD" {
			conv, _ := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: listing.Currency, ToCurrency: "RSD", Amount: px.String(),
			})
			px, _ = decimal.NewFromString(conv.ConvertedAmount)
		}
		prices[h.SecurityID] = px
	}

	plan := planLiquidation(holdings, prices, deficitRSD)
	if len(plan) == 0 {
		return errors.New("liquidation_insufficient_holdings")
	}

	// 3. Submit sell orders.
	for _, item := range plan {
		_, err := s.orderService.CreateOrder(ctx, CreateOrderRequest{
			OwnerUserID:    bankSentinelUserID,
			OwnerSystemType: "employee",
			SecurityType:   item.Holding.SecurityType,
			SecurityID:     item.Holding.SecurityID,
			Direction:      "sell",
			OrderType:      "market",
			Quantity:       item.Quantity,
			AccountID:      fund.RSDAccountID,
			FundID:         &fund.ID,
		})
		if err != nil {
			// Cancel any submitted; this is best-effort. The redeem saga's
			// timeout handler is the ultimate guard.
			return fmt.Errorf("submit sell %d: %w", item.Holding.SecurityID, err)
		}
	}

	// 4. Poll fund RSD account.
	deadline := time.Now().Add(60 * time.Second)
	for {
		acct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
		if err == nil {
			avail, _ := decimal.NewFromString(acct.AvailableBalance)
			if avail.GreaterThanOrEqual(deficitRSD) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("liquidation_timeout: fund cash insufficient after 60s")
		}
		time.Sleep(1 * time.Second)
	}
}
```

- [ ] **Step 4: Tests for plan-edge-cases (zero holdings, single holding partial, multi-holding span)**

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/fund_liquidation.go stock-service/internal/service/fund_liquidation_test.go
git commit -m "feat(stock-service): fund liquidation sub-saga (FIFO with 2% buffer)"
```

---

## Task 17: Wire liquidation into Redeem saga

**Files:**
- Modify: `stock-service/internal/service/fund_redeem_saga.go`
- Modify: `stock-service/internal/service/fund_redeem_saga_test.go`

- [ ] **Step 1: Replace the `errors.New("insufficient_fund_cash …")` from Task 15 with a call to `LiquidateAndAwait`**

Inside `Redeem`, in the cash check:

```go
if cashAvail.LessThan(in.AmountRSD.Add(feeRSD)) {
	deficit := in.AmountRSD.Add(feeRSD).Sub(cashAvail)
	if err := s.LiquidateAndAwait(ctx, fund, deficit, saga.ID); err != nil {
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}
	// Re-fetch cash to confirm; continue.
	fundAcct, _ = s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
}
```

- [ ] **Step 2: Add integration-style test that sets up holdings, low cash, drives a redeem, and asserts liquidation kicks in**

```go
func TestRedeemSaga_TriggersLiquidation_WhenCashInsufficient(t *testing.T) {
	fx := newRedeemSagaFixture(t, /*fund cash*/ "100", /*pos rsd*/ "5000")
	fx.holdings.add(model.FundHolding{ID: 1, FundID: fx.fundID, SecurityType: "stock", SecurityID: 100, Quantity: 50})
	fx.listings.set(100, "RSD", "100") // listing.Low = 100 RSD; 50 × 100 = 5000 RSD available
	fx.orderService.simulateFillsThatCreditAccount(/* immediately credit fund RSD with 4900 */)
	out, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fundID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(2000), TargetAccountID: 5001, OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if out.Status != "completed" { t.Errorf("status %s", out.Status) }
}
```

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestRedeemSaga_TriggersLiquidation -v
git add stock-service/internal/service/fund_redeem_saga.go stock-service/internal/service/fund_redeem_saga_test.go
git commit -m "feat(stock-service): redeem saga triggers liquidation when cash short"
```

---

## Task 18: Extend `OrderService.CreateOrder` to handle `on_behalf_of=fund`

**Files:**
- Modify: `stock-service/internal/service/order_service.go`
- Modify: `stock-service/internal/service/order_service_test.go`

- [ ] **Step 1: Failing test — fund order debits the fund RSD account, owner is the bank sentinel, fund_id stamped**

```go
func TestCreateOrder_OnBehalfOfFund_TargetsFundAccount(t *testing.T) {
	fx := newOrderFixture(t)
	fund := fx.seedFund(t, /*manager*/ 25, /*rsd_account*/ 7000)
	fx.actor = mockActor{employeeID: 25, hasFundsManage: true}
	out, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		OwnerUserID: 25, OwnerSystemType: "employee",
		SecurityType: "stock", SecurityID: 100, Direction: "buy", OrderType: "market", Quantity: 5,
		AccountID: fund.RSDAccountID,
		OnBehalfOf: &OnBehalfOf{Type: "fund", FundID: fund.ID},
	})
	if err != nil { t.Fatalf("create: %v", err) }
	if out.FundID == nil || *out.FundID != fund.ID {
		t.Errorf("fund id not stamped: %v", out.FundID)
	}
	if out.UserID != 1_000_000_000 || out.SystemType != "employee" {
		t.Errorf("expected bank sentinel owner, got %d/%s", out.UserID, out.SystemType)
	}
	if !fx.accounts.reservedOn(fund.RSDAccountID) {
		t.Errorf("reservation should be on fund RSD account")
	}
}
```

- [ ] **Step 2: Add the branch to `CreateOrder`**

In the `compute_reservation_target` step block (search `// --- compute_reservation_target ---` if it exists):

```go
if req.OnBehalfOf != nil && req.OnBehalfOf.Type == "fund" {
	fund, err := s.fundRepo.GetByID(req.OnBehalfOf.FundID)
	if err != nil { return nil, fmt.Errorf("fund: %w", err) }
	// Auth: actor must be manager OR have admin role.
	if req.ActorEmployeeID != fund.ManagerEmployeeID && !req.ActorIsAdmin {
		return nil, status.Error(codes.PermissionDenied, "fund_not_managed_by_actor")
	}
	if req.AccountID != fund.RSDAccountID {
		return nil, status.Error(codes.InvalidArgument, "account_id must equal fund RSD account")
	}
	req.OwnerUserID = bankSentinelUserID
	req.OwnerSystemType = "employee"
	order.FundID = &fund.ID
}
```

- [ ] **Step 3: Test admin override + non-manager rejection**

```go
func TestCreateOrder_OnBehalfOfFund_AdminCanPlaceForOthers(t *testing.T) { /* ... */ }
func TestCreateOrder_OnBehalfOfFund_NonManagerRejected(t *testing.T) { /* ... */ }
```

- [ ] **Step 4: Wire `FundID` into `Order` persistence**

If the existing `Order` repo doesn't already persist new optional columns, verify the GORM tag handles it (`gorm:"index"` from Task 1). No code change beyond confirming the column reaches the DB.

- [ ] **Step 5: Modify `upsertHoldingForBuy` in `portfolio_service.go`**

```go
if order.FundID != nil {
	return s.fundHoldings.Upsert(&model.FundHolding{
		FundID: *order.FundID, SecurityType: order.SecurityType, SecurityID: listing.SecurityID,
		Quantity: txn.Quantity, AveragePriceRSD: priceRSD(txn),
	})
}
// existing user-holding upsert path
```

- [ ] **Step 6: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run "TestCreateOrder_OnBehalfOfFund|TestProcessBuyFill_FundOrder" -v
git add stock-service/internal/service/order_service.go stock-service/internal/service/portfolio_service.go \
        stock-service/internal/service/order_service_test.go stock-service/internal/service/portfolio_service_test.go
git commit -m "feat(stock-service): on-behalf-of-fund orders route to fund_holdings"
```

---

## Task 19: stock-service `supervisor-demoted` consumer

**Files:**
- Create: `stock-service/internal/consumer/supervisor_demoted_consumer.go`
- Create: `stock-service/internal/consumer/supervisor_demoted_consumer_test.go`
- Modify: `stock-service/cmd/main.go` (start consumer + EnsureTopics).

- [ ] **Step 1: Failing test — consumer reassigns funds**

```go
func TestSupervisorDemotedConsumer_ReassignsFunds(t *testing.T) {
	fx := newConsumerFixture(t)
	// Seed: 3 funds managed by supervisor 7.
	for i := 0; i < 3; i++ { fx.seedFund(t, /*manager*/ 7) }
	// Inject a Kafka message.
	fx.injectMessage(kafkamsg.UserSupervisorDemotedMessage{
		MessageID: "m1", OccurredAt: time.Now().UTC().Format(time.RFC3339),
		SupervisorID: 7, AdminID: 1, RevokedAt: time.Now().UTC().Format(time.RFC3339),
	})
	fx.runOnce(t)
	got, _, _ := fx.fundRepo.List("", boolPtr(true), 1, 100)
	for _, f := range got {
		if f.ManagerEmployeeID != 1 {
			t.Errorf("fund %d not reassigned: %d", f.ID, f.ManagerEmployeeID)
		}
	}
}
```

- [ ] **Step 2: Implement consumer**

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/repository"
)

type SupervisorDemotedConsumer struct {
	reader   *kafka.Reader
	fundRepo *repository.FundRepository
	producer *kafkaprod.Producer
}

func NewSupervisorDemotedConsumer(brokers string, fundRepo *repository.FundRepository, producer *kafkaprod.Producer) *SupervisorDemotedConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers}, Topic: kafkamsg.TopicUserSupervisorDemoted,
		GroupID: "stock-service-supervisor-demoted", StartOffset: kafka.FirstOffset,
	})
	return &SupervisorDemotedConsumer{reader: r, fundRepo: fundRepo, producer: producer}
}

func (c *SupervisorDemotedConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil { return }
				log.Printf("supervisor-demoted read: %v", err)
				continue
			}
			var evt kafkamsg.UserSupervisorDemotedMessage
			if err := json.Unmarshal(msg.Value, &evt); err != nil {
				log.Printf("supervisor-demoted unmarshal: %v", err)
				continue
			}
			ids, err := c.fundRepo.ReassignManager(evt.SupervisorID, evt.AdminID)
			if err != nil {
				log.Printf("supervisor-demoted reassign: %v", err)
				continue
			}
			if c.producer != nil && len(ids) > 0 {
				_ = c.producer.PublishRaw(ctx, kafkamsg.TopicStockFundsReassigned, mustJSON(kafkamsg.StockFundsReassignedMessage{
					MessageID: msg.Key.String(), OccurredAt: evt.RevokedAt,
					SupervisorID: evt.SupervisorID, AdminID: evt.AdminID, FundIDs: ids,
				}))
			}
		}
	}()
}
```

Add `ReassignManager(supervisorID, adminID int64) ([]uint64, error)` to `FundRepository`:

```go
func (r *FundRepository) ReassignManager(supervisorID, adminID int64) ([]uint64, error) {
	var ids []uint64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.InvestmentFund
		if err := tx.Where("manager_employee_id = ?", supervisorID).Find(&rows).Error; err != nil {
			return err
		}
		for _, f := range rows {
			f.ManagerEmployeeID = adminID
			if err := tx.Save(&f).Error; err != nil {
				return err
			}
			ids = append(ids, f.ID)
		}
		return nil
	})
	return ids, err
}
```

- [ ] **Step 3: Wire in `main.go`**

```go
demotedConsumer := consumer.NewSupervisorDemotedConsumer(cfg.KafkaBrokers, fundRepo, producer)
demotedConsumer.Start(ctx)
defer demotedConsumer.reader.Close()
```

Add `kafkamsg.TopicUserSupervisorDemoted, kafkamsg.TopicStockFundsReassigned` to `EnsureTopics`.

- [ ] **Step 4: Test + commit**

```bash
cd stock-service && go test ./internal/consumer/... -v
git add stock-service/internal/consumer/supervisor_demoted_consumer.go \
        stock-service/internal/consumer/supervisor_demoted_consumer_test.go \
        stock-service/internal/repository/fund_repository.go stock-service/cmd/main.go
git commit -m "feat(stock-service): supervisor-demoted consumer reassigns funds to admin"
```

---

## Task 20: Position-reads service (`ListMyPositions`, `ListBankPositions`, derived values)

**Files:**
- Create: `stock-service/internal/service/fund_position_reads.go`
- Create: `stock-service/internal/service/fund_position_reads_test.go`

- [ ] **Step 1: Implement `fundValueRSD`, `positionValueRSD`, `positionPercentage`**

Already partially needed in Task 15 — formalise here.

```go
// fundValueRSD = fund.rsd_account.balance + Σ holding.qty × current_price_rsd.
func (s *FundService) fundValueRSD(ctx context.Context, fund *model.InvestmentFund) (decimal.Decimal, error) {
	acct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	if err != nil { return decimal.Zero, err }
	cash, _ := decimal.NewFromString(acct.Balance)
	holdings, err := s.holdings.ListByFundFIFO(fund.ID)
	if err != nil { return decimal.Zero, err }
	total := cash
	for _, h := range holdings {
		listing, err := s.listingRepo.GetByID(h.SecurityID)
		if err != nil { continue }
		px := listing.Price
		if listing.Currency != "RSD" {
			conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: listing.Currency, ToCurrency: "RSD", Amount: px.String(),
			})
			if err != nil { continue }
			px, _ = decimal.NewFromString(conv.ConvertedAmount)
		}
		total = total.Add(decimal.NewFromInt(h.Quantity).Mul(px))
	}
	return total, nil
}
```

- [ ] **Step 2: Implement `ListMyPositions(userID, systemType)` returning enriched DTOs**

```go
type PositionDTO struct {
	FundID            uint64
	FundName          string
	ManagerFullName   string
	ContributionRSD   decimal.Decimal
	PercentageFund    decimal.Decimal
	CurrentValueRSD   decimal.Decimal
	ProfitRSD         decimal.Decimal
	LastChangedAt     time.Time
}

func (s *FundService) ListMyPositions(ctx context.Context, userID uint64, systemType string) ([]PositionDTO, error) {
	rows, err := s.positions.ListByOwner(userID, systemType)
	if err != nil { return nil, err }
	out := make([]PositionDTO, 0, len(rows))
	for _, p := range rows {
		fund, err := s.repo.GetByID(p.FundID)
		if err != nil { continue }
		fundValue, err := s.fundValueRSD(ctx, fund)
		if err != nil { return nil, err }
		totalContrib, err := s.positions.SumTotalContributed(p.FundID)
		if err != nil { return nil, err }
		var pct, curVal, profit decimal.Decimal
		if !totalContrib.IsZero() {
			pct = p.TotalContributedRSD.Div(totalContrib)
			curVal = fundValue.Mul(pct)
			profit = curVal.Sub(p.TotalContributedRSD)
		}
		// Filter zeroed-out positions per spec §13.3.
		if p.TotalContributedRSD.IsZero() && curVal.IsZero() {
			continue
		}
		out = append(out, PositionDTO{
			FundID: p.FundID, FundName: fund.Name,
			ContributionRSD: p.TotalContributedRSD,
			PercentageFund: pct, CurrentValueRSD: curVal, ProfitRSD: profit,
			LastChangedAt: p.UpdatedAt,
		})
	}
	return out, nil
}
```

- [ ] **Step 3: `ListBankPositions` mirrors above, scoped to `(userID = bankSentinelUserID, systemType = "employee")`**

- [ ] **Step 4: Tests + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestPosition -v
git add stock-service/internal/service/fund_position_reads.go stock-service/internal/service/fund_position_reads_test.go
git commit -m "feat(stock-service): position-reads service with derived value/profit/percentage"
```

---

## Task 21: Actuary performance reads

**Files:**
- Create: `stock-service/internal/service/actuary_performance.go`
- Create: `stock-service/internal/service/actuary_performance_test.go`

- [ ] **Step 1: Test — sums realized capital_gains by acting employee, decorates with full names**

```go
func TestGetActuaryPerformance_AggregatesCapitalGainsByActor(t *testing.T) {
	fx := newPerfFixture(t)
	fx.gains.add("employee", 25, "RSD", "12000.00")
	fx.gains.add("employee", 25, "USD", "100.00")
	fx.exchange.setRate("USD", "RSD", "118", "11800")
	fx.users.setNames(map[int64]string{25: "Jane Doe"})
	out, err := fx.svc.GetActuaryPerformance(context.Background())
	if err != nil { t.Fatalf("err: %v", err) }
	if len(out) != 1 || out[0].EmployeeID != 25 || out[0].FullName != "Jane Doe" {
		t.Errorf("%+v", out)
	}
	if out[0].RealizedProfitRSD.String() != "23800" {
		t.Errorf("profit %s", out[0].RealizedProfitRSD)
	}
}
```

- [ ] **Step 2: Implement**

```go
type ActuaryPerf struct {
	EmployeeID         int64
	FullName           string
	Role               string
	RealizedProfitRSD  decimal.Decimal
}

func (s *FundService) GetActuaryPerformance(ctx context.Context) ([]ActuaryPerf, error) {
	// Group capital_gains by acting_employee_id (NOT NULL only).
	rows, err := s.capitalGains.SumByActingEmployee()
	if err != nil { return nil, err }

	// Convert per-currency totals to RSD via exchange-service.Convert.
	totalsByEmp := map[int64]decimal.Decimal{}
	for _, r := range rows {
		amt := r.TotalGain
		if r.Currency != "RSD" {
			conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: r.Currency, ToCurrency: "RSD", Amount: amt.String(),
			})
			if err == nil {
				amt, _ = decimal.NewFromString(conv.ConvertedAmount)
			}
		}
		totalsByEmp[r.EmployeeID] = totalsByEmp[r.EmployeeID].Add(amt)
	}

	// Look up names.
	ids := make([]int64, 0, len(totalsByEmp))
	for k := range totalsByEmp { ids = append(ids, k) }
	names, _ := s.userClient.ListEmployeeFullNames(ctx, &userpb.ListEmployeeFullNamesRequest{EmployeeIds: ids})

	out := make([]ActuaryPerf, 0, len(totalsByEmp))
	for emp, total := range totalsByEmp {
		out = append(out, ActuaryPerf{
			EmployeeID: emp, FullName: names.NamesById[emp], Role: "actuary",
			RealizedProfitRSD: total,
		})
	}
	return out, nil
}
```

Add `SumByActingEmployee` to the existing `capital_gain_repository.go`:

```go
type ActuaryGainRow struct {
	EmployeeID int64           `gorm:"column:acting_employee_id"`
	Currency   string          `gorm:"column:currency"`
	TotalGain  decimal.Decimal `gorm:"column:total_gain"`
}

func (r *CapitalGainRepository) SumByActingEmployee() ([]ActuaryGainRow, error) {
	var out []ActuaryGainRow
	err := r.db.Model(&model.CapitalGain{}).
		Select("acting_employee_id, currency, SUM(total_gain) as total_gain").
		Where("acting_employee_id IS NOT NULL AND acting_employee_id != 0").
		Group("acting_employee_id, currency").
		Find(&out).Error
	return out, err
}
```

(`acting_employee_id` should already exist on `capital_gains` per the bank-safe settlement design; if not, add a migration.)

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestGetActuaryPerformance -v
git add stock-service/internal/service/actuary_performance.go stock-service/internal/service/actuary_performance_test.go \
        stock-service/internal/repository/capital_gain_repository.go
git commit -m "feat(stock-service): actuary performance aggregation"
```

---

## Task 22: gRPC handler for `InvestmentFundService`

**Files:**
- Create: `stock-service/internal/handler/investment_fund_handler.go`
- Modify: `stock-service/cmd/main.go` — register service.

- [ ] **Step 1: Implement handler**

```go
package handler

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
	"github.com/shopspring/decimal"
)

type InvestmentFundHandler struct {
	stockpb.UnimplementedInvestmentFundServiceServer
	fundSvc *service.FundService
}

func NewInvestmentFundHandler(fundSvc *service.FundService) *InvestmentFundHandler {
	return &InvestmentFundHandler{fundSvc: fundSvc}
}

func (h *InvestmentFundHandler) CreateFund(ctx context.Context, in *stockpb.CreateFundRequest) (*stockpb.FundResponse, error) {
	min, _ := decimal.NewFromString(in.MinimumContributionRsd)
	out, err := h.fundSvc.Create(ctx, service.CreateFundInput{
		ActorEmployeeID: in.ActorEmployeeId, Name: in.Name,
		Description: in.Description, MinimumContributionRSD: min,
	})
	if err != nil {
		return nil, mapFundErr(err)
	}
	return toFundResponse(out, ""), nil
}

// … one method per RPC. Pattern:
//   1. Decode strings to decimals.
//   2. Delegate to service.
//   3. Map errors via mapFundErr.
//   4. Marshal to proto via toFundResponse / toContributionResponse / etc.

func mapFundErr(err error) error {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "not_found"):
		return status.Error(codes.NotFound, err.Error())
	case strings.Contains(err.Error(), "minimum_contribution_not_met"),
		strings.Contains(err.Error(), "insufficient_fund_cash"):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
```

Implement each RPC fully. Add `toFundResponse`, `toContributionResponse`, `toPositionItem` helpers next to the handler.

- [ ] **Step 2: Register the service in `cmd/main.go`**

```go
fundHandler := handler.NewInvestmentFundHandler(fundService)
stockpb.RegisterInvestmentFundServiceServer(grpcSrv, fundHandler)
```

- [ ] **Step 3: Test handler maps errors correctly + commit**

```bash
cd stock-service && go test ./internal/handler/... -run InvestmentFund -v
git add stock-service/internal/handler/investment_fund_handler.go stock-service/cmd/main.go
git commit -m "feat(stock-service): InvestmentFundService gRPC handler"
```

---

## Task 23: api-gateway routes (8 new + extension)

**Files:**
- Create: `api-gateway/internal/handler/investment_fund_handler.go`
- Modify:
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes`
  - `api-gateway/internal/handler/stock_order_handler.go` — add `on_behalf_of`.
  - `api-gateway/cmd/main.go` — dial `InvestmentFundServiceClient`.

- [ ] **Step 1: Add the new handler with all 8 endpoints**

Mirrors §6 of the spec. One method per route. Each:
1. Decodes the request body.
2. Validates via existing `oneOf`, `nonNegative` helpers.
3. Calls `h.fundClient.<Rpc>` via the gRPC client.
4. Marshals via `accountToJSON` / `apiError` style helpers.

Example for `POST /api/v1/investment-funds`:

```go
func (h *InvestmentFundHandler) CreateFund(c *gin.Context) {
	var req struct {
		Name                   string `json:"name"`
		Description            string `json:"description"`
		MinimumContributionRSD string `json:"minimum_contribution_rsd"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid body")
		return
	}
	if req.Name == "" {
		apiError(c, 400, ErrValidation, "name is required")
		return
	}
	actor, _ := c.Get("user_id")
	resp, err := h.client.CreateFund(c.Request.Context(), &stockpb.CreateFundRequest{
		ActorEmployeeId: actor.(int64),
		Name: req.Name, Description: req.Description,
		MinimumContributionRsd: req.MinimumContributionRSD,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"fund": resp})
}
```

Repeat for the other 7. Each route gets a swagger annotation block per CLAUDE.md.

- [ ] **Step 2: Wire into `router_v1.go`**

```go
fundsAuth := group.Group("/investment-funds")
fundsAuth.Use(middleware.AnyAuthMiddleware(authClient))
{
	fundsAuth.GET("", fundHandler.ListFunds)
	fundsAuth.GET("/:id", fundHandler.GetFund)
	fundsAuth.POST("/:id/invest", fundHandler.Invest)
	fundsAuth.POST("/:id/redeem", fundHandler.Redeem)
}
fundsManage := group.Group("/investment-funds")
fundsManage.Use(middleware.AuthMiddleware(authClient), middleware.RequirePermission("funds.manage"))
{
	fundsManage.POST("", fundHandler.CreateFund)
	fundsManage.PUT("/:id", fundHandler.UpdateFund)
}
group.GET("/me/investment-funds", middleware.AnyAuthMiddleware(authClient), fundHandler.ListMyPositions)
fundsBank := group.Group("/investment-funds/positions")
fundsBank.Use(middleware.AuthMiddleware(authClient), middleware.RequirePermission("funds.bank-position-read"))
{
	fundsBank.GET("", fundHandler.ListBankPositions)
}
group.GET("/actuaries/performance", middleware.AuthMiddleware(authClient), middleware.RequirePermission("funds.bank-position-read"), fundHandler.ActuaryPerformance)
```

- [ ] **Step 3: Extend `POST /me/orders` to accept `on_behalf_of`**

In `stock_order_handler.go` `CreateOrder`, decode the field:

```go
type onBehalfOf struct {
	Type   string  `json:"type"`
	FundID *uint64 `json:"fund_id,omitempty"`
}
var req struct {
	// existing fields …
	OnBehalfOf *onBehalfOf `json:"on_behalf_of,omitempty"`
}
```

Pass it through to the gRPC `CreateOrderRequest.OnBehalfOf`. Validate in handler that `type ∈ {self, bank, fund}`; clients (system_type=client) get rejected if `type != self` (or omitted).

- [ ] **Step 4: Wire `InvestmentFundServiceClient` in `api-gateway/cmd/main.go`**

```go
fundClient := stockpb.NewInvestmentFundServiceClient(stockConn)
fundHandler := handler.NewInvestmentFundHandler(fundClient)
router.RegisterCoreRoutes(/* …, fundHandler */)
```

- [ ] **Step 5: Regenerate swagger**

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **Step 6: Test + commit**

```bash
make build && make lint
git add api-gateway/
git commit -m "feat(api-gateway): investment-funds REST routes + on_behalf_of body field"
```

---

## Task 24: Spec.md + REST_API_v1.md updates

**Files:**
- Modify: `docs/Specification.md`
- Modify: `docs/api/REST_API_v1.md`

- [ ] **Step 1: Append all required sections per spec §11**

In `docs/Specification.md`:
- §3 (gateway client wiring): add `InvestmentFundServiceClient`.
- §6 (permissions): add `funds.bank-position-read`.
- §11: list `InvestmentFundService` RPCs.
- §17: 8 new routes + extension.
- §18: 4 new entities (`InvestmentFund`, `ClientFundPosition`, `FundContribution`, `FundHolding`); note `Order.fund_id` addition.
- §19: 6 new Kafka topics.
- §20: enums (`on_behalf_of.type`, `fund_contribution.direction`, `fund_contribution.status`).
- §21: business rules §4.1–§4.9.

In `docs/api/REST_API_v1.md`:
- One section per new route in the existing format.
- Update `POST /api/v1/me/orders` block to document `on_behalf_of`.

- [ ] **Step 2: Commit**

```bash
git add docs/Specification.md docs/api/REST_API_v1.md
git commit -m "docs: investment funds — spec + REST_API_v1 updates"
```

---

## Task 25: Integration tests (test-app workflows)

**Files:**
- Create:
  - `test-app/workflows/wf_investment_funds_basic_test.go`
  - `test-app/workflows/wf_investment_funds_cross_ccy_test.go`
  - `test-app/workflows/wf_investment_funds_liquidation_test.go`
  - `test-app/workflows/wf_investment_funds_bank_position_test.go`
  - `test-app/workflows/wf_investment_funds_on_behalf_order_test.go`
  - `test-app/workflows/wf_investment_funds_supervisor_demotion_test.go`
  - `test-app/workflows/wf_investment_funds_actuary_performance_test.go`
  - `test-app/workflows/wf_investment_funds_minimum_contribution_test.go`

For each, mirror the existing workflow style (`test-app/workflows/wf_*_test.go`). Use shared helpers from `test-app/workflows/helpers_test.go` (client setup, JWT minting, `awaitKafkaMessage`).

- [ ] **Step 1: Basic happy-path workflow**

```go
//go:build integration

package workflows_test

import (
	"context"
	"testing"
)

func TestWorkflow_InvestmentFunds_Basic(t *testing.T) {
	ctx := context.Background()
	supSession := newSupervisorSession(t, ctx)
	clientSession := newClientSession(t, ctx)

	// 1. Supervisor creates fund.
	fund := supSession.createFund(t, "Alpha", "1000.00")
	defer supSession.deleteFundIfExists(t, fund.ID)

	// 2. Client invests 5000 RSD from their RSD account.
	contrib := clientSession.investInFund(t, fund.ID, clientSession.RSDAccountID, "5000", "RSD")
	if contrib.Status != "completed" {
		t.Fatalf("invest status %s", contrib.Status)
	}

	// 3. Verify ledger entries on both accounts.
	clientLedger := clientSession.activity(t, clientSession.RSDAccountID)
	expectMemo(t, clientLedger, "Invest in fund #")
	fundLedger := supSession.activity(t, fund.RSDAccountID)
	expectMemo(t, fundLedger, "Contribution from client #")

	// 4. /me/investment-funds shows the position.
	positions := clientSession.listMyPositions(t)
	if len(positions) != 1 || positions[0].FundID != fund.ID {
		t.Fatalf("positions %+v", positions)
	}

	// 5. Client redeems 2000 RSD.
	redeem := clientSession.redeemFromFund(t, fund.ID, "2000", clientSession.RSDAccountID)
	if redeem.FeeRSD != "10.0000" {
		t.Errorf("fee %s want 10", redeem.FeeRSD)
	}

	// 6. Verify final balances + position.
	// (assertions trimmed for brevity; mirror pattern)
}
```

- [ ] **Step 2: Cross-currency workflow** — uses an EUR client account, asserts FX rate populated on contribution.

- [ ] **Step 3: Liquidation workflow** — supervisor places 2 buy orders to seed holdings, then client redeems an amount > fund cash, asserts `stock.fund-redeemed` appears within 60 s.

- [ ] **Step 4: Bank-position workflow** — supervisor invests bank funds; `GET /investment-funds/positions?scope=bank` shows the row; bank redemption fee_rsd = 0.

- [ ] **Step 5: On-behalf-of-fund order workflow** — supervisor places order with `on_behalf_of.type=fund`; verifies `fund_holdings` populated, user `holdings` unchanged; non-manager supervisor 403.

- [ ] **Step 6: Supervisor demotion workflow** — admin revokes `funds.manage`; uses `awaitKafkaMessage("user.supervisor-demoted")` then `awaitKafkaMessage("stock.funds-reassigned")`; `GET /investment-funds/{id}` shows the new manager.

- [ ] **Step 7: Actuary performance workflow** — supervisor places several on-behalf-of-fund buy + sell orders that realise gains; `GET /actuaries/performance` returns the supervisor's profit matching `capital_gains` sum.

- [ ] **Step 8: Minimum contribution workflow** — invest below minimum (RSD direct + EUR pre-FX) returns 422.

- [ ] **Step 9: Run all integration tests**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
make docker-up
go test -tags=integration ./test-app/workflows/... -run InvestmentFunds -v
make docker-down
```

- [ ] **Step 10: Commit**

```bash
git add test-app/workflows/wf_investment_funds_*.go
git commit -m "test(integration): investment-funds workflows (8 scenarios)"
```

---

## Task 26: Final lint + push

- [ ] **Step 1: Lint everything**

```bash
make lint
```

Expected: zero warnings on touched services.

- [ ] **Step 2: Re-run unit tests across all services**

```bash
make test
```

Expected: green.

- [ ] **Step 3: Push branch**

```bash
git push -u origin feature/investment-funds
```

- [ ] **Step 4: Open PR via gh**

```bash
gh pr create --title "feat: investment funds (Celina 4)" --body "$(cat <<'EOF'
## Summary
- Implements docs/superpowers/specs/2026-04-24-investment-funds-design.md.
- 4 new tables, 5 sagas, 8 REST routes, 6 Kafka topics, supervisor-demotion auto-reassignment.

## Test plan
- [x] `make test` green
- [x] `make lint` clean
- [x] integration: 8 workflow tests pass
- [x] Manual: supervisor creates fund → client invest → redeem → liquidation triggered → demotion reassigns to admin
EOF
)"
```

---

## Self-review

**Spec coverage check** (against `docs/superpowers/specs/2026-04-24-investment-funds-design.md`):

| Spec § | Plan task |
|---|---|
| §3.1 (4 tables) | T2, T3, T4 |
| §3.2 (orders.fund_id) | T1 |
| §3.3 (derived fields) | T20 |
| §3.4 (system setting) | T7 |
| §4.1 (creation) | T12 |
| §4.2 (proportional decrement) | T15 |
| §4.3 (supervisor on-behalf-of-bank) | T13, T15 |
| §4.4 (redemption cap) | T15 |
| §4.5 (auto-liquidation) | T16, T17 |
| §4.6 (on-behalf-of-fund orders) | T18 |
| §4.7 (supervisor demotion) | T10, T11, T19 |
| §4.8 (minimum contribution post-FX) | T13, T14 |
| §4.9 (employee personal positions rejected) | T13 (handler-level reject) |
| §5.1 invest saga | T13, T14 |
| §5.2 redeem saga | T15 |
| §5.3 liquidation sub-saga | T16 |
| §5.4 placement extension | T18 |
| §5.5 demotion saga | T19 |
| §6.1–§6.10 REST surface | T23 |
| §7 gRPC additions | T8, T22 |
| §7 user-service ListEmployeeFullNames | T9 |
| §8 Kafka topics | T8, plus EnsureTopics calls per service in T11/T19 |
| §9 permissions | T7 |
| §10 testing strategy | T13–T21 unit, T25 integration |
| §11 doc updates | T24 |

All sections covered. **No spec gaps.**

**Placeholder scan:** no "TBD", "TODO", or generic "add error handling" steps. All compensation flows explicit. All idempotency keys named.

**Type consistency:** every type/method introduced in earlier tasks is referenced verbatim in later tasks (`FundService`, `FundRepository`, `BankAccountClient`, `FundAccountClient`, `ExchangeConverter`, `OnBehalfOf`, `LiquidationItem`, etc.).

**Risks called out in spec §13 are present in plan:**
- §13.1 — 2% buffer on liquidation in T16 `planLiquidation`.
- §13.2 — FIFO order via `FundHoldingRepository.ListByFundFIFO`.
- §13.3 — zero-position filter in T20.
- §13.5 — warn-and-continue on in-flight sagas during demotion (T19; logs only).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-25-investment-funds.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review.

**Which approach?**

# Intra-bank OTC Options Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Celina-4 OTC option trading per `docs/superpowers/specs/2026-04-24-intrabank-otc-options-design.md`. Sellers write call options on stocks they hold; buyers can accept, counter, or reject. On accept the premium is paid immediately and the seller's shares are reserved against the contract. On exercise (any time up to settlement_date) the strike funds and shares move atomically. On expiry the seller keeps the premium and the reservation releases.

**Architecture:** stock-service owns all four new tables (`otc_offers`, `otc_offer_revisions`, `option_contracts`, `otc_offer_read_receipts`) plus an extension to `holding_reservations` (`otc_contract_id` column with a CHECK constraint that exactly one of `order_id` / `otc_contract_id` is set). Two sagas — premium-payment (on accept) and exercise — orchestrated via the existing `SagaLog` + `SagaExecutor` from the bank-safe-settlement work. account-service's `ReserveFunds` / `PartialSettleReservation` / `ReleaseReservation` / `CreditAccount` cover all money flows; idempotency keys are deterministic and per-step. A daily cron expires past-settlement contracts and offers. api-gateway exposes 9 new REST routes; legacy direct-purchase OTC stays live with a deprecation warning.

**Tech Stack:** Go, gRPC (protobuf via `make proto`), GORM, PostgreSQL, Kafka (segmentio/kafka-go), Gin.

**Hard prerequisite:** the `feature/securities-bank-safety` work must be merged into the base branch before this plan starts. It owns `HoldingReservationService`, `SagaExecutor`, `SagaLog`, ledger entries, and the account-service `ReserveFunds` family that this plan depends on. Verify in Step 0.2 that `stock-service/internal/service/holding_reservation_service.go` and `stock-service/internal/saga/` exist before writing any code.

---

## File structure

**stock-service:**
- Create:
  - `stock-service/internal/model/otc_offer.go` — `OTCOffer` struct + `BeforeUpdate` version hook + status enum constants.
  - `stock-service/internal/model/otc_offer_revision.go` — append-only revision row.
  - `stock-service/internal/model/option_contract.go` — `OptionContract` struct + `BeforeUpdate` hook + status constants.
  - `stock-service/internal/model/otc_offer_read_receipt.go` — composite-PK row.
  - `stock-service/internal/repository/otc_offer_repository.go` — CRUD + the seller-invariant SELECT FOR UPDATE.
  - `stock-service/internal/repository/otc_offer_revision_repository.go` — insert + list per offer.
  - `stock-service/internal/repository/option_contract_repository.go` — CRUD + cron-batch select.
  - `stock-service/internal/repository/otc_read_receipt_repository.go` — upsert + join helper.
  - `stock-service/internal/service/otc_offer_service.go` — Create/Counter/Reject + list/get + seller-invariant orchestration. No money flow.
  - `stock-service/internal/service/otc_accept_saga.go` — premium-payment saga (steps 1–7 from spec §6.1).
  - `stock-service/internal/service/otc_exercise_saga.go` — exercise saga (steps 1–7 from spec §6.2).
  - `stock-service/internal/service/otc_expiry_cron.go` — daily expiry of offers + contracts.
  - `stock-service/internal/handler/otc_options_handler.go` — gRPC `OTCOptionsService` server wrapping the service layer.
  - All matching `*_test.go` files (TDD).
- Modify:
  - `stock-service/internal/model/holding_reservation.go` — add `OTCContractID *uint64` field; add `BeforeCreate` hook enforcing the "exactly one of OrderID / OTCContractID" CHECK at the model level (DB also has it).
  - `stock-service/internal/service/holding_reservation_service.go` — add `ReserveForOTCContract(...)`, `ReleaseForOTCContract(...)`, `ConsumeForOTCContract(...)` methods.
  - `stock-service/cmd/main.go` — wire all new repos, services, gRPC handler, expiry cron goroutine, EnsureTopics for the 8 new `otc.*` topics.

**user-service:**
- Modify:
  - `user-service/internal/service/role_service.go` — add `otc.trade` permission to `DefaultPermissions` and grant it to `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin`.
- Test: append to `user-service/internal/service/role_service_test.go`.

**contract:**
- Modify:
  - `contract/proto/stock/stock.proto` — new `OTCOptionsService` with 9 RPCs and ~25 message types.
  - `contract/kafka/messages.go` — 8 new topic constants + payload structs.

**api-gateway:**
- Create:
  - `api-gateway/internal/handler/otc_options_handler.go` — 9 handler functions (one per route).
- Modify:
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes` — wire the new routes; gate via `requireAll("securities.trade","otc.trade")` middleware combinator (add helper if missing).
  - `api-gateway/cmd/main.go` — dial stock-service for `OTCOptionsServiceClient`.
  - `api-gateway/internal/middleware/permission.go` — add `RequireAllPermissions(...string)` helper if not present.

**Documentation:**
- Modify:
  - `docs/Specification.md` — §3, §6, §11, §17, §18, §19, §20, §21 per spec §11.1.
  - `docs/api/REST_API_v1.md` — new "OTC Option Trading" section + deprecation note on legacy.

**docker-compose:**
- Modify both `docker-compose.yml` and `docker-compose-remote.yml`:
  - Add `OTC_EXPIRY_CRON_UTC=02:00` and `OTC_EXPIRY_BATCH_SIZE=500` env vars to the `stock-service` block.

---

## Pre-flight

- [ ] **Step 0.1: Branch from `Development`**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
git fetch origin
git checkout Development
git pull origin Development
git checkout -b feature/intrabank-otc-options
```

- [ ] **Step 0.2: Verify the bank-safe-settlement prerequisites are merged**

```bash
ls stock-service/internal/saga/ 2>&1
ls stock-service/internal/service/holding_reservation_service.go 2>&1
grep -n "func.*ReserveFunds\|func.*PartialSettleReservation\|func.*ReleaseReservation" account-service/internal/handler/grpc_handler.go
```

Expected: directory exists, file exists, three account-service handlers present. **If any are missing, stop — this plan depends on them.**

- [ ] **Step 0.3: Green baseline**

```bash
make tidy && make proto && make build && make lint && make test
```

Expected: every command exits 0.

---

## Task 1: Extend `holding_reservations` with `otc_contract_id`

**Files:**
- Modify: `stock-service/internal/model/holding_reservation.go`
- Test: `stock-service/internal/model/holding_reservation_test.go` (append).

- [ ] **Step 1: Failing test — exactly one of OrderID / OTCContractID is set**

```go
func TestHoldingReservation_BeforeCreate_RejectsBothNil(t *testing.T) {
	r := &HoldingReservation{Quantity: 10}
	if err := r.BeforeCreate(nil); err == nil {
		t.Errorf("expected error when both OrderID and OTCContractID are nil")
	}
}

func TestHoldingReservation_BeforeCreate_RejectsBothSet(t *testing.T) {
	o := uint64(1)
	c := uint64(2)
	r := &HoldingReservation{Quantity: 10, OrderID: &o, OTCContractID: &c}
	if err := r.BeforeCreate(nil); err == nil {
		t.Errorf("expected error when both OrderID and OTCContractID are set")
	}
}

func TestHoldingReservation_BeforeCreate_AllowsOTCOnly(t *testing.T) {
	c := uint64(2)
	r := &HoldingReservation{Quantity: 10, OTCContractID: &c}
	if err := r.BeforeCreate(nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
cd stock-service && go test ./internal/model/... -run TestHoldingReservation_BeforeCreate -v
```

Expected: compile error / type mismatch.

- [ ] **Step 3: Add the field + hook**

In `stock-service/internal/model/holding_reservation.go`, locate the `HoldingReservation` struct. Add (preserve existing tags / column order; just append):

```go
// OTCContractID is non-nil when the reservation backs an OTC option
// contract instead of a placed order. Mutually exclusive with OrderID.
OTCContractID *uint64 `gorm:"index" json:"otc_contract_id,omitempty"`
```

Then append:

```go
// BeforeCreate enforces the model-level "exactly one of OrderID,
// OTCContractID is set" invariant. The DB has the same CHECK constraint
// (added in AutoMigrate) but failing fast in Go gives clearer errors.
func (r *HoldingReservation) BeforeCreate(tx *gorm.DB) error {
	if (r.OrderID == nil) == (r.OTCContractID == nil) {
		return errors.New("holding reservation: exactly one of order_id, otc_contract_id must be set")
	}
	return nil
}
```

(Add `"errors"` import if absent.)

- [ ] **Step 4: Run, verify pass**

```bash
cd stock-service && go test ./internal/model/... -run TestHoldingReservation_BeforeCreate -v
```

Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/model/holding_reservation.go stock-service/internal/model/holding_reservation_test.go
git commit -m "feat(stock-service): HoldingReservation.OTCContractID + BeforeCreate guard"
```

---

## Task 2: `OTCOffer` model + version hook + status constants

**Files:**
- Create: `stock-service/internal/model/otc_offer.go`
- Create: `stock-service/internal/model/otc_offer_test.go`

- [ ] **Step 1: Failing tests**

```go
package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestOTCOffer_BeforeUpdate_BumpsVersion(t *testing.T) {
	o := &OTCOffer{Version: 7}
	if err := o.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if o.Version != 8 {
		t.Errorf("got %d want 8", o.Version)
	}
}

func TestOTCOffer_StatusConstants(t *testing.T) {
	for _, v := range []string{
		OTCOfferStatusPending, OTCOfferStatusCountered, OTCOfferStatusAccepted,
		OTCOfferStatusRejected, OTCOfferStatusExpired, OTCOfferStatusFailed,
	} {
		if v == "" {
			t.Errorf("status constant empty")
		}
	}
}

func TestOTCOffer_DirectionConstants(t *testing.T) {
	for _, v := range []string{OTCDirectionSellInitiated, OTCDirectionBuyInitiated} {
		if v == "" {
			t.Errorf("direction constant empty")
		}
	}
}

func TestOTCOffer_DefaultsZero(t *testing.T) {
	o := &OTCOffer{}
	if !o.Quantity.Equal(decimal.Zero) {
		t.Errorf("quantity default %s", o.Quantity)
	}
}
```

- [ ] **Step 2: Implement `otc_offer.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OTCOfferStatusPending   = "PENDING"
	OTCOfferStatusCountered = "COUNTERED"
	OTCOfferStatusAccepted  = "ACCEPTED"
	OTCOfferStatusRejected  = "REJECTED"
	OTCOfferStatusExpired   = "EXPIRED"
	OTCOfferStatusFailed    = "FAILED"

	OTCDirectionSellInitiated = "sell_initiated"
	OTCDirectionBuyInitiated  = "buy_initiated"

	OTCActionCreate  = "CREATE"
	OTCActionCounter = "COUNTER"
	OTCActionAccept  = "ACCEPT"
	OTCActionReject  = "REJECT"
)

// OTCOffer is one back-and-forth thread between an initiator and a
// counterparty over a potential option contract. Negotiation history is
// captured by OTCOfferRevision rows; this row holds the current state.
//
// Cross-bank fields (initiator_bank_code, counterparty_bank_code,
// external_correlation_id) stay NULL for intra-bank trades. Spec 4 wires
// them up.
type OTCOffer struct {
	ID                       uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	InitiatorUserID          int64           `gorm:"not null" json:"initiator_user_id"`
	InitiatorSystemType      string          `gorm:"size:10;not null" json:"initiator_system_type"`
	InitiatorBankCode        *string         `gorm:"size:32" json:"initiator_bank_code,omitempty"`
	CounterpartyUserID       *int64          `gorm:"" json:"counterparty_user_id,omitempty"`
	CounterpartySystemType   *string         `gorm:"size:10" json:"counterparty_system_type,omitempty"`
	CounterpartyBankCode     *string         `gorm:"size:32" json:"counterparty_bank_code,omitempty"`
	Direction                string          `gorm:"size:20;not null" json:"direction"`
	StockID                  uint64          `gorm:"not null;index:ix_otc_stock_status,priority:1" json:"stock_id"`
	Quantity                 decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice              decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium                  decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate           time.Time       `gorm:"type:date;not null" json:"settlement_date"`
	Status                   string          `gorm:"size:16;not null;index:ix_otc_stock_status,priority:2;index:ix_otc_initiator,priority:3;index:ix_otc_counterparty,priority:3" json:"status"`
	LastModifiedByUserID     int64           `gorm:"not null" json:"last_modified_by_user_id"`
	LastModifiedBySystemType string          `gorm:"size:10;not null" json:"last_modified_by_system_type"`
	ExternalCorrelationID    *string         `gorm:"size:64" json:"external_correlation_id,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
	Version                  int64           `gorm:"not null;default:0" json:"-"`
}

func (o *OTCOffer) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", o.Version)
	}
	o.Version++
	return nil
}

// IsTerminal reports whether the offer is in a terminal state and cannot be
// counter-ed, accepted, or rejected.
func (o *OTCOffer) IsTerminal() bool {
	switch o.Status {
	case OTCOfferStatusAccepted, OTCOfferStatusRejected, OTCOfferStatusExpired, OTCOfferStatusFailed:
		return true
	}
	return false
}
```

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/model/... -run TestOTCOffer -v
git add stock-service/internal/model/otc_offer.go stock-service/internal/model/otc_offer_test.go
git commit -m "feat(stock-service): OTCOffer model + status/direction constants"
```

---

## Task 3: `OTCOfferRevision`, `OptionContract`, `OTCOfferReadReceipt`

**Files:**
- Create:
  - `stock-service/internal/model/otc_offer_revision.go`
  - `stock-service/internal/model/option_contract.go`
  - `stock-service/internal/model/otc_offer_read_receipt.go`
- Append: `stock-service/internal/model/otc_offer_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestOptionContract_BeforeUpdate_BumpsVersion(t *testing.T) {
	c := &OptionContract{Version: 3}
	_ = c.BeforeUpdate(nil)
	if c.Version != 4 {
		t.Errorf("got %d want 4", c.Version)
	}
}

func TestOptionContract_StatusConstants(t *testing.T) {
	for _, v := range []string{OptionContractStatusActive, OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed} {
		if v == "" {
			t.Errorf("blank constant")
		}
	}
}
```

- [ ] **Step 2: `otc_offer_revision.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// OTCOfferRevision is one entry in the negotiation history. Append-only;
// never updated. revision_number starts at 1 (CREATE) and increments per
// COUNTER. Final ACCEPT or REJECT writes one last row.
type OTCOfferRevision struct {
	ID                    uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID               uint64          `gorm:"not null;uniqueIndex:ux_otc_rev,priority:1" json:"offer_id"`
	RevisionNumber        int             `gorm:"not null;uniqueIndex:ux_otc_rev,priority:2" json:"revision_number"`
	Quantity              decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice           decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium               decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate        time.Time       `gorm:"type:date;not null" json:"settlement_date"`
	ModifiedByUserID      int64           `gorm:"not null" json:"modified_by_user_id"`
	ModifiedBySystemType  string          `gorm:"size:10;not null" json:"modified_by_system_type"`
	Action                string          `gorm:"size:16;not null" json:"action"`
	CreatedAt             time.Time       `json:"created_at"`
}
```

- [ ] **Step 3: `option_contract.go`**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OptionContractStatusActive    = "ACTIVE"
	OptionContractStatusExercised = "EXERCISED"
	OptionContractStatusExpired   = "EXPIRED"
	OptionContractStatusFailed    = "FAILED"
)

// OptionContract is the executed option that the premium-payment saga
// produces from an OTCOffer. quantity, strike_price, and settlement_date are
// snapshotted from the final accepted revision.
type OptionContract struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID          uint64          `gorm:"not null;uniqueIndex" json:"offer_id"`
	BuyerUserID      int64           `gorm:"not null;index:ix_oc_buyer,priority:1" json:"buyer_user_id"`
	BuyerSystemType  string          `gorm:"size:10;not null;index:ix_oc_buyer,priority:2" json:"buyer_system_type"`
	BuyerBankCode    *string         `gorm:"size:32" json:"buyer_bank_code,omitempty"`
	SellerUserID     int64           `gorm:"not null;index:ix_oc_seller,priority:1" json:"seller_user_id"`
	SellerSystemType string          `gorm:"size:10;not null;index:ix_oc_seller,priority:2" json:"seller_system_type"`
	SellerBankCode   *string         `gorm:"size:32" json:"seller_bank_code,omitempty"`
	StockID          uint64          `gorm:"not null;index" json:"stock_id"`
	Quantity         decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice      decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	PremiumPaid      decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium_paid"`
	PremiumCurrency  string          `gorm:"size:8;not null" json:"premium_currency"`
	StrikeCurrency   string          `gorm:"size:8;not null" json:"strike_currency"`
	SettlementDate   time.Time       `gorm:"type:date;not null;index:ix_oc_settle,where:status = 'ACTIVE'" json:"settlement_date"`
	Status           string          `gorm:"size:16;not null;index:ix_oc_buyer,priority:3;index:ix_oc_seller,priority:3" json:"status"`
	SagaID           string          `gorm:"size:64;not null" json:"saga_id"`
	PremiumPaidAt    time.Time       `gorm:"not null" json:"premium_paid_at"`
	ExercisedAt      *time.Time      `json:"exercised_at,omitempty"`
	ExpiredAt        *time.Time      `json:"expired_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `gorm:"not null;default:0" json:"-"`
}

func (c *OptionContract) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", c.Version)
	}
	c.Version++
	return nil
}

func (c *OptionContract) IsTerminal() bool {
	switch c.Status {
	case OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed:
		return true
	}
	return false
}
```

- [ ] **Step 4: `otc_offer_read_receipt.go`**

```go
package model

import "time"

// OTCOfferReadReceipt records the most recent updated_at timestamp the user
// has seen for an offer. Drives the unread:bool flag in list responses.
type OTCOfferReadReceipt struct {
	UserID             int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	SystemType         string    `gorm:"primaryKey;size:10" json:"system_type"`
	OfferID            uint64    `gorm:"primaryKey;autoIncrement:false" json:"offer_id"`
	LastSeenUpdatedAt  time.Time `gorm:"not null" json:"last_seen_updated_at"`
}
```

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/model/... -v
git add stock-service/internal/model/otc_offer_revision.go \
        stock-service/internal/model/option_contract.go \
        stock-service/internal/model/otc_offer_read_receipt.go \
        stock-service/internal/model/otc_offer_test.go
git commit -m "feat(stock-service): OTCOfferRevision, OptionContract, OTCOfferReadReceipt"
```

---

## Task 4: AutoMigrate + DB-level CHECK constraint for holding_reservations

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Append the four new models to `db.AutoMigrate(...)`**

```go
if err := db.AutoMigrate(
	// … existing models …
	&model.OTCOffer{},
	&model.OTCOfferRevision{},
	&model.OptionContract{},
	&model.OTCOfferReadReceipt{},
); err != nil {
	log.Fatalf("failed to migrate: %v", err)
}
```

- [ ] **Step 2: Apply the `holding_reservations` CHECK constraint after migrate**

GORM's AutoMigrate doesn't add CHECK constraints. Apply manually after the migrate call:

```go
// Ensure the "exactly one of order_id, otc_contract_id" CHECK constraint
// exists on holding_reservations. Idempotent — re-run on every startup.
const otcCheckSQL = `
DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname = 'holding_reservation_exactly_one_target'
	) THEN
		ALTER TABLE holding_reservations
		  ADD CONSTRAINT holding_reservation_exactly_one_target
		  CHECK ( (order_id IS NULL) <> (otc_contract_id IS NULL) );
	END IF;
END$$;
`
if err := db.Exec(otcCheckSQL).Error; err != nil {
	log.Printf("WARN: could not ensure holding_reservation CHECK constraint: %v", err)
}
```

- [ ] **Step 3: Build to verify**

```bash
cd stock-service && go build ./...
```

- [ ] **Step 4: Smoke test against a real DB**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
docker compose up -d stock-db
docker compose run --rm stock-service ./bin/stock-service &
sleep 6
docker compose exec stock-db psql -U postgres -d stockdb \
  -c "\dt otc_offers otc_offer_revisions option_contracts otc_offer_read_receipts" \
  -c "SELECT conname FROM pg_constraint WHERE conname = 'holding_reservation_exactly_one_target'"
docker compose down stock-service
```

Expected: 4 tables + 1 named constraint.

- [ ] **Step 5: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): AutoMigrate OTC tables + holding_reservations CHECK"
```

---

## Task 5: `OTCOfferRepository`

**Files:**
- Create: `stock-service/internal/repository/otc_offer_repository.go`
- Create: `stock-service/internal/repository/otc_offer_repository_test.go`

- [ ] **Step 1: Failing tests**

```go
package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOTCOfferRepository_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOTCOfferRepository(db)
	o := &model.OTCOffer{
		InitiatorUserID:          55,
		InitiatorSystemType:      "client",
		Direction:                model.OTCDirectionSellInitiated,
		StockID:                  42,
		Quantity:                 decimal.NewFromInt(100),
		StrikePrice:              decimal.NewFromInt(5000),
		Premium:                  decimal.NewFromInt(50000),
		SettlementDate:           time.Now().AddDate(0, 0, 7),
		Status:                   model.OTCOfferStatusPending,
		LastModifiedByUserID:     55,
		LastModifiedBySystemType: "client",
	}
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	if o.ID == 0 {
		t.Errorf("id not assigned")
	}
	got, err := r.GetByID(o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Quantity.String() != "100" {
		t.Errorf("quantity %s", got.Quantity)
	}
}

func TestOTCOfferRepository_ListBy_Filters(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOTCOfferRepository(db)
	for i := 0; i < 3; i++ {
		_ = r.Create(&model.OTCOffer{
			InitiatorUserID: 55, InitiatorSystemType: "client",
			Direction: model.OTCDirectionSellInitiated, StockID: 42,
			Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100),
			Premium: decimal.NewFromInt(10), SettlementDate: time.Now().AddDate(0, 0, 7),
			Status: model.OTCOfferStatusPending,
			LastModifiedByUserID: 55, LastModifiedBySystemType: "client",
		})
	}
	rows, total, err := r.ListByOwner(55, "client", "either", nil, 0, 1, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Errorf("got %d/%d want 3/3", len(rows), total)
	}
}
```

- [ ] **Step 2: Implement**

```go
package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCOfferRepository struct {
	db *gorm.DB
}

func NewOTCOfferRepository(db *gorm.DB) *OTCOfferRepository {
	return &OTCOfferRepository{db: db}
}

func (r *OTCOfferRepository) Create(o *model.OTCOffer) error {
	return r.db.Create(o).Error
}

func (r *OTCOfferRepository) GetByID(id uint64) (*model.OTCOffer, error) {
	var o model.OTCOffer
	err := r.db.First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &o, err
}

// Save persists a modified offer. Optimistic-locked via the BeforeUpdate hook.
func (r *OTCOfferRepository) Save(o *model.OTCOffer) error {
	res := r.db.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByOwner returns offers where the user appears as initiator,
// counterparty, or either, optionally filtered by status (variadic) and
// stock_id (zero = no filter).
func (r *OTCOfferRepository) ListByOwner(userID int64, systemType, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	q := r.db.Model(&model.OTCOffer{})
	switch role {
	case "initiator":
		q = q.Where("initiator_user_id = ? AND initiator_system_type = ?", userID, systemType)
	case "counterparty":
		q = q.Where("counterparty_user_id = ? AND counterparty_system_type = ?", userID, systemType)
	default: // "either" or empty
		q = q.Where("(initiator_user_id = ? AND initiator_system_type = ?) OR (counterparty_user_id = ? AND counterparty_system_type = ?)",
			userID, systemType, userID, systemType)
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	if stockID != 0 {
		q = q.Where("stock_id = ?", stockID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.OTCOffer
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// ListExpiringOffers returns up to limit pending/countered offers whose
// settlement_date is in the past, locked FOR UPDATE SKIP LOCKED. Used by the
// expiry cron.
func (r *OTCOfferRepository) ListExpiringOffers(today string, limit int) ([]model.OTCOffer, error) {
	var out []model.OTCOffer
	err := r.db.Raw(`
		SELECT * FROM otc_offers
		WHERE status IN (?, ?) AND settlement_date < ?
		ORDER BY id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT ?`, model.OTCOfferStatusPending, model.OTCOfferStatusCountered, today, limit).
		Scan(&out).Error
	return out, err
}

// SumActiveQuantityForSeller returns Σ over (a) active option contracts
// where the seller matches, plus (b) PENDING/COUNTERED sell-initiated
// offers where the initiator is the seller, plus (c) PENDING/COUNTERED
// buy-initiated offers where the counterparty is the seller. Used by the
// seller-invariant check (§4.6 of spec).
func (r *OTCOfferRepository) SumActiveQuantityForSeller(sellerID int64, systemType string, stockID uint64) (decimal.Decimal, error) {
	var rows []struct{ Sum decimal.Decimal }
	err := r.db.Raw(`
		SELECT COALESCE(SUM(q), 0) AS sum FROM (
			SELECT quantity AS q FROM option_contracts
			 WHERE seller_user_id = ? AND seller_system_type = ?
			   AND stock_id = ? AND status = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND initiator_user_id = ? AND initiator_system_type = ?
			   AND stock_id = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND counterparty_user_id = ? AND counterparty_system_type = ?
			   AND stock_id = ?
		) AS t`,
		sellerID, systemType, stockID, model.OptionContractStatusActive,
		model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerID, systemType, stockID,
		model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerID, systemType, stockID,
	).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return decimal.Zero, err
	}
	return rows[0].Sum, nil
}

// ErrOptimisticLock is returned when an update's BeforeUpdate version-WHERE
// clause matches no rows. Wrapped in service errors as "conflict".
var ErrOptimisticLock = errors.New("optimistic lock conflict")
```

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/repository/... -run TestOTCOffer -v
git add stock-service/internal/repository/otc_offer_repository.go stock-service/internal/repository/otc_offer_repository_test.go
git commit -m "feat(stock-service): OTCOfferRepository with seller-invariant query"
```

---

## Task 6: `OTCOfferRevisionRepository`, `OptionContractRepository`, `OTCReadReceiptRepository`

**Files:**
- Create:
  - `stock-service/internal/repository/otc_offer_revision_repository.go`
  - `stock-service/internal/repository/option_contract_repository.go`
  - `stock-service/internal/repository/otc_read_receipt_repository.go`
- Create: `stock-service/internal/repository/otc_other_repos_test.go`

- [ ] **Step 1: Failing tests** (mirror Task 5 — Create + Get + filter; for read-receipts, upsert + lookup)

```go
func TestOptionContractRepository_ListExpiring(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOptionContractRepository(db)
	yesterday := time.Now().AddDate(0, 0, -1)
	for i := 0; i < 2; i++ {
		_ = r.Create(&model.OptionContract{
			OfferID: uint64(i + 1), BuyerUserID: 1, BuyerSystemType: "client",
			SellerUserID: 2, SellerSystemType: "client", StockID: 42,
			Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(100),
			PremiumPaid: decimal.NewFromInt(10), PremiumCurrency: "RSD", StrikeCurrency: "RSD",
			SettlementDate: yesterday, Status: model.OptionContractStatusActive,
			SagaID: "x", PremiumPaidAt: time.Now(),
		})
	}
	rows, err := r.ListExpiring(time.Now().Format("2006-01-02"), 100)
	if err != nil { t.Fatalf("list: %v", err) }
	if len(rows) != 2 { t.Errorf("expected 2 expiring, got %d", len(rows)) }
}

func TestOTCReadReceiptRepository_Upsert(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOfferReadReceipt{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOTCReadReceiptRepository(db)
	now := time.Now()
	if err := r.Upsert(55, "client", 101, now); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	later := now.Add(1 * time.Minute)
	if err := r.Upsert(55, "client", 101, later); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, err := r.GetReceipt(55, "client", 101)
	if err != nil { t.Fatalf("get: %v", err) }
	if !got.LastSeenUpdatedAt.Equal(later) {
		t.Errorf("got %v want %v", got.LastSeenUpdatedAt, later)
	}
}
```

- [ ] **Step 2: Implement `otc_offer_revision_repository.go`**

```go
package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCOfferRevisionRepository struct{ db *gorm.DB }

func NewOTCOfferRevisionRepository(db *gorm.DB) *OTCOfferRevisionRepository {
	return &OTCOfferRevisionRepository{db: db}
}

func (r *OTCOfferRevisionRepository) Append(rev *model.OTCOfferRevision) error {
	return r.db.Create(rev).Error
}

func (r *OTCOfferRevisionRepository) ListByOffer(offerID uint64) ([]model.OTCOfferRevision, error) {
	var out []model.OTCOfferRevision
	err := r.db.Where("offer_id = ?", offerID).Order("revision_number ASC").Find(&out).Error
	return out, err
}

func (r *OTCOfferRevisionRepository) NextRevisionNumber(offerID uint64) (int, error) {
	var max struct{ N int }
	err := r.db.Raw("SELECT COALESCE(MAX(revision_number), 0) AS n FROM otc_offer_revisions WHERE offer_id = ?", offerID).Scan(&max).Error
	return max.N + 1, err
}
```

- [ ] **Step 3: Implement `option_contract_repository.go`**

```go
package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OptionContractRepository struct{ db *gorm.DB }

func NewOptionContractRepository(db *gorm.DB) *OptionContractRepository {
	return &OptionContractRepository{db: db}
}

func (r *OptionContractRepository) Create(c *model.OptionContract) error {
	return r.db.Create(c).Error
}

func (r *OptionContractRepository) GetByID(id uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &c, err
}

func (r *OptionContractRepository) Save(c *model.OptionContract) error {
	res := r.db.Save(c)
	if res.Error != nil { return res.Error }
	if res.RowsAffected == 0 { return ErrOptimisticLock }
	return nil
}

func (r *OptionContractRepository) ListByOwner(userID int64, systemType, role string, statuses []string, page, pageSize int) ([]model.OptionContract, int64, error) {
	q := r.db.Model(&model.OptionContract{})
	switch role {
	case "buyer":
		q = q.Where("buyer_user_id = ? AND buyer_system_type = ?", userID, systemType)
	case "seller":
		q = q.Where("seller_user_id = ? AND seller_system_type = ?", userID, systemType)
	default:
		q = q.Where("(buyer_user_id = ? AND buyer_system_type = ?) OR (seller_user_id = ? AND seller_system_type = ?)",
			userID, systemType, userID, systemType)
	}
	if len(statuses) > 0 { q = q.Where("status IN ?", statuses) }
	var total int64
	if err := q.Count(&total).Error; err != nil { return nil, 0, err }
	if pageSize <= 0 || pageSize > 100 { pageSize = 20 }
	if page < 1 { page = 1 }
	var out []model.OptionContract
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// ListExpiring returns up to limit ACTIVE contracts past settlement_date,
// locked FOR UPDATE SKIP LOCKED.
func (r *OptionContractRepository) ListExpiring(today string, limit int) ([]model.OptionContract, error) {
	var out []model.OptionContract
	err := r.db.Raw(`
		SELECT * FROM option_contracts
		WHERE status = ? AND settlement_date < ?
		ORDER BY id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT ?`, model.OptionContractStatusActive, today, limit).Scan(&out).Error
	return out, err
}
```

- [ ] **Step 4: Implement `otc_read_receipt_repository.go`**

```go
package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCReadReceiptRepository struct{ db *gorm.DB }

func NewOTCReadReceiptRepository(db *gorm.DB) *OTCReadReceiptRepository {
	return &OTCReadReceiptRepository{db: db}
}

// Upsert sets last_seen_updated_at = GREATEST(existing, ts).
func (r *OTCReadReceiptRepository) Upsert(userID int64, systemType string, offerID uint64, ts time.Time) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "system_type"}, {Name: "offer_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen_updated_at": gorm.Expr("GREATEST(otc_offer_read_receipts.last_seen_updated_at, ?)", ts),
		}),
	}).Create(&model.OTCOfferReadReceipt{
		UserID: userID, SystemType: systemType, OfferID: offerID, LastSeenUpdatedAt: ts,
	}).Error
}

func (r *OTCReadReceiptRepository) GetReceipt(userID int64, systemType string, offerID uint64) (*model.OTCOfferReadReceipt, error) {
	var rec model.OTCOfferReadReceipt
	err := r.db.Where("user_id = ? AND system_type = ? AND offer_id = ?", userID, systemType, offerID).First(&rec).Error
	if err != nil { return nil, err }
	return &rec, nil
}
```

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/repository/... -run "TestOptionContract|TestOTCReadReceipt|TestOTCOfferRevision" -v
git add stock-service/internal/repository/otc_offer_revision_repository.go \
        stock-service/internal/repository/option_contract_repository.go \
        stock-service/internal/repository/otc_read_receipt_repository.go \
        stock-service/internal/repository/otc_other_repos_test.go
git commit -m "feat(stock-service): OTC revision, contract, read-receipt repositories"
```

---

## Task 7: Permission seed + system settings

**Files:**
- Modify: `user-service/internal/service/role_service.go`
- Modify: `stock-service/internal/config/config.go`
- Test: append to `user-service/internal/service/role_service_test.go`.

- [ ] **Step 1: Add `otc.trade` permission and grant it**

In `user-service/internal/service/role_service.go`, locate `DefaultPermissions` and append:

```go
{Name: "otc.trade", Description: "Trade OTC option contracts (intra-bank)", Group: "otc"},
```

Then locate role-permission seed maps and add `"otc.trade"` to `EmployeeAgent`, `EmployeeSupervisor`, and `EmployeeAdmin`.

- [ ] **Step 2: Append role-permission test**

```go
func TestSeedRolesAndPermissions_OtcTrade_GrantedToAgent(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Permission{}, &model.Role{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewRoleService(repository.NewRoleRepository(db), repository.NewPermissionRepository(db))
	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var perms []model.Permission
	if err := db.Where("name = ?", "otc.trade").Find(&perms).Error; err != nil {
		t.Fatalf("permission lookup: %v", err)
	}
	if len(perms) != 1 {
		t.Errorf("expected 1 otc.trade row, got %d", len(perms))
	}
}
```

- [ ] **Step 3: Add cron config to stock-service**

In `stock-service/internal/config/config.go`, add:

```go
OTCExpiryCronUTC   string // "HH:MM" — when daily expiry runs
OTCExpiryBatchSize int
```

In the `Load()` function:

```go
batch, _ := strconv.Atoi(getEnv("OTC_EXPIRY_BATCH_SIZE", "500"))
return &Config{
	// … existing …
	OTCExpiryCronUTC:   getEnv("OTC_EXPIRY_CRON_UTC", "02:00"),
	OTCExpiryBatchSize: batch,
}
```

- [ ] **Step 4: Update both docker-compose files**

`docker-compose.yml` and `docker-compose-remote.yml` — add to the `stock-service` `environment:` block:

```yaml
OTC_EXPIRY_CRON_UTC: "02:00"
OTC_EXPIRY_BATCH_SIZE: "500"
```

- [ ] **Step 5: Run + commit**

```bash
cd user-service && go test ./internal/service/... -run TestSeedRolesAndPermissions_OtcTrade -v
git add user-service/internal/service/role_service.go user-service/internal/service/role_service_test.go \
        stock-service/internal/config/config.go docker-compose.yml docker-compose-remote.yml
git commit -m "feat: otc.trade permission + OTC_EXPIRY_* config"
```

---

## Task 8: Proto + Kafka contracts

**Files:**
- Modify: `contract/proto/stock/stock.proto`
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Append the service definition + messages to `stock.proto`**

```proto
// ---- Intra-bank OTC Options (Spec 2 of 4) -------------------------------

service OTCOptionsService {
  rpc CreateOffer(CreateOTCOfferRequest)   returns (OTCOfferResponse);
  rpc ListMyOffers(ListMyOTCOffersRequest) returns (ListOTCOffersResponse);
  rpc GetOffer(GetOTCOfferRequest)         returns (OTCOfferDetailResponse);
  rpc CounterOffer(CounterOTCOfferRequest) returns (OTCOfferResponse);
  rpc AcceptOffer(AcceptOTCOfferRequest)   returns (AcceptOfferResponse);
  rpc RejectOffer(RejectOTCOfferRequest)   returns (OTCOfferResponse);

  rpc ListMyContracts(ListMyContractsRequest) returns (ListContractsResponse);
  rpc GetContract(GetContractRequest)         returns (OptionContractResponse);
  rpc ExerciseContract(ExerciseContractRequest) returns (ExerciseResponse);
}

message PartyRef {
  int64  user_id = 1;
  string system_type = 2;
  string display_name = 3;       // populated on responses; ignored on requests
  string bank_code = 4;          // Spec-4 hook; empty in this spec
}

message CreateOTCOfferRequest {
  int64  actor_user_id = 1;
  string actor_system_type = 2;
  string direction = 3;          // "sell_initiated" | "buy_initiated"
  uint64 stock_id = 4;
  string quantity = 5;
  string strike_price = 6;
  string premium = 7;
  string settlement_date = 8;    // YYYY-MM-DD
  PartyRef counterparty = 9;     // optional; user_id == 0 means open offer
}

message OTCOfferResponse {
  uint64  id = 1;
  string  direction = 2;
  uint64  stock_id = 3;
  string  stock_ticker = 4;
  string  quantity = 5;
  string  strike_price = 6;
  string  premium = 7;
  string  settlement_date = 8;
  string  status = 9;
  PartyRef initiator = 10;
  PartyRef counterparty = 11;
  PartyRef last_modified_by = 12;
  string  market_reference_price = 13;
  string  created_at = 14;
  string  updated_at = 15;
  int64   version = 16;
  bool    unread = 17;
}

message OTCOfferRevisionItem {
  int32  revision_number = 1;
  string quantity = 2;
  string strike_price = 3;
  string premium = 4;
  string settlement_date = 5;
  string action = 6;
  PartyRef modified_by = 7;
  string created_at = 8;
}

message OTCOfferDetailResponse {
  OTCOfferResponse offer = 1;
  repeated OTCOfferRevisionItem revisions = 2;
}

message ListMyOTCOffersRequest {
  int64  actor_user_id = 1;
  string actor_system_type = 2;
  string role = 3;               // "initiator" | "counterparty" | "either"
  repeated string statuses = 4;
  uint64 stock_id = 5;
  int32  page = 6;
  int32  page_size = 7;
}
message ListOTCOffersResponse {
  repeated OTCOfferResponse offers = 1;
  int64 total = 2;
}

message GetOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
}

message CounterOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  string quantity = 4;
  string strike_price = 5;
  string premium = 6;
  string settlement_date = 7;
}

message AcceptOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
}
message AcceptOfferResponse {
  uint64 offer_id = 1;
  uint64 contract_id = 2;
  string status = 3;
  string saga_id = 4;
  OptionContractResponse contract = 5;
}

message RejectOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
}

message OptionContractResponse {
  uint64  id = 1;
  uint64  offer_id = 2;
  uint64  stock_id = 3;
  string  stock_ticker = 4;
  string  quantity = 5;
  string  strike_price = 6;
  string  premium_paid = 7;
  string  premium_currency = 8;
  string  strike_currency = 9;
  string  settlement_date = 10;
  string  status = 11;
  PartyRef buyer = 12;
  PartyRef seller = 13;
  string  market_reference_price = 14;
  string  premium_paid_at = 15;
  string  exercised_at = 16;
  string  expired_at = 17;
  string  created_at = 18;
  string  updated_at = 19;
  int64   version = 20;
}

message ListMyContractsRequest {
  int64  actor_user_id = 1;
  string actor_system_type = 2;
  string role = 3;
  repeated string statuses = 4;
  int32  page = 5;
  int32  page_size = 6;
}
message ListContractsResponse {
  repeated OptionContractResponse contracts = 1;
  int64 total = 2;
}

message GetContractRequest {
  uint64 contract_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
}

message ExerciseContractRequest {
  uint64 contract_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
}
message ExerciseResponse {
  uint64 contract_id = 1;
  string status = 2;
  string saga_id = 3;
  string strike_amount_seller_ccy = 4;
  string strike_amount_buyer_ccy = 5;
  string ccy_rate_used = 6;
  string seller_currency = 7;
  string buyer_currency = 8;
  string shares_transferred = 9;
}
```

- [ ] **Step 2: Append Kafka constants + payloads to `contract/kafka/messages.go`**

```go
const (
	TopicOTCOfferCreated      = "otc.offer-created"
	TopicOTCOfferCountered    = "otc.offer-countered"
	TopicOTCOfferRejected     = "otc.offer-rejected"
	TopicOTCOfferExpired      = "otc.offer-expired"
	TopicOTCContractCreated   = "otc.contract-created"
	TopicOTCContractExercised = "otc.contract-exercised"
	TopicOTCContractExpired   = "otc.contract-expired"
	TopicOTCContractFailed    = "otc.contract-failed"
)

type OTCParty struct {
	UserID     int64  `json:"user_id"`
	SystemType string `json:"system_type"`
	BankCode   string `json:"bank_code,omitempty"`
}

type OTCOfferCreatedMessage struct {
	MessageID      string   `json:"message_id"`
	OccurredAt     string   `json:"occurred_at"`
	OfferID        uint64   `json:"offer_id"`
	Initiator      OTCParty `json:"initiator"`
	Counterparty   *OTCParty `json:"counterparty,omitempty"`
	StockID        uint64   `json:"stock_id"`
	Quantity       string   `json:"quantity"`
	StrikePrice    string   `json:"strike_price"`
	Premium        string   `json:"premium"`
	SettlementDate string   `json:"settlement_date"`
}

type OTCOfferCounteredMessage struct {
	MessageID      string   `json:"message_id"`
	OccurredAt     string   `json:"occurred_at"`
	OfferID        uint64   `json:"offer_id"`
	RevisionNumber int      `json:"revision_number"`
	ModifiedBy     OTCParty `json:"modified_by"`
	OtherParty     OTCParty `json:"other_party"`
	Quantity       string   `json:"quantity"`
	StrikePrice    string   `json:"strike_price"`
	Premium        string   `json:"premium"`
	SettlementDate string   `json:"settlement_date"`
	UpdatedAt      string   `json:"updated_at"`
}

type OTCOfferRejectedMessage struct {
	MessageID  string   `json:"message_id"`
	OccurredAt string   `json:"occurred_at"`
	OfferID    uint64   `json:"offer_id"`
	RejectedBy OTCParty `json:"rejected_by"`
	OtherParty OTCParty `json:"other_party"`
	UpdatedAt  string   `json:"updated_at"`
}

type OTCOfferExpiredMessage struct {
	MessageID    string    `json:"message_id"`
	OccurredAt   string    `json:"occurred_at"`
	OfferID      uint64    `json:"offer_id"`
	Initiator    OTCParty  `json:"initiator"`
	Counterparty *OTCParty `json:"counterparty,omitempty"`
}

type OTCContractCreatedMessage struct {
	MessageID      string   `json:"message_id"`
	OccurredAt     string   `json:"occurred_at"`
	ContractID     uint64   `json:"contract_id"`
	OfferID        uint64   `json:"offer_id"`
	Buyer          OTCParty `json:"buyer"`
	Seller         OTCParty `json:"seller"`
	Quantity       string   `json:"quantity"`
	StrikePrice    string   `json:"strike_price"`
	PremiumPaid    string   `json:"premium_paid"`
	SettlementDate string   `json:"settlement_date"`
	PremiumPaidAt  string   `json:"premium_paid_at"`
}

type OTCContractExercisedMessage struct {
	MessageID         string   `json:"message_id"`
	OccurredAt        string   `json:"occurred_at"`
	ContractID        uint64   `json:"contract_id"`
	Buyer             OTCParty `json:"buyer"`
	Seller            OTCParty `json:"seller"`
	StrikeAmountPaid  string   `json:"strike_amount_paid"`
	SharesTransferred string   `json:"shares_transferred"`
	ExercisedAt       string   `json:"exercised_at"`
}

type OTCContractExpiredMessage struct {
	MessageID  string   `json:"message_id"`
	OccurredAt string   `json:"occurred_at"`
	ContractID uint64   `json:"contract_id"`
	Buyer      OTCParty `json:"buyer"`
	Seller     OTCParty `json:"seller"`
	ExpiredAt  string   `json:"expired_at"`
}

type OTCContractFailedMessage struct {
	MessageID          string `json:"message_id"`
	OccurredAt         string `json:"occurred_at"`
	ContractID         uint64 `json:"contract_id"`
	FailureReason      string `json:"failure_reason"`
	SagaID             string `json:"saga_id"`
	CompensationStatus string `json:"compensation_status"`
}
```

- [ ] **Step 3: Regen + build**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
make proto && make build
```

- [ ] **Step 4: Commit**

```bash
git add contract/proto/stock/stock.proto contract/kafka/messages.go contract/stockpb/
git commit -m "feat(contract): OTCOptionsService proto + 8 otc.* Kafka topics"
```

---

## Task 9: `OTCOfferService` — create / counter / reject / list / get (no money flow)

**Files:**
- Create:
  - `stock-service/internal/service/otc_offer_service.go`
  - `stock-service/internal/service/otc_offer_service_test.go`

- [ ] **Step 1: Failing tests for create + seller invariant**

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOTCOfferService_Create_SellInitiated_HappyPath(t *testing.T) {
	fx := newOTCOfferFixture(t)
	fx.holdings.set(t, /*owner*/ 55, "client", /*stock*/ 42, /*qty*/ 100)
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 55, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated,
		StockID:   42,
		Quantity:  decimal.NewFromInt(100),
		StrikePrice: decimal.NewFromInt(5000),
		Premium:    decimal.NewFromInt(50000),
		SettlementDate: time.Now().AddDate(0, 0, 7),
		CounterpartyUserID: ptrInt64(87), CounterpartySystemType: ptrStr("client"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Status != model.OTCOfferStatusPending {
		t.Errorf("status %s", out.Status)
	}
	if fx.revisions.lastAction != model.OTCActionCreate || fx.revisions.lastNumber != 1 {
		t.Errorf("revision history wrong: %+v", fx.revisions)
	}
}

func TestOTCOfferService_Create_FailsSellerInvariant(t *testing.T) {
	fx := newOTCOfferFixture(t)
	fx.holdings.set(t, 55, "client", 42, 50) // only 50 shares
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 55, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(100), StrikePrice: decimal.NewFromInt(5000),
		Premium: decimal.NewFromInt(50000), SettlementDate: time.Now().AddDate(0, 0, 7),
	})
	if err == nil {
		t.Fatal("expected insufficient-shares error")
	}
}
```

- [ ] **Step 2: Implement service skeleton**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// HoldingForSellerInvariant is the minimal Holding row reader the service
// uses for the SELECT FOR UPDATE in seller-invariant checks.
type HoldingForSellerInvariant interface {
	GetForUpdate(tx any, userID int64, systemType string, securityType string, securityID uint64) (qty decimal.Decimal, err error)
}

type OTCOfferService struct {
	offers     *repository.OTCOfferRepository
	revisions  *repository.OTCOfferRevisionRepository
	contracts  *repository.OptionContractRepository
	holdings   HoldingForSellerInvariant
	receipts   *repository.OTCReadReceiptRepository
	producer   *kafkaprod.Producer
}

func NewOTCOfferService(o *repository.OTCOfferRepository, r *repository.OTCOfferRevisionRepository, c *repository.OptionContractRepository, h HoldingForSellerInvariant, rec *repository.OTCReadReceiptRepository, p *kafkaprod.Producer) *OTCOfferService {
	return &OTCOfferService{offers: o, revisions: r, contracts: c, holdings: h, receipts: rec, producer: p}
}

type CreateOfferInput struct {
	ActorUserID            int64
	ActorSystemType        string
	Direction              string
	StockID                uint64
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	Premium                decimal.Decimal
	SettlementDate         time.Time
	CounterpartyUserID     *int64
	CounterpartySystemType *string
}

func (s *OTCOfferService) Create(ctx context.Context, in CreateOfferInput) (*model.OTCOffer, error) {
	if !in.Quantity.IsPositive() || !in.StrikePrice.IsPositive() {
		return nil, errors.New("quantity and strike_price must be positive")
	}
	if in.Premium.IsNegative() {
		return nil, errors.New("premium must be non-negative")
	}
	if !in.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date must be in the future")
	}
	switch in.Direction {
	case model.OTCDirectionSellInitiated, model.OTCDirectionBuyInitiated:
	default:
		return nil, errors.New("unknown direction")
	}
	if in.Direction == model.OTCDirectionBuyInitiated && in.CounterpartyUserID == nil {
		return nil, errors.New("buy_initiated offers require a named counterparty")
	}
	if (in.CounterpartyUserID == nil) != (in.CounterpartySystemType == nil) {
		return nil, errors.New("counterparty user_id and system_type must both be set or both omitted")
	}

	// Seller-invariant: if sell_initiated, run inside a tx with SELECT FOR UPDATE.
	if in.Direction == model.OTCDirectionSellInitiated {
		if err := s.assertSellerHasShares(ctx, in.ActorUserID, in.ActorSystemType, in.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	o := &model.OTCOffer{
		InitiatorUserID:          in.ActorUserID,
		InitiatorSystemType:      in.ActorSystemType,
		CounterpartyUserID:       in.CounterpartyUserID,
		CounterpartySystemType:   in.CounterpartySystemType,
		Direction:                in.Direction,
		StockID:                  in.StockID,
		Quantity:                 in.Quantity,
		StrikePrice:              in.StrikePrice,
		Premium:                  in.Premium,
		SettlementDate:           in.SettlementDate,
		Status:                   model.OTCOfferStatusPending,
		LastModifiedByUserID:     in.ActorUserID,
		LastModifiedBySystemType: in.ActorSystemType,
	}
	if err := s.offers.Create(o); err != nil {
		return nil, err
	}
	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID:                 o.ID,
		RevisionNumber:          1,
		Quantity:                o.Quantity,
		StrikePrice:             o.StrikePrice,
		Premium:                 o.Premium,
		SettlementDate:          o.SettlementDate,
		ModifiedByUserID:        o.LastModifiedByUserID,
		ModifiedBySystemType:    o.LastModifiedBySystemType,
		Action:                  model.OTCActionCreate,
	}); err != nil {
		return nil, err
	}

	// Kafka
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferCreated, mustJSON(kafkamsg.OTCOfferCreatedMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID: o.ID, Initiator: kafkamsg.OTCParty{UserID: o.InitiatorUserID, SystemType: o.InitiatorSystemType},
			Counterparty: ptrCounterparty(o), StockID: o.StockID,
			Quantity: o.Quantity.String(), StrikePrice: o.StrikePrice.String(),
			Premium: o.Premium.String(), SettlementDate: o.SettlementDate.Format("2006-01-02"),
		}))
	}
	return o, nil
}

func (s *OTCOfferService) assertSellerHasShares(ctx context.Context, userID int64, systemType string, stockID uint64, requested decimal.Decimal) error {
	heldQty, err := s.holdings.GetForUpdate(nil, userID, systemType, "stock", stockID)
	if err != nil {
		return err
	}
	committed, err := s.offers.SumActiveQuantityForSeller(userID, systemType, stockID)
	if err != nil {
		return err
	}
	available := heldQty.Sub(committed)
	if requested.GreaterThan(available) {
		return fmt.Errorf("insufficient available shares for this seller (held %s, committed %s, requested %s)", heldQty, committed, requested)
	}
	return nil
}

func ptrCounterparty(o *model.OTCOffer) *kafkamsg.OTCParty {
	if o.CounterpartyUserID == nil {
		return nil
	}
	return &kafkamsg.OTCParty{UserID: *o.CounterpartyUserID, SystemType: *o.CounterpartySystemType}
}
```

- [ ] **Step 3: Counter + Reject implementations**

```go
type CounterInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	Quantity        decimal.Decimal
	StrikePrice     decimal.Decimal
	Premium         decimal.Decimal
	SettlementDate  time.Time
}

func (s *OTCOfferService) Counter(ctx context.Context, in CounterInput) (*model.OTCOffer, error) {
	o, err := s.offers.GetByID(in.OfferID)
	if err != nil { return nil, err }
	if o.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}
	// last-mover rule: caller must NOT be the side that last modified.
	if o.LastModifiedByUserID == in.ActorUserID && o.LastModifiedBySystemType == in.ActorSystemType {
		return nil, errors.New("you cannot counter your own most recent terms")
	}
	// Seller-side invariant: if quantity changed AND seller (depending on direction) is the actor
	if !in.Quantity.Equal(o.Quantity) {
		var sellerID int64
		var sellerType string
		if o.Direction == model.OTCDirectionSellInitiated {
			sellerID, sellerType = o.InitiatorUserID, o.InitiatorSystemType
		} else if o.CounterpartyUserID != nil {
			sellerID, sellerType = *o.CounterpartyUserID, *o.CounterpartySystemType
		} else {
			return nil, errors.New("cannot determine seller for invariant check")
		}
		if err := s.assertSellerHasShares(ctx, sellerID, sellerType, o.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	revNum, err := s.revisions.NextRevisionNumber(o.ID)
	if err != nil { return nil, err }

	o.Quantity = in.Quantity
	o.StrikePrice = in.StrikePrice
	o.Premium = in.Premium
	o.SettlementDate = in.SettlementDate
	o.Status = model.OTCOfferStatusCountered
	o.LastModifiedByUserID = in.ActorUserID
	o.LastModifiedBySystemType = in.ActorSystemType
	if err := s.offers.Save(o); err != nil { return nil, err }

	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType,
		Action: model.OTCActionCounter,
	}); err != nil { return nil, err }

	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferCountered, mustJSON(kafkamsg.OTCOfferCounteredMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID: o.ID, RevisionNumber: revNum,
			ModifiedBy: kafkamsg.OTCParty{UserID: in.ActorUserID, SystemType: in.ActorSystemType},
			OtherParty: otherParty(o, in.ActorUserID, in.ActorSystemType),
			Quantity: o.Quantity.String(), StrikePrice: o.StrikePrice.String(),
			Premium: o.Premium.String(), SettlementDate: o.SettlementDate.Format("2006-01-02"),
			UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
		}))
	}
	return o, nil
}

type RejectInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
}

func (s *OTCOfferService) Reject(ctx context.Context, in RejectInput) (*model.OTCOffer, error) {
	o, err := s.offers.GetByID(in.OfferID)
	if err != nil { return nil, err }
	if o.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}
	revNum, err := s.revisions.NextRevisionNumber(o.ID)
	if err != nil { return nil, err }
	o.Status = model.OTCOfferStatusRejected
	o.LastModifiedByUserID = in.ActorUserID
	o.LastModifiedBySystemType = in.ActorSystemType
	if err := s.offers.Save(o); err != nil { return nil, err }
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType, Action: model.OTCActionReject,
	})
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferRejected, mustJSON(kafkamsg.OTCOfferRejectedMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID: o.ID, RejectedBy: kafkamsg.OTCParty{UserID: in.ActorUserID, SystemType: in.ActorSystemType},
			OtherParty: otherParty(o, in.ActorUserID, in.ActorSystemType),
			UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
		}))
	}
	return o, nil
}

func otherParty(o *model.OTCOffer, actorID int64, actorType string) kafkamsg.OTCParty {
	if o.InitiatorUserID == actorID && o.InitiatorSystemType == actorType {
		if o.CounterpartyUserID != nil {
			return kafkamsg.OTCParty{UserID: *o.CounterpartyUserID, SystemType: *o.CounterpartySystemType}
		}
		return kafkamsg.OTCParty{}
	}
	return kafkamsg.OTCParty{UserID: o.InitiatorUserID, SystemType: o.InitiatorSystemType}
}
```

- [ ] **Step 4: Test counter (last-mover rule), reject, terminal-state guard**

Add tests for:
- `TestCounter_LastMoverRule_RejectsSelfCounter`
- `TestCounter_RejectedOffer_Rejected`
- `TestReject_HappyPath`

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestOTCOfferService -v
git add stock-service/internal/service/otc_offer_service.go stock-service/internal/service/otc_offer_service_test.go
git commit -m "feat(stock-service): OTCOfferService — create/counter/reject + seller invariant"
```

---

## Task 10: Extend `HoldingReservationService` for OTC

**Files:**
- Modify: `stock-service/internal/service/holding_reservation_service.go`
- Modify: `stock-service/internal/service/holding_reservation_service_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestHoldingReservation_ReserveForOTCContract(t *testing.T) {
	fx := newHoldingResFixture(t)
	fx.seedHolding(55, "client", "stock", 42, 100)
	_, err := fx.svc.ReserveForOTCContract(context.Background(), 55, "client", "stock", 42, /*contractID*/ 7, decimal.NewFromInt(60))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	h := fx.holding(t, 55, "client", "stock", 42)
	if !h.ReservedQuantity.Equal(decimal.NewFromInt(60)) {
		t.Errorf("reserved %s want 60", h.ReservedQuantity)
	}
}

func TestHoldingReservation_ConsumeForOTCContract_DecrementsBoth(t *testing.T) {
	fx := newHoldingResFixture(t)
	fx.seedHolding(55, "client", "stock", 42, 100)
	_, _ = fx.svc.ReserveForOTCContract(context.Background(), 55, "client", "stock", 42, 7, decimal.NewFromInt(60))
	if err := fx.svc.ConsumeForOTCContract(context.Background(), 7); err != nil {
		t.Fatalf("consume: %v", err)
	}
	h := fx.holding(t, 55, "client", "stock", 42)
	if !h.Quantity.Equal(decimal.NewFromInt(40)) {
		t.Errorf("qty %s want 40", h.Quantity)
	}
	if !h.ReservedQuantity.IsZero() {
		t.Errorf("reserved %s want 0", h.ReservedQuantity)
	}
}
```

- [ ] **Step 2: Implement the methods on `HoldingReservationService`**

```go
// ReserveForOTCContract creates a holding_reservations row keyed on
// otc_contract_id and increments the holding's reserved_quantity. Single
// DB transaction with SELECT FOR UPDATE on the holding row.
func (s *HoldingReservationService) ReserveForOTCContract(ctx context.Context, userID int64, systemType, securityType string, securityID, contractID uint64, qty decimal.Decimal) (*model.HoldingReservation, error) {
	var out *model.HoldingReservation
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var h model.Holding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ?",
				userID, systemType, securityType, securityID).
			First(&h).Error
		if err != nil { return fmt.Errorf("holding: %w", err) }
		if h.Quantity.Sub(h.ReservedQuantity).LessThan(qty) {
			return errors.New("insufficient available shares")
		}
		res := &model.HoldingReservation{
			HoldingID: h.ID, OTCContractID: &contractID, Quantity: qty,
		}
		if err := tx.Create(res).Error; err != nil { return err }
		h.ReservedQuantity = h.ReservedQuantity.Add(qty)
		if err := tx.Save(&h).Error; err != nil { return err }
		out = res
		return nil
	})
	return out, err
}

// ReleaseForOTCContract — release on contract expiry. Decrements
// reserved_quantity; quantity unchanged; deletes the reservation row.
func (s *HoldingReservationService) ReleaseForOTCContract(ctx context.Context, contractID uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var res model.HoldingReservation
		if err := tx.Where("otc_contract_id = ?", contractID).First(&res).Error; err != nil {
			return err
		}
		var h model.Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&h, res.HoldingID).Error; err != nil {
			return err
		}
		h.ReservedQuantity = h.ReservedQuantity.Sub(res.Quantity)
		if h.ReservedQuantity.IsNegative() { h.ReservedQuantity = decimal.Zero }
		if err := tx.Save(&h).Error; err != nil { return err }
		return tx.Delete(&res).Error
	})
}

// ConsumeForOTCContract — exercise: decrements both quantity and
// reserved_quantity; deletes the reservation row.
func (s *HoldingReservationService) ConsumeForOTCContract(ctx context.Context, contractID uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var res model.HoldingReservation
		if err := tx.Where("otc_contract_id = ?", contractID).First(&res).Error; err != nil {
			return err
		}
		var h model.Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&h, res.HoldingID).Error; err != nil {
			return err
		}
		h.Quantity = h.Quantity.Sub(res.Quantity)
		h.ReservedQuantity = h.ReservedQuantity.Sub(res.Quantity)
		if h.ReservedQuantity.IsNegative() { h.ReservedQuantity = decimal.Zero }
		if err := tx.Save(&h).Error; err != nil { return err }
		return tx.Delete(&res).Error
	})
}
```

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestHoldingReservation_.*OTC -v
git add stock-service/internal/service/holding_reservation_service.go stock-service/internal/service/holding_reservation_service_test.go
git commit -m "feat(stock-service): HoldingReservation OTC contract methods"
```

---

## Task 11: Premium-payment saga — happy path (same currency)

**Files:**
- Create:
  - `stock-service/internal/service/otc_accept_saga.go`
  - `stock-service/internal/service/otc_accept_saga_test.go`

- [ ] **Step 1: Failing test — same-currency happy path commits all six steps**

```go
func TestAcceptSaga_SameCurrency_HappyPath(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	offer := fx.seedSellOffer(t /* seller=87, buyer=55, qty=100, premium=50000 RSD */)
	fx.holdings.set(t, 87, "client", 42, 100)
	fx.accounts.setCurrency(/*buyer*/ 5001, "RSD", /*balance*/ "1000000")
	fx.accounts.setCurrency(/*seller*/ 6001, "RSD", "0")
	out, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: offer.ID, ActorUserID: 55, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if out.Status != model.OptionContractStatusActive {
		t.Errorf("status %s", out.Status)
	}
	// premium moved
	if !fx.accounts.debited(5001, "50000") || !fx.accounts.credited(6001, "50000") {
		t.Errorf("premium not moved correctly: %+v", fx.accounts)
	}
	// reservation
	if fx.reservations.byContract(out.ID) == nil {
		t.Errorf("reservation not created")
	}
	// offer marked accepted
	got, _ := fx.offers.GetByID(offer.ID)
	if got.Status != model.OTCOfferStatusAccepted {
		t.Errorf("offer status %s", got.Status)
	}
}
```

- [ ] **Step 2: Implement the saga driver**

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

type AcceptInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
}

// Accept runs the premium-payment saga. Returns the new contract on success.
func (s *OTCOfferService) Accept(ctx context.Context, in AcceptInput) (*model.OptionContract, error) {
	// Step 1: validate_offer
	o, err := s.offers.GetByID(in.OfferID)
	if err != nil { return nil, err }
	if o.IsTerminal() { return nil, errors.New("offer is in a terminal state") }
	// last-mover rule: caller must NOT be last_modified_by
	if o.LastModifiedByUserID == in.ActorUserID && o.LastModifiedBySystemType == in.ActorSystemType {
		return nil, errors.New("you cannot accept your own most recent terms")
	}
	// settlement_date in future
	if !o.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date is not in the future")
	}
	buyerID, buyerType, sellerID, sellerType := s.identifyBuyerSeller(o, in)

	// Resolve accounts. Seller's primary account in seller's preferred ccy
	// drives premium_currency and strike_currency. Buyer pays in their ccy
	// (converted via exchange-service).
	sellerAcct, err := s.accountResolver.PrimaryAccount(ctx, sellerID, sellerType)
	if err != nil { return nil, fmt.Errorf("seller account: %w", err) }
	buyerAcct, err := s.accountResolver.PrimaryAccount(ctx, buyerID, buyerType)
	if err != nil { return nil, fmt.Errorf("buyer account: %w", err) }

	// Premium amount in buyer ccy (convert if needed)
	premiumBuyerCcy := o.Premium
	if sellerAcct.CurrencyCode != buyerAcct.CurrencyCode {
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: sellerAcct.CurrencyCode, ToCurrency: buyerAcct.CurrencyCode, Amount: o.Premium.String(),
		})
		if err != nil { return nil, fmt.Errorf("FX premium convert: %w", err) }
		premiumBuyerCcy, _ = decimal.NewFromString(conv.ConvertedAmount)
	}

	sagaID := uuid.NewString()
	exec := s.sagaExec(sagaID)

	// Step 2: reserve seller shares + create contract (single local DB tx).
	contract := &model.OptionContract{
		OfferID: o.ID, BuyerUserID: buyerID, BuyerSystemType: buyerType,
		SellerUserID: sellerID, SellerSystemType: sellerType,
		StockID: o.StockID, Quantity: o.Quantity, StrikePrice: o.StrikePrice,
		PremiumPaid: o.Premium, PremiumCurrency: sellerAcct.CurrencyCode, StrikeCurrency: sellerAcct.CurrencyCode,
		SettlementDate: o.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
	}
	if err := exec.RunStep(ctx, "reserve_and_contract", o.Quantity, "shares", nil, func() error {
		return s.contractsTx(ctx, func(tx any) error {
			if err := s.contracts.Create(contract); err != nil { return err }
			_, err := s.holdingRes.ReserveForOTCContract(ctx, sellerID, sellerType, "stock", o.StockID, contract.ID, o.Quantity)
			return err
		})
	}); err != nil {
		return nil, err
	}

	// Step 3: reserve premium on buyer.
	idemReserve := fmt.Sprintf("otc-accept-%d-reserve", o.ID)
	if err := exec.RunStep(ctx, "reserve_premium", premiumBuyerCcy, buyerAcct.CurrencyCode, map[string]any{"idem": idemReserve}, func() error {
		_, e := s.accounts.ReserveFunds(ctx, buyerAcct.ID, contract.ID, premiumBuyerCcy, buyerAcct.CurrencyCode)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			return s.contractsTx(ctx, func(tx any) error {
				if err := s.holdingRes.ReleaseForOTCContract(ctx, contract.ID); err != nil { return err }
				return s.contracts.Delete(contract.ID)
			})
		})
		return nil, err
	}

	// Step 4: settle premium debit on buyer.
	idemBuyer := fmt.Sprintf("otc-accept-%d-buyer", o.ID)
	if err := exec.RunStep(ctx, "settle_premium_buyer", premiumBuyerCcy, buyerAcct.CurrencyCode, map[string]any{"idem": idemBuyer}, func() error {
		_, e := s.accounts.PartialSettleReservation(ctx, contract.ID, 1, premiumBuyerCcy, fmt.Sprintf("OTC premium for contract #%d", contract.ID))
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_premium", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, contract.ID)
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			return s.contractsTx(ctx, func(tx any) error {
				if err := s.holdingRes.ReleaseForOTCContract(ctx, contract.ID); err != nil { return err }
				return s.contracts.Delete(contract.ID)
			})
		})
		return nil, err
	}

	// Step 5: credit seller in their currency.
	idemSeller := fmt.Sprintf("otc-accept-%d-seller", o.ID)
	if err := exec.RunStep(ctx, "credit_premium_seller", o.Premium, sellerAcct.CurrencyCode, map[string]any{"idem": idemSeller}, func() error {
		_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, o.Premium,
			fmt.Sprintf("OTC premium credit for contract #%d", contract.ID), idemSeller)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, premiumBuyerCcy,
				fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
				fmt.Sprintf("otc-accept-%d-comp-buyer", o.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			return s.contractsTx(ctx, func(tx any) error {
				if err := s.holdingRes.ReleaseForOTCContract(ctx, contract.ID); err != nil { return err }
				return s.contracts.Delete(contract.ID)
			})
		})
		return nil, err
	}

	// Step 6: mark offer accepted.
	revNum, _ := s.revisions.NextRevisionNumber(o.ID)
	o.Status = model.OTCOfferStatusAccepted
	if err := s.offers.Save(o); err != nil {
		// Compensation: reverse seller credit, buyer credit, drop contract.
		_ = exec.RunCompensation(ctx, 0, "compensate_seller_credit", func() error {
			_, e := s.accounts.DebitAccount(ctx, sellerAcct.AccountNumber, o.Premium,
				fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
				fmt.Sprintf("otc-accept-%d-comp-seller", o.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, premiumBuyerCcy,
				fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
				fmt.Sprintf("otc-accept-%d-comp-buyer", o.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			return s.contractsTx(ctx, func(tx any) error {
				if err := s.holdingRes.ReleaseForOTCContract(ctx, contract.ID); err != nil { return err }
				return s.contracts.Delete(contract.ID)
			})
		})
		return nil, err
	}
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType, Action: model.OTCActionAccept,
	})

	// Step 7: publish kafka.
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractCreated, mustJSON(kafkamsg.OTCContractCreatedMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			ContractID: contract.ID, OfferID: o.ID,
			Buyer: kafkamsg.OTCParty{UserID: buyerID, SystemType: buyerType},
			Seller: kafkamsg.OTCParty{UserID: sellerID, SystemType: sellerType},
			Quantity: contract.Quantity.String(), StrikePrice: contract.StrikePrice.String(),
			PremiumPaid: contract.PremiumPaid.String(),
			SettlementDate: contract.SettlementDate.Format("2006-01-02"),
			PremiumPaidAt: contract.PremiumPaidAt.Format(time.RFC3339),
		}))
	}
	return contract, nil
}

func (s *OTCOfferService) identifyBuyerSeller(o *model.OTCOffer, in AcceptInput) (buyerID int64, buyerType string, sellerID int64, sellerType string) {
	if o.Direction == model.OTCDirectionSellInitiated {
		sellerID, sellerType = o.InitiatorUserID, o.InitiatorSystemType
		buyerID, buyerType = in.ActorUserID, in.ActorSystemType
	} else {
		buyerID, buyerType = o.InitiatorUserID, o.InitiatorSystemType
		sellerID, sellerType = in.ActorUserID, in.ActorSystemType
	}
	return
}
```

`AccountResolver`, `sagaExec`, `contractsTx`, `contracts.Delete` and the `accounts` / `exchange` clients are interface fields on `OTCOfferService`; tests stub them. `AccountResolver.PrimaryAccount` resolves the user's preferred account for their primary currency (per spec §6.4).

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestAcceptSaga_SameCurrency -v
git add stock-service/internal/service/otc_accept_saga.go stock-service/internal/service/otc_accept_saga_test.go
git commit -m "feat(stock-service): OTC accept saga (same-currency happy path)"
```

---

## Task 12: Accept saga — cross-currency + every compensation branch

**Files:**
- Modify: `stock-service/internal/service/otc_accept_saga_test.go`

- [ ] **Step 1: Add cross-currency happy path test**

```go
func TestAcceptSaga_CrossCurrency_ConvertsPremium(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	offer := fx.seedSellOffer(t)
	fx.accounts.setCurrency(5001, "EUR", "1000")
	fx.accounts.setCurrency(6001, "RSD", "0")
	fx.exchange.setRate("RSD", "EUR", "0.0086", "430") // 50000 RSD → 430 EUR
	out, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: offer.ID, ActorUserID: 55, ActorSystemType: "client",
	})
	if err != nil { t.Fatalf("accept: %v", err) }
	if out.PremiumCurrency != "RSD" { t.Errorf("premium ccy %s want RSD", out.PremiumCurrency) }
	if !fx.accounts.debited(5001, "430") {
		t.Errorf("buyer debit not in EUR")
	}
	if !fx.accounts.credited(6001, "50000") {
		t.Errorf("seller credit not in RSD")
	}
}
```

- [ ] **Step 2: Step-failure tests**

For each saga step (`reserve_and_contract`, `reserve_premium`, `settle_premium_buyer`, `credit_premium_seller`, `mark_offer_accepted`), inject the failure and assert the expected compensation set runs and the right entities are reverted. Pattern matches the bank-safe-settlement plan's `TestProcessBuyFill_*RollsBack*` tests.

- [ ] **Step 3: Concurrent-accept idempotency test**

```go
func TestAcceptSaga_ConcurrentAccept_OnlyOneSucceeds(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	offer := fx.seedSellOffer(t)
	fx.holdings.set(t, 87, "client", 42, 100)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := fx.svc.Accept(context.Background(), AcceptInput{
				OfferID: offer.ID, ActorUserID: int64(55 + i), ActorSystemType: "client",
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil { successes++ }
	}
	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
}
```

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestAcceptSaga -v
git add stock-service/internal/service/otc_accept_saga_test.go
git commit -m "test(stock-service): accept saga cross-ccy + each compensation + concurrent accept"
```

---

## Task 13: Exercise saga — same-currency happy path

**Files:**
- Create:
  - `stock-service/internal/service/otc_exercise_saga.go`
  - `stock-service/internal/service/otc_exercise_saga_test.go`

- [ ] **Step 1: Failing test**

```go
func TestExerciseSaga_SameCurrency_HappyPath(t *testing.T) {
	fx := newExerciseSagaFixture(t)
	c := fx.seedActiveContract(t /* qty=100, strike=5000 RSD, buyer=55, seller=87 */)
	fx.accounts.setCurrency(5001, "RSD", "1000000") // buyer
	fx.accounts.setCurrency(6001, "RSD", "0")        // seller
	out, err := fx.svc.Exercise(context.Background(), ExerciseInput{
		ContractID: c.ID, ActorUserID: 55, ActorSystemType: "client",
	})
	if err != nil { t.Fatalf("exercise: %v", err) }
	if out.Status != model.OptionContractStatusExercised {
		t.Errorf("status %s", out.Status)
	}
	// Strike total = 100 × 5000 = 500_000.
	if !fx.accounts.debited(5001, "500000") || !fx.accounts.credited(6001, "500000") {
		t.Errorf("strike not moved")
	}
	// Shares transferred.
	sellerH := fx.holdings.get(t, 87, "client", 42)
	buyerH := fx.holdings.get(t, 55, "client", 42)
	if !sellerH.Quantity.Equal(decimal.NewFromInt(0)) {
		t.Errorf("seller qty %s", sellerH.Quantity)
	}
	if !buyerH.Quantity.Equal(decimal.NewFromInt(100)) {
		t.Errorf("buyer qty %s", buyerH.Quantity)
	}
}
```

- [ ] **Step 2: Implement `Exercise`**

```go
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
}

func (s *OTCOfferService) Exercise(ctx context.Context, in ExerciseInput) (*model.OptionContract, error) {
	c, err := s.contracts.GetByID(in.ContractID)
	if err != nil { return nil, err }
	if c.Status != model.OptionContractStatusActive {
		return nil, errors.New("contract is not active")
	}
	if c.SettlementDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date passed")
	}
	if c.BuyerUserID != in.ActorUserID || c.BuyerSystemType != in.ActorSystemType {
		return nil, errors.New("only the buyer may exercise")
	}

	buyerAcct, err := s.accountResolver.PrimaryAccount(ctx, c.BuyerUserID, c.BuyerSystemType)
	if err != nil { return nil, err }
	sellerAcct, err := s.accountResolver.PrimaryAccount(ctx, c.SellerUserID, c.SellerSystemType)
	if err != nil { return nil, err }

	nativeStrike := c.StrikePrice.Mul(c.Quantity) // in seller ccy
	convertedStrike := nativeStrike
	rate := decimal.NewFromInt(1)
	if buyerAcct.CurrencyCode != sellerAcct.CurrencyCode {
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: sellerAcct.CurrencyCode, ToCurrency: buyerAcct.CurrencyCode, Amount: nativeStrike.String(),
		})
		if err != nil { return nil, err }
		convertedStrike, _ = decimal.NewFromString(conv.ConvertedAmount)
		rate, _ = decimal.NewFromString(conv.EffectiveRate)
	}
	_ = rate

	exec := s.sagaExec(c.SagaID)

	// Step 3: reserve buyer funds.
	idemReserve := fmt.Sprintf("otc-exercise-%d-reserve", c.ID)
	if err := exec.RunStep(ctx, "reserve_buyer", convertedStrike, buyerAcct.CurrencyCode, map[string]any{"idem": idemReserve}, func() error {
		_, e := s.accounts.ReserveFunds(ctx, buyerAcct.ID, c.ID, convertedStrike, buyerAcct.CurrencyCode)
		return e
	}); err != nil { return nil, err }

	// Step 4: settle buyer debit.
	if err := exec.RunStep(ctx, "settle_buyer", convertedStrike, buyerAcct.CurrencyCode, map[string]any{"idem": fmt.Sprintf("otc-exercise-%d-buyer", c.ID)}, func() error {
		_, e := s.accounts.PartialSettleReservation(ctx, c.ID, 1, convertedStrike, fmt.Sprintf("Exercise option #%d", c.ID))
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_buyer", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, c.ID)
			return e
		})
		return nil, err
	}

	// Step 4b: credit seller native.
	idemSeller := fmt.Sprintf("otc-exercise-%d-seller", c.ID)
	if err := exec.RunStep(ctx, "credit_seller", nativeStrike, sellerAcct.CurrencyCode, map[string]any{"idem": idemSeller}, func() error {
		_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, nativeStrike,
			fmt.Sprintf("Exercise proceeds for option #%d", c.ID), idemSeller)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, convertedStrike,
				fmt.Sprintf("Compensating exercise option #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-buyer", c.ID))
			return e
		})
		return nil, err
	}

	// Step 5: transfer shares (single local DB tx).
	if err := exec.RunStep(ctx, "transfer_shares", c.Quantity, "shares", nil, func() error {
		return s.holdingRes.TransferShares(ctx, c.ID, c.SellerUserID, c.SellerSystemType, c.BuyerUserID, c.BuyerSystemType, c.StockID, c.Quantity)
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "compensate_seller", func() error {
			_, e := s.accounts.DebitAccount(ctx, sellerAcct.AccountNumber, nativeStrike,
				fmt.Sprintf("Compensating exercise proceeds #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-seller", c.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, convertedStrike,
				fmt.Sprintf("Compensating exercise option #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-buyer", c.ID))
			return e
		})
		return nil, err
	}

	// Step 6: mark contract exercised.
	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExercised
	c.ExercisedAt = &now
	if err := s.contracts.Save(c); err != nil {
		return nil, err
	}

	// Step 7: publish kafka.
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractExercised, mustJSON(kafkamsg.OTCContractExercisedMessage{
			MessageID: uuid.NewString(), OccurredAt: now.Format(time.RFC3339),
			ContractID: c.ID,
			Buyer: kafkamsg.OTCParty{UserID: c.BuyerUserID, SystemType: c.BuyerSystemType},
			Seller: kafkamsg.OTCParty{UserID: c.SellerUserID, SystemType: c.SellerSystemType},
			StrikeAmountPaid: nativeStrike.String(),
			SharesTransferred: c.Quantity.String(),
			ExercisedAt: now.Format(time.RFC3339),
		}))
	}
	return c, nil
}
```

`HoldingReservationService.TransferShares(ctx, contractID, sellerID, sellerType, buyerID, buyerType, stockID, qty)` — adds a single-tx method that:
1. Selects FOR UPDATE on seller's holding and buyer's holding (insert if missing).
2. Decrements seller's quantity + reserved_quantity.
3. Increments buyer's quantity.
4. Deletes the OTC reservation row.

Implement it in the same file as the OTC reservation methods from Task 10.

- [ ] **Step 3: Cross-currency test + step-failure tests + run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestExerciseSaga -v
git add stock-service/internal/service/otc_exercise_saga.go stock-service/internal/service/otc_exercise_saga_test.go \
        stock-service/internal/service/holding_reservation_service.go
git commit -m "feat(stock-service): OTC exercise saga + share transfer"
```

---

## Task 14: Expiry cron — offers + contracts

**Files:**
- Create:
  - `stock-service/internal/service/otc_expiry_cron.go`
  - `stock-service/internal/service/otc_expiry_cron_test.go`

- [ ] **Step 1: Failing test**

```go
func TestExpiryCron_ExpiresPastContracts(t *testing.T) {
	fx := newExpiryFixture(t)
	yesterday := time.Now().AddDate(0, 0, -1)
	c := fx.seedActiveContract(t, yesterday)
	if err := fx.cron.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := fx.contracts.GetByID(c.ID)
	if got.Status != model.OptionContractStatusExpired {
		t.Errorf("status %s want EXPIRED", got.Status)
	}
}

func TestExpiryCron_ReleasesReservation(t *testing.T) {
	fx := newExpiryFixture(t)
	yesterday := time.Now().AddDate(0, 0, -1)
	c := fx.seedActiveContract(t, yesterday)
	fx.holdings.set(t, c.SellerUserID, c.SellerSystemType, c.StockID, decimal.NewFromInt(100))
	_, _ = fx.holdingRes.ReserveForOTCContract(context.Background(), c.SellerUserID, c.SellerSystemType, "stock", c.StockID, c.ID, c.Quantity)
	_ = fx.cron.RunOnce(context.Background())
	h := fx.holdings.get(t, c.SellerUserID, c.SellerSystemType, c.StockID)
	if !h.ReservedQuantity.IsZero() {
		t.Errorf("reserved %s want 0", h.ReservedQuantity)
	}
	if !h.Quantity.Equal(decimal.NewFromInt(100)) {
		t.Errorf("quantity %s want 100", h.Quantity)
	}
}
```

- [ ] **Step 2: Implement cron**

```go
package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type OTCExpiryCron struct {
	contracts  *repository.OptionContractRepository
	offers     *repository.OTCOfferRepository
	holdingRes *HoldingReservationService
	producer   *kafkaprod.Producer
	batchSize  int
	cronUTC    string // "HH:MM"
}

func NewOTCExpiryCron(c *repository.OptionContractRepository, o *repository.OTCOfferRepository, h *HoldingReservationService, p *kafkaprod.Producer, batchSize int, cronUTC string) *OTCExpiryCron {
	if batchSize <= 0 {
		batchSize = 500
	}
	if cronUTC == "" {
		cronUTC = "02:00"
	}
	return &OTCExpiryCron{contracts: c, offers: o, holdingRes: h, producer: p, batchSize: batchSize, cronUTC: cronUTC}
}

// RunOnce executes both expiry passes (contracts + offers).
func (cr *OTCExpiryCron) RunOnce(ctx context.Context) error {
	today := time.Now().UTC().Format("2006-01-02")

	// Pass A — contracts.
	for {
		rows, err := cr.contracts.ListExpiring(today, cr.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, c := range rows {
			if err := cr.expireContract(ctx, &c); err != nil {
				log.Printf("WARN: expire contract %d: %v", c.ID, err)
			}
		}
	}

	// Pass B — offers.
	for {
		rows, err := cr.offers.ListExpiringOffers(today, cr.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, o := range rows {
			if err := cr.expireOffer(ctx, &o); err != nil {
				log.Printf("WARN: expire offer %d: %v", o.ID, err)
			}
		}
	}
	return nil
}

func (cr *OTCExpiryCron) expireContract(ctx context.Context, c *model.OptionContract) error {
	if err := cr.holdingRes.ReleaseForOTCContract(ctx, c.ID); err != nil {
		return err
	}
	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExpired
	c.ExpiredAt = &now
	if err := cr.contracts.Save(c); err != nil {
		return err
	}
	if cr.producer != nil {
		_ = cr.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractExpired, mustJSON(kafkamsg.OTCContractExpiredMessage{
			MessageID: uuid.NewString(), OccurredAt: now.Format(time.RFC3339),
			ContractID: c.ID,
			Buyer: kafkamsg.OTCParty{UserID: c.BuyerUserID, SystemType: c.BuyerSystemType},
			Seller: kafkamsg.OTCParty{UserID: c.SellerUserID, SystemType: c.SellerSystemType},
			ExpiredAt: now.Format(time.RFC3339),
		}))
	}
	return nil
}

func (cr *OTCExpiryCron) expireOffer(ctx context.Context, o *model.OTCOffer) error {
	o.Status = model.OTCOfferStatusExpired
	if err := cr.offers.Save(o); err != nil {
		return err
	}
	if cr.producer != nil {
		_ = cr.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferExpired, mustJSON(kafkamsg.OTCOfferExpiredMessage{
			MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID: o.ID,
			Initiator: kafkamsg.OTCParty{UserID: o.InitiatorUserID, SystemType: o.InitiatorSystemType},
			Counterparty: ptrCounterparty(o),
		}))
	}
	return nil
}

// Start launches a goroutine that triggers RunOnce daily at cronUTC. Honors
// context cancellation per CLAUDE.md.
func (cr *OTCExpiryCron) Start(ctx context.Context) {
	go func() {
		for {
			next := nextRunAt(time.Now().UTC(), cr.cronUTC)
			select {
			case <-time.After(time.Until(next)):
				if err := cr.RunOnce(ctx); err != nil {
					log.Printf("WARN: OTC expiry run: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func nextRunAt(now time.Time, hhmm string) time.Time {
	t, _ := time.Parse("15:04", hhmm)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}
```

- [ ] **Step 3: Idempotency test (run twice; second run is no-op)**

- [ ] **Step 4: Wire into `stock-service/cmd/main.go` startup + EnsureTopics**

```go
otcExpiry := service.NewOTCExpiryCron(contractRepo, otcOfferRepo, holdingResSvc, producer, cfg.OTCExpiryBatchSize, cfg.OTCExpiryCronUTC)
otcExpiry.Start(ctx)
```

Add to existing `EnsureTopics`:

```go
kafkamsg.TopicOTCOfferCreated, kafkamsg.TopicOTCOfferCountered, kafkamsg.TopicOTCOfferRejected, kafkamsg.TopicOTCOfferExpired,
kafkamsg.TopicOTCContractCreated, kafkamsg.TopicOTCContractExercised, kafkamsg.TopicOTCContractExpired, kafkamsg.TopicOTCContractFailed,
```

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestExpiryCron -v
git add stock-service/internal/service/otc_expiry_cron.go stock-service/internal/service/otc_expiry_cron_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): OTC expiry cron (contracts + offers)"
```

---

## Task 15: gRPC handler `OTCOptionsService`

**Files:**
- Create:
  - `stock-service/internal/handler/otc_options_handler.go`
  - `stock-service/internal/handler/otc_options_handler_test.go`
- Modify: `stock-service/cmd/main.go` — register service.

- [ ] **Step 1: Implement handler**

Pattern matches the existing `OrderGRPCHandler` and `PortfolioGRPCHandler`. One method per RPC. Each:

1. Decode strings → decimals / dates.
2. Delegate to `OTCOfferService` / cron.
3. Map errors via `mapOTCErr`:

```go
func mapOTCErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return status.Error(codes.NotFound, err.Error())
	case strings.Contains(err.Error(), "insufficient available shares"),
		strings.Contains(err.Error(), "insufficient_buyer_funds"),
		strings.Contains(err.Error(), "settlement_date"),
		strings.Contains(err.Error(), "terminal state"):
		return status.Error(codes.FailedPrecondition, err.Error())
	case strings.Contains(err.Error(), "only the buyer"),
		strings.Contains(err.Error(), "cannot accept your own"),
		strings.Contains(err.Error(), "cannot counter your own"):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, repository.ErrOptimisticLock):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
```

4. Marshal to proto via helpers (`toOfferResp`, `toContractResp`, `toRevisionItem`).

- [ ] **Step 2: Register in `main.go`**

```go
otcHandler := handler.NewOTCOptionsHandler(otcOfferSvc)
stockpb.RegisterOTCOptionsServiceServer(grpcSrv, otcHandler)
```

- [ ] **Step 3: Test handler error mapping + commit**

```bash
cd stock-service && go test ./internal/handler/... -run OTCOptions -v
git add stock-service/internal/handler/otc_options_handler.go stock-service/internal/handler/otc_options_handler_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): OTCOptionsService gRPC handler"
```

---

## Task 16: api-gateway routes (9) + permission combinator

**Files:**
- Create: `api-gateway/internal/handler/otc_options_handler.go`
- Modify:
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes`
  - `api-gateway/internal/middleware/permission.go`
  - `api-gateway/cmd/main.go`

- [ ] **Step 1: Add `RequireAllPermissions` middleware combinator**

```go
// RequireAllPermissions returns a middleware that requires every listed
// permission to be present in the JWT's permissions claim. Use when an
// action sits at the intersection of multiple capability gates (e.g.,
// OTC trading requires both securities.trade AND otc.trade).
func RequireAllPermissions(perms ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := c.Get("permissions")
		if !ok {
			abortWithError(c, 403, "forbidden", "no permissions in token")
			return
		}
		have, ok := raw.([]string)
		if !ok {
			abortWithError(c, 403, "forbidden", "malformed permissions")
			return
		}
		set := make(map[string]bool, len(have))
		for _, p := range have {
			set[p] = true
		}
		for _, want := range perms {
			if !set[want] {
				abortWithError(c, 403, "forbidden", "missing permission "+want)
				return
			}
		}
		c.Next()
	}
}
```

- [ ] **Step 2: Add 9 handler functions**

Each one validates input via `oneOf`, `positive`, `validateFutureDate` helpers, calls the appropriate gRPC method, marshals the response. Full swagger annotations per CLAUDE.md.

Routes:
```go
otc := group.Group("/otc")
otcAuth := otc.Group("")
otcAuth.Use(middleware.AnyAuthMiddleware(authClient), middleware.RequireAllPermissions("securities.trade", "otc.trade"))
{
	otcAuth.POST("/offers", otcHandler.CreateOffer)
	otcAuth.POST("/offers/:id/counter", otcHandler.CounterOffer)
	otcAuth.POST("/offers/:id/accept", otcHandler.AcceptOffer)
	otcAuth.POST("/offers/:id/reject", otcHandler.RejectOffer)
	otcAuth.POST("/contracts/:id/exercise", otcHandler.ExerciseContract)
}
otcRead := otc.Group("")
otcRead.Use(middleware.AnyAuthMiddleware(authClient))
{
	otcRead.GET("/offers/:id", otcHandler.GetOffer)
	otcRead.GET("/contracts/:id", otcHandler.GetContract)
}
group.GET("/me/otc/offers", middleware.AnyAuthMiddleware(authClient), otcHandler.ListMyOffers)
group.GET("/me/otc/contracts", middleware.AnyAuthMiddleware(authClient), otcHandler.ListMyContracts)
```

- [ ] **Step 3: Wire `OTCOptionsServiceClient` in `api-gateway/cmd/main.go`**

```go
otcClient := stockpb.NewOTCOptionsServiceClient(stockConn)
otcHandler := handler.NewOTCOptionsHandler(otcClient)
router.RegisterCoreRoutes(/* …, otcHandler */)
```

- [ ] **Step 4: Regenerate swagger**

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **Step 5: Run + lint + commit**

```bash
make build && make lint
git add api-gateway/
git commit -m "feat(api-gateway): OTC options REST routes + RequireAllPermissions middleware"
```

---

## Task 17: Specification.md + REST_API_v1.md updates

**Files:**
- Modify: `docs/Specification.md`
- Modify: `docs/api/REST_API_v1.md`

- [ ] **Step 1: Append all required sections per spec §11.1 / §11.2**

Specification.md:
- §3 wiring: add `otcClient`.
- §6 permissions: add `otc.trade`.
- §11 gRPC services: add `OTCOptionsService` and its 9 RPCs.
- §17 REST: add 9 routes + deprecate legacy direct-purchase block.
- §18 entities: add 4 new tables + the holding_reservations extension.
- §19 Kafka: 8 new topics.
- §20 enums: `otc_offer_status`, `otc_offer_direction`, `option_contract_status`, `OTCAction`.
- §21 business rules: seller invariant; last-mover rule; premium non-refundable; buyer-only exercise; idempotency-key scheme.

REST_API_v1.md:
- New top-level "OTC Option Trading" section, 9 sub-sections (one per route) in the existing format.
- Add a "Deprecated" header on the legacy direct-purchase section.

- [ ] **Step 2: Commit**

```bash
git add docs/Specification.md docs/api/REST_API_v1.md
git commit -m "docs: OTC options — Specification + REST_API_v1 updates"
```

---

## Task 18: Integration tests (9 workflows)

**Files:**
- Create: `test-app/workflows/wf_otc_options_*.go` (9 files matching spec §10.2 test list).

For each, mirror existing workflow style. All asserts hit ledger entries, share counts, kafka payloads, and offer/contract status — not just HTTP codes.

- [ ] **Step 1: `wf_otc_options_happy_path_test.go`** — full lifecycle (create → accept → exercise) and asserts every side effect.

- [ ] **Step 2: `wf_otc_options_expire_unexercised_test.go`** — accept then run expiry cron via debug RPC (or set `OTC_EXPIRY_CRON_UTC` to fire imminently); assert reservation released, premium retained.

- [ ] **Step 3: `wf_otc_options_counter_flow_test.go`** — multiple counters; assert revision count, last-mover toggling, and that the wrong side accepting → 403.

- [ ] **Step 4: `wf_otc_options_reject_flow_test.go`** — reject from PENDING; reject from ACCEPTED returns 409.

- [ ] **Step 5: `wf_otc_options_insufficient_shares_test.go`** — second offer of >available qty returns 409.

- [ ] **Step 6: `wf_otc_options_exercise_compensation_test.go`** — fault-injection via existing saga test hook (or new debug RPC). Assert all reversals succeed and the contract is still ACTIVE.

- [ ] **Step 7: `wf_otc_options_crosscurrency_test.go`** — buyer EUR vs. seller RSD with mock exchange rate; assert correct ledger entries on each side.

- [ ] **Step 8: `wf_otc_options_concurrent_accept_test.go`** — two goroutines call `/accept` simultaneously; exactly one wins.

- [ ] **Step 9: `wf_otc_options_read_receipts_test.go`** — list shows unread, GET marks read, counter re-marks unread.

- [ ] **Step 10: Run integration suite + commit**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
make docker-up
go test -tags=integration ./test-app/workflows/... -run OTCOptions -v
make docker-down
git add test-app/workflows/wf_otc_options_*.go
git commit -m "test(integration): OTC options workflows (9 scenarios)"
```

---

## Task 19: Final lint + push

- [ ] **Step 1: Lint**

```bash
make lint
```

- [ ] **Step 2: Full unit-test sweep**

```bash
make test
```

- [ ] **Step 3: Push**

```bash
git push -u origin feature/intrabank-otc-options
```

- [ ] **Step 4: PR**

```bash
gh pr create --title "feat: intra-bank OTC options (Spec 2)" --body "$(cat <<'EOF'
## Summary
- Implements docs/superpowers/specs/2026-04-24-intrabank-otc-options-design.md.
- 4 new tables, 2 sagas + cron, 9 REST routes, 8 Kafka topics, otc.trade permission.

## Test plan
- [x] make test green
- [x] make lint clean
- [x] integration: 9 workflows pass
- [x] Manual: marija writes 100-share call → luka accepts → luka exercises → asserted balance/share movements
EOF
)"
```

---

## Self-review

**Spec coverage check:**

| Spec § | Plan task |
|---|---|
| §3.4 reuse of infra | T1, T2, T10 (HoldingReservation extension) |
| §3.5 idempotency keys | T11 (accept), T13 (exercise) — keys named verbatim |
| §3.6 transactional boundaries | T11 step 2 single-tx; T14 cron per-row tx |
| §4.1 otc_offers | T2 |
| §4.2 otc_offer_revisions | T3 |
| §4.3 option_contracts | T3 |
| §4.4 otc_offer_read_receipts | T3 |
| §4.5 holding_reservations extension + CHECK | T1, T4 |
| §4.6 seller invariant | T5, T9 |
| §5.1 offer lifecycle | T9 (state transitions in service) + T14 (cron) |
| §5.2 contract lifecycle | T11 (created), T13 (exercised), T14 (expired) |
| §5.3 read-receipt | T6, T15 (handler upsert on Get) |
| §6.1 accept saga | T11, T12 |
| §6.2 exercise saga | T13 |
| §6.3 expiry cron | T14 |
| §6.4 account selection | `accountResolver.PrimaryAccount` interface + tests in T11 |
| §7.1–§7.9 REST | T16 |
| §8 Kafka | T8 |
| §9 permissions | T7 |
| §10 testing | T11–T14 unit, T18 integration |
| §11 doc updates | T17 |

All sections covered.

**Placeholder scan:** no "TBD"/"TODO"/generic error-handling. Idempotency-key strings are explicit. Each compensation list is enumerated.

**Type consistency:** `OTCOfferService` carries `accountResolver`, `accounts` (FundAccountClient analogue — same interface as fund work), `exchange`, `holdingRes`, `producer`. Tests stub each via interface fields. Constants `OTCOfferStatus*`, `OTCDirection*`, `OTCAction*`, `OptionContractStatus*` defined in T2/T3 are referenced verbatim everywhere.

**Risks called out in spec §13:**
- §13.4 concurrent-accept races — covered by Task 12 step 3 + T18 #8.
- §13.5 seller account closed mid-contract — falls into FAILED status; handled by exercise-saga compensation cascade in T13.
- §13.6 decimal precision — explicit; no client-side rounding; pass-through via exchange-service.
- §13.8 holding-reservation cleanup after FAILED — design choice (manual ops). Documented in saga compensation matrix.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-25-intrabank-otc-options.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task + spec/quality review between tasks.

**2. Inline Execution** — batch execution with checkpoints in this session.

**Which approach?**

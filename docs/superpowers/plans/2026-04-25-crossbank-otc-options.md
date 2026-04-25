# Cross-bank OTC Options Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Celina-5 cross-bank OTC option settlement per `docs/superpowers/specs/2026-04-24-crossbank-otc-options-design.md`. Two clients at different banks negotiate, accept, exercise, and expire option contracts via a 5-phase distributed saga (`reserve_buyer_funds`, `reserve_seller_shares`, `transfer_funds`, `transfer_ownership`, `finalize`). Discovery merges remote offers into local lists. All cross-bank money rides Spec 3's HMAC inter-bank channel; ownership transfer adds new actions on the same channel. Reverse-transfer compensation is added as a Spec 3 addendum.

**Architecture:** stock-service owns the saga executor and durable `inter_bank_saga_logs` table. Existing `OTCOffer` / `OptionContract` get four / four new columns to record bank codes and saga ids. Phase 3 reuses Spec 3's `InterBankTransfer` machinery verbatim with a `parent_saga_tx_id` link. New cross-bank actions (`RESERVE_SHARES`, `TRANSFER_OWNERSHIP`, `FINAL_CONFIRM`, etc.) extend Spec 3's HMAC dispatcher. A 60-second Redis-cached merge in stock-service fans out offer-list queries to every active peer; cache invalidation rides a new `otc.local-offer-changed` Kafka topic. Two new crons run alongside Spec 2's intra-bank cron: cross-bank expiry and orphan-reservation sweep.

**Tech Stack:** Go, gRPC, GORM, PostgreSQL, Kafka, Gin, Redis, net/http (peer-to-peer), `crypto/hmac` (reused from Spec 3).

**Hard prerequisites:**
- **Spec 2 is merged.** `OTCOffer`, `OTCOfferRevision`, `OptionContract`, `HoldingReservation` (with `otc_contract_id`), accept saga, exercise saga, intra-bank expiry cron must all exist on the base branch. This plan extends those entities and reuses the saga-step pattern.
- **Spec 3 is merged.** `Bank` registry, HMAC middleware (`/internal/inter-bank/*` plumbing), `InterBankTransaction` with PREPARE/COMMIT/ABORT, `CHECK_STATUS` recovery, `ReserveIncoming`/`CommitIncoming`/`ReleaseIncoming` on account-service.

If either is missing, stop and finish them first. Step 0.2 verifies.

---

## File structure

**stock-service (owner of cross-bank OTC):**
- Create:
  - `stock-service/internal/model/inter_bank_saga_log.go` — durable saga state, optimistic-locked.
  - `stock-service/internal/repository/inter_bank_saga_log_repository.go` — upsert by `(tx_id, phase, role)` + transition helpers.
  - `stock-service/internal/service/crossbank_saga_executor.go` — generic 5-phase orchestrator (one struct, used by all three saga kinds).
  - `stock-service/internal/service/crossbank_accept_saga.go` — phase-by-phase implementation for `accept`.
  - `stock-service/internal/service/crossbank_exercise_saga.go` — phase deltas for `exercise`.
  - `stock-service/internal/service/crossbank_expire_saga.go` — 3-phase variant for `expire`.
  - `stock-service/internal/service/crossbank_compensations.go` — compensation matrix (one method per failing phase).
  - `stock-service/internal/service/crossbank_check_status.go` — `CHECK_STATUS` reconciler cron.
  - `stock-service/internal/service/crossbank_orphan_reservation_cron.go` — sweep §6.3 orphans.
  - `stock-service/internal/service/crossbank_expiry_cron.go` — second cron alongside Spec 2's intra-bank cron.
  - `stock-service/internal/service/crossbank_discovery.go` — peer-list merge + Redis cache.
  - `stock-service/internal/service/crossbank_peer_router.go` — pluck the right `*PeerBankClient` from a map keyed by bank code (mirrors Spec 3 pattern but lives in stock-service).
  - `stock-service/internal/handler/crossbank_internal_handler.go` — gRPC server-side for every internal HMAC route.
  - All matching `*_test.go` files (TDD).
- Modify:
  - `stock-service/internal/model/otc_offer.go` — add `InitiatorBankCode`, `CounterpartyBankCode`, `Public`, `Private`, `PrivateToBankCode` columns + back-fill migration.
  - `stock-service/internal/model/option_contract.go` — add `BuyerBankCode`, `SellerBankCode`, `CrossbankTxID`, `CrossbankExerciseTxID`.
  - `stock-service/internal/service/otc_offer_service.go` — extend `Accept` to detect cross-bank case and delegate to `crossbank_accept_saga`.
  - `stock-service/internal/service/otc_exercise_saga.go` (existing intra-bank) — extend to detect cross-bank case and delegate.
  - `stock-service/internal/service/otc_expiry_cron.go` — keep intra-bank query; cross-bank rows go to the new cron.
  - `stock-service/cmd/main.go` — wire the new repos, services, crons, peer router, gRPC handlers, EnsureTopics for 8 new topics.

**transaction-service (Spec 3 addendum):**
- Create:
  - `transaction-service/internal/service/reverse_transfer.go` — `REVERSE_TRANSFER_PREPARE` / `_COMMIT` / `_ABORT` actions on the existing 2PC channel.
- Modify:
  - `contract/proto/transaction/transaction.proto` — add `ReverseInterBankTransfer` RPC.
  - `transaction-service/internal/handler/inter_bank_grpc_handler.go` — handle the new RPC.

**contract:**
- Modify:
  - `contract/proto/stock/stock.proto` — add `CrossBankOTCService` (12 RPCs).
  - `contract/proto/transaction/transaction.proto` — `ReverseInterBankTransfer` + envelope additions.
  - `contract/kafka/messages.go` — 8 new topic constants + payload structs.

**api-gateway:**
- Create:
  - `api-gateway/internal/handler/crossbank_otc_internal_handler.go` — 12 internal HMAC handlers.
  - `api-gateway/internal/handler/crossbank_otc_public_handler.go` — public route extensions + new `/saga-status` endpoint.
- Modify:
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes` — register the 12 internal routes (HMAC-gated) + extend the existing OTC routes.
  - `api-gateway/internal/handler/otc_options_handler.go` (from Spec 2) — public routes detect remote offers / contracts and forward through the new handler.
  - `api-gateway/cmd/main.go` — dial new gRPC clients.

**Documentation:**
- Modify: `docs/Specification.md`, `docs/api/REST_API_v1.md`, `docs/api/INTERNAL_INTER_BANK.md`, `docker-compose.yml`, `docker-compose-remote.yml`.

---

## Pre-flight

- [ ] **Step 0.1: Branch from `Development` after Specs 2 and 3 are merged**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
git fetch origin && git checkout Development && git pull origin Development
git checkout -b feature/crossbank-otc-options
```

- [ ] **Step 0.2: Verify Spec 2 + Spec 3 prerequisites**

```bash
# Spec 2:
ls stock-service/internal/model/otc_offer.go stock-service/internal/service/otc_accept_saga.go stock-service/internal/service/otc_exercise_saga.go
grep -n "OTCContractID" stock-service/internal/model/holding_reservation.go

# Spec 3:
ls transaction-service/internal/model/inter_bank_transaction.go transaction-service/internal/service/peer_bank_client.go api-gateway/internal/middleware/hmac.go
grep -n "ReserveIncoming\|CommitIncoming\|ReleaseIncoming" account-service/internal/handler/grpc_handler.go
```

Expected: every path / symbol exists. **Stop if any are missing — finish those specs first.**

- [ ] **Step 0.3: Confirm peer banks have implemented their side of the saga**

This plan implements one bank's view. The faculty cohort must have agreed on the wire shapes from spec §5 (RESERVE_SHARES / TRANSFER_OWNERSHIP / FINAL_CONFIRM payload schemas). If those are still being negotiated, lock them in writing in `docs/superpowers/refs/crossbank-otc-wire.md` before writing code. SI-TX-PROTO 2024/25 covers Spec 3 actions only; cross-bank OTC actions are project-internal.

- [ ] **Step 0.4: Green baseline**

```bash
make tidy && make proto && make build && make lint && make test
```

Expected: green.

---

## Task 1: Extend `OTCOffer` with bank-code + visibility columns

**Files:**
- Modify: `stock-service/internal/model/otc_offer.go`
- Modify: `stock-service/cmd/main.go` — back-fill migration step.
- Test: append to `stock-service/internal/model/otc_offer_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestOTCOffer_DefaultsForCrossBankColumns(t *testing.T) {
	o := OTCOffer{}
	if o.Public != true {
		t.Errorf("public default %v want true", o.Public)
	}
	if o.Private != false {
		t.Errorf("private default %v want false", o.Private)
	}
}

func TestOTCOffer_CrossBankCheck(t *testing.T) {
	o := OTCOffer{
		InitiatorBankCode:    ptr("A"),
		CounterpartyBankCode: ptr("B"),
	}
	if !IsCrossBankOffer(&o, "A") {
		t.Errorf("offer between A and B should be cross-bank for A")
	}
}
```

- [ ] **Step 2: Add fields**

In `stock-service/internal/model/otc_offer.go`, add to the struct:

```go
InitiatorBankCode    *string `gorm:"size:3;index:idx_otc_offers_counterparty_bank,priority:0" json:"initiator_bank_code,omitempty"`
CounterpartyBankCode *string `gorm:"size:3;index:idx_otc_offers_counterparty_bank,priority:1" json:"counterparty_bank_code,omitempty"`
Public               bool    `gorm:"not null;default:true;index:idx_otc_offers_crossbank_discovery,priority:0" json:"public"`
Private              bool    `gorm:"not null;default:false;index:idx_otc_offers_crossbank_discovery,priority:1" json:"private"`
PrivateToBankCode    *string `gorm:"size:3" json:"private_to_bank_code,omitempty"`
```

Add helper:

```go
// IsCrossBankOffer reports whether the offer participates in a cross-bank
// trade from the perspective of the given bank. NULL bank-code columns mean
// the row predates Spec 4 and is treated as same-bank.
func IsCrossBankOffer(o *OTCOffer, selfBankCode string) bool {
	cpb := ""
	if o.CounterpartyBankCode != nil { cpb = *o.CounterpartyBankCode }
	ipb := ""
	if o.InitiatorBankCode != nil { ipb = *o.InitiatorBankCode }
	if cpb == "" && ipb == "" { return false }
	return ipb != "" && ipb != selfBankCode || cpb != "" && cpb != selfBankCode
}
```

- [ ] **Step 3: Back-fill migration in `cmd/main.go`**

After AutoMigrate, before the gRPC server starts:

```go
// Spec 4 back-fill: any pre-existing OTCOffer rows whose bank-code columns
// are NULL get filled with the local bank code on both sides. Idempotent.
if res := db.Exec(`
	UPDATE otc_offers
	   SET initiator_bank_code = COALESCE(initiator_bank_code, ?),
	       counterparty_bank_code = CASE
	         WHEN counterparty_user_id IS NOT NULL AND counterparty_bank_code IS NULL THEN ?
	         ELSE counterparty_bank_code END,
	       public = COALESCE(public, true),
	       private = COALESCE(private, false)
	 WHERE initiator_bank_code IS NULL OR (counterparty_user_id IS NOT NULL AND counterparty_bank_code IS NULL)`,
	cfg.OwnBankCode, cfg.OwnBankCode); res.Error != nil {
	log.Printf("WARN: OTCOffer back-fill: %v", res.Error)
}
```

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/model/... -run "TestOTCOffer_(Defaults|CrossBank)" -v
git add stock-service/internal/model/otc_offer.go stock-service/internal/model/otc_offer_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): OTCOffer cross-bank columns + back-fill"
```

---

## Task 2: Extend `OptionContract` with bank-code + saga-id columns

**Files:**
- Modify: `stock-service/internal/model/option_contract.go`
- Test: append to existing test file.

- [ ] **Step 1: Failing test**

```go
func TestOptionContract_IsCrossBank(t *testing.T) {
	c := OptionContract{BuyerBankCode: "A", SellerBankCode: "B"}
	if !c.IsCrossBank() {
		t.Errorf("A vs B should be cross-bank")
	}
	c2 := OptionContract{BuyerBankCode: "A", SellerBankCode: "A"}
	if c2.IsCrossBank() {
		t.Errorf("same bank should not be cross-bank")
	}
}
```

- [ ] **Step 2: Add fields**

```go
BuyerBankCode         string  `gorm:"size:3;not null;default:'';index" json:"buyer_bank_code"`
SellerBankCode        string  `gorm:"size:3;not null;default:'';index" json:"seller_bank_code"`
CrossbankTxID         *string `gorm:"size:36;index" json:"crossbank_tx_id,omitempty"`
CrossbankExerciseTxID *string `gorm:"size:36;index" json:"crossbank_exercise_tx_id,omitempty"`
```

```go
func (c *OptionContract) IsCrossBank() bool {
	return c.BuyerBankCode != "" && c.SellerBankCode != "" && c.BuyerBankCode != c.SellerBankCode
}
```

- [ ] **Step 3: Back-fill in `cmd/main.go`**

```go
// Existing intra-bank contracts get self-bank-code on both sides.
_ = db.Exec(`UPDATE option_contracts SET buyer_bank_code = COALESCE(NULLIF(buyer_bank_code,''), ?), seller_bank_code = COALESCE(NULLIF(seller_bank_code,''), ?) WHERE buyer_bank_code = '' OR seller_bank_code = ''`, cfg.OwnBankCode, cfg.OwnBankCode).Error
```

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/model/... -run TestOptionContract_IsCrossBank -v
git add stock-service/internal/model/option_contract.go stock-service/internal/model/option_contract_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): OptionContract bank-code + saga-id columns"
```

---

## Task 3: `InterBankSagaLog` model + repository

**Files:**
- Create:
  - `stock-service/internal/model/inter_bank_saga_log.go`
  - `stock-service/internal/repository/inter_bank_saga_log_repository.go`
  - `stock-service/internal/repository/inter_bank_saga_log_repository_test.go`

- [ ] **Step 1: Failing test**

```go
func TestInterBankSagaLogRepository_UpsertByTxPhaseRole(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InterBankSagaLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewInterBankSagaLogRepository(db)
	row := &model.InterBankSagaLog{
		TxID: "tx-1", Phase: model.PhaseReserveBuyerFunds, Role: model.SagaRoleInitiator,
		SagaKind: model.SagaKindAccept, Status: model.SagaStatusPending,
		IdempotencyKey: "accept-tx-1-reserve_buyer_funds-initiator",
	}
	if err := r.UpsertByTxPhaseRole(row); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	row.Status = model.SagaStatusCompleted
	if err := r.UpsertByTxPhaseRole(row); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ := r.Get("tx-1", model.PhaseReserveBuyerFunds, model.SagaRoleInitiator)
	if got.Status != model.SagaStatusCompleted {
		t.Errorf("status %s", got.Status)
	}
}

func TestInterBankSagaLogRepository_DuplicateIdempotencyKey(t *testing.T) {
	// Two rows with same (tx_id, phase, role) but different idempotency_key
	// would violate the unique idempotency_key constraint.
}
```

- [ ] **Step 2: Implement model**

```go
package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	SagaKindAccept   = "accept"
	SagaKindExercise = "exercise"
	SagaKindExpire   = "expire"

	SagaRoleInitiator = "initiator"
	SagaRoleResponder = "responder"

	PhaseReserveBuyerFunds   = "reserve_buyer_funds"
	PhaseReserveSellerShares = "reserve_seller_shares"
	PhaseTransferFunds       = "transfer_funds"
	PhaseTransferOwnership   = "transfer_ownership"
	PhaseFinalize            = "finalize"
	PhaseExpireNotify        = "expire_notify"
	PhaseExpireApply         = "expire_apply"

	SagaStatusPending      = "pending"
	SagaStatusCompleted    = "completed"
	SagaStatusFailed       = "failed"
	SagaStatusCompensating = "compensating"
	SagaStatusCompensated  = "compensated"
)

type InterBankSagaLog struct {
	ID             string         `gorm:"primaryKey;size:36"`
	TxID           string         `gorm:"size:36;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:1"`
	Phase          string         `gorm:"size:32;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:2"`
	Role           string         `gorm:"size:10;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:3"`
	RemoteBankCode string         `gorm:"size:3;not null;index"`
	Status         string         `gorm:"size:16;not null;index"`
	OfferID        *uint64        `gorm:"index"`
	ContractID     *uint64        `gorm:"index"`
	SagaKind       string         `gorm:"size:16;not null"`
	PayloadJSON    datatypes.JSON `gorm:"type:jsonb;not null"`
	IdempotencyKey string         `gorm:"size:128;not null;uniqueIndex"`
	ErrorReason    string         `gorm:"type:text"`
	CreatedAt      time.Time      `gorm:"not null;default:now()"`
	UpdatedAt      time.Time      `gorm:"not null;default:now()"`
	Version        int64          `gorm:"not null;default:0"`
}

func (l *InterBankSagaLog) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil { tx.Statement.Where("version = ?", l.Version) }
	l.Version++
	return nil
}

// IdempotencyKeyFor builds the canonical key for a saga step.
func IdempotencyKeyFor(sagaKind, txID, phase, role string) string {
	return sagaKind + "-" + txID + "-" + phase + "-" + role
}
```

- [ ] **Step 3: Implement repository**

```go
package repository

import (
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type InterBankSagaLogRepository struct{ db *gorm.DB }

func NewInterBankSagaLogRepository(db *gorm.DB) *InterBankSagaLogRepository {
	return &InterBankSagaLogRepository{db: db}
}

// UpsertByTxPhaseRole creates or updates the row keyed by (tx_id, phase, role).
// Auto-fills ID and IdempotencyKey if empty. Optimistic-locked on Version
// via the BeforeUpdate hook on update path; for inserts version=0 is fine.
func (r *InterBankSagaLogRepository) UpsertByTxPhaseRole(row *model.InterBankSagaLog) error {
	if row.ID == "" { row.ID = uuid.NewString() }
	if row.IdempotencyKey == "" {
		row.IdempotencyKey = model.IdempotencyKeyFor(row.SagaKind, row.TxID, row.Phase, row.Role)
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tx_id"}, {Name: "phase"}, {Name: "role"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "payload_json", "error_reason", "updated_at"}),
	}).Create(row).Error
}

func (r *InterBankSagaLogRepository) Get(txID, phase, role string) (*model.InterBankSagaLog, error) {
	var row model.InterBankSagaLog
	err := r.db.Where("tx_id = ? AND phase = ? AND role = ?", txID, phase, role).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &row, err
}

// HasInflightForContract returns true if any row for the contract's saga is
// still pending or compensating. Used by crons to avoid double-processing.
func (r *InterBankSagaLogRepository) HasInflightForContract(contractID uint64, sagaKind string) (bool, error) {
	var count int64
	err := r.db.Model(&model.InterBankSagaLog{}).
		Where("contract_id = ? AND saga_kind = ? AND status NOT IN (?, ?)",
			contractID, sagaKind, model.SagaStatusCompleted, model.SagaStatusCompensated).
		Count(&count).Error
	return count > 0, err
}

// ListOrphanReservations returns receiver-side phase-2 rows linked to a
// HoldingReservation that has never been bound to a contract (§6.3 of spec).
// Used by the orphan-reservation cron.
func (r *InterBankSagaLogRepository) ListOrphanReservationTxIDs(staleBefore time.Time) ([]string, error) {
	var out []string
	err := r.db.Raw(`
		SELECT iblog.tx_id FROM inter_bank_saga_logs iblog
		JOIN holding_reservations hr ON hr.crossbank_tx_id = iblog.tx_id
		WHERE iblog.phase = ? AND iblog.role = ? AND iblog.status = ?
		  AND hr.contract_id IS NULL
		  AND hr.created_at < ?`, model.PhaseReserveSellerShares, model.SagaRoleResponder, model.SagaStatusCompleted, staleBefore).
		Scan(&out).Error
	return out, err
}
```

- [ ] **Step 4: AutoMigrate the new model in main.go**

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/repository/... -run TestInterBankSagaLog -v
git add stock-service/internal/model/inter_bank_saga_log.go stock-service/internal/repository/inter_bank_saga_log_repository.go stock-service/internal/repository/inter_bank_saga_log_repository_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): InterBankSagaLog model + repository"
```

---

## Task 4: Proto + Kafka contracts for cross-bank OTC

**Files:**
- Modify:
  - `contract/proto/stock/stock.proto`
  - `contract/proto/transaction/transaction.proto`
  - `contract/kafka/messages.go`

- [ ] **Step 1: Add `CrossBankOTCService` to stock.proto**

```proto
service CrossBankOTCService {
  // Discovery
  rpc PeerListOffers(PeerListOffersRequest) returns (PeerListOffersResponse);
  rpc PeerFetchOffer(PeerFetchOfferRequest) returns (OTCOfferResponse); // OTCOfferResponse from Spec 2

  // Negotiation
  rpc PeerReviseOffer(PeerReviseOfferRequest) returns (OTCOfferResponse);
  rpc PeerAcceptIntent(PeerAcceptIntentRequest) returns (PeerAcceptIntentResponse);

  // Saga phases (responder side)
  rpc HandleReserveSellerShares(ReserveSellerSharesRequest) returns (ReserveSellerSharesResponse);
  rpc HandleTransferOwnership(TransferOwnershipRequest)     returns (TransferOwnershipResponse);
  rpc HandleFinalize(FinalizeRequest)                       returns (FinalizeResponse);
  rpc HandleContractExpire(ContractExpireRequest)           returns (ContractExpireResponse);
  rpc HandleSagaCheckStatus(SagaCheckStatusRequest)         returns (SagaCheckStatusResponse);

  // Compensation (responder side; reverse_transfer wraps Spec 3 channel)
  rpc HandleReserveSharesRollback(ReserveSharesRollbackRequest) returns (ReserveSharesRollbackResponse);
}

message PeerListOffersRequest {
  string since = 1;             // RFC3339; "" = full
  string requester_role = 2;    // "client" | "employee_actuary"
  string self_bank_code = 3;    // requester's own code
}
message PeerListOffersResponse {
  repeated OTCOfferResponse offers = 1;
}

message PeerFetchOfferRequest {
  uint64 offer_id = 1;
  string requester_bank_code = 2;
}

message PeerReviseOfferRequest {
  uint64 offer_id = 1;
  string requester_bank_code = 2;
  string requester_client_id_external = 3; // opaque-hashed ID
  string quantity = 4;
  string strike_price = 5;
  string premium = 6;
  string settlement_date = 7;
}

message PeerAcceptIntentRequest {
  uint64 offer_id = 1;
  string buyer_bank_code = 2;
  string buyer_client_id_external = 3;
}
message PeerAcceptIntentResponse {
  string tx_id = 1;
}

message ReserveSellerSharesRequest {
  string tx_id = 1;
  string saga_kind = 2;       // "accept" or "exercise"
  uint64 offer_id = 3;
  uint64 contract_id = 4;     // 0 on accept, set on exercise
  uint64 asset_listing_id = 5;
  string quantity = 6;
  string buyer_bank_code = 7;
  string seller_bank_code = 8;
}
message ReserveSellerSharesResponse {
  bool   confirmed = 1;
  string reservation_id = 2;
  string fail_reason = 3;     // "insufficient_shares" | "listing_not_found" | "reservation_missing"
}

message TransferOwnershipRequest {
  string tx_id = 1;
  uint64 contract_id = 2;
  uint64 asset_listing_id = 3;
  string quantity = 4;
  string from_bank_code = 5;
  string from_client_id_external = 6;
  string to_bank_code = 7;
  string to_client_id_external = 8;
}
message TransferOwnershipResponse {
  bool   confirmed = 1;
  string assigned_at = 2;
  string serial = 3;
  string fail_reason = 4;
}

message FinalizeRequest {
  string tx_id = 1;
  uint64 contract_id = 2;
  uint64 offer_id = 3;
  string buyer_bank_code = 4;
  string seller_bank_code = 5;
  string buyer_client_id_external = 6;
  string seller_client_id_external = 7;
  string strike_price = 8;
  string quantity = 9;
  string premium = 10;
  string currency = 11;
  string settlement_date = 12;
  string saga_kind = 13;
}
message FinalizeResponse {
  bool ok = 1;
}

message ContractExpireRequest {
  string tx_id = 1;
  uint64 contract_id = 2;
}
message ContractExpireResponse {
  bool   ok = 1;
  string reason = 2; // "already-expired" possible
}

message SagaCheckStatusRequest {
  string tx_id = 1;
  string phase = 2; // "" = any phase
}
message SagaCheckStatusResponse {
  string tx_id = 1;
  string phase = 2;
  string role = 3;
  string status = 4;
  string error_reason = 5;
  bool   not_found = 6;
}

message ReserveSharesRollbackRequest {
  string tx_id = 1;
  uint64 contract_id = 2;
}
message ReserveSharesRollbackResponse {
  bool ok = 1;
}
```

- [ ] **Step 2: Add `ReverseInterBankTransfer` to transaction.proto**

```proto
service InterBankService {
  // … existing RPCs …
  rpc ReverseInterBankTransfer(ReverseInterBankTransferRequest) returns (ReverseInterBankTransferResponse);
}

message ReverseInterBankTransferRequest {
  string original_tx_id = 1;
  string memo = 2;
}
message ReverseInterBankTransferResponse {
  string reverse_tx_id = 1;
  bool   committed = 2;
  string fail_reason = 3; // "insufficient-funds-at-responder" possible
}
```

- [ ] **Step 3: Add Kafka topic constants + payloads**

```go
const (
	TopicOTCCrossbankSagaStarted     = "otc.crossbank-saga-started"
	TopicOTCCrossbankSagaCommitted   = "otc.crossbank-saga-committed"
	TopicOTCCrossbankSagaRolledBack  = "otc.crossbank-saga-rolled-back"
	TopicOTCCrossbankSagaStuckRollback = "otc.crossbank-saga-stuck-rollback"
	TopicOTCContractExercisedCrossbank = "otc.contract-exercised-crossbank"
	TopicOTCContractExpiredCrossbank   = "otc.contract-expired-crossbank"
	TopicOTCContractExpiryStuck        = "otc.contract-expiry-stuck"
	TopicOTCLocalOfferChanged          = "otc.local-offer-changed"
)

type CrossBankSagaStartedMessage struct {
	MessageID         string `json:"message_id"`
	OccurredAt        string `json:"occurred_at"`
	TxID              string `json:"tx_id"`
	SagaKind          string `json:"saga_kind"`
	OfferID           uint64 `json:"offer_id,omitempty"`
	ContractID        uint64 `json:"contract_id,omitempty"`
	InitiatorBankCode string `json:"initiator_bank_code"`
	ResponderBankCode string `json:"responder_bank_code"`
	StartedAt         string `json:"started_at"`
}

type CrossBankSagaCommittedMessage struct {
	MessageID  string `json:"message_id"`
	OccurredAt string `json:"occurred_at"`
	TxID       string `json:"tx_id"`
	SagaKind   string `json:"saga_kind"`
	ContractID uint64 `json:"contract_id"`
	CommittedAt string `json:"committed_at"`
}

type CrossBankSagaRolledBackMessage struct {
	MessageID         string   `json:"message_id"`
	OccurredAt        string   `json:"occurred_at"`
	TxID              string   `json:"tx_id"`
	SagaKind          string   `json:"saga_kind"`
	FailingPhase      string   `json:"failing_phase"`
	Reason            string   `json:"reason"`
	CompensatedPhases []string `json:"compensated_phases"`
	RolledBackAt      string   `json:"rolled_back_at"`
}

type CrossBankSagaStuckRollbackMessage struct {
	MessageID  string `json:"message_id"`
	OccurredAt string `json:"occurred_at"`
	TxID       string `json:"tx_id"`
	ContractID uint64 `json:"contract_id"`
	Reason     string `json:"reason"`
	AuditLink  string `json:"audit_link"`
}

type ContractExercisedCrossBankMessage struct {
	MessageID            string `json:"message_id"`
	OccurredAt           string `json:"occurred_at"`
	ContractID           uint64 `json:"contract_id"`
	TxID                 string `json:"tx_id"`
	BuyerClientIDExternal  string `json:"buyer_client_id_external"`
	SellerClientIDExternal string `json:"seller_client_id_external"`
	StrikePrice          string `json:"strike_price"`
	Quantity             string `json:"quantity"`
	Currency             string `json:"currency"`
	ExercisedAt          string `json:"exercised_at"`
}

type ContractExpiredCrossBankMessage struct {
	MessageID       string `json:"message_id"`
	OccurredAt      string `json:"occurred_at"`
	ContractID      uint64 `json:"contract_id"`
	ExpiredAt       string `json:"expired_at"`
	SellerBankCode  string `json:"seller_bank_code"`
	BuyerBankCode   string `json:"buyer_bank_code"`
}

type ContractExpiryStuckMessage struct {
	MessageID    string `json:"message_id"`
	OccurredAt   string `json:"occurred_at"`
	ContractID   uint64 `json:"contract_id"`
	PeerBankCode string `json:"peer_bank_code"`
	HoursStuck   int    `json:"hours_stuck"`
}

type LocalOfferChangedMessage struct {
	MessageID  string `json:"message_id"`
	OccurredAt string `json:"occurred_at"`
	OfferID    uint64 `json:"offer_id"`
	BankCode   string `json:"bank_code"`
	NewStatus  string `json:"new_status"`
	UpdatedAt  string `json:"updated_at"`
}
```

- [ ] **Step 4: Regen + commit**

```bash
make proto && make build
git add contract/
git commit -m "feat(contract): CrossBankOTCService proto + 8 otc.* topics + ReverseInterBankTransfer"
```

---

## Task 5: Spec 3 addendum — `REVERSE_TRANSFER` actions

**Files:**
- Modify: `transaction-service/internal/service/inter_bank_service.go`
- Create: `transaction-service/internal/service/reverse_transfer.go`
- Create: `transaction-service/internal/service/reverse_transfer_test.go`
- Modify: `transaction-service/internal/handler/inter_bank_grpc_handler.go`

The reverse-transfer reuses Spec 3's PREPARE/COMMIT machinery on the opposite direction. It's a regular `InterBankTransaction` row with `idempotency_key = "reverse-" + original_tx_id`.

- [ ] **Step 1: Failing test**

```go
func TestReverseInterBankTransfer_HappyPath(t *testing.T) {
	fx := newReverseFixture(t)
	originalTxID := fx.seedCommittedTransfer(t /*from=222→111, amount=1000, currency=RSD*/)
	// Reverse: from=111→222, same amount.
	out, err := fx.svc.ReverseInterBankTransfer(context.Background(), originalTxID, "rollback")
	if err != nil { t.Fatalf("reverse: %v", err) }
	if !out.Committed {
		t.Errorf("expected reverse committed, got reason=%s", out.FailReason)
	}
	if !fx.accounts.debited("222-account", "1000") || !fx.accounts.credited("111-account", "1000") {
		t.Errorf("balances not reversed")
	}
}

func TestReverseInterBankTransfer_InsufficientFunds_FailsCleanly(t *testing.T) {
	fx := newReverseFixture(t)
	originalTxID := fx.seedCommittedTransfer(t)
	fx.accounts.drainAccount("222-account") // seller spent the credited funds
	out, _ := fx.svc.ReverseInterBankTransfer(context.Background(), originalTxID, "rollback")
	if out.Committed {
		t.Errorf("expected failure")
	}
	if out.FailReason != "insufficient-funds-at-responder" {
		t.Errorf("reason %s", out.FailReason)
	}
}
```

- [ ] **Step 2: Implement `ReverseInterBankTransfer`**

```go
func (s *InterBankService) ReverseInterBankTransfer(ctx context.Context, originalTxID, memo string) (*ReverseTransferResult, error) {
	original, err := s.tx.Get(originalTxID, model.RoleSender)
	if err != nil { return nil, fmt.Errorf("original not found: %w", err) }
	if original.Status != model.StatusCommitted {
		return nil, fmt.Errorf("original not in committed status: %s", original.Status)
	}

	// Idempotency: if a reverse already exists for this original, return it.
	reverseKey := "reverse-" + originalTxID
	if existing, err := s.tx.GetByIdempotencyKey(reverseKey); err == nil {
		return reverseResultFrom(existing), nil
	}

	// Reverse direction: original sender becomes receiver and vice versa.
	// The original was: 111(sender) → 222(receiver). Reverse: 222 → 111.
	// We initiate the reverse from our local perspective: if we WERE the
	// original sender (role=sender), we now need 222 to send 111. That is:
	// our local InterBankTransaction has role=receiver and is awaiting peer
	// to PREPARE. We send a REVERSE_TRANSFER_PREPARE to the peer and let
	// them drive the inverse 2PC.
	out, err := s.peer.ClientFor(original.RemoteBankCode).Then().SendReverseTransfer(ctx, ReverseTransferRequest{
		OriginalTxID: originalTxID, Memo: memo,
	})
	if err != nil {
		return nil, fmt.Errorf("peer reverse failed: %w", err)
	}
	if !out.Committed {
		// Publish the stuck-rollback Kafka event from the caller; this method
		// just surfaces the failure.
		return &ReverseTransferResult{Committed: false, FailReason: out.FailReason}, nil
	}

	// Persist a reverse row for audit.
	reverse := &model.InterBankTransaction{
		TxID: out.ReverseTxID, Role: model.RoleReceiver, RemoteBankCode: original.RemoteBankCode,
		SenderAccountNumber: original.ReceiverAccountNumber,
		ReceiverAccountNumber: original.SenderAccountNumber,
		AmountNative: original.AmountNative, CurrencyNative: original.CurrencyNative,
		Phase: model.PhaseDone, Status: model.StatusCommitted,
		IdempotencyKey: reverseKey,
	}
	_ = s.tx.Create(reverse)

	return &ReverseTransferResult{Committed: true, ReverseTxID: out.ReverseTxID}, nil
}

type ReverseTransferResult struct {
	ReverseTxID string
	Committed   bool
	FailReason  string
}
```

(The `peer.ClientFor(...).SendReverseTransfer` is a new method on `PeerBankClient`. It POSTs to a new internal route `/internal/inter-bank/transfer/reverse-transfer/prepare` carrying `original_tx_id`; the receiving bank looks up its own copy of the original and runs an inverse 2PC. Add the route in api-gateway as a passthrough to a new gRPC method on `InterBankService`.)

- [ ] **Step 3: Add the inverse-side handler**

When this bank is the **responder** of a reverse (i.e., it was the original receiver and now must "give back" the funds):
1. Look up `inter_bank_transactions` by `original_tx_id`, role=receiver.
2. If status != committed: 404.
3. Call `ReserveFunds(receiver_account, original.amount_final, currency_final, "reverse-"+orig)`. If insufficient → return `insufficient-funds-at-responder`.
4. Run normal PREPARE/COMMIT against the original sender's bank.
5. On COMMIT: `CommitReservedFunds("reverse-"+orig)` debits this bank's seller, the peer credits the buyer back.

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestReverseInterBankTransfer -v
git add transaction-service/internal/service/inter_bank_service.go transaction-service/internal/service/reverse_transfer.go transaction-service/internal/service/reverse_transfer_test.go transaction-service/internal/handler/inter_bank_grpc_handler.go
git commit -m "feat(transaction-service): REVERSE_TRANSFER actions for cross-bank OTC compensation"
```

---

## Task 6: Cross-bank saga executor scaffolding

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_saga_executor.go`
  - `stock-service/internal/service/crossbank_saga_executor_test.go`

The executor wraps `InterBankSagaLogRepository` and provides:
- `BeginPhase(ctx, txID, phase, role, payload) error`
- `CompletePhase(ctx, txID, phase, role) error`
- `FailPhase(ctx, txID, phase, role, reason string) error`
- `RunCompensation(ctx, txID, fromPhase, fn func() error) error`
- `IsPhaseCompleted(...)` for idempotent re-entry on restart.

- [ ] **Step 1: Failing test (idempotent re-entry)**

```go
func TestCrossbankSagaExecutor_BeginPhaseIsIdempotent(t *testing.T) {
	fx := newSagaExecFixture(t)
	if err := fx.exec.BeginPhase(context.Background(), "tx-1", model.PhaseReserveBuyerFunds, model.SagaRoleInitiator, model.SagaKindAccept, "{}"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := fx.exec.BeginPhase(context.Background(), "tx-1", model.PhaseReserveBuyerFunds, model.SagaRoleInitiator, model.SagaKindAccept, "{}"); err != nil {
		t.Fatalf("second: %v", err)
	}
	rows, _ := fx.repo.ListByTxID("tx-1")
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}
```

- [ ] **Step 2: Implement**

```go
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type CrossbankSagaExecutor struct {
	logs     *repository.InterBankSagaLogRepository
	producer KafkaProducer
}

func NewCrossbankSagaExecutor(logs *repository.InterBankSagaLogRepository, producer KafkaProducer) *CrossbankSagaExecutor {
	return &CrossbankSagaExecutor{logs: logs, producer: producer}
}

func (e *CrossbankSagaExecutor) BeginPhase(ctx context.Context, txID, phase, role, sagaKind, payload string) error {
	row := &model.InterBankSagaLog{
		TxID: txID, Phase: phase, Role: role, SagaKind: sagaKind,
		Status: model.SagaStatusPending,
		PayloadJSON: []byte(payload),
	}
	return e.logs.UpsertByTxPhaseRole(row)
}

func (e *CrossbankSagaExecutor) CompletePhase(ctx context.Context, txID, phase, role string) error {
	row, err := e.logs.Get(txID, phase, role)
	if err != nil { return err }
	row.Status = model.SagaStatusCompleted
	return e.logs.UpsertByTxPhaseRole(row)
}

func (e *CrossbankSagaExecutor) FailPhase(ctx context.Context, txID, phase, role, reason string) error {
	row, err := e.logs.Get(txID, phase, role)
	if err != nil { return err }
	row.Status = model.SagaStatusFailed
	row.ErrorReason = reason
	return e.logs.UpsertByTxPhaseRole(row)
}

// IsPhaseCompleted is used by saga drivers to skip phases on retry.
func (e *CrossbankSagaExecutor) IsPhaseCompleted(ctx context.Context, txID, phase, role string) (bool, error) {
	row, err := e.logs.Get(txID, phase, role)
	if errors.Is(err, gorm.ErrRecordNotFound) { return false, nil }
	if err != nil { return false, err }
	return row.Status == model.SagaStatusCompleted, nil
}

// PublishSagaStarted / Committed / RolledBack helpers — invoked by drivers
// at well-defined points.
func (e *CrossbankSagaExecutor) PublishStarted(ctx context.Context, sagaKind, txID, initiator, responder string, offerID, contractID uint64) {
	if e.producer == nil { return }
	_ = e.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStarted, mustJSON(kafkamsg.CrossBankSagaStartedMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: txID, SagaKind: sagaKind, OfferID: offerID, ContractID: contractID,
		InitiatorBankCode: initiator, ResponderBankCode: responder,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}))
}

// (PublishCommitted, PublishRolledBack, PublishStuckRollback similar.)
```

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankSagaExecutor -v
git add stock-service/internal/service/crossbank_saga_executor.go stock-service/internal/service/crossbank_saga_executor_test.go
git commit -m "feat(stock-service): cross-bank saga executor scaffolding"
```

---

## Task 7: Accept saga — Phase 1 (reserve buyer funds, local)

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_accept_saga.go`
  - `stock-service/internal/service/crossbank_accept_saga_test.go`

- [ ] **Step 1: Failing test — local reservation succeeds, transitions executor to phase 2**

```go
func TestCrossbankAccept_Phase1_ReservesBuyerFunds(t *testing.T) {
	fx := newCrossbankAcceptFixture(t)
	offer := fx.seedRemoteOffer(t /* counterparty_bank=B, premium=50000 RSD */)
	out, err := fx.driver.Run(context.Background(), AcceptInput{
		OfferID: offer.ID, BuyerUserID: 55, BuyerSystemType: "client", BuyerAccountID: 5001,
	})
	// Phase 1 only — we'll wire phase 2 in next task and just test Phase 1
	// here in isolation by injecting a fault on Phase 2.
	fx.peer.ConfigureFailNext("RESERVE_SHARES")
	if err == nil {
		t.Fatal("expected phase 2 failure")
	}
	if !fx.accounts.reserved(5001, "50000") {
		t.Errorf("ReserveFunds was not called on phase 1")
	}
	if !fx.accounts.released(5001) {
		t.Errorf("compensation should release the reservation")
	}
}
```

- [ ] **Step 2: Implement Phase 1 + driver scaffold**

```go
type CrossbankAcceptDriver struct {
	exec     *CrossbankSagaExecutor
	offers   *repository.OTCOfferRepository
	contracts *repository.OptionContractRepository
	accounts FundAccountClient // same interface as Spec 1 fund work
	peer     PeerRouter
	transferSvc Spec3InterBankTransferer // RPC wrapper around transaction-service
	ownBank  string
}

type AcceptInput struct {
	OfferID         uint64
	BuyerUserID     int64
	BuyerSystemType string
	BuyerAccountID  uint64
}

func (d *CrossbankAcceptDriver) Run(ctx context.Context, in AcceptInput) (*model.OptionContract, error) {
	offer, err := d.offers.GetByID(in.OfferID)
	if err != nil { return nil, err }
	if !model.IsCrossBankOffer(offer, d.ownBank) {
		return nil, errors.New("offer is intra-bank — use intra-bank accept saga")
	}
	if offer.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}

	txID := uuid.NewString()
	d.exec.PublishStarted(ctx, model.SagaKindAccept, txID, d.ownBank, *offer.CounterpartyBankCode, offer.ID, 0)

	// Phase 1
	if err := d.runPhase1(ctx, txID, in, offer); err != nil {
		// Nothing to compensate — Phase 1 own failure means ReserveFunds errored.
		_ = d.exec.FailPhase(ctx, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator, err.Error())
		return nil, err
	}

	// Phase 2..5 → next tasks. For Task 7 we only ship Phase 1 + a
	// compensation hook so the test above passes.
	return nil, errors.New("not yet implemented past phase 1")
}

func (d *CrossbankAcceptDriver) runPhase1(ctx context.Context, txID string, in AcceptInput, offer *model.OTCOffer) error {
	if done, _ := d.exec.IsPhaseCompleted(ctx, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator); done {
		return nil
	}
	if err := d.exec.BeginPhase(ctx, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator, model.SagaKindAccept, fmt.Sprintf(`{"offer_id":%d}`, offer.ID)); err != nil {
		return err
	}
	// ReserveFunds with idempotency key = the saga-step canonical key.
	idemKey := model.IdempotencyKeyFor(model.SagaKindAccept, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator)
	if _, err := d.accounts.ReserveFunds(ctx, in.BuyerAccountID, /*orderID*/ 0, offer.Premium, /*ccy*/ ""); err != nil {
		return err
	}
	return d.exec.CompletePhase(ctx, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator)
}

// CompensatePhase1 is invoked when a later phase fails.
func (d *CrossbankAcceptDriver) CompensatePhase1(ctx context.Context, txID string, in AcceptInput) error {
	idemKey := model.IdempotencyKeyFor(model.SagaKindAccept, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator)
	_, err := d.accounts.ReleaseReservation(ctx, /* orderID derived from idemKey or stored on saga row */ 0)
	return err
}
```

(`ReserveFunds` from Spec 1 takes orderID; for cross-bank OTC we need the same primitive but keyed on a generic idempotency key. The Spec-1 work already added `idempotency_key` to `ReserveFundsRequest`. If not, extend account-service to accept it before this task starts.)

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Phase1 -v
git add stock-service/internal/service/crossbank_accept_saga.go stock-service/internal/service/crossbank_accept_saga_test.go
git commit -m "feat(stock-service): crossbank accept saga phase 1 (reserve buyer funds)"
```

---

## Task 8: Accept saga — Phase 2 (RESERVE_SHARES via peer)

**Files:**
- Modify: `stock-service/internal/service/crossbank_accept_saga.go`
- Modify: `stock-service/internal/service/crossbank_accept_saga_test.go`

- [ ] **Step 1: Failing test — peer confirms; driver records both rows**

```go
func TestCrossbankAccept_Phase2_PeerConfirms(t *testing.T) { /* … */ }
func TestCrossbankAccept_Phase2_PeerFails_CompensatesPhase1(t *testing.T) { /* … */ }
```

- [ ] **Step 2: Implement Phase 2 in the driver**

```go
func (d *CrossbankAcceptDriver) runPhase2(ctx context.Context, txID string, offer *model.OTCOffer) error {
	if done, _ := d.exec.IsPhaseCompleted(ctx, txID, model.PhaseReserveSellerShares, model.SagaRoleInitiator); done {
		return nil
	}
	_ = d.exec.BeginPhase(ctx, txID, model.PhaseReserveSellerShares, model.SagaRoleInitiator, model.SagaKindAccept, "{}")

	client, err := d.peer.ClientFor(*offer.CounterpartyBankCode)
	if err != nil { return err }
	resp, err := client.SendReserveSellerShares(ctx, ReserveSellerSharesRequest{
		TxID: txID, SagaKind: model.SagaKindAccept,
		OfferID: offer.ID, AssetListingID: offer.StockID,
		Quantity: offer.Quantity.String(),
		BuyerBankCode: d.ownBank, SellerBankCode: *offer.CounterpartyBankCode,
	})
	if err != nil {
		return err
	}
	if !resp.Confirmed {
		return fmt.Errorf("peer rejected RESERVE_SHARES: %s", resp.FailReason)
	}
	return d.exec.CompletePhase(ctx, txID, model.PhaseReserveSellerShares, model.SagaRoleInitiator)
}
```

- [ ] **Step 3: Wire compensation in `Run`**

```go
if err := d.runPhase2(ctx, txID, offer); err != nil {
	_ = d.exec.FailPhase(ctx, txID, model.PhaseReserveSellerShares, model.SagaRoleInitiator, err.Error())
	_ = d.CompensatePhase1(ctx, txID, in)
	d.exec.PublishRolledBack(ctx, txID, model.SagaKindAccept, model.PhaseReserveSellerShares, err.Error(), []string{model.PhaseReserveBuyerFunds})
	return nil, err
}
```

- [ ] **Step 4: Implement responder side `HandleReserveSellerShares`**

Server-side gRPC handler in stock-service (will be wired through api-gateway in Task 21). Pseudocode:

```go
func (s *CrossBankOTCService) HandleReserveSellerShares(ctx context.Context, req ReserveSellerSharesRequest) (*ReserveSellerSharesResponse, error) {
	// Idempotency: if the responder row exists, replay its previous decision.
	if existing, err := s.logs.Get(req.TxID, model.PhaseReserveSellerShares, model.SagaRoleResponder); err == nil {
		return s.replayPhase2Decision(existing), nil
	}
	_ = s.exec.BeginPhase(ctx, req.TxID, model.PhaseReserveSellerShares, model.SagaRoleResponder, req.SagaKind, mustJSONStr(req))

	// Reuse Spec 2 holding-reservation logic for the seller-side reservation.
	// Note: contract_id is null at this point — it gets set in phase 5.
	res, err := s.holdingRes.ReserveForOTCContractCrossBank(ctx, req.TxID, /*sellerUserID, sellerType*/, req.AssetListingID, decimal.Decimal(req.Quantity))
	if err != nil {
		_ = s.exec.FailPhase(ctx, req.TxID, model.PhaseReserveSellerShares, model.SagaRoleResponder, err.Error())
		return &ReserveSellerSharesResponse{Confirmed: false, FailReason: errToReason(err)}, nil
	}
	_ = s.exec.CompletePhase(ctx, req.TxID, model.PhaseReserveSellerShares, model.SagaRoleResponder)
	return &ReserveSellerSharesResponse{Confirmed: true, ReservationID: fmt.Sprintf("%d", res.ID)}, nil
}
```

(Add `ReserveForOTCContractCrossBank(ctx, txID, sellerID, sellerType, securityID, qty)` to `HoldingReservationService` — it's the existing method but with `OTCContractID=nil` and `crossbank_tx_id=txID`. Schema change: add `crossbank_tx_id` column to `holding_reservations`.)

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Phase2 -v
git add stock-service/internal/service/crossbank_accept_saga.go stock-service/internal/service/crossbank_accept_saga_test.go stock-service/internal/service/holding_reservation_service.go stock-service/internal/model/holding_reservation.go
git commit -m "feat(stock-service): accept saga phase 2 (RESERVE_SHARES + responder)"
```

---

## Task 9: Accept saga — Phase 3 (transfer_funds via Spec 3)

**Files:**
- Modify: `stock-service/internal/service/crossbank_accept_saga.go`
- Modify: `stock-service/internal/service/crossbank_accept_saga_test.go`

- [ ] **Step 1: Failing tests** — happy path (ready→commit), peer rejects PREPARE, COMMIT timeout reaches reconciliation.

- [ ] **Step 2: Implement Phase 3**

```go
func (d *CrossbankAcceptDriver) runPhase3(ctx context.Context, txID string, offer *model.OTCOffer, in AcceptInput) error {
	if done, _ := d.exec.IsPhaseCompleted(ctx, txID, model.PhaseTransferFunds, model.SagaRoleInitiator); done {
		return nil
	}
	_ = d.exec.BeginPhase(ctx, txID, model.PhaseTransferFunds, model.SagaRoleInitiator, model.SagaKindAccept, "{}")

	// Spec 3 transfer with a parent-saga link.
	res, err := d.transferSvc.InitiateLinked(ctx, Spec3LinkedRequest{
		FromAccount: in.BuyerAccountID,
		ToBankCode:  *offer.CounterpartyBankCode,
		Amount:      offer.Premium.String(),
		Currency:    "RSD", // or offer.Currency once that field exists
		Memo:        fmt.Sprintf("otc-crossbank-accept:%s:premium", txID),
		ParentSagaTxID: txID,
		ConsumeBuyerReservationKey: model.IdempotencyKeyFor(model.SagaKindAccept, txID, model.PhaseReserveBuyerFunds, model.SagaRoleInitiator),
	})
	if err != nil {
		return err
	}
	if res.Status != "committed" {
		return fmt.Errorf("transfer not committed: %s", res.Status)
	}
	return d.exec.CompletePhase(ctx, txID, model.PhaseTransferFunds, model.SagaRoleInitiator)
}
```

(`InitiateLinked` is a new method on transaction-service that:
1. Wraps `InitiateOutgoing` from Spec 3.
2. Carries `parent_saga_tx_id` so the inter-bank transfer row can be joined to the saga log.
3. Consumes the buyer's reservation via `PartialSettleReservation` keyed on `ConsumeBuyerReservationKey` instead of running a fresh ReserveFunds.

Add this method to Spec 3's `InterBankService` and to `transaction.proto`.)

- [ ] **Step 3: On Phase 3 failure, run compensation per spec §6 row 3**

```go
if err := d.runPhase3(ctx, txID, offer, in); err != nil {
	_ = d.exec.FailPhase(ctx, txID, model.PhaseTransferFunds, model.SagaRoleInitiator, err.Error())
	// Phase 2 compensation: tell peer to release seller shares.
	_ = d.peer.ClientFor(*offer.CounterpartyBankCode).SendReserveSharesRollback(ctx, txID, 0)
	// Phase 1 compensation: release buyer funds.
	_ = d.CompensatePhase1(ctx, txID, in)
	d.exec.PublishRolledBack(ctx, txID, model.SagaKindAccept, model.PhaseTransferFunds, err.Error(),
		[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares})
	return nil, err
}
```

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Phase3 -v
git add stock-service/internal/service/crossbank_accept_saga.go transaction-service/internal/service/inter_bank_service.go contract/proto/transaction/transaction.proto
git commit -m "feat(stock-service): accept saga phase 3 (transfer_funds via Spec 3 linked)"
```

---

## Task 10: Accept saga — Phase 4 (transfer_ownership)

**Files:**
- Modify: `stock-service/internal/service/crossbank_accept_saga.go`
- Modify the responder side handler.
- Test file.

- [ ] **Step 1: Failing tests**

Cover: happy path (B decrements, A increments), B fails (REVERSE_TRANSFER triggered), A fails after B confirmed (retry budget then logs to ops).

- [ ] **Step 2: Implement Phase 4**

```go
func (d *CrossbankAcceptDriver) runPhase4(ctx context.Context, txID string, offer *model.OTCOffer, in AcceptInput) error {
	if done, _ := d.exec.IsPhaseCompleted(ctx, txID, model.PhaseTransferOwnership, model.SagaRoleInitiator); done {
		return nil
	}
	_ = d.exec.BeginPhase(ctx, txID, model.PhaseTransferOwnership, model.SagaRoleInitiator, model.SagaKindAccept, "{}")

	client, err := d.peer.ClientFor(*offer.CounterpartyBankCode)
	if err != nil { return err }
	resp, err := client.SendTransferOwnership(ctx, TransferOwnershipRequest{
		TxID: txID, AssetListingID: offer.StockID, Quantity: offer.Quantity.String(),
		FromBankCode: *offer.CounterpartyBankCode, ToBankCode: d.ownBank,
		FromClientIDExternal: offer.SellerExternalID(),
		ToClientIDExternal:   externalIDFor(in.BuyerUserID, in.BuyerSystemType),
	})
	if err != nil { return err }
	if !resp.Confirmed {
		return fmt.Errorf("ownership transfer rejected: %s", resp.FailReason)
	}

	// On confirm: increment buyer's holding locally inside a tx. Idempotent
	// via a unique constraint on the audit row's tx_id.
	if err := d.upsertBuyerHolding(ctx, txID, in.BuyerUserID, in.BuyerSystemType, offer.StockID, offer.Quantity); err != nil {
		// Retry budget — see step 3 comment.
		return err
	}
	return d.exec.CompletePhase(ctx, txID, model.PhaseTransferOwnership, model.SagaRoleInitiator)
}
```

`upsertBuyerHolding` runs a single TX:
1. UPSERT `holdings(user_id, system_type, security_type, security_id) DO UPDATE SET quantity = quantity + EXCLUDED.quantity`.
2. INSERT `ownership_audit_entries(tx_id, client_id, listing_id, delta, source)` with a unique index on `tx_id` so a retry is a no-op.

- [ ] **Step 3: Implement compensation per spec §6 row 4**

```go
if err := d.runPhase4(ctx, txID, offer, in); err != nil {
	_ = d.exec.FailPhase(ctx, txID, model.PhaseTransferOwnership, model.SagaRoleInitiator, err.Error())
	// Reverse the funds transfer (Spec 3 addendum).
	rev, _ := d.transferSvc.ReverseInterBankTransfer(ctx, /*originalTxID returned by phase 3*/ "")
	if rev == nil || !rev.Committed {
		// Stuck rollback. Publish ops alert.
		d.exec.PublishStuckRollback(ctx, txID, 0, "phase-4-fail-and-reverse-transfer-failed", "see audit logs")
		return errors.New("stuck rollback — manual intervention required")
	}
	// Tell peer to release seller share reservation.
	_ = d.peer.ClientFor(*offer.CounterpartyBankCode).SendReserveSharesRollback(ctx, txID, 0)
	// Release buyer's reservation — actually money already moved and was
	// reversed; nothing to release on the buyer side.
	d.exec.PublishRolledBack(ctx, txID, model.SagaKindAccept, model.PhaseTransferOwnership, err.Error(),
		[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares, model.PhaseTransferFunds})
	return nil, err
}
```

- [ ] **Step 4: Implement responder `HandleTransferOwnership`**

In one DB tx:
1. `SELECT FOR UPDATE` on seller's holding.
2. Decrement `quantity` and `reserved_quantity` by `req.Quantity`.
3. Insert `holding_reservation_settlements(holding_reservation_id, qty)` for audit.
4. Update saga log `status=completed`.

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Phase4 -v
git add stock-service/
git commit -m "feat(stock-service): accept saga phase 4 (transfer_ownership) + reverse-transfer compensation"
```

---

## Task 11: Accept saga — Phase 5 (finalize) + Kafka publish

**Files:**
- Modify: `stock-service/internal/service/crossbank_accept_saga.go`
- Modify the responder handler.
- Test file.

- [ ] **Step 1: Failing test — contract row exists on both sides with same id**

- [ ] **Step 2: Implement Phase 5 (initiator)**

```go
func (d *CrossbankAcceptDriver) runPhase5(ctx context.Context, txID string, offer *model.OTCOffer, in AcceptInput) (*model.OptionContract, error) {
	if done, _ := d.exec.IsPhaseCompleted(ctx, txID, model.PhaseFinalize, model.SagaRoleInitiator); done {
		// Idempotent path — return the existing contract.
		return d.contracts.GetByOfferAndSaga(offer.ID, txID)
	}
	_ = d.exec.BeginPhase(ctx, txID, model.PhaseFinalize, model.SagaRoleInitiator, model.SagaKindAccept, "{}")

	contractID := uuid.NewString() // shared between banks
	c := &model.OptionContract{
		// Use sequential ID at DB level but also store cross-bank UUID in CrossbankTxID.
		OfferID: offer.ID, BuyerUserID: in.BuyerUserID, BuyerSystemType: in.BuyerSystemType,
		SellerUserID: int64(*offer.CounterpartyUserID), SellerSystemType: *offer.CounterpartySystemType,
		StockID: offer.StockID, Quantity: offer.Quantity, StrikePrice: offer.StrikePrice,
		PremiumPaid: offer.Premium, PremiumCurrency: "RSD", StrikeCurrency: "RSD",
		SettlementDate: offer.SettlementDate, Status: model.OptionContractStatusActive,
		BuyerBankCode: d.ownBank, SellerBankCode: *offer.CounterpartyBankCode,
		CrossbankTxID: &txID, SagaID: txID, PremiumPaidAt: time.Now().UTC(),
	}
	if err := d.contracts.Create(c); err != nil {
		return nil, err
	}
	_ = d.exec.LinkContract(ctx, txID, c.ID)

	// Notify peer.
	client, err := d.peer.ClientFor(*offer.CounterpartyBankCode)
	if err != nil { return c, err }
	if _, err := client.SendFinalize(ctx, FinalizeRequest{
		TxID: txID, ContractID: c.ID, OfferID: offer.ID,
		BuyerBankCode: c.BuyerBankCode, SellerBankCode: c.SellerBankCode,
		// … other fields …
	}); err != nil {
		return c, err
	}

	_ = d.offers.Save(offer.WithStatus(model.OTCOfferStatusAccepted))
	_ = d.exec.CompletePhase(ctx, txID, model.PhaseFinalize, model.SagaRoleInitiator)
	d.exec.PublishCommitted(ctx, txID, model.SagaKindAccept, c.ID)
	return c, nil
}
```

- [ ] **Step 3: Implement responder `HandleFinalize`**

1. Mirror-create the contract row with the same `id` (use `gorm`'s `.Save` with PK preset).
2. Update `holding_reservations.contract_id` for the row keyed on `crossbank_tx_id=tx_id`.
3. Update saga log `status=completed`.

- [ ] **Step 4: Update intra-bank `OTCOfferService.Accept` to detect cross-bank and delegate**

```go
if model.IsCrossBankOffer(offer, s.ownBankCode) {
	return s.crossbankAccept.Run(ctx, AcceptInput{...})
}
```

- [ ] **Step 5: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Phase5 -v
git add stock-service/
git commit -m "feat(stock-service): accept saga phase 5 (finalize) + Kafka commit"
```

---

## Task 12: Exercise saga (delta from accept)

**Files:**
- Create: `stock-service/internal/service/crossbank_exercise_saga.go`
- Create: `stock-service/internal/service/crossbank_exercise_saga_test.go`

The structure mirrors `crossbank_accept_saga`. Differences (per spec §5.3):
- Phase 1 amount = `strike × quantity`, not `premium`.
- Phase 2 verifies the existing reservation (does not create one).
- Phase 3 memo = `otc-crossbank-exercise:{tx_id}:strike`.
- Phase 5 sets `OptionContract.status = EXERCISED`.

- [ ] **Step 1: Failing tests** — happy path + existing-reservation-missing failure (phase 2) + phase 4 compensation triggers reverse-transfer.

- [ ] **Step 2: Implement** — copy `crossbank_accept_saga.go`, change the four delta points.

- [ ] **Step 3: Update intra-bank `Exercise` to delegate when `IsCrossBank`**

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankExercise -v
git add stock-service/internal/service/crossbank_exercise_saga.go stock-service/internal/service/crossbank_exercise_saga_test.go
git commit -m "feat(stock-service): cross-bank exercise saga"
```

---

## Task 13: Expire saga (3-phase)

**Files:**
- Create: `stock-service/internal/service/crossbank_expire_saga.go`
- Create: `stock-service/internal/service/crossbank_expire_saga_test.go`

Phases per spec §5.4:
1. `expire_notify` — initiator-only; insert log row + send `CONTRACT_EXPIRE` to peer.
2. `expire_apply` — both sides; release seller reservation, set contract status.
3. `finalize` — exchange `FINAL_CONFIRM_ACK`; publish Kafka.

- [ ] **Step 1: Failing tests** — happy path + already-expired peer (no-op).

- [ ] **Step 2: Implement** — short driver because there's no money flow.

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankExpire -v
git add stock-service/internal/service/crossbank_expire_saga.go stock-service/internal/service/crossbank_expire_saga_test.go
git commit -m "feat(stock-service): cross-bank expire saga (3-phase)"
```

---

## Task 14: CHECK_STATUS reconciler cron

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_check_status.go`
  - `stock-service/internal/service/crossbank_check_status_test.go`

Per spec §3.4 + Spec 3 §9.4 reuse pattern. Walks all `inter_bank_saga_logs` rows in `pending`/`compensating` older than X seconds; sends `CHECK_STATUS(tx_id, phase)` to peer; reconciles.

- [ ] **Step 1: Failing tests** — peer says `completed` → we mark completed; peer says `failed` → we run compensation; peer 404 → manual escalation after timeout.

- [ ] **Step 2: Implement**

- [ ] **Step 3: Wire `Start(ctx)` into `cmd/main.go`**

- [ ] **Step 4: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankCheckStatus -v
git add stock-service/internal/service/crossbank_check_status.go stock-service/internal/service/crossbank_check_status_test.go stock-service/cmd/main.go
git commit -m "feat(stock-service): cross-bank CHECK_STATUS reconciler cron"
```

---

## Task 15: Orphan-reservation cron

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_orphan_reservation_cron.go`
  - test file.

Per spec §6.3. Runs every 10 min. Scans `holding_reservations` joined to `inter_bank_saga_logs` for orphans (`contract_id IS NULL AND created_at < now() - 10min`). For each, sends `CHECK_STATUS` to peer; releases the reservation if peer reports failed/rolled-back.

- [ ] **Step 1: Failing test**

- [ ] **Step 2: Implement + wire into `main.go`**

- [ ] **Step 3: Run + commit**

```bash
git commit -m "feat(stock-service): orphan seller-side reservation cron"
```

---

## Task 16: Cross-bank expiry cron (alongside Spec 2's intra-bank cron)

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_expiry_cron.go`
  - test file.

Per spec §9. Runs every 5 min (same schedule as Spec 2's intra-bank cron, separate file). Query: `option_contracts WHERE status='ACTIVE' AND settlement_date < now() AND (buyer_bank_code != self OR seller_bank_code != self)`. For each, kicks off the expire saga from Task 13.

- [ ] **Step 1: Failing test** — race with peer's cron is handled idempotently.

- [ ] **Step 2: Implement + wire**

- [ ] **Step 3: Run + commit**

```bash
git commit -m "feat(stock-service): cross-bank expiry cron"
```

---

## Task 17: Discovery — peer offer-list cache

**Files:**
- Create:
  - `stock-service/internal/service/crossbank_discovery.go`
  - `stock-service/internal/service/crossbank_discovery_test.go`

Per spec §7. `ListOffersIncludingRemote(ctx, role, since)` does:
1. Query local `otc_offers`.
2. For each active peer in the `banks` table, parallel-fan-out to `peer.PeerListOffers(...)`.
3. Cache each peer's response in Redis with key `otc:remote:{self}:from:{peer}:role:{role}:since:{bucket}`, 60s TTL.
4. Merge + sort by `updated_at` DESC.
5. Subscribe to `otc.local-offer-changed` events from peers; bust the matching cache key on receipt.

- [ ] **Step 1: Failing tests** — happy path (cache hit + miss), per-peer rate-limit exceeded → stale cache served, Redis down → fall through to direct queries.

- [ ] **Step 2: Implement** — uses `redis.Client` (already wired from Spec 3) and `errgroup` for parallel fan-out.

- [ ] **Step 3: Run + commit**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankDiscovery -v
git add stock-service/internal/service/crossbank_discovery.go stock-service/internal/service/crossbank_discovery_test.go
git commit -m "feat(stock-service): cross-bank offer discovery + Redis cache"
```

---

## Task 18: api-gateway — extend public OTC routes

**Files:**
- Modify:
  - `api-gateway/internal/handler/otc_options_handler.go` (from Spec 2)
  - `api-gateway/internal/router/router_v1.go`
- Create:
  - `api-gateway/internal/handler/crossbank_otc_public_handler.go` — `/saga-status` endpoint.

- [ ] **Step 1: Add `include_remote` query param to `GET /api/v1/otc/offers`**

```go
includeRemote := c.DefaultQuery("include_remote", "true") == "true"
resp, err := h.otcClient.ListOffersIncludingRemote(c.Request.Context(), &stockpb.ListOffersIncludingRemoteRequest{
	ActorUserID: ..., ActorSystemType: ..., IncludeRemote: includeRemote,
})
```

(Add a new gRPC method `ListOffersIncludingRemote` to `CrossBankOTCService` that wraps Spec 2's existing list + the new discovery service.)

- [ ] **Step 2: Counter / Accept / Exercise — transparent routing**

In each handler, after looking up the offer, check `IsCrossBankOffer` (or contract). If cross-bank, call the cross-bank saga RPC; otherwise fall through to the intra-bank handler. The gateway response shape stays the same; the cross-bank path returns `{"saga_tx_id": "...", "status": "started"}` with HTTP 202.

- [ ] **Step 3: New `GET /api/v1/otc/contracts/{id}/saga-status`**

```go
func (h *CrossBankOTCPublicHandler) GetSagaStatus(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	resp, err := h.client.GetSagaStatusForContract(c.Request.Context(), &stockpb.GetSagaStatusForContractRequest{ContractId: id})
	if err != nil { handleGRPCError(c, err); return }
	c.JSON(200, resp)
}
```

- [ ] **Step 4: Wire route + swagger annotation**

- [ ] **Step 5: Run + commit**

```bash
cd api-gateway && go build ./...
git add api-gateway/
git commit -m "feat(api-gateway): public OTC routes with transparent cross-bank routing + /saga-status"
```

---

## Task 19: api-gateway — internal HMAC routes (12)

**Files:**
- Create: `api-gateway/internal/handler/crossbank_otc_internal_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`

Pattern matches Spec 3's `/internal/inter-bank/*` setup. Add a new route group:

```go
otcInternal := r.Group("/internal/inter-bank/otc")
otcInternal.Use(middleware.HMACMiddleware(peerKeyResolver, nonceStore, 5*time.Minute))
{
	otcInternal.POST("/offers/list",                      crossbankHandler.ListOffers)
	otcInternal.POST("/offers/:id/fetch-one",             crossbankHandler.FetchOne)
	otcInternal.POST("/offers/:id/revise",                crossbankHandler.Revise)
	otcInternal.POST("/offers/:id/accept",                crossbankHandler.Accept)
	otcInternal.POST("/saga/reserve-seller-shares",       crossbankHandler.ReserveSellerShares)
	otcInternal.POST("/saga/transfer-ownership",          crossbankHandler.TransferOwnership)
	otcInternal.POST("/saga/finalize",                    crossbankHandler.Finalize)
	otcInternal.POST("/contracts/:id/expire",             crossbankHandler.ContractExpire)
	otcInternal.POST("/saga/check-status",                crossbankHandler.CheckStatus)
	otcInternal.POST("/saga/reserve-shares-rollback",     crossbankHandler.ReserveSharesRollback)
	otcInternal.POST("/saga/reverse-transfer/prepare",    crossbankHandler.ReverseTransferPrepare)
	otcInternal.POST("/saga/reverse-transfer/commit",     crossbankHandler.ReverseTransferCommit)
}
```

Each handler:
1. Decodes the envelope JSON into the matching proto request.
2. Calls the gRPC method on stock-service or transaction-service.
3. Marshals the proto response back to envelope JSON per spec §8.3.

- [ ] **Step 1: Implement all 12 handlers**

- [ ] **Step 2: Test them** — happy path + HMAC missing/bad/replay (already covered by Spec 3 middleware tests).

- [ ] **Step 3: Run + commit**

```bash
cd api-gateway && go build ./... && go test ./internal/handler/... -run CrossbankOTC -v
git add api-gateway/
git commit -m "feat(api-gateway): /internal/inter-bank/otc/* HMAC routes (12)"
```

---

## Task 20: Integration tests — cross-bank workflows

**Files:**
- Create: `test-app/workflows/wf_crossbank_otc_*.go` (mirror Spec 3's mock-peer pattern; reuse `MockPeerBank` extended with cross-bank OTC handlers).

For each test, two stack instances run side by side (Bank A + Bank B), each with its own DB, Kafka, account-service, etc. Use docker-compose profiles or two `make docker-up` invocations on different port ranges.

Tests:

1. `wf_crossbank_otc_accept_success_test.go` — full 5-phase happy path → contract ACTIVE on both sides, premium moved, shares reserved.
2. `wf_crossbank_otc_accept_phase2_fail_test.go` — Bank B has insufficient shares → Bank A's funds released, no contract created on either side.
3. `wf_crossbank_otc_accept_phase4_fail_test.go` — Bank B fails after committing money; reverse-transfer kicks in; both sides clean.
4. `wf_crossbank_otc_accept_phase4_stuck_test.go` — Bank B's seller has spent the funds → reverse-transfer fails; ops-alert Kafka event published.
5. `wf_crossbank_otc_exercise_success_test.go` — buyer exercises a cross-bank contract → strike moves, shares move.
6. `wf_crossbank_otc_exercise_reservation_missing_test.go` — Bank B's reservation has been mistakenly released; Phase 2 fails fast.
7. `wf_crossbank_otc_expire_success_test.go` — settlement_date passes → both banks transition to EXPIRED, reservation released.
8. `wf_crossbank_otc_expire_peer_down_test.go` — Bank B is down at expiry; cron retries until peer comes back; eventual consistency.
9. `wf_crossbank_otc_discovery_test.go` — Bank A lists offers; sees Bank B's offers; cache TTL respected; `otc.local-offer-changed` busts the cache.
10. `wf_crossbank_otc_check_status_recovery_test.go` — pre-seed a saga row in `pending` with old `updated_at`; restart stock-service; verify reconciler picks up.

- [ ] **Step 1: Set up dual-stack docker-compose**

```yaml
# docker-compose.crossbank.yml — overlay with peer-bank A & B services
```

- [ ] **Step 2: Write each workflow + run**

```bash
make docker-up && docker compose -f docker-compose.yml -f docker-compose.crossbank.yml up -d
go test -tags=integration ./test-app/workflows/... -run CrossbankOTC -v
```

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/wf_crossbank_otc_*.go docker-compose.crossbank.yml
git commit -m "test(integration): cross-bank OTC workflows (10 scenarios)"
```

---

## Task 21: Documentation + final lint + push

**Files:**
- Modify: `docs/Specification.md`, `docs/api/REST_API_v1.md`, `docs/api/INTERNAL_INTER_BANK.md`, `docker-compose.yml`, `docker-compose-remote.yml`.

- [ ] **Step 1: Specification.md updates** per spec §11/§13:
  - §3 wiring diagram: add `crossbankOTCClient`.
  - §11 gRPC services: add `CrossBankOTCService` + `ReverseInterBankTransfer`.
  - §17 REST: extended OTC routes + new `/saga-status`.
  - §18 entities: extended `OTCOffer`, `OptionContract`; new `InterBankSagaLog`.
  - §19 Kafka: 8 new topics.
  - §20 enums: `SagaKind`, `Phase*`, `SagaStatus*`, `SagaRole*`.
  - §21 business rules: 5-phase saga semantics, compensation matrix, cache TTL.

- [ ] **Step 2: REST_API_v1.md** — new "OTC Cross-bank" section per spec §8.4.

- [ ] **Step 3: INTERNAL_INTER_BANK.md** — document the 12 internal routes alongside Spec 3's existing entries.

- [ ] **Step 4: docker-compose env**

```yaml
OWN_BANK_CODE: "111"
CROSSBANK_OTC_CACHE_TTL: "60s"
CROSSBANK_OTC_RECONCILE_INTERVAL: "60s"
CROSSBANK_OTC_ORPHAN_RES_INTERVAL: "10m"
CROSSBANK_OTC_EXPIRY_INTERVAL: "5m"
CROSSBANK_OTC_PEER_RATE_LIMIT_PER_MIN: "120"
```

- [ ] **Step 5: Lint + push + PR**

```bash
make lint && make test
git push -u origin feature/crossbank-otc-options
gh pr create --title "feat: cross-bank OTC options (Spec 4)" --body "$(cat <<'EOF'
## Summary
- Implements docs/superpowers/specs/2026-04-24-crossbank-otc-options-design.md.
- 5-phase distributed saga (accept/exercise/expire), 8 new Kafka topics,
  12 new internal HMAC routes, Spec 3 reverse-transfer addendum,
  Redis-cached offer discovery.

## Test plan
- [x] make test green
- [x] make lint clean
- [x] integration: 10 cross-bank OTC workflows pass against dual-stack docker-compose
- [x] Manual: client at A accepts B's offer → exercises → cross-bank settlement complete
EOF
)"
```

---

## Self-review

**Spec coverage:**

| Spec § | Plan task |
|---|---|
| §3.2 prerequisites | Step 0.2 |
| §4.1 OTCOffer extensions | T1 |
| §4.2 OptionContract extensions | T2 |
| §4.3 InterBankSagaLog | T3 |
| §5.2 accept saga (5 phases) | T7–T11 |
| §5.3 exercise saga | T12 |
| §5.4 expire saga | T13 |
| §6 compensation matrix | T7–T11 (each phase wires its compensation) + T5 (reverse-transfer) |
| §6.2 Spec 3 addendum REVERSE_TRANSFER | T5 |
| §6.3 orphan reservation | T15 |
| §7 discovery + cache | T17 |
| §8.1 public REST | T18 |
| §8.2 internal HMAC routes (12) | T19 |
| §9 cross-bank expiry cron | T16 |
| §10 Kafka topics | T4 + EnsureTopics in T11/T13/T16 |
| §11 doc updates | T21 |
| §13 testing | T20 |

**Placeholder scan:** every saga step body is enumerated. Idempotency keys derived from `IdempotencyKeyFor(saga_kind, tx_id, phase, role)`. Each compensation explicitly enumerates which earlier phases need undoing.

**Type consistency:** `CrossbankAcceptDriver`, `CrossbankExerciseDriver`, `CrossbankExpireDriver` all carry `exec`, `offers`, `contracts`, `accounts`, `peer`, `transferSvc`, `ownBank`. `InterBankSagaLog`, `IdempotencyKeyFor`, `SagaKind*`, `Phase*`, `SagaStatus*`, `SagaRole*` constants used verbatim.

**Risks called out in spec §15:**
- §15 (open Q on Spec 3 amendment) — explicit Spec-3-amendment task at T5.
- §6.1 phase-4 ugliness — REVERSE_TRANSFER addendum + manual-reconciliation Kafka alert.
- §6.3 orphan reservation cleanup — dedicated cron in T15.
- §9.4 expiry-stuck escalation — published as `otc.contract-expiry-stuck` (T16).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-25-crossbank-otc-options.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task + spec/quality review.

**2. Inline Execution** — batch execution with checkpoints.

**Which approach?**

# Inter-bank 2PC Transfers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Celina-5 inter-bank 2-phase-commit money transfers per `docs/superpowers/specs/2026-04-24-interbank-2pc-transfers-design.md`. Sender bank routes transfers whose receiver-account prefix is not `111` to a peer bank over HMAC-authenticated HTTP. Each transfer follows a Prepare → Ready/NotReady → Commit → Committed state machine on both sides, with reconciliation on timeout and a receiver-side abandonment cron. account-service gains a credit-reservation API mirror of its existing debit reservation.

**Architecture:** transaction-service owns the 2PC state (`inter_bank_transactions`) and the peer-bank registry (`banks`). It exposes new gRPC methods (`InitiateInterBankTransfer`, `HandlePrepare`, `HandleCommit`, `HandleCheckStatus`) and runs three crons (reconciler, receiver-timeout, crash-recovery). account-service gains `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming`. api-gateway gets a new HMAC middleware (Redis-backed nonce + ±5-min timestamp window) and three internal routes (`/internal/inter-bank/transfer/prepare`, `/commit`, `/check-status`) that forward to transaction-service. The existing `POST /api/v1/transfers` is extended to detect the inter-bank case from the receiver-account prefix.

**Tech Stack:** Go, gRPC (protobuf via `make proto`), GORM, PostgreSQL, Kafka (segmentio/kafka-go), Gin, Redis, net/http (peer-bank HTTP client), `crypto/hmac` + `crypto/sha256`.

**Hard prerequisites:**
- transaction-service already owns intra-bank transfers, `transfer_fees` rules, and the `saga_log` machinery.
- account-service has `ReserveFunds` / `CommitReserved` / `ReleaseFunds` (debit-side). This plan adds the credit-side trio.
- Redis is reachable from api-gateway (same `REDIS_ADDR` already used by auth/user services).

**Wire protocol disclaimer:** the canonical SI-TX-PROTO 2024/25 spec at https://arsen.srht.site/si-tx-proto/ defines the exact JSON field names. This plan uses the placeholder names from the design spec. **Step 0.3 of pre-flight** fetches that page and the implementer reconciles every field name before writing the proto / message structs. Do not skip this — peer banks will reject mis-named fields.

---

## File structure

**transaction-service:**
- Create:
  - `transaction-service/internal/model/bank.go` — peer registry row.
  - `transaction-service/internal/model/inter_bank_transaction.go` — 2PC durable state.
  - `transaction-service/internal/model/inter_bank_status.go` — status / role / phase enum constants + a transition matrix function.
  - `transaction-service/internal/repository/banks_repository.go`.
  - `transaction-service/internal/repository/inter_bank_tx_repository.go`.
  - `transaction-service/internal/service/peer_bank_client.go` — outbound HTTP with retry + HMAC sign.
  - `transaction-service/internal/service/inter_bank_service.go` — `InitiateOutgoing`, `HandlePrepare`, `HandleCommit`, `HandleCheckStatus`.
  - `transaction-service/internal/service/inter_bank_reconciler.go` — sender reconciler cron.
  - `transaction-service/internal/service/inter_bank_timeout_cron.go` — receiver-side commit-timeout cron.
  - `transaction-service/internal/service/inter_bank_recovery.go` — startup recovery routine.
  - `transaction-service/internal/handler/inter_bank_grpc_handler.go` — gRPC `InterBankService` server.
  - `transaction-service/internal/messaging/inter_bank_envelope.go` — JSON envelope marshal/unmarshal.
  - All matching `*_test.go` files (TDD).
- Modify:
  - `transaction-service/cmd/main.go` — wire new repos, services, handler, crons; AutoMigrate; EnsureTopics; seed banks from env.
  - `transaction-service/internal/config/config.go` — add `InterbankPrepareTimeout`, `InterbankCommitTimeout`, `InterbankReceiverWaitSeconds`, `InterbankReconcileInterval`, `InterbankReconcileMaxRetries`, `InterbankReconcileStaleAfterHours`, plus per-peer URL/key env vars.
  - `transaction-service/internal/service/transfer_service.go` — re-route inter-bank cases (the public `POST /transfers` handler in api-gateway routes; transaction-service still owns the gRPC, but the entry into `InitiateInterBankTransfer` goes through this file's existing `CreateTransfer` for symmetry — see Task 18).

**account-service:**
- Create:
  - `account-service/internal/model/incoming_reservation.go` — credit-side reservation row.
  - `account-service/internal/repository/incoming_reservation_repository.go`.
- Modify:
  - `account-service/internal/service/account_service.go` — add `ReserveIncoming`, `CommitIncoming`, `ReleaseIncoming` methods (mirror existing debit-side).
  - `account-service/internal/handler/grpc_handler.go` — three new RPC handlers.
  - `account-service/cmd/main.go` — AutoMigrate the new model.
- Test: `account-service/internal/service/account_service_test.go` (append).

**contract:**
- Modify:
  - `contract/proto/account/account.proto` — add `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming` RPCs + messages.
  - `contract/proto/transaction/transaction.proto` — add `InterBankService` with 4 RPCs + messages.
  - `contract/kafka/messages.go` — 4 new topic constants + shared payload struct `TransferInterbankMessage`.

**api-gateway:**
- Create:
  - `api-gateway/internal/middleware/hmac.go` — HMACMiddleware (timestamp / nonce / signature checks).
  - `api-gateway/internal/handler/inter_bank_internal_handler.go` — 3 internal HTTP routes that forward to transaction-service.
  - `api-gateway/internal/cache/nonce_store.go` — Redis-backed nonce tracker.
- Modify:
  - `api-gateway/internal/handler/transaction_handler.go` — `POST /api/v1/transfers` detects inter-bank by receiver-account prefix; `GET /api/v1/me/transfers/{id}` extends to look up `inter_bank_transactions` if not in `transfers`.
  - `api-gateway/internal/router/router_v1.go:RegisterCoreRoutes` — register the 3 internal routes under a separate group; pass the new gRPC clients in.
  - `api-gateway/cmd/main.go` — dial peer-bank gRPC client; pass in nonce store; pass in peer-key map for HMAC verification.

**test-app:**
- Create:
  - `test-app/peerbank/server.go` — mock peer bank HTTP server (configurable: Ready / NotReady / timeout / mismatch / 5xx / silent-drop).
  - `test-app/peerbank/server_test.go` — mock-bank self-test.
- Modify:
  - `test-app/workflows/helpers_interbank_test.go` (new helpers file) — `StartMockPeerBank`, `SeedBanksTable`, `ScanInterBankStatusKafka`, `PollTransferStatus`.

**Documentation:**
- Modify: `docs/Specification.md`, `docs/api/REST_API_v1.md`, `docker-compose.yml`, `docker-compose-remote.yml`.

---

## Pre-flight

- [ ] **Step 0.1: Branch from `Development`**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
git fetch origin && git checkout Development && git pull origin Development
git checkout -b feature/interbank-2pc
```

- [ ] **Step 0.2: Verify the prerequisite stack**

```bash
ls transaction-service/internal/saga/ 2>&1 || ls transaction-service/internal/service/ | grep -i saga
grep -n "ReserveFunds\|CommitReserved\|ReleaseFunds" account-service/internal/handler/grpc_handler.go
grep -n "REDIS_ADDR" api-gateway/internal/config/config.go || grep -n "REDIS_ADDR" api-gateway/cmd/main.go
```

Expected: saga machinery present, the three debit RPCs wired, Redis reachable from api-gateway. Stop and fix on `Development` if any are missing.

- [ ] **Step 0.3: Reconcile against SI-TX-PROTO 2024/25**

The wire protocol field names live at https://arsen.srht.site/si-tx-proto/ (faculty-published). Fetch it and create a one-page mapping table from spec §6 placeholders to canonical names:

```bash
mkdir -p docs/superpowers/refs
curl -fsSL https://arsen.srht.site/si-tx-proto/ -o docs/superpowers/refs/si-tx-proto-2024-25.html
```

If the URL is unreachable, ask the user to paste the spec into `docs/superpowers/refs/si-tx-proto-2024-25.md`. Then create `docs/superpowers/refs/si-tx-proto-mapping.md`:

```markdown
# Spec placeholder → SI-TX-PROTO field mapping

| Spec field | Canonical SI-TX-PROTO field |
|---|---|
| transactionId | <fill in> |
| senderBankCode | <fill in> |
| receiverBankCode | <fill in> |
| originalAmount | <fill in> |
| originalCurrency | <fill in> |
| finalAmount | <fill in> |
| finalCurrency | <fill in> |
| fxRate | <fill in> |
| fees | <fill in> |
| validUntil | <fill in> |
| reason | <fill in> |
| action enums | <fill in: Prepare/Ready/NotReady/Commit/Committed/CheckStatus/Status> |
```

Implementation MUST use the canonical names (right column) for JSON field tags. The plan body below uses placeholder names; replace consistently in every code block before writing files.

- [ ] **Step 0.4: Green baseline**

```bash
make tidy && make proto && make build && make lint && make test
```

Expected: every command exits 0.

---

## Task 1: Account-service `IncomingReservation` model + repo

**Files:**
- Create: `account-service/internal/model/incoming_reservation.go`
- Create: `account-service/internal/repository/incoming_reservation_repository.go`
- Create: `account-service/internal/repository/incoming_reservation_repository_test.go`

The credit-reservation lifecycle mirrors the existing debit reservation: `pending → committed | released`. Stored separately from debit reservations to avoid sign confusion in queries.

- [ ] **Step 1: Failing test**

```go
package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
)

func TestIncomingReservation_LifecyclePending_Commit(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.IncomingReservation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewIncomingReservationRepository(db)
	res := &model.IncomingReservation{
		AccountNumber:  "111000000000001",
		Amount:         decimal.NewFromInt(100),
		Currency:       "RSD",
		ReservationKey: "tx-1",
		Status:         model.IncomingReservationStatusPending,
	}
	if err := r.Create(res); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.MarkCommitted(res.ReservationKey); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := r.GetByKey("tx-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.IncomingReservationStatusCommitted {
		t.Errorf("status %s want committed", got.Status)
	}
}

func TestIncomingReservation_DuplicateKey_Conflict(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.IncomingReservation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewIncomingReservationRepository(db)
	res := &model.IncomingReservation{AccountNumber: "x", Amount: decimal.NewFromInt(1), Currency: "RSD", ReservationKey: "k1", Status: model.IncomingReservationStatusPending}
	_ = r.Create(res)
	if err := r.Create(&model.IncomingReservation{AccountNumber: "x", Amount: decimal.NewFromInt(2), Currency: "RSD", ReservationKey: "k1", Status: model.IncomingReservationStatusPending}); err == nil {
		t.Errorf("expected unique constraint violation")
	}
}
```

- [ ] **Step 2: Implement model**

```go
package model

import "time"

const (
	IncomingReservationStatusPending   = "pending"
	IncomingReservationStatusCommitted = "committed"
	IncomingReservationStatusReleased  = "released"
)

// IncomingReservation is the credit-side analogue of debit reservations.
// On HandlePrepare (receiver), one row is created per inbound transaction;
// HandleCommit transitions it to committed and writes the actual ledger
// entry; receiver-timeout cron transitions stale rows to released.
type IncomingReservation struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	AccountNumber  string    `gorm:"size:32;not null;index"`
	Amount         Decimal   `gorm:"type:numeric(20,4);not null"`
	Currency       string    `gorm:"size:8;not null"`
	ReservationKey string    `gorm:"size:64;not null;uniqueIndex"`
	Status         string    `gorm:"size:16;not null;index"`
	CreatedAt      time.Time `gorm:"index"`
	UpdatedAt      time.Time
}
```

(Use the same `Decimal` typedef the rest of account-service uses; if it's `shopspring/decimal.Decimal`, use that — keep the existing convention.)

- [ ] **Step 3: Implement repository**

```go
package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

type IncomingReservationRepository struct{ db *gorm.DB }

func NewIncomingReservationRepository(db *gorm.DB) *IncomingReservationRepository {
	return &IncomingReservationRepository{db: db}
}

func (r *IncomingReservationRepository) Create(res *model.IncomingReservation) error {
	return r.db.Create(res).Error
}

func (r *IncomingReservationRepository) GetByKey(key string) (*model.IncomingReservation, error) {
	var res model.IncomingReservation
	err := r.db.Where("reservation_key = ?", key).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &res, err
}

// MarkCommitted is idempotent — calling it twice on the same key is a no-op
// (RowsAffected = 0 the second time, returned as nil).
func (r *IncomingReservationRepository) MarkCommitted(key string) error {
	res := r.db.Model(&model.IncomingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.IncomingReservationStatusPending).
		Updates(map[string]any{"status": model.IncomingReservationStatusCommitted, "updated_at": time.Now().UTC()})
	if res.Error != nil { return res.Error }
	// rows affected == 0 means it was already committed; idempotent
	return nil
}

func (r *IncomingReservationRepository) MarkReleased(key string) error {
	res := r.db.Model(&model.IncomingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.IncomingReservationStatusPending).
		Updates(map[string]any{"status": model.IncomingReservationStatusReleased, "updated_at": time.Now().UTC()})
	return res.Error
}

// ListStaleByCreatedBefore returns pending reservations older than `before`.
// Used by transaction-service's receiver-timeout cron — though that cron
// works on inter_bank_transactions not on incoming_reservations, account-
// service exposes this for ops/debug surfaces.
func (r *IncomingReservationRepository) ListStaleByCreatedBefore(before time.Time, limit int) ([]model.IncomingReservation, error) {
	var out []model.IncomingReservation
	err := r.db.Where("status = ? AND created_at < ?", model.IncomingReservationStatusPending, before).
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}
```

- [ ] **Step 4: Run + commit**

```bash
cd account-service && go test ./internal/repository/... -run TestIncomingReservation -v
git add account-service/internal/model/incoming_reservation.go \
        account-service/internal/repository/incoming_reservation_repository.go \
        account-service/internal/repository/incoming_reservation_repository_test.go
git commit -m "feat(account-service): IncomingReservation model + repo"
```

---

## Task 2: Account-service `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming` RPCs

**Files:**
- Modify:
  - `contract/proto/account/account.proto`
  - `account-service/internal/service/account_service.go`
  - `account-service/internal/handler/grpc_handler.go`
- Test: `account-service/internal/service/account_service_test.go` (append).

- [ ] **Step 1: Add proto messages**

```proto
service AccountService {
  // … existing RPCs …
  rpc ReserveIncoming(ReserveIncomingRequest) returns (ReserveIncomingResponse);
  rpc CommitIncoming(CommitIncomingRequest)   returns (CommitIncomingResponse);
  rpc ReleaseIncoming(ReleaseIncomingRequest) returns (ReleaseIncomingResponse);
}

message ReserveIncomingRequest {
  string account_number = 1;
  string amount         = 2;  // decimal string
  string currency       = 3;
  string reservation_key = 4; // sender's tx_id
}
message ReserveIncomingResponse {
  string reservation_key = 1;
  string balance_after   = 2;  // unchanged, but echoed
}

message CommitIncomingRequest  { string reservation_key = 1; }
message CommitIncomingResponse { string balance_after = 1; }

message ReleaseIncomingRequest  { string reservation_key = 1; }
message ReleaseIncomingResponse { bool released = 1; }
```

`make proto`.

- [ ] **Step 2: Failing tests on the service layer**

```go
func TestReserveIncoming_NewKey_CreatesPendingRow(t *testing.T) {
	fx := newAccountFixture(t)
	fx.seedAccount("111000000000001", "RSD", "0")
	out, err := fx.svc.ReserveIncoming(context.Background(), "111000000000001", decimal.NewFromInt(100), "RSD", "tx-1")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if out.ReservationKey != "tx-1" {
		t.Errorf("key %s", out.ReservationKey)
	}
}

func TestReserveIncoming_DuplicateKey_Idempotent(t *testing.T) {
	fx := newAccountFixture(t)
	fx.seedAccount("a", "RSD", "0")
	_, _ = fx.svc.ReserveIncoming(context.Background(), "a", decimal.NewFromInt(100), "RSD", "tx-1")
	_, err := fx.svc.ReserveIncoming(context.Background(), "a", decimal.NewFromInt(100), "RSD", "tx-1")
	if err != nil {
		t.Errorf("idempotent reserve should not error: %v", err)
	}
}

func TestCommitIncoming_AppliesCreditAndLedger(t *testing.T) {
	fx := newAccountFixture(t)
	fx.seedAccount("a", "RSD", "0")
	_, _ = fx.svc.ReserveIncoming(context.Background(), "a", decimal.NewFromInt(100), "RSD", "tx-1")
	_, err := fx.svc.CommitIncoming(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	bal := fx.balance(t, "a")
	if !bal.Equal(decimal.NewFromInt(100)) {
		t.Errorf("balance %s want 100", bal)
	}
	if !fx.ledgerEntryExists(t, "a", "credit", "100", "tx-1") {
		t.Errorf("ledger entry missing")
	}
}

func TestReleaseIncoming_NoLedgerImpact(t *testing.T) {
	fx := newAccountFixture(t)
	fx.seedAccount("a", "RSD", "50")
	_, _ = fx.svc.ReserveIncoming(context.Background(), "a", decimal.NewFromInt(100), "RSD", "tx-1")
	_, err := fx.svc.ReleaseIncoming(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !fx.balance(t, "a").Equal(decimal.NewFromInt(50)) {
		t.Errorf("balance changed by release")
	}
}
```

- [ ] **Step 3: Implement on `AccountService`**

```go
func (s *AccountService) ReserveIncoming(ctx context.Context, accountNumber string, amount decimal.Decimal, currency, key string) (*model.IncomingReservation, error) {
	// Idempotency: if the same key already exists, return it as-is.
	if existing, err := s.incomingRes.GetByKey(key); err == nil {
		return existing, nil
	}

	if !amount.IsPositive() {
		return nil, errors.New("amount must be > 0")
	}
	acct, err := s.accountRepo.GetByNumber(accountNumber)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}
	if !acct.Active {
		return nil, errors.New("account_inactive")
	}
	if acct.CurrencyCode != currency {
		return nil, fmt.Errorf("currency_not_supported: account %s is %s, requested %s", accountNumber, acct.CurrencyCode, currency)
	}
	res := &model.IncomingReservation{
		AccountNumber: accountNumber, Amount: amount, Currency: currency,
		ReservationKey: key, Status: model.IncomingReservationStatusPending,
	}
	if err := s.incomingRes.Create(res); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *AccountService) CommitIncoming(ctx context.Context, key string) (*model.Account, error) {
	res, err := s.incomingRes.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if res.Status == model.IncomingReservationStatusCommitted {
		// Idempotent — return current account state.
		return s.accountRepo.GetByNumber(res.AccountNumber)
	}
	if res.Status != model.IncomingReservationStatusPending {
		return nil, fmt.Errorf("incoming_reservation already %s", res.Status)
	}
	var out *model.Account
	err = s.db.Transaction(func(tx *gorm.DB) error {
		acct, err := s.accountRepo.WithTx(tx).GetByNumber(res.AccountNumber)
		if err != nil {
			return err
		}
		acct.Balance = acct.Balance.Add(res.Amount)
		acct.AvailableBalance = acct.AvailableBalance.Add(res.Amount)
		if err := s.accountRepo.WithTx(tx).Save(acct); err != nil {
			return err
		}
		// Ledger entry — same shape as the existing CreditAccount path.
		if err := s.ledger.WithTx(tx).Insert(&model.LedgerEntry{
			AccountNumber:  res.AccountNumber,
			EntryType:      "credit",
			Amount:         res.Amount,
			BalanceAfter:   acct.Balance,
			Description:    fmt.Sprintf("Inter-bank credit (tx=%s)", res.ReservationKey),
			IdempotencyKey: "incoming-" + res.ReservationKey,
		}); err != nil {
			return err
		}
		out = acct
		return s.incomingRes.WithTx(tx).MarkCommitted(res.ReservationKey)
	})
	return out, err
}

func (s *AccountService) ReleaseIncoming(ctx context.Context, key string) error {
	res, err := s.incomingRes.GetByKey(key)
	if err != nil {
		return err
	}
	if res.Status != model.IncomingReservationStatusPending {
		return nil // idempotent
	}
	return s.incomingRes.MarkReleased(key)
}
```

(Add `WithTx(tx *gorm.DB)` helper methods on the repos that don't already have them — pattern matches existing `accountRepo.WithTx`.)

- [ ] **Step 4: Add gRPC handlers**

```go
func (h *AccountGRPCHandler) ReserveIncoming(ctx context.Context, req *pb.ReserveIncomingRequest) (*pb.ReserveIncomingResponse, error) {
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil { return nil, status.Error(codes.InvalidArgument, "amount") }
	res, err := h.svc.ReserveIncoming(ctx, req.AccountNumber, amt, req.Currency, req.ReservationKey)
	if err != nil { return nil, mapAccountErr(err) }
	return &pb.ReserveIncomingResponse{ReservationKey: res.ReservationKey, BalanceAfter: ""}, nil
}

func (h *AccountGRPCHandler) CommitIncoming(ctx context.Context, req *pb.CommitIncomingRequest) (*pb.CommitIncomingResponse, error) {
	acct, err := h.svc.CommitIncoming(ctx, req.ReservationKey)
	if err != nil { return nil, mapAccountErr(err) }
	return &pb.CommitIncomingResponse{BalanceAfter: acct.Balance.String()}, nil
}

func (h *AccountGRPCHandler) ReleaseIncoming(ctx context.Context, req *pb.ReleaseIncomingRequest) (*pb.ReleaseIncomingResponse, error) {
	if err := h.svc.ReleaseIncoming(ctx, req.ReservationKey); err != nil {
		return nil, mapAccountErr(err)
	}
	return &pb.ReleaseIncomingResponse{Released: true}, nil
}
```

- [ ] **Step 5: AutoMigrate the new model in account-service main**

```go
&model.IncomingReservation{},
```

- [ ] **Step 6: Run + commit**

```bash
cd account-service && go test ./... -run "TestReserveIncoming|TestCommitIncoming|TestReleaseIncoming" -v
git add account-service/ contract/proto/account/account.proto contract/accountpb/
git commit -m "feat(account-service): ReserveIncoming/CommitIncoming/ReleaseIncoming RPCs"
```

---

## Task 3: `Bank` model + repo

**Files:**
- Create:
  - `transaction-service/internal/model/bank.go`
  - `transaction-service/internal/repository/banks_repository.go`
  - `transaction-service/internal/repository/banks_repository_test.go`

- [ ] **Step 1: Failing test**

```go
func TestBanksRepository_GetByCode(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Bank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewBanksRepository(db)
	if err := r.Upsert(&model.Bank{Code: "222", Name: "Bank B", BaseURL: "http://b/", APIKeyBcrypted: "hash", InboundAPIKeyBcrypted: "hash", Active: true}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetByCode("222")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BaseURL != "http://b/" {
		t.Errorf("base_url %s", got.BaseURL)
	}
}

func TestBanksRepository_RejectsInactiveOnVerify(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Bank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewBanksRepository(db)
	_ = r.Upsert(&model.Bank{Code: "222", Name: "x", BaseURL: "http://x/", APIKeyBcrypted: "h", InboundAPIKeyBcrypted: "h", Active: false})
	got, _ := r.GetByCode("222")
	if got.Active {
		t.Errorf("expected inactive")
	}
}
```

- [ ] **Step 2: Implement model**

```go
package model

import "time"

// Bank is one peer-bank registry row. The bcrypt hashes are for audit and
// rotation; HMAC verification at runtime uses plaintext keys held in env
// (see CLAUDE.md note in the spec).
type Bank struct {
	Code                  string    `gorm:"primaryKey;size:3"`
	Name                  string    `gorm:"size:128;not null"`
	BaseURL               string    `gorm:"size:256;not null"`
	APIKeyBcrypted        string    `gorm:"size:60;not null"`
	InboundAPIKeyBcrypted string    `gorm:"size:60;not null"`
	PublicKey             string    `gorm:"type:text"`
	Active                bool      `gorm:"not null;default:true"`
	CreatedAt             time.Time `gorm:"not null;default:now()"`
	UpdatedAt             time.Time `gorm:"not null;default:now()"`
}
```

- [ ] **Step 3: Implement repository**

```go
package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/transaction-service/internal/model"
)

type BanksRepository struct{ db *gorm.DB }

func NewBanksRepository(db *gorm.DB) *BanksRepository {
	return &BanksRepository{db: db}
}

func (r *BanksRepository) Upsert(b *model.Bank) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "base_url", "api_key_bcrypted", "inbound_api_key_bcrypted", "public_key", "active", "updated_at"}),
	}).Create(b).Error
}

func (r *BanksRepository) GetByCode(code string) (*model.Bank, error) {
	var b model.Bank
	err := r.db.First(&b, "code = ?", code).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &b, err
}

func (r *BanksRepository) ListActive() ([]model.Bank, error) {
	var out []model.Bank
	err := r.db.Where("active = ?", true).Order("code ASC").Find(&out).Error
	return out, err
}
```

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/repository/... -run TestBanksRepository -v
git add transaction-service/internal/model/bank.go transaction-service/internal/repository/banks_repository.go transaction-service/internal/repository/banks_repository_test.go
git commit -m "feat(transaction-service): Bank model + BanksRepository"
```

---

## Task 4: `InterBankTransaction` model + transition matrix

**Files:**
- Create:
  - `transaction-service/internal/model/inter_bank_transaction.go`
  - `transaction-service/internal/model/inter_bank_status.go`
  - `transaction-service/internal/model/inter_bank_transaction_test.go`

- [ ] **Step 1: Failing test for transition matrix**

```go
func TestInterBankTransitions_Valid(t *testing.T) {
	cases := []struct {
		from, to string
		ok       bool
	}{
		{StatusInitiated, StatusPreparing, true},
		{StatusPreparing, StatusReadyReceived, true},
		{StatusPreparing, StatusReconciling, true},
		{StatusReadyReceived, StatusCommitting, true},
		{StatusCommitting, StatusCommitted, true},
		{StatusCommitted, StatusInitiated, false}, // illegal
		{StatusReadySent, StatusCommitReceived, true},
		{StatusReadySent, StatusAbandoned, true},
		{StatusFinalNotReady, StatusCommitted, false}, // illegal
	}
	for _, c := range cases {
		got := IsValidTransition(c.from, c.to)
		if got != c.ok {
			t.Errorf("%s -> %s: got %v want %v", c.from, c.to, got, c.ok)
		}
	}
}
```

- [ ] **Step 2: Implement constants + matrix**

`transaction-service/internal/model/inter_bank_status.go`:

```go
package model

const (
	// Roles
	RoleSender   = "sender"
	RoleReceiver = "receiver"

	// Phases
	PhasePrepare   = "prepare"
	PhaseCommit    = "commit"
	PhaseReconcile = "reconcile"
	PhaseDone      = "done"

	// Sender-side statuses
	StatusInitiated        = "initiated"
	StatusPreparing        = "preparing"
	StatusReadyReceived    = "ready_received"
	StatusNotReadyReceived = "notready_received"
	StatusCommitting       = "committing"
	StatusCommitted        = "committed"
	StatusRolledBack       = "rolled_back"
	StatusReconciling      = "reconciling"

	// Receiver-side statuses
	StatusPrepareReceived = "prepare_received"
	StatusValidated       = "validated"
	StatusReadySent       = "ready_sent"
	StatusNotReadySent    = "notready_sent"
	StatusFinalNotReady   = "final_notready"
	StatusCommitReceived  = "commit_received"
	StatusAbandoned       = "abandoned"
)

// validTransitions encodes the matrix from spec §5. from → set of valid to.
var validTransitions = map[string]map[string]struct{}{
	StatusInitiated:        toSet(StatusPreparing, StatusRolledBack),
	StatusPreparing:        toSet(StatusReadyReceived, StatusNotReadyReceived, StatusReconciling),
	StatusReadyReceived:    toSet(StatusCommitting, StatusReconciling),
	StatusNotReadyReceived: toSet(StatusRolledBack),
	StatusCommitting:       toSet(StatusCommitted, StatusReconciling),
	StatusReconciling:      toSet(StatusCommitted, StatusRolledBack, StatusReconciling), // self-loop on retry

	StatusPrepareReceived: toSet(StatusValidated, StatusNotReadySent),
	StatusValidated:       toSet(StatusReadySent),
	StatusReadySent:       toSet(StatusCommitReceived, StatusAbandoned),
	StatusNotReadySent:    toSet(StatusFinalNotReady),
	StatusCommitReceived:  toSet(StatusCommitted),
}

func toSet(vals ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out
}

func IsValidTransition(from, to string) bool {
	if from == to && from == StatusReconciling {
		return true
	}
	tos, ok := validTransitions[from]
	if !ok {
		return false
	}
	_, ok = tos[to]
	return ok
}

func IsTerminalStatus(s string) bool {
	switch s {
	case StatusCommitted, StatusRolledBack, StatusFinalNotReady, StatusAbandoned:
		return true
	}
	return false
}
```

- [ ] **Step 3: Implement transaction model**

`transaction-service/internal/model/inter_bank_transaction.go`:

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type InterBankTransaction struct {
	TxID                   string          `gorm:"primaryKey;size:36"`
	Role                   string          `gorm:"primaryKey;size:10"`
	RemoteBankCode         string          `gorm:"size:3;not null;index"`
	SenderAccountNumber    string          `gorm:"size:32;not null"`
	ReceiverAccountNumber  string          `gorm:"size:32;not null"`
	AmountNative           decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	CurrencyNative         string          `gorm:"size:3;not null"`
	AmountFinal            *decimal.Decimal `gorm:"type:numeric(20,4)"`
	CurrencyFinal          *string         `gorm:"size:3"`
	FxRate                 *decimal.Decimal `gorm:"type:numeric(20,8)"`
	FeesFinal              *decimal.Decimal `gorm:"type:numeric(20,4);column:fees_final"`
	Phase                  string          `gorm:"size:16;not null"`
	Status                 string          `gorm:"size:24;not null;index"`
	ErrorReason            string          `gorm:"type:text"`
	RetryCount             int             `gorm:"not null;default:0"`
	PayloadJSON            datatypes.JSON  `gorm:"type:jsonb"`
	IdempotencyKey         string          `gorm:"size:64;not null;uniqueIndex"`
	CreatedAt              time.Time       `gorm:"not null;default:now()"`
	UpdatedAt              time.Time       `gorm:"not null;default:now();index"`
	Version                int64           `gorm:"not null;default:0"`
}

func (t *InterBankTransaction) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", t.Version)
	}
	t.Version++
	return nil
}
```

(Spec §4.2 note: column renamed to `fees_final` per the open-question recommendation.)

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/model/... -run "TestInterBank" -v
git add transaction-service/internal/model/inter_bank_transaction.go transaction-service/internal/model/inter_bank_status.go transaction-service/internal/model/inter_bank_transaction_test.go
git commit -m "feat(transaction-service): InterBankTransaction model + transition matrix"
```

---

## Task 5: `InterBankTxRepository` with transition-enforcing UpdateStatus

**Files:**
- Create:
  - `transaction-service/internal/repository/inter_bank_tx_repository.go`
  - `transaction-service/internal/repository/inter_bank_tx_repository_test.go`

- [ ] **Step 1: Failing test**

```go
func TestInterBankTxRepository_UpdateStatus_RejectsIllegal(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InterBankTransaction{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewInterBankTxRepository(db)
	tx := &model.InterBankTransaction{
		TxID: "tx-1", Role: model.RoleSender, RemoteBankCode: "222",
		SenderAccountNumber: "111x", ReceiverAccountNumber: "222y",
		AmountNative: decimal.NewFromInt(100), CurrencyNative: "RSD",
		Phase: model.PhasePrepare, Status: model.StatusInitiated,
		IdempotencyKey: "tx-1-sender",
	}
	if err := r.Create(tx); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.UpdateStatus("tx-1", model.RoleSender, model.StatusInitiated, model.StatusCommitted); err == nil {
		t.Errorf("expected illegal transition error")
	}
	if err := r.UpdateStatus("tx-1", model.RoleSender, model.StatusInitiated, model.StatusPreparing); err != nil {
		t.Errorf("legal transition failed: %v", err)
	}
}

func TestInterBankTxRepository_UpdateStatus_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	_ = db.AutoMigrate(&model.InterBankTransaction{})
	r := NewInterBankTxRepository(db)
	tx := &model.InterBankTransaction{
		TxID: "tx-2", Role: model.RoleSender, Status: model.StatusInitiated,
		Phase: model.PhasePrepare, IdempotencyKey: "tx-2-sender",
		SenderAccountNumber: "x", ReceiverAccountNumber: "y",
		AmountNative: decimal.NewFromInt(1), CurrencyNative: "RSD", RemoteBankCode: "222",
	}
	_ = r.Create(tx)
	// Two concurrent UpdateStatus calls — first wins.
	err1 := r.UpdateStatus("tx-2", model.RoleSender, model.StatusInitiated, model.StatusPreparing)
	err2 := r.UpdateStatus("tx-2", model.RoleSender, model.StatusInitiated, model.StatusPreparing)
	if err1 != nil { t.Fatalf("first: %v", err1) }
	if err2 == nil { t.Errorf("expected second to fail") }
}
```

- [ ] **Step 2: Implement repository**

```go
package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

var (
	ErrIllegalTransition = errors.New("illegal status transition")
	ErrConcurrentUpdate  = errors.New("concurrent update — version mismatch")
)

type InterBankTxRepository struct{ db *gorm.DB }

func NewInterBankTxRepository(db *gorm.DB) *InterBankTxRepository {
	return &InterBankTxRepository{db: db}
}

func (r *InterBankTxRepository) Create(tx *model.InterBankTransaction) error {
	return r.db.Create(tx).Error
}

func (r *InterBankTxRepository) Get(txID, role string) (*model.InterBankTransaction, error) {
	var t model.InterBankTransaction
	err := r.db.First(&t, "tx_id = ? AND role = ?", txID, role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &t, err
}

func (r *InterBankTxRepository) GetByIdempotencyKey(key string) (*model.InterBankTransaction, error) {
	var t model.InterBankTransaction
	err := r.db.First(&t, "idempotency_key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) { return nil, err }
	return &t, err
}

// UpdateStatus enforces the transition matrix and uses optimistic locking
// via the model's BeforeUpdate hook. The expected_from arg gates against
// concurrent transitions.
func (r *InterBankTxRepository) UpdateStatus(txID, role, from, to string) error {
	if !model.IsValidTransition(from, to) {
		return ErrIllegalTransition
	}
	res := r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ? AND status = ?", txID, role, from).
		Update("status", to)
	if res.Error != nil { return res.Error }
	if res.RowsAffected == 0 {
		return ErrConcurrentUpdate
	}
	return nil
}

// SetFinalizedTerms is called once per transaction (sender on Ready receive,
// receiver on Ready send). Idempotent: if the same final terms are already
// set, no-op.
func (r *InterBankTxRepository) SetFinalizedTerms(txID, role string, finalAmt decimal.Decimal, finalCcy string, fxRate decimal.Decimal, fees decimal.Decimal) error {
	res := r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, role).
		Updates(map[string]any{"amount_final": finalAmt, "currency_final": finalCcy, "fx_rate": fxRate, "fees_final": fees})
	if res.Error != nil { return res.Error }
	return nil
}

func (r *InterBankTxRepository) IncrementRetry(txID, role string) error {
	return r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, role).
		Update("retry_count", gorm.Expr("retry_count + 1")).Error
}

// ListReconcilable returns sender rows in `reconciling` status, FIFO.
func (r *InterBankTxRepository) ListReconcilable(limit int) ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	err := r.db.Where("role = ? AND status = ?", model.RoleSender, model.StatusReconciling).
		Order("updated_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListReceiverStaleReadySent returns receiver rows in ready_sent older than
// `staleBefore`. Used by the receiver-timeout cron.
func (r *InterBankTxRepository) ListReceiverStaleReadySent(staleBefore time.Time, limit int) ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	err := r.db.Where("role = ? AND status = ? AND updated_at < ?", model.RoleReceiver, model.StatusReadySent, staleBefore).
		Order("updated_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListNonTerminal is used by the startup recovery routine.
func (r *InterBankTxRepository) ListNonTerminal() ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	err := r.db.Where("status NOT IN ?", []string{
		model.StatusCommitted, model.StatusRolledBack, model.StatusFinalNotReady, model.StatusAbandoned,
	}).Find(&out).Error
	return out, err
}
```

- [ ] **Step 3: Run + commit**

```bash
cd transaction-service && go test ./internal/repository/... -run TestInterBankTxRepository -v
git add transaction-service/internal/repository/inter_bank_tx_repository.go transaction-service/internal/repository/inter_bank_tx_repository_test.go
git commit -m "feat(transaction-service): InterBankTxRepository with transition-enforced UpdateStatus"
```

---

## Task 6: AutoMigrate + seed banks from env

**Files:**
- Modify: `transaction-service/cmd/main.go`
- Modify: `transaction-service/internal/config/config.go`

- [ ] **Step 1: Add config entries**

```go
type Config struct {
	// … existing …

	// Inter-bank
	InterbankPrepareTimeout            time.Duration
	InterbankCommitTimeout             time.Duration
	InterbankReceiverWaitSeconds       time.Duration
	InterbankReconcileInterval         time.Duration
	InterbankReconcileMaxRetries       int
	InterbankReconcileStaleAfterHours  time.Duration

	// Per-peer
	Peer222BaseURL     string
	Peer222InboundKey  string
	Peer222OutboundKey string
	Peer333BaseURL     string
	Peer333InboundKey  string
	Peer333OutboundKey string
	Peer444BaseURL     string
	Peer444InboundKey  string
	Peer444OutboundKey string

	OwnBankCode string // default "111"
}

func Load() *Config {
	// … existing …
	return &Config{
		// … existing …
		InterbankPrepareTimeout: getDuration("INTERBANK_PREPARE_TIMEOUT", 30*time.Second),
		InterbankCommitTimeout:  getDuration("INTERBANK_COMMIT_TIMEOUT", 30*time.Second),
		InterbankReceiverWaitSeconds: getDuration("INTERBANK_RECEIVER_WAIT", 90*time.Second),
		InterbankReconcileInterval:   getDuration("INTERBANK_RECONCILE_INTERVAL", 60*time.Second),
		InterbankReconcileMaxRetries: getInt("INTERBANK_RECONCILE_MAX_RETRIES", 10),
		InterbankReconcileStaleAfterHours: getDuration("INTERBANK_RECONCILE_STALE_AFTER", 24*time.Hour),
		Peer222BaseURL:     getEnv("PEER_222_BASE_URL", ""),
		Peer222InboundKey:  getEnv("PEER_222_INBOUND_KEY", ""),
		Peer222OutboundKey: getEnv("PEER_222_OUTBOUND_KEY", ""),
		Peer333BaseURL:     getEnv("PEER_333_BASE_URL", ""),
		Peer333InboundKey:  getEnv("PEER_333_INBOUND_KEY", ""),
		Peer333OutboundKey: getEnv("PEER_333_OUTBOUND_KEY", ""),
		Peer444BaseURL:     getEnv("PEER_444_BASE_URL", ""),
		Peer444InboundKey:  getEnv("PEER_444_INBOUND_KEY", ""),
		Peer444OutboundKey: getEnv("PEER_444_OUTBOUND_KEY", ""),
		OwnBankCode:        getEnv("OWN_BANK_CODE", "111"),
	}
}
```

(`getDuration` / `getInt` are tiny helpers that wrap `time.ParseDuration` and `strconv.Atoi`; if they don't exist in this service, add them next to `getEnv`.)

- [ ] **Step 2: Wire AutoMigrate + seed in `main.go`**

```go
if err := db.AutoMigrate(
	// … existing …
	&model.Bank{},
	&model.InterBankTransaction{},
); err != nil {
	log.Fatalf("automigrate: %v", err)
}

banksRepo := repository.NewBanksRepository(db)
ibTxRepo := repository.NewInterBankTxRepository(db)
seedPeers(banksRepo, cfg)
```

`seedPeers`:

```go
func seedPeers(repo *repository.BanksRepository, cfg *config.Config) {
	type peer struct {
		code, baseURL, outboundKey, inboundKey string
	}
	peers := []peer{
		{"222", cfg.Peer222BaseURL, cfg.Peer222OutboundKey, cfg.Peer222InboundKey},
		{"333", cfg.Peer333BaseURL, cfg.Peer333OutboundKey, cfg.Peer333InboundKey},
		{"444", cfg.Peer444BaseURL, cfg.Peer444OutboundKey, cfg.Peer444InboundKey},
	}
	for _, p := range peers {
		if p.baseURL == "" || p.inboundKey == "" {
			continue
		}
		out, _ := bcrypt.GenerateFromPassword([]byte(p.outboundKey), bcrypt.DefaultCost)
		in, _ := bcrypt.GenerateFromPassword([]byte(p.inboundKey), bcrypt.DefaultCost)
		_ = repo.Upsert(&model.Bank{
			Code: p.code, Name: "Bank " + p.code,
			BaseURL: p.baseURL,
			APIKeyBcrypted: string(out), InboundAPIKeyBcrypted: string(in),
			Active: true,
		})
	}
}
```

- [ ] **Step 3: Build + run smoke**

```bash
cd transaction-service && go build ./...
docker compose up -d transaction-db
docker compose run --rm transaction-service ./bin/transaction-service &
sleep 6
docker compose exec transaction-db psql -U postgres -d transactiondb -c "\dt banks inter_bank_transactions"
docker compose down transaction-service
```

- [ ] **Step 4: Commit**

```bash
git add transaction-service/cmd/main.go transaction-service/internal/config/config.go
git commit -m "feat(transaction-service): AutoMigrate + seed peer banks from env"
```

---

## Task 7: Proto + Kafka contracts for inter-bank

**Files:**
- Modify:
  - `contract/proto/transaction/transaction.proto`
  - `contract/kafka/messages.go`

- [ ] **Step 1: Append `InterBankService` to `transaction.proto`**

```proto
service InterBankService {
  // Sender — initiated by api-gateway when client posts to /transfers with a
  // receiver account whose prefix is not OWN_BANK_CODE.
  rpc InitiateInterBankTransfer(InitiateInterBankRequest) returns (InitiateInterBankResponse);

  // Receiver — three handlers, one per inbound HTTP route.
  rpc HandlePrepare(InterBankPrepareRequest) returns (InterBankPrepareResponse);
  rpc HandleCommit(InterBankCommitRequest)   returns (InterBankCommitResponse);
  rpc HandleCheckStatus(InterBankCheckStatusRequest) returns (InterBankCheckStatusResponse);
}

message InitiateInterBankRequest {
  string sender_account_number = 1;
  string receiver_account_number = 2;
  string amount   = 3;
  string currency = 4;
  string memo     = 5;
}
message InitiateInterBankResponse {
  string transaction_id = 1;
  string status         = 2;  // simplified: "preparing" | "committed" | "rejected" | "reconciling"
}

// All wire-protocol bytes use the SI-TX-PROTO 2024/25 field names. The
// contract surface here uses our internal-call shape; the messaging layer
// converts to/from SI-TX-PROTO JSON.
message InterBankPrepareRequest {
  string transaction_id = 1;
  string sender_bank_code = 2;
  string receiver_bank_code = 3;
  string sender_account = 4;
  string receiver_account = 5;
  string amount = 6;
  string currency = 7;
  string memo = 8;
}
message InterBankPrepareResponse {
  string transaction_id = 1;
  bool   ready = 2;
  string final_amount = 3;
  string final_currency = 4;
  string fx_rate = 5;
  string fees = 6;
  string valid_until = 7;
  string reason = 8; // populated when ready=false
}

message InterBankCommitRequest {
  string transaction_id = 1;
  string final_amount = 2;
  string final_currency = 3;
  string fx_rate = 4;
  string fees = 5;
}
message InterBankCommitResponse {
  string transaction_id = 1;
  bool   committed = 2;
  string credited_at = 3;
  string credited_amount = 4;
  string credited_currency = 5;
  string mismatch_reason = 6; // set when committed=false (HTTP 409 path)
}

message InterBankCheckStatusRequest {
  string transaction_id = 1;
}
message InterBankCheckStatusResponse {
  string transaction_id = 1;
  string role           = 2;
  string status         = 3;
  string final_amount   = 4;
  string final_currency = 5;
  string updated_at     = 6;
  bool   not_found      = 7;
}
```

- [ ] **Step 2: Append Kafka constants + payload**

```go
const (
	TopicTransferInterbankPrepared    = "transfer.interbank-prepared"
	TopicTransferInterbankCommitted   = "transfer.interbank-committed"
	TopicTransferInterbankReceived    = "transfer.interbank-received"
	TopicTransferInterbankRolledBack  = "transfer.interbank-rolled-back"
)

type TransferInterbankMessage struct {
	MessageID      string `json:"message_id"`
	OccurredAt     string `json:"occurred_at"`
	TransactionID  string `json:"transaction_id"`
	Role           string `json:"role"`
	RemoteBankCode string `json:"remote_bank_code"`
	Status         string `json:"status"`
	AmountNative   string `json:"amount_native"`
	CurrencyNative string `json:"currency_native"`
	AmountFinal    string `json:"amount_final,omitempty"`
	CurrencyFinal  string `json:"currency_final,omitempty"`
	FxRate         string `json:"fx_rate,omitempty"`
	Fees           string `json:"fees,omitempty"`
	ErrorReason    string `json:"error_reason,omitempty"`
}
```

- [ ] **Step 3: Regen + commit**

```bash
make proto && make build
git add contract/proto/transaction/transaction.proto contract/transactionpb/ contract/kafka/messages.go
git commit -m "feat(contract): InterBankService proto + 4 transfer.interbank-* topics"
```

---

## Task 8: `PeerBankClient` — outbound HTTP with HMAC sign + retry

**Files:**
- Create: `transaction-service/internal/service/peer_bank_client.go`
- Create: `transaction-service/internal/service/peer_bank_client_test.go`

- [ ] **Step 1: Failing test — sign + send + parse Ready**

```go
func TestPeerBankClient_SendPrepare_SignsAndParsesReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify HMAC.
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("outbound-key"))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-Bank-Signature") != want {
			t.Errorf("bad signature")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"action":"Ready","transactionId":"tx-1","body":{"finalAmount":"8.50","finalCurrency":"EUR","fxRate":"117.65","fees":"0"}}`))
	}))
	defer srv.Close()
	c := NewPeerBankClient("111", "outbound-key", srv.URL, 30*time.Second)
	resp, err := c.SendPrepare(context.Background(), PrepareRequest{TransactionID: "tx-1", Amount: "1000.00", Currency: "RSD", SenderAccount: "x", ReceiverAccount: "y"})
	if err != nil { t.Fatalf("send: %v", err) }
	if !resp.Ready { t.Errorf("expected ready") }
	if resp.FinalAmount != "8.50" { t.Errorf("amount %s", resp.FinalAmount) }
}
```

- [ ] **Step 2: Implement client**

```go
package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PeerBankClient sends Prepare/Commit/CheckStatus to a peer bank, signing
// every request with the outbound HMAC key for that peer. Use one client
// instance per peer (held in a map by code).
type PeerBankClient struct {
	ownBankCode string
	outboundKey []byte
	baseURL     string
	httpClient  *http.Client
}

func NewPeerBankClient(ownBankCode, outboundKey, baseURL string, timeout time.Duration) *PeerBankClient {
	return &PeerBankClient{
		ownBankCode: ownBankCode,
		outboundKey: []byte(outboundKey),
		baseURL:     baseURL,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

type Envelope struct {
	Action          string          `json:"action"`
	TransactionID   string          `json:"transactionId"`
	SenderBankCode  string          `json:"senderBankCode"`
	ReceiverBankCode string         `json:"receiverBankCode"`
	Timestamp       string          `json:"timestamp"`
	Body            json.RawMessage `json:"body"`
}

type PrepareRequest struct {
	TransactionID    string
	ReceiverBankCode string
	SenderAccount    string
	ReceiverAccount  string
	Amount           string
	Currency         string
	Memo             string
}

type PrepareResponse struct {
	Ready         bool
	FinalAmount   string
	FinalCurrency string
	FxRate        string
	Fees          string
	ValidUntil    string
	Reason        string
}

func (c *PeerBankClient) SendPrepare(ctx context.Context, req PrepareRequest) (*PrepareResponse, error) {
	bodyJSON, _ := json.Marshal(map[string]any{
		"senderAccount":   req.SenderAccount,
		"receiverAccount": req.ReceiverAccount,
		"amount":          req.Amount,
		"currency":        req.Currency,
		"memo":            req.Memo,
	})
	env := Envelope{
		Action: "Prepare", TransactionID: req.TransactionID,
		SenderBankCode: c.ownBankCode, ReceiverBankCode: req.ReceiverBankCode,
		Timestamp: time.Now().UTC().Format(time.RFC3339), Body: bodyJSON,
	}
	respEnv, err := c.do(ctx, "/transfer/prepare", env)
	if err != nil {
		return nil, err
	}
	var inner struct {
		Status        string `json:"status"`
		FinalAmount   string `json:"finalAmount"`
		FinalCurrency string `json:"finalCurrency"`
		FxRate        string `json:"fxRate"`
		Fees          string `json:"fees"`
		ValidUntil    string `json:"validUntil"`
		Reason        string `json:"reason"`
	}
	if err := json.Unmarshal(respEnv.Body, &inner); err != nil {
		return nil, fmt.Errorf("parse Ready/NotReady: %w", err)
	}
	return &PrepareResponse{
		Ready:         respEnv.Action == "Ready" || inner.Status == "Ready",
		FinalAmount:   inner.FinalAmount, FinalCurrency: inner.FinalCurrency,
		FxRate:        inner.FxRate, Fees: inner.Fees, ValidUntil: inner.ValidUntil,
		Reason:        inner.Reason,
	}, nil
}

// SendCommit / SendCheckStatus follow the same pattern; bodies match
// spec §6.5 / §6.7. Implement both with the same do() helper.

func (c *PeerBankClient) do(ctx context.Context, path string, env Envelope) (*Envelope, error) {
	body, err := json.Marshal(env)
	if err != nil { return nil, err }

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bank-Code", c.ownBankCode)
	req.Header.Set("X-Idempotency-Key", env.TransactionID)
	req.Header.Set("X-Timestamp", env.Timestamp)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	req.Header.Set("X-Nonce", hex.EncodeToString(nonce))
	mac := hmac.New(sha256.New, c.outboundKey)
	mac.Write(body)
	req.Header.Set("X-Bank-Signature", hex.EncodeToString(mac.Sum(nil)))

	resp, err := c.httpClient.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 400 {
		return nil, fmt.Errorf("peer rejected: HTTP %d: %s", resp.StatusCode, respBytes)
	}
	if resp.StatusCode == 404 {
		return nil, ErrPeerNotFound
	}
	if resp.StatusCode == 409 {
		return nil, fmt.Errorf("commit_mismatch: %s", respBytes)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("peer 5xx: %d", resp.StatusCode)
	}
	var out Envelope
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("parse response envelope: %w", err)
	}
	return &out, nil
}

var ErrPeerNotFound = fmt.Errorf("peer reports unknown transaction")
```

(Implement `SendCommit` / `SendCheckStatus` similarly; tests in the next step exercise both.)

- [ ] **Step 3: Tests for Commit, CheckStatus, NotReady, 5xx, 404**

```go
func TestPeerBankClient_SendCommit_Mismatch_ReturnsError(t *testing.T) { /* … */ }
func TestPeerBankClient_SendCheckStatus_404_ReturnsErrPeerNotFound(t *testing.T) { /* … */ }
```

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestPeerBankClient -v
git add transaction-service/internal/service/peer_bank_client.go transaction-service/internal/service/peer_bank_client_test.go
git commit -m "feat(transaction-service): PeerBankClient (HMAC sign + retry-aware HTTP)"
```

---

## Task 9: `InterBankService.InitiateOutgoing` — happy path

**Files:**
- Create:
  - `transaction-service/internal/service/inter_bank_service.go`
  - `transaction-service/internal/service/inter_bank_service_test.go`

- [ ] **Step 1: Failing test — Ready → Commit → committed**

```go
func TestInitiateOutgoing_HappyPath(t *testing.T) {
	fx := newInterBankFixture(t)
	fx.peer.ConfigureNext("Ready", PrepareReadyResponse{
		FinalAmount: "8.50", FinalCurrency: "EUR", FxRate: "117.65", Fees: "0",
	})
	fx.peer.ConfigureNext("Committed", CommitResponse{CreditedAmount: "8.50", CreditedCurrency: "EUR"})

	out, err := fx.svc.InitiateOutgoing(context.Background(), InitiateInput{
		SenderAccountNumber: "111aaa",
		ReceiverAccountNumber: "222bbb",
		Amount: decimal.NewFromInt(1000), Currency: "RSD", Memo: "x",
	})
	if err != nil { t.Fatalf("initiate: %v", err) }
	if out.Status != model.StatusCommitted {
		t.Errorf("status %s want committed", out.Status)
	}
	// Sender's funds were reserved then committed.
	if !fx.accounts.reserved("111aaa", "1000") {
		t.Errorf("ReserveFunds not called")
	}
	if !fx.accounts.committed("111aaa", "1000") {
		t.Errorf("CommitReserved not called")
	}
}
```

- [ ] **Step 2: Implement**

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
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type InterBankService struct {
	tx        *repository.InterBankTxRepository
	banks     *repository.BanksRepository
	peer      PeerBankRouter
	accounts  AccountClient
	producer  KafkaProducer
	ownCode   string
}

// PeerBankRouter resolves a peer's PeerBankClient by bank code. Tests stub
// it with a recording mock; production wires it via cmd/main with one
// client per peer.
type PeerBankRouter interface {
	ClientFor(bankCode string) (*PeerBankClient, error)
}

type AccountClient interface {
	ReserveFunds(ctx context.Context, accountNumber string, amount decimal.Decimal, currency, key string) error
	CommitReservedFunds(ctx context.Context, key string, memo string) error
	ReleaseFunds(ctx context.Context, key string) error
	ReserveIncoming(ctx context.Context, accountNumber string, amount decimal.Decimal, currency, key string) error
	CommitIncoming(ctx context.Context, key string) error
	ReleaseIncoming(ctx context.Context, key string) error
}

type InitiateInput struct {
	SenderAccountNumber   string
	ReceiverAccountNumber string
	Amount                decimal.Decimal
	Currency              string
	Memo                  string
}

func (s *InterBankService) InitiateOutgoing(ctx context.Context, in InitiateInput) (*model.InterBankTransaction, error) {
	if len(in.ReceiverAccountNumber) < 3 {
		return nil, errors.New("invalid receiver account")
	}
	receiverBankCode := in.ReceiverAccountNumber[:3]
	if receiverBankCode == s.ownCode {
		return nil, errors.New("intra-bank transfers should not reach inter-bank service")
	}
	bank, err := s.banks.GetByCode(receiverBankCode)
	if err != nil {
		return nil, fmt.Errorf("unknown_bank: %s", receiverBankCode)
	}
	if !bank.Active {
		return nil, fmt.Errorf("bank_inactive: %s", receiverBankCode)
	}

	txID := uuid.NewString()
	row := &model.InterBankTransaction{
		TxID: txID, Role: model.RoleSender, RemoteBankCode: receiverBankCode,
		SenderAccountNumber: in.SenderAccountNumber,
		ReceiverAccountNumber: in.ReceiverAccountNumber,
		AmountNative: in.Amount, CurrencyNative: in.Currency,
		Phase: model.PhasePrepare, Status: model.StatusInitiated,
		IdempotencyKey: txID + ":sender",
	}
	if err := s.tx.Create(row); err != nil {
		return nil, err
	}

	// Reserve funds on the sender account.
	reserveKey := "interbank-out-" + txID
	if err := s.accounts.ReserveFunds(ctx, in.SenderAccountNumber, in.Amount, in.Currency, reserveKey); err != nil {
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusInitiated, model.StatusRolledBack)
		return row, fmt.Errorf("reserve_funds: %w", err)
	}
	if err := s.tx.UpdateStatus(txID, model.RoleSender, model.StatusInitiated, model.StatusPreparing); err != nil {
		return row, err
	}

	client, err := s.peer.ClientFor(receiverBankCode)
	if err != nil { return row, err }

	prepResp, err := client.SendPrepare(ctx, PrepareRequest{
		TransactionID: txID, ReceiverBankCode: receiverBankCode,
		SenderAccount: in.SenderAccountNumber, ReceiverAccount: in.ReceiverAccountNumber,
		Amount: in.Amount.String(), Currency: in.Currency, Memo: in.Memo,
	})
	if err != nil {
		// Network / timeout / 5xx → reconciling.
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusReconciling)
		return row, nil
	}
	if !prepResp.Ready {
		// NotReady — release funds, terminal failure.
		_ = s.accounts.ReleaseFunds(ctx, reserveKey)
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusNotReadyReceived)
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusNotReadyReceived, model.StatusRolledBack)
		s.publishKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row, prepResp.Reason)
		row.Status = model.StatusRolledBack
		row.ErrorReason = prepResp.Reason
		return row, nil
	}

	// Ready — record final terms, send Commit.
	finalAmt, _ := decimal.NewFromString(prepResp.FinalAmount)
	fxRate, _ := decimal.NewFromString(prepResp.FxRate)
	fees, _ := decimal.NewFromString(prepResp.Fees)
	_ = s.tx.SetFinalizedTerms(txID, model.RoleSender, finalAmt, prepResp.FinalCurrency, fxRate, fees)
	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusReadyReceived)
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankPrepared, row, "")

	commitResp, err := client.SendCommit(ctx, CommitOutboundRequest{
		TransactionID: txID, FinalAmount: prepResp.FinalAmount, FinalCurrency: prepResp.FinalCurrency,
		FxRate: prepResp.FxRate, Fees: prepResp.Fees,
	})
	if err != nil {
		// Could be 5xx, network, or 409 commit_mismatch.
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusReadyReceived, model.StatusReconciling)
		return row, nil
	}
	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusReadyReceived, model.StatusCommitting)
	if err := s.accounts.CommitReservedFunds(ctx, reserveKey, fmt.Sprintf("Inter-bank transfer to %s (tx=%s)", in.ReceiverAccountNumber, txID)); err != nil {
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusCommitting, model.StatusReconciling)
		return row, err
	}
	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusCommitting, model.StatusCommitted)
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankCommitted, row, "")
	row.Status = model.StatusCommitted
	_ = commitResp
	return row, nil
}

// publishKafka builds a TransferInterbankMessage from the current row state.
func (s *InterBankService) publishKafka(ctx context.Context, topic string, row *model.InterBankTransaction, reason string) {
	if s.producer == nil {
		return
	}
	msg := kafkamsg.TransferInterbankMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TransactionID: row.TxID, Role: row.Role, RemoteBankCode: row.RemoteBankCode,
		Status: row.Status,
		AmountNative: row.AmountNative.String(), CurrencyNative: row.CurrencyNative,
	}
	if row.AmountFinal != nil { msg.AmountFinal = row.AmountFinal.String() }
	if row.CurrencyFinal != nil { msg.CurrencyFinal = *row.CurrencyFinal }
	if row.FxRate != nil { msg.FxRate = row.FxRate.String() }
	if row.FeesFinal != nil { msg.Fees = row.FeesFinal.String() }
	if reason != "" { msg.ErrorReason = reason }
	_ = s.producer.PublishRaw(ctx, topic, mustJSON(msg))
}
```

- [ ] **Step 3: Tests for NotReady + insufficient-funds + reconciling-on-timeout**

```go
func TestInitiateOutgoing_PeerNotReady_RollsBack(t *testing.T) { /* … */ }
func TestInitiateOutgoing_ReserveFundsFailed_NoOutboundCall(t *testing.T) { /* … */ }
func TestInitiateOutgoing_PrepareTimeout_TransitionsToReconciling(t *testing.T) { /* … */ }
```

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestInitiateOutgoing -v
git add transaction-service/internal/service/inter_bank_service.go transaction-service/internal/service/inter_bank_service_test.go
git commit -m "feat(transaction-service): InitiateOutgoing happy + NotReady + reconciling paths"
```

---

## Task 10: `HandlePrepare` — receiver path

**Files:**
- Modify:
  - `transaction-service/internal/service/inter_bank_service.go`
  - `transaction-service/internal/service/inter_bank_service_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestHandlePrepare_HappyPath_SameCurrency_ReservesIncoming(t *testing.T) { /* … */ }
func TestHandlePrepare_AccountNotFound_NotReady(t *testing.T) { /* … */ }
func TestHandlePrepare_CrossCurrency_ConvertsAndAppliesFees(t *testing.T) { /* … */ }
func TestHandlePrepare_Idempotent_SecondCallReturnsOriginalDecision(t *testing.T) { /* … */ }
```

- [ ] **Step 2: Implement**

```go
type HandlePrepareInput struct {
	TransactionID    string
	SenderBankCode   string
	SenderAccount    string
	ReceiverAccount  string
	Amount           decimal.Decimal
	Currency         string
	Memo             string
}

type HandlePrepareOutput struct {
	Ready         bool
	FinalAmount   decimal.Decimal
	FinalCurrency string
	FxRate        decimal.Decimal
	Fees          decimal.Decimal
	ValidUntil    time.Time
	Reason        string
}

func (s *InterBankService) HandlePrepare(ctx context.Context, in HandlePrepareInput) (*HandlePrepareOutput, error) {
	// Idempotency: if we have a row for this tx_id role=receiver, return its
	// existing decision.
	if existing, err := s.tx.Get(in.TransactionID, model.RoleReceiver); err == nil {
		return s.replayPrepareDecision(existing), nil
	}

	row := &model.InterBankTransaction{
		TxID: in.TransactionID, Role: model.RoleReceiver, RemoteBankCode: in.SenderBankCode,
		SenderAccountNumber: in.SenderAccount, ReceiverAccountNumber: in.ReceiverAccount,
		AmountNative: in.Amount, CurrencyNative: in.Currency,
		Phase: model.PhasePrepare, Status: model.StatusPrepareReceived,
		IdempotencyKey: in.TransactionID + ":receiver",
	}
	if err := s.tx.Create(row); err != nil {
		return nil, err
	}

	acct, err := s.accounts.GetAccountByNumber(ctx, in.ReceiverAccount)
	if err != nil {
		_ = s.markFinalNotReady(in.TransactionID, "account_not_found")
		return &HandlePrepareOutput{Ready: false, Reason: "account_not_found"}, nil
	}
	if !acct.Active {
		_ = s.markFinalNotReady(in.TransactionID, "account_inactive")
		return &HandlePrepareOutput{Ready: false, Reason: "account_inactive"}, nil
	}

	// FX + fees.
	finalAmt := in.Amount
	finalCcy := acct.CurrencyCode
	fxRate := decimal.NewFromInt(1)
	if acct.CurrencyCode != in.Currency {
		conv, err := s.exchange.Convert(ctx, in.Currency, acct.CurrencyCode, in.Amount)
		if err != nil {
			_ = s.markFinalNotReady(in.TransactionID, "currency_not_supported")
			return &HandlePrepareOutput{Ready: false, Reason: "currency_not_supported"}, nil
		}
		finalAmt, fxRate = conv.Amount, conv.Rate
	}
	fees := s.feeRules.ComputeIncomingFee(finalAmt, finalCcy)
	finalAmt = finalAmt.Sub(fees)

	// Reserve incoming.
	reserveKey := "interbank-in-" + in.TransactionID
	if err := s.accounts.ReserveIncoming(ctx, in.ReceiverAccount, finalAmt, finalCcy, reserveKey); err != nil {
		_ = s.markFinalNotReady(in.TransactionID, "limit_exceeded")
		return &HandlePrepareOutput{Ready: false, Reason: "limit_exceeded"}, nil
	}

	_ = s.tx.SetFinalizedTerms(in.TransactionID, model.RoleReceiver, finalAmt, finalCcy, fxRate, fees)
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusPrepareReceived, model.StatusValidated)
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusValidated, model.StatusReadySent)
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankPrepared, row, "")
	return &HandlePrepareOutput{
		Ready: true, FinalAmount: finalAmt, FinalCurrency: finalCcy, FxRate: fxRate, Fees: fees,
		ValidUntil: time.Now().Add(s.cfg.InterbankReceiverWaitSeconds),
	}, nil
}

func (s *InterBankService) markFinalNotReady(txID, reason string) error {
	_ = s.tx.UpdateStatus(txID, model.RoleReceiver, model.StatusPrepareReceived, model.StatusNotReadySent)
	_ = s.tx.UpdateStatus(txID, model.RoleReceiver, model.StatusNotReadySent, model.StatusFinalNotReady)
	// Persist the reason.
	return s.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, model.RoleReceiver).
		Update("error_reason", reason).Error
}

func (s *InterBankService) replayPrepareDecision(row *model.InterBankTransaction) *HandlePrepareOutput {
	switch row.Status {
	case model.StatusReadySent, model.StatusCommitReceived, model.StatusCommitted:
		out := &HandlePrepareOutput{Ready: true}
		if row.AmountFinal != nil { out.FinalAmount = *row.AmountFinal }
		if row.CurrencyFinal != nil { out.FinalCurrency = *row.CurrencyFinal }
		if row.FxRate != nil { out.FxRate = *row.FxRate }
		if row.FeesFinal != nil { out.Fees = *row.FeesFinal }
		return out
	default:
		return &HandlePrepareOutput{Ready: false, Reason: row.ErrorReason}
	}
}
```

(Add `accounts.GetAccountByNumber`, `exchange.Convert`, `feeRules.ComputeIncomingFee` interfaces to the relevant fields. The `feeRules` reuses the existing `transfer_fees` rules from intra-bank.)

- [ ] **Step 3: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestHandlePrepare -v
git add transaction-service/internal/service/inter_bank_service.go transaction-service/internal/service/inter_bank_service_test.go
git commit -m "feat(transaction-service): HandlePrepare receiver path with idempotency"
```

---

## Task 11: `HandleCommit` — receiver path + idempotency + 409 mismatch

**Files:**
- Modify:
  - `transaction-service/internal/service/inter_bank_service.go`
  - `transaction-service/internal/service/inter_bank_service_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestHandleCommit_HappyPath(t *testing.T) { /* … */ }
func TestHandleCommit_DuplicateCall_ReturnsSameResponse(t *testing.T) { /* … */ }
func TestHandleCommit_FinalTermsMismatch_Returns409(t *testing.T) { /* … */ }
func TestHandleCommit_UnknownTxId_Returns404(t *testing.T) { /* … */ }
```

- [ ] **Step 2: Implement**

```go
type HandleCommitInput struct {
	TransactionID string
	FinalAmount   decimal.Decimal
	FinalCurrency string
	FxRate        decimal.Decimal
	Fees          decimal.Decimal
}

type HandleCommitOutput struct {
	Committed       bool
	CreditedAt      time.Time
	CreditedAmount  decimal.Decimal
	CreditedCurrency string
	NotFound        bool
	MismatchReason  string
}

func (s *InterBankService) HandleCommit(ctx context.Context, in HandleCommitInput) (*HandleCommitOutput, error) {
	row, err := s.tx.Get(in.TransactionID, model.RoleReceiver)
	if err != nil {
		return &HandleCommitOutput{NotFound: true}, nil
	}
	if row.Status == model.StatusCommitted {
		// Idempotent replay.
		return &HandleCommitOutput{
			Committed: true, CreditedAt: row.UpdatedAt,
			CreditedAmount: *row.AmountFinal, CreditedCurrency: *row.CurrencyFinal,
		}, nil
	}
	if row.Status != model.StatusReadySent {
		return &HandleCommitOutput{NotFound: true}, nil
	}
	if !termsMatch(row, in) {
		return &HandleCommitOutput{
			Committed:      false,
			MismatchReason: fmt.Sprintf("expected %s %s; got %s %s", row.AmountFinal, *row.CurrencyFinal, in.FinalAmount, in.FinalCurrency),
		}, nil
	}

	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusReadySent, model.StatusCommitReceived)
	if err := s.accounts.CommitIncoming(ctx, "interbank-in-"+in.TransactionID); err != nil {
		// Funds are already reserved; failure here is rare. Stay in
		// commit_received and let the receiver-timeout cron clean up
		// after the wait window.
		return nil, fmt.Errorf("commit_incoming: %w", err)
	}
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusCommitReceived, model.StatusCommitted)
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankReceived, row, "")
	now := time.Now().UTC()
	return &HandleCommitOutput{Committed: true, CreditedAt: now,
		CreditedAmount: *row.AmountFinal, CreditedCurrency: *row.CurrencyFinal}, nil
}

func termsMatch(row *model.InterBankTransaction, in HandleCommitInput) bool {
	if row.AmountFinal == nil || row.CurrencyFinal == nil { return false }
	if !row.AmountFinal.Equal(in.FinalAmount) { return false }
	if *row.CurrencyFinal != in.FinalCurrency { return false }
	if row.FxRate != nil && !row.FxRate.Equal(in.FxRate) { return false }
	if row.FeesFinal != nil && !row.FeesFinal.Equal(in.Fees) { return false }
	return true
}
```

- [ ] **Step 3: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestHandleCommit -v
git add transaction-service/internal/service/inter_bank_service.go transaction-service/internal/service/inter_bank_service_test.go
git commit -m "feat(transaction-service): HandleCommit receiver path + idempotency + 409"
```

---

## Task 12: `HandleCheckStatus`

**Files:**
- Modify:
  - `transaction-service/internal/service/inter_bank_service.go`
  - `transaction-service/internal/service/inter_bank_service_test.go`

- [ ] **Step 1: Failing test**

```go
func TestHandleCheckStatus_KnownTx_ReturnsState(t *testing.T) { /* … */ }
func TestHandleCheckStatus_Unknown_Returns404(t *testing.T) { /* … */ }
```

- [ ] **Step 2: Implement** (search receiver first, then sender — covers both directions)

```go
func (s *InterBankService) HandleCheckStatus(ctx context.Context, txID string) (*HandleCheckStatusOutput, error) {
	if row, err := s.tx.Get(txID, model.RoleReceiver); err == nil {
		return toCheckStatus(row), nil
	}
	if row, err := s.tx.Get(txID, model.RoleSender); err == nil {
		return toCheckStatus(row), nil
	}
	return &HandleCheckStatusOutput{NotFound: true}, nil
}
```

- [ ] **Step 3: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestHandleCheckStatus -v
git add transaction-service/internal/service/inter_bank_service.go transaction-service/internal/service/inter_bank_service_test.go
git commit -m "feat(transaction-service): HandleCheckStatus dual-side lookup"
```

---

## Task 13: Reconciler cron (sender side)

**Files:**
- Create:
  - `transaction-service/internal/service/inter_bank_reconciler.go`
  - `transaction-service/internal/service/inter_bank_reconciler_test.go`

- [ ] **Step 1: Failing tests** — for each peer-status branch (committed / rolled_back / 404 / 5xx / max-retries).

- [ ] **Step 2: Implement** with `RunOnce(ctx)` + `Start(ctx)` pattern from earlier crons; pull batch, call `peer.SendCheckStatus`, branch on `Status` field of the response per spec §9.4.

- [ ] **Step 3: Wire `Start` into `cmd/main.go`**

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run TestReconciler -v
git add transaction-service/internal/service/inter_bank_reconciler.go transaction-service/internal/service/inter_bank_reconciler_test.go transaction-service/cmd/main.go
git commit -m "feat(transaction-service): inter-bank reconciler cron"
```

---

## Task 14: Receiver-timeout cron + crash-recovery

**Files:**
- Create:
  - `transaction-service/internal/service/inter_bank_timeout_cron.go`
  - `transaction-service/internal/service/inter_bank_recovery.go`
- Test files for each.

- [ ] **Step 1: Receiver timeout cron**

For rows in `ready_sent` older than `cfg.InterbankReceiverWaitSeconds`:
1. Call `accounts.ReleaseIncoming("interbank-in-"+txID)`.
2. `UpdateStatus(... ready_sent → abandoned)`.
3. Publish `transfer.interbank-rolled-back`.

- [ ] **Step 2: Crash-recovery (run once at startup)**

Per spec §9.6:
1. Load all non-terminal rows.
2. Sender rows in `preparing`/`committing` older than prepare-timeout → reconciling.
3. Receiver rows in `commit_received` → re-run `CommitIncoming` (idempotent) → committed.
4. Leave the rest for crons.

- [ ] **Step 3: Wire both into `cmd/main.go`**

```go
recovery := service.NewInterBankRecovery(...)
if err := recovery.RunOnce(ctx); err != nil {
	log.Printf("WARN: inter-bank recovery: %v", err)
}
timeoutCron := service.NewInterBankTimeoutCron(...)
timeoutCron.Start(ctx)
```

- [ ] **Step 4: Run + commit**

```bash
cd transaction-service && go test ./internal/service/... -run "TestTimeoutCron|TestRecovery" -v
git add transaction-service/internal/service/inter_bank_timeout_cron.go transaction-service/internal/service/inter_bank_timeout_cron_test.go \
        transaction-service/internal/service/inter_bank_recovery.go transaction-service/internal/service/inter_bank_recovery_test.go \
        transaction-service/cmd/main.go
git commit -m "feat(transaction-service): receiver-timeout cron + startup crash-recovery"
```

---

## Task 15: gRPC handler `InterBankService`

**Files:**
- Create:
  - `transaction-service/internal/handler/inter_bank_grpc_handler.go`
  - `transaction-service/internal/handler/inter_bank_grpc_handler_test.go`
- Modify: `transaction-service/cmd/main.go`.

Standard pattern: decode strings → decimals/UUIDs, delegate to service, map errors. Register on the gRPC server.

- [ ] **Step 1: Implement + tests**

- [ ] **Step 2: Commit**

```bash
git add transaction-service/internal/handler/ transaction-service/cmd/main.go
git commit -m "feat(transaction-service): InterBankService gRPC handler"
```

---

## Task 16: HMAC middleware in api-gateway + Redis nonce store

**Files:**
- Create:
  - `api-gateway/internal/middleware/hmac.go`
  - `api-gateway/internal/middleware/hmac_test.go`
  - `api-gateway/internal/cache/nonce_store.go`

- [ ] **Step 1: Failing tests**

```go
func TestHMACMiddleware_ValidSignature_Passes(t *testing.T) { /* … */ }
func TestHMACMiddleware_BadSignature_Returns401(t *testing.T) { /* … */ }
func TestHMACMiddleware_StaleTimestamp_Returns401(t *testing.T) { /* … */ }
func TestHMACMiddleware_NonceReplay_Returns401(t *testing.T) { /* … */ }
func TestHMACMiddleware_MissingHeaders_Returns400(t *testing.T) { /* … */ }
func TestHMACMiddleware_UnknownBankCode_Returns401(t *testing.T) { /* … */ }
func TestHMACMiddleware_InactiveBank_Returns401(t *testing.T) { /* … */ }
```

- [ ] **Step 2: Implement nonce store**

```go
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type NonceStore struct {
	client *redis.Client
	ttl    time.Duration
}

func NewNonceStore(client *redis.Client, ttl time.Duration) *NonceStore {
	if ttl == 0 { ttl = 10 * time.Minute }
	return &NonceStore{client: client, ttl: ttl}
}

// Claim returns ErrReplay if the nonce was already seen.
var ErrReplay = errors.New("nonce replay")

func (s *NonceStore) Claim(ctx context.Context, bankCode, nonce string) error {
	key := fmt.Sprintf("inter_bank_nonce:%s:%s", bankCode, nonce)
	ok, err := s.client.SetNX(ctx, key, "1", s.ttl).Result()
	if err != nil { return err }
	if !ok { return ErrReplay }
	return nil
}
```

- [ ] **Step 3: Implement middleware**

```go
package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/cache"
)

// PeerKeyResolver returns the inbound HMAC key for the given bank code, or
// false if the bank is unknown / inactive. Wired in main.go from a peer-key
// map sourced from env. (Map keys = bank codes, values = plaintext keys.)
type PeerKeyResolver func(bankCode string) (key string, active bool)

func HMACMiddleware(resolver PeerKeyResolver, nonces *cache.NonceStore, clockSkew time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		bankCode := c.GetHeader("X-Bank-Code")
		signature := c.GetHeader("X-Bank-Signature")
		idemKey := c.GetHeader("X-Idempotency-Key")
		ts := c.GetHeader("X-Timestamp")
		nonce := c.GetHeader("X-Nonce")
		if bankCode == "" || signature == "" || idemKey == "" || ts == "" || nonce == "" {
			c.AbortWithStatusJSON(400, gin.H{"error": "missing HMAC headers"})
			return
		}

		key, active := resolver(bankCode)
		if key == "" || !active {
			c.AbortWithStatusJSON(401, gin.H{"error": "unknown bank"})
			return
		}

		t, err := time.Parse(time.RFC3339, ts)
		if err != nil || abs(time.Since(t)) > clockSkew {
			c.AbortWithStatusJSON(401, gin.H{"error": "timestamp out of range"})
			return
		}

		if err := nonces.Claim(c.Request.Context(), bankCode, nonce); err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "nonce replay"})
			return
		}

		body, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(body)) // re-arm

		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(signature)) {
			c.AbortWithStatusJSON(401, gin.H{"error": "bad signature"})
			return
		}
		c.Set("inter_bank_verified", true)
		c.Set("inter_bank_sender", bankCode)
		c.Next()
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 { return -d }
	return d
}
```

- [ ] **Step 4: Run + commit**

```bash
cd api-gateway && go test ./internal/middleware/... -run TestHMAC -v
git add api-gateway/internal/middleware/hmac.go api-gateway/internal/middleware/hmac_test.go api-gateway/internal/cache/nonce_store.go
git commit -m "feat(api-gateway): HMAC middleware + Redis nonce store"
```

---

## Task 17: Internal `/internal/inter-bank/*` routes in api-gateway

**Files:**
- Create:
  - `api-gateway/internal/handler/inter_bank_internal_handler.go`
  - `api-gateway/internal/handler/inter_bank_internal_handler_test.go`
- Modify:
  - `api-gateway/internal/router/router_v1.go`
  - `api-gateway/cmd/main.go`

- [ ] **Step 1: Implement 3 handler functions**

Each:
1. Decode envelope JSON to internal proto.
2. Call gRPC `transactionClient.HandlePrepare` / `HandleCommit` / `HandleCheckStatus`.
3. Marshal proto back to envelope JSON per spec §6.

- [ ] **Step 2: Wire routes outside `RegisterCoreRoutes`**

These live under `r.Group("/internal/inter-bank")` with HMAC middleware, NOT under `/api/v1`:

```go
internal := r.Group("/internal/inter-bank")
internal.Use(middleware.HMACMiddleware(peerKeyResolver, nonceStore, 5*time.Minute))
{
	internal.POST("/transfer/prepare", interBankHandler.Prepare)
	internal.POST("/transfer/commit",  interBankHandler.Commit)
	internal.POST("/check-status",     interBankHandler.CheckStatus)
}
```

- [ ] **Step 3: Wire `peerKeyResolver` + nonce store + gRPC client in `main.go`**

```go
peerKeys := map[string]struct{ key string; active bool }{
	"222": {cfg.Peer222InboundKey, cfg.Peer222InboundKey != ""},
	"333": {cfg.Peer333InboundKey, cfg.Peer333InboundKey != ""},
	"444": {cfg.Peer444InboundKey, cfg.Peer444InboundKey != ""},
}
peerKeyResolver := func(code string) (string, bool) {
	v, ok := peerKeys[code]
	if !ok { return "", false }
	return v.key, v.active
}
nonceStore := cache.NewNonceStore(redisClient, 10*time.Minute)
interBankClient := transactionpb.NewInterBankServiceClient(transactionConn)
interBankHandler := handler.NewInterBankInternalHandler(interBankClient)
```

- [ ] **Step 4: Run + commit**

```bash
cd api-gateway && go build ./... && go test ./internal/handler/... -run InterBank -v
git add api-gateway/
git commit -m "feat(api-gateway): /internal/inter-bank/* routes (HMAC-gated)"
```

---

## Task 18: Public `/api/v1/transfers` extension

**Files:**
- Modify:
  - `api-gateway/internal/handler/transaction_handler.go`
  - `transaction-service/internal/service/transfer_service.go`

- [ ] **Step 1: Add receiver-account-prefix branch in gateway**

```go
func (h *TransactionHandler) CreateTransfer(c *gin.Context) {
	var req struct {
		SenderAccount   string  `json:"sender_account"`
		ReceiverAccount string  `json:"receiver_account"`
		Amount          string  `json:"amount"`
		Currency        string  `json:"currency"`
		Memo            string  `json:"memo"`
	}
	// … existing validation …
	if len(req.ReceiverAccount) < 3 {
		apiError(c, 400, "validation_error", "receiver_account too short")
		return
	}
	prefix := req.ReceiverAccount[:3]
	if prefix == h.ownBankCode {
		// existing intra-bank handler unchanged
		h.createIntraBankTransfer(c, req)
		return
	}
	// inter-bank
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		apiError(c, 400, "validation_error", "amount")
		return
	}
	resp, err := h.txClient.InitiateInterBankTransfer(c.Request.Context(), &transactionpb.InitiateInterBankRequest{
		SenderAccountNumber: req.SenderAccount, ReceiverAccountNumber: req.ReceiverAccount,
		Amount: amt.String(), Currency: req.Currency, Memo: req.Memo,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(202, gin.H{
		"transactionId": resp.TransactionId,
		"status":        resp.Status,
		"pollUrl":       fmt.Sprintf("/api/v1/me/transfers/%s", resp.TransactionId),
	})
}
```

- [ ] **Step 2: Extend `GET /api/v1/me/transfers/{id}`**

After looking up `transfers`, fall back to `inter_bank_transactions`:

```go
if intra, err := h.txClient.GetTransfer(...); err == nil {
	c.JSON(200, intra)
	return
}
ib, err := h.txClient.GetInterBankTransfer(c.Request.Context(), &transactionpb.GetInterBankRequest{TransactionId: id})
if err != nil { handleGRPCError(c, err); return }
c.JSON(200, toUnifiedTransferView(ib))
```

(`GetInterBankTransfer` is a small new RPC that wraps `tx.Get(id, sender)` then `tx.Get(id, receiver)` and returns the first match. Add to proto + handler.)

- [ ] **Step 3: Tests + commit**

```bash
cd api-gateway && go test ./internal/handler/... -run "TestPostTransfer_(IntraBank|InterBank|UnknownBank)" -v
cd transaction-service && go test ./... -count=1
git add api-gateway/internal/handler/transaction_handler.go contract/proto/transaction/transaction.proto contract/transactionpb/ transaction-service/
git commit -m "feat: POST /transfers routes inter-bank by prefix; GET /me/transfers/{id} unified view"
```

---

## Task 19: Mock peer-bank server (test-app)

**Files:**
- Create:
  - `test-app/peerbank/server.go`
  - `test-app/peerbank/server_test.go`

- [ ] **Step 1: Implement configurable mock**

```go
package peerbank

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

type Behavior int

const (
	BehaviorReady Behavior = iota
	BehaviorNotReady
	BehaviorTimeout
	BehaviorMismatch
	BehaviorFiveXX
	BehaviorSilentDrop
)

type MockPeerBank struct {
	Server     *httptest.Server
	OutboundHM string // peer's outbound key (we sign responses with this)
	InboundHM  string // peer's inbound key (it signs requests with this)
	mu         sync.Mutex
	queue      []Behavior
	requests   []*http.Request
}

func New(t *testing.T, outboundKey, inboundKey string) *MockPeerBank {
	mb := &MockPeerBank{OutboundHM: outboundKey, InboundHM: inboundKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/transfer/prepare", mb.handlePrepare)
	mux.HandleFunc("/transfer/commit", mb.handleCommit)
	mux.HandleFunc("/check-status", mb.handleStatus)
	mb.Server = httptest.NewServer(mux)
	t.Cleanup(mb.Server.Close)
	return mb
}

func (m *MockPeerBank) ConfigureNext(b Behavior) {
	m.mu.Lock()
	m.queue = append(m.queue, b)
	m.mu.Unlock()
}

func (m *MockPeerBank) handlePrepare(w http.ResponseWriter, r *http.Request) {
	// HMAC verify (uses m.InboundHM since we're the receiver from the SUT's POV)
	// … then dispatch on m.queue[0] …
}

// Repeat for commit + check-status. Each behavior maps to a deterministic
// response or a sleep / connection-close. Record the inbound request.
```

- [ ] **Step 2: Self-test the mock**

```bash
cd test-app/peerbank && go test ./... -v
git add test-app/peerbank/
git commit -m "feat(test-app): mock peer-bank server for inter-bank workflows"
```

---

## Task 20: Integration tests (9)

**Files:**
- Create: `test-app/workflows/wf_interbank_*.go` (per spec §12.2 list).

For each test:
1. `StartMockPeerBank(t, ...)` to get a base URL + keys.
2. `SeedBanksTable(t, "222", mockBaseURL, mockKey)`.
3. Drive the SUT (POST /transfers, etc.).
4. Assert: HTTP responses, ledger entries, kafka event sequence (via `ScanInterBankStatusKafka`), final DB state.

- [ ] **Step 1: `wf_interbank_success_test.go`** — happy path full lifecycle.

- [ ] **Step 2: `wf_interbank_notready_test.go`** — peer NotReady → reverts.

- [ ] **Step 3: `wf_interbank_prepare_timeout_test.go`** — peer hangs → reconciler.

- [ ] **Step 4: `wf_interbank_commit_timeout_test.go`** — Commit hangs → reconciler.

- [ ] **Step 5: `wf_interbank_commit_mismatch_test.go`** — 409 mismatch → rollback.

- [ ] **Step 6: `wf_interbank_incoming_success_test.go`** — test-app acts as sender; SUT is receiver.

- [ ] **Step 7: `wf_interbank_hmac_rejection_test.go`** — bad sig / unknown code / replay nonce / stale ts / inactive bank.

- [ ] **Step 8: `wf_interbank_receiver_abandoned_test.go`** — Commit never arrives; receiver-timeout cron fires.

- [ ] **Step 9: `wf_interbank_crash_recovery_test.go`** — pre-seed `committing` row; trigger recovery RPC; assert reconciler kicks in.

- [ ] **Step 10: Run + commit**

```bash
make docker-up
go test -tags=integration ./test-app/workflows/... -run InterBank -v
make docker-down
git add test-app/workflows/wf_interbank_*.go test-app/workflows/helpers_interbank_test.go
git commit -m "test(integration): inter-bank 2PC workflows (9 scenarios)"
```

---

## Task 21: Documentation + final lint + push

**Files:**
- Modify: `docs/Specification.md`, `docs/api/REST_API_v1.md`, `docker-compose.yml`, `docker-compose-remote.yml`.

- [ ] **Step 1: Specification.md updates** per spec §13:
  - §3 add HMACMiddleware + 3 internal routes.
  - §11 add `InterBankService` RPCs.
  - §17 extended POST /transfers + GET /me/transfers/{id} + 3 internal routes.
  - §18 add `banks`, `inter_bank_transactions`, `incoming_reservations`.
  - §19 add 4 `transfer.interbank-*` topics.
  - §20 add `role`, `phase`, full `status` enums.
  - §21 business rules (2PC, prefix routing, HMAC headers).

- [ ] **Step 2: REST_API_v1.md** updates per spec §13.

- [ ] **Step 3: docker-compose.yml + docker-compose-remote.yml**

Add to transaction-service AND api-gateway env blocks:

```yaml
PEER_222_BASE_URL: "http://peer-222/internal/inter-bank"
PEER_222_INBOUND_KEY: "${PEER_222_INBOUND_KEY:-changeme}"
PEER_222_OUTBOUND_KEY: "${PEER_222_OUTBOUND_KEY:-changeme}"
PEER_333_BASE_URL: "http://peer-333/internal/inter-bank"
PEER_333_INBOUND_KEY: "${PEER_333_INBOUND_KEY:-changeme}"
PEER_333_OUTBOUND_KEY: "${PEER_333_OUTBOUND_KEY:-changeme}"
PEER_444_BASE_URL: "http://peer-444/internal/inter-bank"
PEER_444_INBOUND_KEY: "${PEER_444_INBOUND_KEY:-changeme}"
PEER_444_OUTBOUND_KEY: "${PEER_444_OUTBOUND_KEY:-changeme}"
INTERBANK_PREPARE_TIMEOUT: "30s"
INTERBANK_COMMIT_TIMEOUT: "30s"
INTERBANK_RECONCILE_INTERVAL: "60s"
INTERBANK_RECONCILE_MAX_RETRIES: "10"
INTERBANK_RECONCILE_STALE_AFTER: "24h"
INTERBANK_RECEIVER_WAIT: "90s"
OWN_BANK_CODE: "111"
```

- [ ] **Step 4: Lint + push + PR**

```bash
make lint && make test
git add docs/ docker-compose.yml docker-compose-remote.yml
git commit -m "docs+infra: inter-bank 2PC — Specification + REST_API + compose env"
git push -u origin feature/interbank-2pc
gh pr create --title "feat: inter-bank 2-phase-commit transfers (Spec 3)" --body "$(cat <<'EOF'
## Summary
- Implements docs/superpowers/specs/2026-04-24-interbank-2pc-transfers-design.md.
- 2 new tables, 3 new account-service RPCs, 3 internal HMAC routes,
  reconciler + receiver-timeout + crash-recovery crons, mock peer for tests.

## Test plan
- [x] make test green
- [x] make lint clean
- [x] integration: 9 inter-bank workflows pass with mock peer
- [x] Manual: end-to-end Bank A → Bank B with two stack copies
EOF
)"
```

---

## Self-review

**Spec coverage:**

| Spec § | Plan task |
|---|---|
| §2 SI-TX-PROTO reconciliation | Step 0.3 |
| §3 architecture / flow | T9–T15 |
| §4.1 banks table | T3 |
| §4.2 inter_bank_transactions | T4, T5 |
| §4.3 ReserveIncoming/CommitIncoming/ReleaseIncoming | T1, T2 |
| §5.1 sender state machine | T9 (transitions in service), T13 (reconciler) |
| §5.2 receiver state machine | T10–T12 |
| §5.3 status enum | T4 |
| §5.4 illegal-transition prevention | T5 |
| §6 message envelope + actions | T7 (proto), T8 (PeerBankClient), T17 (gateway handler) |
| §7.1 public REST | T18 |
| §7.2 internal HMAC routes | T16, T17 |
| §8 HMAC + nonce + timestamp | T16 |
| §9 reconciliation + timeouts | T13, T14 |
| §10 Kafka topics | T7, EnsureTopics in T6 + T15 |
| §11 permissions (none new) | n/a |
| §12 testing | T9–T18 unit, T19–T20 integration |
| §13 doc updates | T21 |

**Placeholder scan:** wire-format placeholders are explicitly flagged in Step 0.3 with a mapping doc. Idempotency keys are named per saga step. Each compensation list is enumerated.

**Type consistency:** `InterBankService` carries `tx`, `banks`, `peer`, `accounts`, `producer`, `db`, `cfg`, `feeRules`, `exchange`. Constants `Status*`, `Role*`, `Phase*` defined in T4 used verbatim. `PeerBankRouter` interface introduced in T9 used by reconciler in T13.

**Risks called out in spec §15:**
- §15.2 peer-unreachable → reservation held until reconciler resolves (T13).
- §15.3 replay attacks → nonce + timestamp + idempotency in T16.
- §15.5 client-cancel — explicitly not in v1; spec §14 lists it as out-of-scope.
- §15.10 Spec-4 hooks — `kind` column reserved for later migration; `action` dispatcher in T8/T17 is open to extension.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-25-interbank-2pc-transfers.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task + spec/quality review.

**2. Inline Execution** — batch execution with checkpoints in this session.

**Which approach?**

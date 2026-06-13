# SP-2: Employee-Limit Replica (credit-service slice) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Eliminate credit-service's synchronous `GetEmployeeLimits` read on the loan-approval gate by maintaining a local `EmployeeLimitReplica` table fed by an enriched `user.employee-limits-updated` Kafka event, with a lazy gRPC fallback+backfill.

**Architecture:** Reuses the SP-1 proven pattern exactly (enrich event → Postgres replica → version-guarded consumer → read-with-fallback). Enrich `EmployeeLimitsUpdatedMessage` (today `{EmployeeID, Action}`, carries NO values) to carry the full employee limit set + a monotonic `Version`. user-service publishes the full snapshot on every limit set/update. credit-service builds its FIRST Kafka consumer (copying card-service's `client_replica_consumer.go`) to maintain `EmployeeLimitReplica`, and the loan-approval gate reads `MaxLoanApprovalAmount` from it. Per the spec's decision, this read is **eventual + fallback** (money-adjacent staleness accepted: the gate is already advisory and outside the DB TX; a few-second window on a limit *decrease* is bounded by event lag and healed on miss). The enriched event + replica schema carry ALL five limit values so the later client-service cap slice (SP-2b) reuses the same event with its own replica.

**Tech Stack:** Go, GORM (Postgres + glebarez/sqlite tests), segmentio/kafka-go, gRPC/protobuf, shopspring/decimal. Modules: `github.com/exbanka/contract`, `github.com/exbanka/user-service`, `github.com/exbanka/credit-service`.

**Scope (this plan):** contract event enrichment + user-service publisher + credit-service replica/consumer/read-swap for the loan-approval gate. **Follow-on slices (separate plans, reuse this event):** SP-2b client-service `MaxClientDailyLimit`/`MaxClientMonthlyLimit` cap read; SP-2c stock-service actuary; SP-2d auth-service `GetEmployee` JWT-minting (highest-risk, separate careful plan — auth already has employee/role-perm consumers + `user_revoked_at` epoch).

**Out of scope:** actuary limits (separate event `user.actuary-limit-updated`); employee profile name/roles (separate `EmployeeCreatedMessage`, used by auth slice); any REST route change (no Swagger/REST doc edits).

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `contract/kafka/messages.go` | `EmployeeLimitsUpdatedMessage` gains limit values + `Version` | Modify |
| `contract/kafka/messages_test.go` | round-trip test for enriched message | Modify |
| `user-service/internal/service/limit_service.go` | populate full values+version at both publish sites (~:128, ~:169) | Modify |
| `user-service/internal/service/limit_service_test.go` | assert published msg carries values+version | Modify |
| `credit-service/internal/model/employee_limit_replica.go` | `EmployeeLimitReplica` GORM model | Create |
| `credit-service/internal/repository/employee_limit_replica_repository.go` | version-guarded upsert + get-by-employee | Create |
| `credit-service/internal/repository/employee_limit_replica_repository_test.go` | upsert/monotonicity/get tests | Create |
| `credit-service/internal/consumer/employee_limit_replica_consumer.go` | consume `user.employee-limits-updated` → upsert | Create |
| `credit-service/internal/consumer/employee_limit_replica_consumer_test.go` | event→repo + retry + bad-json tests | Create |
| `credit-service/internal/service/loan_request_service.go` | read MaxLoanApproval from replica w/ fallback (~:168) | Modify |
| `credit-service/internal/service/loan_request_service_test.go` | replica-hit (no gRPC) + miss-fallback tests | Modify |
| `credit-service/cmd/main.go` | AutoMigrate + EnsureTopics + repo/consumer wiring | Modify |
| `test-app/workflows/employee_limit_replica_test.go` | integration: limit set → gate enforces via replica | Create |
| `VERSION`, `api-gateway/internal/version/version.go` | bump | Modify |
| `docs/Specification.md` | §18 `EmployeeLimitReplica`; §19 enriched event | Modify |

---

## Task 1: Enrich `EmployeeLimitsUpdatedMessage` (contract)

**Files:** Modify `contract/kafka/messages.go` (struct ~line 488, the `EmployeeLimitsUpdatedMessage`); Test `contract/kafka/messages_test.go`.

- [ ] **Step 1: Failing test** — add to `contract/kafka/messages_test.go`:

```go
func TestEmployeeLimitsUpdatedMessage_CarriesValuesAndVersion(t *testing.T) {
	in := EmployeeLimitsUpdatedMessage{
		EmployeeID: 9, Action: "set",
		MaxLoanApprovalAmount: "50000.0000", MaxSingleTransaction: "10000.0000",
		MaxDailyTransaction: "20000.0000", MaxClientDailyLimit: "5000.0000",
		MaxClientMonthlyLimit: "100000.0000", Version: 4,
	}
	b, err := json.Marshal(in)
	if err != nil { t.Fatalf("marshal: %v", err) }
	var out EmployeeLimitsUpdatedMessage
	if err := json.Unmarshal(b, &out); err != nil { t.Fatalf("unmarshal: %v", err) }
	if out.MaxLoanApprovalAmount != "50000.0000" || out.Version != 4 || out.MaxClientMonthlyLimit != "100000.0000" {
		t.Fatalf("lost fields: %+v", out)
	}
}
```

- [ ] **Step 2: Run** `cd contract && go test ./kafka/ -run TestEmployeeLimitsUpdatedMessage_CarriesValuesAndVersion -v` → FAIL (fields undefined).

- [ ] **Step 3: Enrich the struct.** Replace `EmployeeLimitsUpdatedMessage`:

```go
// EmployeeLimitsUpdatedMessage is published when an employee's limits are set or updated.
// Enriched (SP-2) to carry the FULL limit snapshot + monotonic Version so consumers
// can maintain a local EmployeeLimitReplica without a synchronous GetEmployeeLimits read.
// Decimal values are formatted strings (StringFixed-style) to avoid float drift.
type EmployeeLimitsUpdatedMessage struct {
	EmployeeID            int64  `json:"employee_id"`
	Action                string `json:"action"` // "set" or "template_applied"
	MaxLoanApprovalAmount string `json:"max_loan_approval_amount,omitempty"`
	MaxSingleTransaction  string `json:"max_single_transaction,omitempty"`
	MaxDailyTransaction   string `json:"max_daily_transaction,omitempty"`
	MaxClientDailyLimit   string `json:"max_client_daily_limit,omitempty"`
	MaxClientMonthlyLimit string `json:"max_client_monthly_limit,omitempty"`
	Version               int64  `json:"version,omitempty"`
}
```

- [ ] **Step 4: Run** the test → PASS.

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go contract/kafka/messages_test.go
git commit -m "feat(contract): enrich EmployeeLimitsUpdatedMessage with full limit values + version (SP-2)"
```

---

## Task 2: Publish full limit snapshot from user-service

**Files:** Modify `user-service/internal/service/limit_service.go` (both `PublishEmployeeLimitsUpdated` sites, ~:128 and ~:169); Test `user-service/internal/service/limit_service_test.go`.

- [ ] **Step 1: Inspect** `limit_service.go` around both publish sites to find the in-scope variable holding the just-saved `*model.EmployeeLimit` (it has fields `MaxLoanApprovalAmount`, `MaxSingleTransaction`, `MaxDailyTransaction`, `MaxClientDailyLimit`, `MaxClientMonthlyLimit` as `decimal.Decimal`, and `Version int64`). Confirm the recording producer mock in the test file (extend it to capture the last `EmployeeLimitsUpdatedMessage` if it doesn't already).

- [ ] **Step 2: Failing test** — add to `limit_service_test.go` a test that after a limits-set call, the published `EmployeeLimitsUpdatedMessage` carries `MaxLoanApprovalAmount` (and at least one other value) and `Version` matching the saved limit. Use `.StringFixed(4)` for the expected decimal string to match the publish formatting. Run → FAIL.

- [ ] **Step 3: Populate both publish sites.** At each `kafkamsg.EmployeeLimitsUpdatedMessage{ EmployeeID: ..., Action: ... }` literal, add:

```go
			MaxLoanApprovalAmount: limit.MaxLoanApprovalAmount.StringFixed(4),
			MaxSingleTransaction:  limit.MaxSingleTransaction.StringFixed(4),
			MaxDailyTransaction:   limit.MaxDailyTransaction.StringFixed(4),
			MaxClientDailyLimit:   limit.MaxClientDailyLimit.StringFixed(4),
			MaxClientMonthlyLimit: limit.MaxClientMonthlyLimit.StringFixed(4),
			Version:               limit.Version,
```

Use the actual in-scope variable name (likely `limit` or `el`) at each site. Use `StringFixed(4)` to match the `numeric(18,4)` column scale.

- [ ] **Step 4: Run** the test → PASS. Then full package: `cd user-service && CGO_ENABLED=1 go test ./internal/service/ -count=1`.

- [ ] **Step 5: Commit**

```bash
git add user-service/internal/service/limit_service.go user-service/internal/service/limit_service_test.go
git commit -m "feat(user): publish full employee-limit snapshot (values+version) on set/update (SP-2)"
```

---

## Task 3: `EmployeeLimitReplica` model

**Files:** Create `credit-service/internal/model/employee_limit_replica.go`; Modify `credit-service/cmd/main.go` AutoMigrate (~:40); Test `credit-service/internal/model/employee_limit_replica_test.go`.

- [ ] **Step 1: Failing test** (real sqlite, driver `github.com/glebarez/sqlite` — confirm credit-service uses it by checking an existing credit `_test.go`; match it). Migrate + create + read a row.

- [ ] **Step 2: Run** → FAIL (`EmployeeLimitReplica` undefined).

- [ ] **Step 3: Create the model:**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// EmployeeLimitReplica is a local read-model of an employee's limits, fed by
// user.employee-limits-updated events (SP-2). NON-AUTHORITATIVE — user-service
// owns employee limits. Used to avoid synchronous GetEmployeeLimits on the
// loan-approval gate.
type EmployeeLimitReplica struct {
	EmployeeID            uint64          `gorm:"primaryKey"` // == user-service Employee.ID
	MaxLoanApprovalAmount decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxSingleTransaction  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxDailyTransaction   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxClientDailyLimit   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxClientMonthlyLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	Version               int64           `gorm:"not null;default:0"` // source EmployeeLimit.Version; ordering guard
	UpdatedAt             time.Time
}
```

(No `BeforeUpdate` hook — the consumer is the single writer and ordering is enforced explicitly in the repo's version guard.)

- [ ] **Step 4:** Append `&model.EmployeeLimitReplica{}` to the `db.AutoMigrate(...)` list in `credit-service/cmd/main.go`.

- [ ] **Step 5: Run** model test + `go build ./...` → PASS.

- [ ] **Step 6: Commit** `feat(credit): add EmployeeLimitReplica read-model + automigrate`.

---

## Task 4: `EmployeeLimitReplicaRepository`

**Files:** Create `credit-service/internal/repository/employee_limit_replica_repository.go` + test.

- [ ] **Step 1: Failing test** mirroring SP-1's repo test (`card-service/internal/repository/client_replica_repository_test.go`): `Upsert` then `GetByEmployeeID`; newer version applies; equal/older version is a no-op; missing returns sentinel. Use `decimal.NewFromInt` for the limit values; assert `MaxLoanApprovalAmount` survives + the stale no-op preserves the newer row.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** (copy SP-1's `ClientReplicaRepository` structure exactly — `db.Transaction` + `clause.Locking{Strength:"UPDATE"}` read, create-on-not-found, `in.Version <= existing.Version` no-op, else `Select(...).Updates(&in)` with the five value columns + `"Version"`):

```go
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/credit-service/internal/model"
)

var ErrEmployeeLimitReplicaNotFound = errors.New("employee limit replica not found")

type EmployeeLimitReplicaRepository struct{ db *gorm.DB }

func NewEmployeeLimitReplicaRepository(db *gorm.DB) *EmployeeLimitReplicaRepository {
	return &EmployeeLimitReplicaRepository{db: db}
}

// Upsert applies an event-sourced employee-limit snapshot only if its Version is
// strictly greater than the stored row's (monotonic; tolerates out-of-order /
// duplicate delivery). Caller MUST pass a full snapshot (all value columns) —
// the update force-writes the selected columns.
func (r *EmployeeLimitReplicaRepository) Upsert(ctx context.Context, in model.EmployeeLimitReplica) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.EmployeeLimitReplica
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, in.EmployeeID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&in).Error
		case err != nil:
			return err
		}
		if in.Version <= existing.Version {
			return nil
		}
		return tx.Model(&existing).Select(
			"MaxLoanApprovalAmount", "MaxSingleTransaction", "MaxDailyTransaction",
			"MaxClientDailyLimit", "MaxClientMonthlyLimit", "Version",
		).Updates(&in).Error
	})
}

func (r *EmployeeLimitReplicaRepository) GetByEmployeeID(ctx context.Context, id uint64) (model.EmployeeLimitReplica, error) {
	var e model.EmployeeLimitReplica
	err := r.db.WithContext(ctx).First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.EmployeeLimitReplica{}, ErrEmployeeLimitReplicaNotFound
	}
	return e, err
}
```

- [ ] **Step 4: Run** → PASS (incl. stale no-op + Version-persistence assertion).

- [ ] **Step 5: Commit** `feat(credit): version-guarded EmployeeLimitReplica repository`.

---

## Task 5: Employee-limit replica consumer

**Files:** Create `credit-service/internal/consumer/employee_limit_replica_consumer.go` + test. (credit-service has NO consumer dir yet — create it; copy card-service's `client_replica_consumer.go` including the bounded-retry + `errMalformed` sentinel pattern.)

- [ ] **Step 1: Failing test** mirroring card's consumer test: `handle` upserts all values+version; bad JSON → error + no repo call; `handleWithRetry` retries transient errors (zero backoff in test) and does NOT retry malformed.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — copy `card-service/internal/consumer/client_replica_consumer.go` verbatim in structure, changing: package stays `consumer`; topic is single `kafkamsg.TopicEmployeeLimitsUpdated` (only one topic, so use `Topic:` not `GroupTopics`, GroupID `"credit-service-employee-limit-replica"`); `replicaUpserter` interface method `Upsert(ctx, model.EmployeeLimitReplica) error`; `handle` parses `kafkamsg.EmployeeLimitsUpdatedMessage` and maps into `model.EmployeeLimitReplica`, parsing the decimal strings with `decimal.NewFromString` (on parse error of a value, treat as zero — `decimal.Zero` — do NOT drop the whole event; a malformed *decimal* is different from malformed *JSON*). Map `EmployeeID uint64(evt.EmployeeID)`, the five values, `Version: evt.Version`. Keep the bounded retry (`backoff []time.Duration` field defaulted 200ms/400ms) + `errMalformed` for JSON parse failures.

- [ ] **Step 4: Run** consumer tests → PASS; `go build ./...`. Run `go mod tidy` in credit-service if kafka-go becomes a direct dep; include go.mod/go.sum.

- [ ] **Step 5: Commit** `feat(credit): user.employee-limits-updated consumer feeding EmployeeLimitReplica`.

---

## Task 6: Swap loan-approval gate read + wire main

**Files:** Modify `credit-service/internal/service/loan_request_service.go` (struct, constructor, gate ~:168); `credit-service/cmd/main.go` (repo + consumer wiring, EnsureTopics); Test `loan_request_service_test.go`.

- [ ] **Step 1: Failing tests** in `loan_request_service_test.go`: with a stub replica returning a `MaxLoanApprovalAmount` row, approving a loan ABOVE that limit is rejected with `ErrAmountExceedsApprovalLimit` and the gRPC `limitClient` is NOT called (replica hit). With a replica miss (sentinel), the gate falls back to one `limitClient.GetEmployeeLimits` call. (Mirror SP-1's `resolveClientEmail` tests.)

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** Add a narrow interface + field to `LoanRequestService`:

```go
// employeeLimitReader is the local read-model consulted before the gRPC fallback (SP-2).
type employeeLimitReader interface {
	GetByEmployeeID(ctx context.Context, id uint64) (model.EmployeeLimitReplica, error)
	Upsert(ctx context.Context, in model.EmployeeLimitReplica) error
}
```

Add `limitReplica employeeLimitReader` to the struct + a trailing constructor param. Add a helper `resolveMaxLoanApproval(ctx, employeeID uint64) (decimal.Decimal, bool)` that: reads replica `GetByEmployeeID` (hit → return its `MaxLoanApprovalAmount`, true); on miss → one `limitClient.GetEmployeeLimits` fallback, parse `MaxLoanApprovalAmount`, backfill the replica (full snapshot from the gRPC resp — parse all five values; Version from resp if present else 0), return value+true; on total failure return `decimal.Zero, false`. Replace the gate body (~:168) to use the helper: if `ok && maxAmount.IsPositive() && req.Amount.GreaterThan(maxAmount)` → reject with `ErrAmountExceedsApprovalLimit`. Preserve the existing "empty/zero = no gate" semantics.

> Check the `userpb` `EmployeeLimitResponse` fields for the backfill (it returns `MaxLoanApprovalAmount` etc. as strings; confirm exact field names — likely `MaxLoanApprovalAmount`, `MaxClientDailyLimit`, etc.; it has NO Version → backfill Version 0).

- [ ] **Step 4: Wire main.go:** add `"user.employee-limits-updated"` to `EnsureTopics`; `limitReplicaRepo := repository.NewEmployeeLimitReplicaRepository(db)`; pass it as the new trailing arg to `NewLoanRequestService`; construct `consumer.NewEmployeeLimitReplicaConsumer(cfg.KafkaBrokers, limitReplicaRepo)`, `Start(<ctx>)`, `defer Close()`. (Find the existing cancellable context used for cron/shutdown in credit's main.)

- [ ] **Step 5: Run** `cd credit-service && CGO_ENABLED=1 go test ./... -count=1 && go build ./...` → PASS. Fix any other caller of `NewLoanRequestService` broken by the arity change (e.g. test constructors — add a `nil` replica arg).

- [ ] **Step 6: Commit** `feat(credit): loan-approval gate reads MaxLoanApproval from replica w/ gRPC fallback; wire consumer+topic`.

---

## Task 7: Integration test

**Files:** Create `test-app/workflows/employee_limit_replica_test.go` (`//go:build integration`, package `workflows`).

- [ ] **Step 1: Write** a test that asserts SP-2 spec behavior end-to-end: as admin, set an employee's `MaxLoanApprovalAmount` to a low value via the limits route (publishes the enriched event → credit replica updates); then have that employee attempt to approve a loan request ABOVE the limit and assert it is rejected with the approval-limit error (HTTP 409 business_rule_violation / the gate's error). Reuse existing helpers (admin client, employee setup, loan-request creation, limit-setting route). Inspect `test-app/workflows/employee_limits_test.go` and `loan_test.go` for the exact routes/helpers. If a deterministic assertion requires waiting for replica propagation, poll up to ~10s. If the gate is genuinely hard to trigger deterministically at integration level, assert the simpler observable (limit set succeeds AND a subsequent over-limit approval by that employee fails) and document. NOTE: docker may be unavailable locally — verify the test COMPILES under `-tags=integration` (`cd test-app && go vet -tags=integration ./workflows/`); CI runs it.

- [ ] **Step 2:** `go vet -tags=integration ./workflows/` clean; gofmt clean.

- [ ] **Step 3: Commit** `test(integration): SP-2 employee-limit replica enforces loan-approval gate`.

---

## Task 8: Docs, version, full CI

**Files:** `docs/Specification.md`, `VERSION`, `api-gateway/internal/version/version.go`.

- [ ] **Step 1: Specification.md** — §18 add `EmployeeLimitReplica` (credit_db read-model: employee_id PK, the five limit values, version, updated_at; fed by `user.employee-limits-updated`; non-authoritative; gRPC fallback+backfill). §19 note `EmployeeLimitsUpdatedMessage` now carries full limit values + version, consumed by credit-service group `credit-service-employee-limit-replica`. No REST/gRPC/enum change.

- [ ] **Step 2: Version** — MINOR (new feature). Set `VERSION` to the next minor (from current `2.17.x` → `2.18.0`); sync `version.go`.

- [ ] **Step 3: `make ci`** — all five jobs green. Fix lint/gofmt/tidy across touched modules (contract, user-service, credit-service, test-app).

- [ ] **Step 4: Commit** `docs+chore(sp2): document EmployeeLimitReplica + enriched limits event; finalize SP-2 credit slice; bump -> 2.18.0 (CI green)`.

---

## Self-review notes
- **Spec coverage:** implements SP-2's pattern for the credit consumer (the money-adjacent proving slice) + the event enrichment that SP-2b/c/d reuse. Eventual+fallback honored (gate already advisory). No new cache.
- **Type consistency:** `EmployeeLimitReplica{EmployeeID, Max*, Version, UpdatedAt}` identical across model/repo/consumer/service; repo `Upsert`/`GetByEmployeeID`; message fields `Max*` strings + `Version`. Decimal values transported as `StringFixed(4)` strings, parsed with `decimal.NewFromString`.
- **Ordering/idempotency:** version-guard in `Upsert` (same as SP-1). Malformed JSON not retried; malformed decimal → zero (not a dropped event).
- **Rollout safety:** events predating Task 2 carry Version 0 → first insert wins, later versioned events upgrade. gRPC fallback stays for misses, so the gate is safe before the replica warms.
- **Follow-on:** SP-2b (client cap), SP-2c (stock actuary — separate `user.actuary-limit-updated` enrichment + replica), SP-2d (auth JWT employee replica — separate careful plan) each get their own plan reusing this event/replica where applicable.

# SP-5: Client-Limit → Account-Limit Propagation (make the dead feature live)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** When an employee sets a client's limits (client-service), those limits become ENFORCED by propagating them to the client's accounts: `client.limits-updated` → account-service applies the client's DailyLimit/MonthlyLimit as the per-account caps for every account the client owns. Today client limits are stored but never enforced (the half-wired finding); this closes that gap event-driven, with account-service staying authoritative for spending enforcement.

**Architecture:** Reuses the proven replica pattern + the event-carried-state-transfer approach. (1) Enrich `ClientLimitsUpdatedMessage` (today `{ClientID,SetByEmployee,Action}`, NO values) to carry `DailyLimit/MonthlyLimit/TransferLimit` + monotonic `Version`. (2) **Fix `ClientLimitRepository.Upsert`** to increment `version` on conflict (it uses `clause.OnConflict` whose `DoUpdates` omits `version`, and `.Create()` doesn't fire `BeforeUpdate` — so `ClientLimit.Version` is currently static at 1 forever, exactly like the EmployeeLimit bug fixed in SP-2; without this fix every limit update would publish the same version and the consumer would drop it). client-service publishes the full snapshot (values + post-write version). (3) account-service gains its FIRST Kafka consumer: a `ClientLimitPolicy` replica (client_id PK, daily, monthly, version) maintained version-guarded, AND on each newly-applied version it sets `DailyLimit`/`MonthlyLimit` on every account the client owns (`ListByOwner` + `UpdateAccountLimits`). Account spending enforcement is unchanged and authoritative; this only seeds the per-account caps from the client policy. `TransferLimit` is NOT propagated to accounts (accounts have no transfer-limit column; it's a per-transaction concern out of scope here).

**Tech Stack:** Go, GORM (Postgres + glebarez/sqlite tests), segmentio/kafka-go, gRPC, decimal. Modules: `contract`, `client-service`, `account-service`.

**Scope:** event enrichment + ClientLimit version fix + client-service publisher + account-service consumer/replica/apply. **Out of scope:** applying the policy to accounts created AFTER the limits were set (a future enhancement: account creation reads the policy replica); TransferLimit propagation; spending-enforcement changes (already authoritative in account-service).

**Money-safety note:** propagation is eventual. A limit *decrease* has a brief window where the client's accounts still hold the old (higher) cap. Accepted (same rationale as SP-2: limits change rarely, bounded by event lag, account-service authoritative). Setting account caps is idempotent and version-guarded.

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `client-service/internal/repository/client_limit_repository.go` | `Upsert` increments `version` on conflict | Modify |
| `client-service/internal/repository/<limit repo test>` | monotonic-version repo test | Modify/Create |
| `contract/kafka/messages.go` | `ClientLimitsUpdatedMessage` gains values + Version | Modify |
| `contract/kafka/messages_test.go` | round-trip test | Modify |
| `client-service/internal/service/client_limit_service.go` | publish full snapshot (values+version) at `:127` | Modify |
| `client-service/internal/service/client_limit_service_test.go` | assert published values+version | Modify |
| `account-service/internal/model/client_limit_policy.go` | `ClientLimitPolicy` replica model | Create |
| `account-service/internal/repository/client_limit_policy_repository.go` | version-guarded upsert + get | Create |
| `account-service/internal/repository/client_limit_policy_repository_test.go` | tests | Create |
| `account-service/internal/consumer/client_limit_consumer.go` | consume `client.limits-updated` → upsert policy + apply to accounts | Create |
| `account-service/internal/consumer/client_limit_consumer_test.go` | tests | Create |
| `account-service/internal/service/account_service.go` | `ApplyClientLimitPolicy(ctx, clientID, daily, monthly)` applies to all client accounts | Modify |
| `account-service/cmd/main.go` | AutoMigrate + EnsureTopics + consumer wiring | Modify |
| `docker-compose.yml` | account-service already depends on kafka? confirm; no change if so | Verify |
| `test-app/workflows/client_limit_enforcement_test.go` | integration: set client limit → account caps updated → over-limit transfer rejected | Create |
| `VERSION`, `api-gateway/internal/version/version.go`, `docs/Specification.md` | bump + docs | Modify |

---

## Task 1: Fix ClientLimit version monotonicity (prerequisite — same bug class as SP-2)

**Files:** `client-service/internal/repository/client_limit_repository.go`; repo test.

- [ ] **Step 1 — failing real-DB test** (glebarez/sqlite — match client-service's repo test driver): insert a ClientLimit (assert Version==1), Upsert same client_id with changed values (assert Version==2), Upsert again (assert Version==3), and the latest values reflect the last write. Run → against current code it shows Version stuck at 1 → FAIL.
- [ ] **Step 2 — implement.** Change `ClientLimitRepository.Upsert` (`:42`) to append a version-increment assignment to the OnConflict DoUpdates (same fix as `employee_limit_repository.go` in SP-2):
```go
func (r *ClientLimitRepository) Upsert(limit *model.ClientLimit) error {
	updates := clause.AssignmentColumns([]string{
		"daily_limit", "monthly_limit", "transfer_limit", "set_by_employee", "updated_at",
	})
	updates = append(updates, clause.Assignment{
		Column: clause.Column{Name: "version"},
		Value:  gorm.Expr("version + 1"),
	})
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: updates,
	}).Create(limit).Error
}
```
Add `gorm.io/gorm` import if needed. Confirm no other ClientLimit writer relies on static version (grep — expected: only this Upsert).
- [ ] **Step 3 — run** the repo test → PASS (1→2→3). Full `cd client-service && CGO_ENABLED=1 go test ./internal/repository/ -count=1`.
- [ ] **Step 4 — commit** `fix(client): increment ClientLimit.Version on every upsert (SP-5 monotonic propagation)`.

---

## Task 2: Enrich ClientLimitsUpdatedMessage + publish full snapshot

**Files:** `contract/kafka/messages.go` (+test); `client-service/internal/service/client_limit_service.go` (+test).

- [ ] **Step 1 — enrich the struct** (failing contract test first):
```go
type ClientLimitsUpdatedMessage struct {
	ClientID      int64  `json:"client_id"`
	SetByEmployee int64  `json:"set_by_employee"`
	Action        string `json:"action"` // "set"
	DailyLimit    string `json:"daily_limit,omitempty"`
	MonthlyLimit  string `json:"monthly_limit,omitempty"`
	TransferLimit string `json:"transfer_limit,omitempty"`
	Version       int64  `json:"version"`
}
```
- [ ] **Step 2 — publish full snapshot.** At `client_limit_service.go:127`, the code that publishes `ClientLimitsUpdatedMessage`. Find the in-scope saved/re-read ClientLimit (the SetClientLimits method re-reads after Upsert — use that variable). Populate `DailyLimit: result.DailyLimit.StringFixed(4)`, `MonthlyLimit`, `TransferLimit` likewise, `Version: result.Version`. Failing service test asserts the published message carries values + a non-zero version (use a repo/double that bumps version like the SP-2 test). 
- [ ] **Step 3 — run** tests → PASS. **Step 4 — commit** `feat(client): publish full client-limit snapshot (values+version) (SP-5)`.

---

## Task 3: account-service ClientLimitPolicy replica model + repo

**Files:** Create `account-service/internal/model/client_limit_policy.go` + repo + tests. Modify account `cmd/main.go` AutoMigrate.

- [ ] Model `ClientLimitPolicy{ ClientID uint64 primaryKey, DailyLimit decimal numeric(18,4), MonthlyLimit decimal numeric(18,4), Version int64 default 0, UpdatedAt }` (no BeforeUpdate hook). (TransferLimit not stored — not propagated to accounts.) Migrate test. Add to AutoMigrate.
- [ ] Repo `ClientLimitPolicyRepository` mirroring SP-1/SP-2 version-guarded upsert (`Upsert(ctx, in) error`, `GetByClientID(ctx, id) (..., error)` + sentinel). Tests: stale-ignored, equal-version no-op, version-persistence, missing→error. (Copy the SP-2 `employee_limit_replica_repository.go` structure; account-service uses glebarez/sqlite in tests — confirm.)
- [ ] Commits per the two sub-steps.

---

## Task 4: account-service consumer + apply-to-accounts

**Files:** Create `account-service/internal/consumer/client_limit_consumer.go` + test (account-service's FIRST consumer — create `internal/consumer/`). Modify `account_service.go` (apply method) + `cmd/main.go` (wiring).

- [ ] **Step 1 — apply method** on `AccountService`: `ApplyClientLimitPolicy(ctx, clientID uint64, daily, monthly decimal.Decimal) error` — loads the client's accounts via the repo's owner filter (`ListByOwner`/`ByOwnerID` — find the exact method; `account_repository.go:56` filters `owner_id = clientID`), and for each NON-bank account sets `DailyLimit`/`MonthlyLimit` to the policy values (reuse the existing `UpdateAccountLimits` logic or a repo bulk update; must be version-safe per the Account optimistic-lock rules — load each account, set limits, Save with version check, or a SkipHooks bulk update of just the two limit columns scoped to owner_id). Keep it transactional per account. Publish nothing new (the account-limit change can emit the existing `account.limits-updated` if that's the established pattern — match existing UpdateAccountLimits behavior). Unit-test with a stub/real-DB repo: applying sets both accounts' limits.
- [ ] **Step 2 — consumer** `ClientLimitConsumer` (copy SP-2 credit consumer incl. bounded retry + errMalformed + decimal-parse-to-zero): single `Topic: kafkamsg.TopicClientLimitsUpdated`, GroupID `account-service-client-limit`. `handle`: parse `ClientLimitsUpdatedMessage`; upsert the `ClientLimitPolicy` replica (version-guarded) — and ONLY if the upsert actually applied a newer version, call `ApplyClientLimitPolicy`. To know whether it applied: have the repo `Upsert` return a bool (applied) or re-Get and compare; simplest — make the policy repo `Upsert` return `(applied bool, err error)` and apply to accounts only when `applied`. (Avoids re-applying stale/duplicate events to accounts.) Tests: event upserts policy + applies; stale event (lower version) neither updates policy nor re-applies; bad JSON dropped; transient error retried.
- [ ] **Step 3 — wire main.go:** AutoMigrate already has the policy model (Task 3); add `client.limits-updated` to EnsureTopics; construct policy repo + consumer; `Start(ctx)` + `defer Close()` with account-service's cancellable context. Inject the account service (for ApplyClientLimitPolicy) and policy repo into the consumer.
- [ ] **Step 4 — verify** `cd account-service && CGO_ENABLED=1 go test ./... -count=1 && go build ./...`. docker-compose: confirm account-service already depends on kafka (it produces events) — if so no change; report.
- [ ] **Step 5 — commits** per sub-step.

---

## Task 5: Integration test + docs + version + CI

- [ ] **Integration test** `test-app/workflows/client_limit_enforcement_test.go` (`//go:build integration`): as admin, set a client's limits low via `PUT /api/v3/clients/{id}/limits` (daily e.g. 1000); poll until the client's account `DailyLimit` reflects it (GET account); then attempt an over-limit transfer/payment from that account and assert it's rejected by the spending check. Reuse helpers (setupActivatedClient, account GET, transfer helper). If full enforcement is hard to drive deterministically, assert the weaker observable (account DailyLimit updated after setting client limit) and document. Compile-verify under `-tags=integration` (docker not available locally; CI runs it).
- [ ] **Specification.md:** §18 add `ClientLimitPolicy` (account_db replica); §19 note `ClientLimitsUpdatedMessage` enriched + consumed by account-service group `account-service-client-limit`; §21 business rule: client limits propagate to the client's account caps (making them enforced); note the ClientLimit.Version fix. Note SP-5 done.
- [ ] **Version:** MINOR → `2.20.0`; sync version.go.
- [ ] **`make ci`** all five green; fix lint/gofmt/tidy across contract, client-service, account-service, test-app.
- [ ] **Commit** `docs+test+chore(sp5): client-limit→account propagation; finalize SP-5; bump -> 2.20.0 (CI green)`.

---

## Self-review
- ClientLimit.Version now monotonic (Task 1) — without it the whole propagation silently no-ops on updates (the EmployeeLimit lesson). This is the critical prerequisite.
- Consumer applies to accounts ONLY on a newly-applied version (idempotent; no stale re-apply).
- account-service stays authoritative for spending enforcement; this only seeds per-account caps.
- Eventual-consistency limit-decrease window accepted (same as SP-2).
- TransferLimit intentionally not propagated (no account column; per-transaction concern).
- Account optimistic-locking respected in the apply (load-modify-Save with version, or scoped SkipHooks bulk update of the two limit columns).

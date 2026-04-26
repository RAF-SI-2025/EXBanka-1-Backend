# 2026-04-26 — Shared Scaffolding Adoption + Granular Permissions

## Goal

Two large refactors, executed in sequence so the second builds on the cleaner foundation laid by the first:

1. **Adopt the new `contract/shared/` scaffolding** in every service. The seven new files (`saga.go`, `saga_recovery.go`, `scheduled.go`, `kafka_topics.go`, `kafka_producer.go`, `grpc_server.go`, `ledger.go`) are already written and compile, but every service still has its own hand-written copies of the same patterns. Replace the duplicates with thin adapters that delegate to the shared code.

2. **Replace the coarse 33-permission model** with a granular, scope-aware permission system. Today an employee with `accounts.update` can rename an account, change its status, change its limits, and deactivate it — all the same gate. Today an `EmployeeBasic` with `credits.approve` can approve a $1000 cash loan or a $500k housing loan with the same permission. Granularity per action and per resource attribute (loan type, transaction scope, account ownership) is the goal.

## Success Criteria

- Every service uses `shared.RunScheduled`, `shared.EnsureTopics`, `shared.RunGRPCServer`, and `shared.Producer` for these concerns. The per-service `internal/kafka/topics.go` files are deleted; per-service `producer.go` files shrink to typed wrapper methods over `shared.Producer`. Every ad-hoc ticker loop is gone.
- Two services (transaction-service first, then stock-service) have migrated their saga code to `shared.Saga` + a per-service `Recorder` adapter. Recovery loops use `shared.RecoveryRunner` + `Classifier`.
- Permissions are split into action-level (and where appropriate, scope-level) codes. The current 33 permissions become roughly 80–100 codes covering each meaningful action distinctly. The four seed roles (`EmployeeBasic` / `Agent` / `Supervisor` / `Admin`) keep the same observable capability — no role gains or loses anything as a side effect of the split.
- Every existing API route still works for every existing role. No API behavior change. No client-visible change. This refactor is invisible to users; it shows up in the admin UI as more checkboxes per role.
- All tests pass. New unit tests cover: each granular permission gates exactly the routes it should; legacy seed roles still admit the same set of requests they admitted before.
- `Specification.md` Section 6 (permissions) updated with the new permission catalog.
- `docs/api/REST_API_v1.md` updated wherever a route's required permission name changed.

## Non-Goals (this plan)

- No changes to the actual route bodies, gRPC handlers, or service business logic.
- No new RBAC features (groups, conditions, time-windowed permissions, ABAC predicates beyond the existing scope/own/all distinction).
- No SSO / external IdP integration.
- No frontend permission-management UI redesign — the existing admin screens keep working with the longer permission list.
- No retroactive renaming of saga step names in audit logs (the new vocabulary applies going forward).

---

## Phase 1 — Adopt `contract/shared/`

### Phase 1.1 — Replace the 11 `internal/kafka/topics.go` files

**Smallest, lowest risk, highest copy-paste win.** Every service has the same 60-line file.

For each of {auth, user, account, client, card, credit, transaction, stock, exchange, verification, notification}-service:

1. In `cmd/main.go`, replace `kafkaprod.EnsureTopics(...)` with `shared.EnsureTopics(...)`.
2. Delete `internal/kafka/topics.go`.
3. Run `go build ./...` for the service; fix any import cleanup.
4. Run the service's tests; nothing should break.

**Verification:** `make build` from repo root succeeds, `make docker-up` brings the stack up, all topics show on the broker.

### Phase 1.2 — Migrate the 11 `internal/kafka/producer.go` files

The typed `PublishX(msg)` helpers are domain-specific sugar — keep them per-service, just change their bodies to delegate to `shared.Producer`.

For each service:

1. Replace the per-service `Producer` struct with one that wraps `*shared.Producer`:
   ```go
   type Producer struct{ inner *shared.Producer }
   func NewProducer(brokers string) *Producer {
       return &Producer{inner: shared.NewProducer(brokers)}
   }
   func (p *Producer) Close() error { return p.inner.Close() }
   ```
2. Rewrite each `PublishX(ctx, msg)` to call `p.inner.Publish(ctx, kafkamsg.TopicX, msg)`.
3. If a producer used a non-default `RequiredAcks` (audit before changing), pass `ProducerConfig` accordingly. Banking-system default is `RequireAll` — confirm this matches existing behavior for each service.
4. Delete the local `publish` helper.
5. Run service tests.

**Verification:** A representative round-trip test (e.g., `notification-service` consumes a `TopicSendEmail` published from `user-service`) confirms wire-format compatibility.

### Phase 1.3 — Replace ad-hoc ticker loops with `shared.RunScheduled`

13 known sites. For each:

| Service | File | Job |
|---|---|---|
| credit-service | `cron_service.go` | mark overdue installments (24h) |
| notification-service | `inbox_cleanup.go` | purge old inbox rows (1m) |
| card-service | `card_cron.go` | release expired card blocks (1m) |
| exchange-service | `cmd/main.go` | sync rates from external API (configurable hours) |
| stock-service | `security_sync.go` (×2), `saga_recovery.go` | security catalog sync, saga reconcile |
| transaction-service | `transfer_service.go` | compensation recovery (5m) |
| verification-service | `cmd/main.go` | challenge expiry (1m) |
| api-gateway | `websocket_handler.go` | heartbeat (30s) |

For each:

1. Replace the goroutine + ticker + select with:
   ```go
   shared.RunScheduled(ctx, shared.ScheduledJob{
       Name:       "card-block-expiry",
       Interval:   1 * time.Minute,
       RunOnStart: true,
       OnTick:     func(ctx context.Context) error { return cron.ReleaseExpiredBlocks(ctx) },
   })
   ```
2. Adjust the work function to take ctx and return error (most already do; some need a thin shim).
3. Remove the now-unused `time.NewTicker` + `defer ticker.Stop()` lines.

**Verification:** services start under `docker-compose up`, scheduled actions still happen on time (verified via existing integration tests where applicable).

### Phase 1.4 — Adopt `shared.RunGRPCServer` in `cmd/main.go`

11 services have near-identical bootstrap dances. For each:

1. Replace the manual `net.Listen` + `grpc.NewServer` + register + `go server.Serve` + signal handling + `GracefulStop` with one `shared.RunGRPCServer(ctx, shared.GRPCServerConfig{...})` call.
2. Pass any per-service `grpc.ServerOption` slice (interceptors, prometheus middleware) via `Options`.
3. Remove the now-unused `os/signal`, `syscall`, listener cleanup code.

**Verification:** existing `make docker-up` health probes still succeed, integration tests pass, services shut down cleanly on `docker-compose down`.

### Phase 1.5 — Migrate transaction-service to `shared.Saga`

Pilot service for the saga refactor. transaction-service is smaller and more linear than stock-service, so it's a good first port.

1. Write a `txAdapter` in `transaction-service/internal/saga/recorder.go` implementing `shared.Recorder` over `transaction-service/internal/repository/saga_log_repository.go`. The existing `RecordStep`, `CompleteStep`, `FailStep` map directly. Add `IsCompleted(sagaID, stepName)` (one-line query). The adapter pulls `account_number` and `amount` out of `*shared.State` for storage.
2. Write a `txClassifier` implementing `shared.Classifier` for the recovery loop. Existing `transfer_recovery.go` and `compensation_recovery.go` step-name switches collapse into `Classify`/`Retry` methods.
3. Rewrite `transfer_service.go` and `payment_service.go` to build a `shared.Saga` instead of calling `executeWithSaga`. Each step becomes a `shared.Step` whose `Forward` and `Backward` close over the existing `accountClient.UpdateBalance` calls.
4. Replace the manual recovery goroutine with `shared.NewRecoveryRunner(...).Run(ctx)`.
5. Delete `internal/service/saga_helper.go` and the bespoke recovery files. Keep `saga_log_repository.go` (the adapter wraps it).

**Verification:** `make test` from `transaction-service/`, full integration suite (`test-app/workflows/wf_*transfer*`, `wf_*payment*`).

### Phase 1.6 — Migrate stock-service to `shared.Saga`

Bigger surface (8 sagas + cross-bank). Same pattern.

1. Write a `stockAdapter` over `stock-service/internal/repository/saga_log_repository.go`. The `Payload` JSON column already exists — adapter serializes `*shared.State` snapshot into it.
2. The cross-bank executor (`crossbank_saga_executor.go`) uses a different table (`InterBankSagaLog`) and a different access pattern (idempotent BeginPhase/CompletePhase keyed on (TxID, Phase, Role)). **Out of scope for this plan** — it stays as-is. Only the local in-process sagas migrate. Cross-bank can be revisited once the local migration is proven.
3. Rewrite, in order: `fund_invest_saga.go`, `fund_redeem_saga.go`, `otc_accept_saga.go`, `otc_exercise_saga.go`, `forex_fill_service.go`, `portfolio_service.go` (buy/sell), `order_service.go` (placement). Each saga becomes a `shared.Saga` with chained `Add(s.stepName())` calls; each step is a method on the service that returns a `shared.Step` populated with closures.
4. Replace `saga_recovery.go` with a `stockClassifier` implementing `shared.Classifier`, plugged into `shared.RunRecovery`. The existing step-name switch logic moves into `Classify` and `Retry`.
5. Delete `saga_helper.go` and the bespoke recovery file.

**Verification:** unit tests for each rewritten saga (`fund_invest_saga_test.go`, `otc_accept_saga_test.go`, etc.) plus integration suite (`test-app/workflows/wf_stock_*`, `wf_forex_*`).

### Phase 1.7 — Adopt `shared.EntryFields` in account-service

Optional, low-risk audit-log normalization. Not load-bearing for the rest of the plan.

1. Change `account-service/internal/model/ledger_entry.go` to embed `shared.EntryFields`. Strip the duplicated columns; keep `AccountNumber` as the service-specific column.
2. Update `LedgerRepository` to populate `EntryType` as `shared.EntryDebit` / `shared.EntryCredit` (typed) instead of raw strings.
3. Add a thin `accountStore` adapter implementing `shared.Store` so cross-service code can read by `IdempotencyKey` uniformly. Existing `RecordEntry`, `DebitWithLock`, `CreditWithLock` keep working unchanged.

**Verification:** account-service unit tests + ledger-related integration tests pass.

### Phase 1 — Risk and rollback

- Each sub-phase is independently committable and testable. If any one breaks something, revert that single commit.
- Pilot order: 1.1 → 1.2 → 1.3 → 1.4 → 1.5 → 1.6 → 1.7. Each phase gated on `make test` + `make docker-up` smoke pass.
- The rewritten sagas keep the same audit-log row shape (same status strings, same step names) so existing recovery tooling sees no change.

---

## Phase 2 — Granular Permissions

### Design principles (revised)

Three guiding principles for the granularity rewrite:

1. **One permission per functionality.** Where two pieces of work today share a single permission only because they happen to live behind the same coarse verb, split them.
2. **Same endpoint, different visibility = different permissions.** The model is *not* one permission per HTTP route. The model is: a route may accept *any* of several permissions, and the *handler* uses which permission the caller holds to scope the response. Example: `GET /api/orders` accepts both `orders.read.all` and `orders.read.own`. If the caller has `.all`, the handler returns every order; if only `.own`, the handler filters to the caller's own orders. Both reach the same endpoint; the visibility differs by permission.
3. **Permissions never encode amounts or thresholds.** Per-amount safety lives in `EmployeeLimits` (`MaxLoanApprovalAmount`, `MaxSingleTransaction`, `MaxClientDailyLimit`, etc.) — that's already where it belongs. Permissions stay categorical.

There is **no backwards-compatibility alias layer**. The role seeder is the single source of truth: when the granular catalog ships, the seeder writes the new codes into the four seed roles directly. Existing employees inherit through their role assignment; no DB migration of per-employee codes is needed beyond re-seeding.

### 2.1 — Permission catalog (revised)

The new catalog uses three suffix conventions:

- **Action verbs** when the resource has multiple meaningfully distinct mutations: `cards.block`, `cards.unblock`, `cards.set-temporary-block`, `cards.update-limits`, `cards.update-pin`, `cards.deactivate`. No `cards.update` umbrella.
- **`.all` vs `.own` scope** when the same read endpoint must serve admins differently from employees: `orders.read.all` / `orders.read.own`. Handler dispatches.
- **Sub-resource discriminators** when the same verb applies to functionally different things: `credits.approve.cash`, `credits.approve.housing`, `credits.approve.auto`, `credits.approve.refinancing`, `credits.approve.student`. **No** approval-by-amount permissions — `EmployeeLimit.MaxLoanApprovalAmount` already gates that.

Full catalog (target ~110 codes; numbers may shift slightly during the route audit):

| Category | Granular codes |
|---|---|
| **clients** | `clients.create`, `clients.read.all`, `clients.read.assigned`, `clients.update.profile`, `clients.update.contact`, `clients.update.limits`, `clients.set-password`, `clients.deactivate` |
| **accounts** | `accounts.create.current`, `accounts.create.foreign`, `accounts.read.all`, `accounts.read.own`, `accounts.update.name`, `accounts.update.status`, `accounts.update.limits`, `accounts.update.beneficiaries`, `accounts.deactivate` |
| **bank-accounts** | `bank-accounts.create`, `bank-accounts.read`, `bank-accounts.update`, `bank-accounts.deactivate` |
| **cards** | `cards.create.physical`, `cards.create.virtual`, `cards.read.all`, `cards.read.own`, `cards.block`, `cards.unblock`, `cards.set-temporary-block`, `cards.update-limits`, `cards.update-pin`, `cards.deactivate`, `cards.approve.physical`, `cards.approve.virtual`, `cards.reject` |
| **payments** | `payments.read.all`, `payments.read.own` |
| **transfers** | `transfers.read.all`, `transfers.read.own` |
| **credits** | `credits.read.all`, `credits.read.own`, `credits.approve.cash`, `credits.approve.housing`, `credits.approve.auto`, `credits.approve.refinancing`, `credits.approve.student`, `credits.reject`, `credits.disburse` |
| **securities** | `securities.read.catalog`, `securities.read.market-data`, `securities.read.holdings.all`, `securities.read.holdings.own`, `securities.read.holdings.bank`, `securities.manage.catalog`, `securities.manage.market-simulator`, `securities.manage.cross-bank` |
| **orders** | `orders.read.all`, `orders.read.own`, `orders.read.bank`, `orders.place.own`, `orders.place.on-behalf-client`, `orders.place.for-bank`, `orders.cancel.all`, `orders.cancel.own`, `orders.approve.market`, `orders.approve.limit`, `orders.approve.stop`, `orders.reject` |
| **otc** | `otc.trade.accept`, `otc.trade.exercise`, `otc.contracts.read.all`, `otc.contracts.read.own`, `otc.contracts.cancel`, `otc.market-makers.manage` |
| **funds** | `funds.create`, `funds.update`, `funds.suspend`, `funds.read.all`, `funds.invest`, `funds.redeem` |
| **forex** | `forex.trade.own`, `forex.trade.on-behalf-client`, `forex.trade.for-bank`, `forex.read.all`, `forex.read.own` |
| **employees** | `employees.create`, `employees.read.all`, `employees.read.own`, `employees.update.profile`, `employees.update.contact`, `employees.update.password`, `employees.deactivate` |
| **roles & perms** | `employees.roles.assign`, `employees.permissions.assign`, `roles.create`, `roles.read`, `roles.update`, `roles.delete`, `permissions.read` |
| **limits** | `limits.employee.read`, `limits.employee.update`, `limits.client.read`, `limits.client.update`, `limit-templates.read`, `limit-templates.create`, `limit-templates.update`, `limit-templates.delete` |
| **fees** | `fees.read`, `fees.create`, `fees.update`, `fees.delete` |
| **interest-rates** | `interest-rates.tiers.read`, `interest-rates.tiers.update`, `interest-rates.bank-margins.read`, `interest-rates.bank-margins.update` |
| **agents** | `agents.read`, `agents.assign`, `agents.unassign` |
| **tax** | `tax.read`, `tax.collect`, `tax.adjust` |
| **exchanges** | `exchanges.read`, `exchanges.update`, `exchanges.testing-mode.toggle` |
| **verification** | `verification.skip.transaction`, `verification.skip.password-change`, `verification.skip.profile-update`, `verification.policies.read`, `verification.policies.update`, `verification.methods.update` |
| **changelog** | `changelog.read.accounts`, `changelog.read.employees`, `changelog.read.clients`, `changelog.read.cards`, `changelog.read.loans`, `changelog.read.orders` |

### 2.2 — Catalog stored in code, with metadata

Move `AllPermissions` to `user-service/internal/permission/catalog.go`:

```go
type PermissionDef struct {
    Code        string   // canonical code, used in routes and DB
    Description string   // shown in admin UI
    Category    string   // grouping for UI (clients, accounts, securities, ...)
    Subcategory string   // finer grouping (e.g., "lifecycle" vs "limits")
    Scope       Scope    // ScopeNone / ScopeOwn / ScopeAll — surfaced for read endpoints
    Sensitive   bool     // true for permissions that bypass safety checks (verification.skip.*)
}

type Scope string
const (
    ScopeNone Scope = ""
    ScopeOwn  Scope = "own"
    ScopeAll  Scope = "all"
    ScopeBank Scope = "bank"  // for resources owned by the bank itself
)
```

The catalog is the single source of truth: the seed role definitions reference the codes from the same file, the route-mapping CSV is generated from it, the admin UI groups by `Category` and `Subcategory`.

### 2.3 — Middleware: `RequireAnyPermission` for shared-endpoint visibility

The single-permission `RequirePermission` middleware stays for routes that have one permission. Add a sibling for the visibility-scoped pattern:

```go
func RequireAnyPermission(codes ...string) gin.HandlerFunc
```

It admits the request if the caller holds any of the listed codes. The handler then reads `c.Get("permissions")` and dispatches: if `orders.read.all` is present, no filter; else if `orders.read.own` is present, filter by `placed_by = caller`; else (impossible after the middleware passed) → 403.

A small handler helper `permission.HighestScope(c, "orders.read")` returns `ScopeAll`, `ScopeOwn`, or `ScopeNone` so handlers don't open-code the lookup.

### 2.4 — Route updates

Per `router_v1.go` / `router_v2.go`:

- Each existing `RequirePermission("foo.bar")` call site is reviewed against the catalog. Replace with the granular code.
- Routes with mixed visibility get `RequireAnyPermission("orders.read.all", "orders.read.own")`. The handler then scopes the result.
- Action routes (block/unblock/approve/reject) split per granular code.

The route-mapping audit is captured in `docs/permissions/route-mapping.csv` (one row per `(method, path, required_permissions[])`) and committed alongside the route changes.

### 2.5 — Seed role expansion (the role seeder is the source of truth)

`DefaultRolePermissions` in `user-service/internal/service/role_service.go` is updated to grant the granular codes directly per role. No alias layer; no migration script. On startup the seeder ensures the four seed roles hold exactly these sets:

- **EmployeeBasic** — clients/accounts/cards/payments/transfers/credits at full granularity, scoped `.own` for visibility on resources they don't manage.
- **EmployeeAgent** — Basic + `securities.read.*` + `orders.read.own` + `orders.place.on-behalf-client` + `orders.place.for-bank` + `forex.trade.on-behalf-client` + `forex.trade.for-bank` + `funds.invest` + `funds.redeem` (the agent CAN trade for the bank or on behalf of clients, but not for their personal account in this codebase since employees don't have personal client portfolios).
- **EmployeeSupervisor** — Agent + `agents.*` + `otc.*` + `funds.*` (full management) + `orders.approve.*` + `orders.cancel.all` + `tax.*` + `exchanges.*` + `verification.skip.*` + `verification.manage.*` + `*.read.all` upgrades wherever applicable.
- **EmployeeAdmin** — Supervisor + `employees.*` + `roles.*` + `bank-accounts.*` + `fees.*` + `interest-rates.*` + `securities.manage.*` + `limits.*` + `limit-templates.*` + `changelog.*` (everything).

The seeder uses a deterministic apply: existing roles' permissions are *replaced* (not merged) with the catalog-derived sets so a release upgrade always lands clean. Existing employees are unaffected because they're attached to roles by reference, not by stored code copies.

### 2.6 — Frontend impact

Frontend permission gating is by **role name**, not by individual permission code (per user clarification). Renaming or adding permission codes does not require a frontend release. The admin role-management UI does see the longer list, but the catalog metadata gives it room to render gracefully via category grouping.

### 2.7 — Specification and docs

- `Specification.md` Section 6 (permissions): replace the 33-code table with the ~110-code one. Drop any reference to alias / legacy compatibility.
- `docs/api/REST_API_v1.md`: update each route's "Required permission" line where it changes; document the `RequireAnyPermission` pattern for read-scoped routes.
- `docs/permissions/granular-codes.md` (new): operator reference grouped by category.
- `docs/permissions/route-mapping.csv` (new): generated audit table.

### 2.8 — Tests

- Unit: `user-service/internal/permission/catalog_test.go` — every code has a category; no duplicates; every code listed in `DefaultRolePermissions` exists in the catalog.
- Unit: `user-service/internal/service/role_service_test.go` — the seeder, when run twice, leaves the four seed roles in the same final state (idempotent).
- Unit: `api-gateway/internal/middleware/auth_test.go` — `RequireAnyPermission` accepts any one of N codes; `permission.HighestScope` returns the right scope across multi-scope holders.
- Integration: `test-app/workflows/wf_permissions_granular_test.go` (new) — for each seed role, exercise representative protected endpoints (a read-all path, a read-own path, a write path, a sensitive path) and confirm the expected allow/deny + visibility filtering.

### Phase 2 — Risk and rollout

- Without aliases, the cutover is atomic. To avoid breaking an instance with custom-curated roles in the wild (faculty graders, demo deployments), the seeder runs in *replace* mode only for the four named seed roles; any custom-named role is left untouched. Operators rebuild custom roles once, post-cutover, using the catalog.
- Each granular code is independently revertible: remove it from `DefaultRolePermissions` and run the seeder; remove the route gate; remove the catalog entry.
- Rollback for the whole phase: revert the route + seeder + catalog commits. The DB rows for the old codes are gone but recreating them is one seeder run on a previous-version build.

---

## Phase 3 — Bank Portfolio + Employee-Buys-For-Bank

This is a new feature, not a refactor. It fills the gap noted in memory: the bank cannot currently own securities because stock-service has no `system_type="bank"` portfolio. Without this, agents cannot accumulate inventory the bank can later sell to clients — a gap that blocks the spec's market-making workflow.

**Scope:** stock orders only (buy/sell of regular securities). OTC and funds use the same model but their migration is deferred to the Celina 4–5 branch merge per user direction.

**Cross-bank sagas:** out of scope. They stay as-is.

### 3.1 — Domain model

**Holdings** (stock-service `holdings` table):
- Existing `system_type` column: today supports `client` and `employee`. Add `bank` as a third allowed value.
- Bank-owned holdings use sentinel `user_id = 1_000_000_000` (same convention account-service uses for bank accounts).
- Migration adds no new columns — the `bank` value is a new string accepted by existing validation.

**Orders** (stock-service `orders` table):
- Add column `placed_by_employee_id BIGINT NULL` — null for client-self-placed orders; set for employee-placed orders (regardless of whether the order is on-behalf or for-bank).
- Existing `system_type` already supports `client` / `employee`; add `bank`.
- Existing `account_id` (the cash account to debit/credit on settlement) must be a bank account when `system_type = bank` — enforced in service layer.

**OrderTransactions** and ledger entries: no schema change. The settlement flow already debits/credits an account by `account_id` and updates a holding by `(user_id, system_type, security_id)`. The new `bank` system_type plugs in unchanged.

### 3.2 — New endpoints

- `POST /api/v1/orders/for-bank` — employee places a stock order whose result lands in the bank's portfolio.
  - Body: `{ security_id, side: "buy"|"sell", order_type, quantity, [limit_price], [stop_price], bank_account_id }`
  - Validation: caller must hold `orders.place.for-bank`; `bank_account_id` must point at a row with `is_bank_account = true`; for sells, the bank must hold ≥ `quantity` of `security_id`.
  - Persists `orders` row with `system_type = "bank"`, `user_id = bank_sentinel`, `account_id = bank_account_id`, `placed_by_employee_id = caller`.
  - Settlement reuses the existing buy/sell saga path with `system_type = bank`.

- `POST /api/v1/orders/on-behalf-client` — existing endpoint, kept; renamed in router for symmetry. `placed_by_employee_id = caller` is now also recorded.

- `POST /api/v1/orders/forex/for-bank` — same shape for forex, deferred to Phase 3.5 (after stock works).

### 3.3 — `/me/orders` semantics

`GET /api/me/orders`:
- **Employee caller**: `WHERE placed_by_employee_id = caller_id`. Returns orders the employee placed regardless of position-owner (bank or client). Surfaces the on-behalf and for-bank orders alongside any employee-self orders (rare).
- **Client caller**: `WHERE system_type = 'client' AND user_id = caller_id`. Surfaces both client-self-placed orders and employee-on-behalf orders that name this client (because on-behalf orders carry the client's `user_id`).

`GET /api/orders` (employee, gated by `RequireAnyPermission("orders.read.all", "orders.read.own", "orders.read.bank")`):
- `.read.all` → all orders.
- `.read.bank` → orders with `system_type = bank`.
- `.read.own` → orders with `placed_by_employee_id = caller`.
- Highest scope wins when multiple are held.

### 3.4 — Bank account selection helper

When an employee places a buy-for-bank order in a non-RSD currency, they must choose which bank account in that currency to draw from. New endpoint:

- `GET /api/v1/bank-accounts?currency=EUR&kind=foreign` — list bank accounts in a currency. Gated by `bank-accounts.read`.

The frontend uses this to render a dropdown in the order placement form.

### 3.5 — Permissions wiring

Granular permissions added in Phase 2 are referenced here:

- `orders.place.for-bank` — required for `POST /api/v1/orders/for-bank`. Granted to `EmployeeAgent`+.
- `orders.place.on-behalf-client` — existing route; permission renamed for symmetry. Granted to `EmployeeAgent`+.
- `orders.read.bank` — visibility on bank-owned orders. Granted to `EmployeeSupervisor`+.
- `securities.read.holdings.bank` — visibility on the bank's portfolio. Granted to `EmployeeAgent`+ (agents need to see what the bank owns to know what they can sell).

### 3.6 — Settlement and audit

Bank orders flow through the same fill saga (`portfolio_service.go` buy/sell). The saga needs no change because it parameterizes on `(user_id, system_type, account_id)`. The new system_type just slots in.

Audit: every bank order's `placed_by_employee_id` is the audit anchor. Listing bank orders with this column shows which employee made each call. Captured in saga_logs Payload column too (already serialized via the new `shared.State` snapshot).

### 3.7 — What's deferred to Celina 4–5 merge

- Bank-as-buyer for **OTC contracts** — same model, but cross-bank handling needs the OTC saga to be aware of bank-owned offers.
- Bank-as-buyer for **funds** — bank invests in / redeems from funds.
- Bank-initiated cross-bank trades — out of scope until the cross-bank saga is migrated to the shared abstraction.

User has confirmed these are deferred and will be designed when the Celina 4–5 branch merges.

### 3.8 — Tests

- Unit: stock-service order_service_test.go — placing for-bank with non-bank account → 400; placing for-bank without `orders.place.for-bank` → 403; placed_by_employee_id is recorded.
- Unit: holdings_test.go — bank-system-type holdings round-trip; sentinel user_id is rejected outside bank flows.
- Integration: `test-app/workflows/wf_bank_portfolio_test.go` (new) — full lifecycle: employee buys-for-bank → bank holding increments → another employee sells-from-bank → bank holding decrements → audit shows both employees' placed_by_employee_id values; client sees neither order in `/me/orders`; original employee sees both in `/me/orders`.

### Phase 3 — Risk and rollout

- Schema changes are additive (new column on `orders`; new accepted value for two enum-like string columns). No data migration needed for existing rows.
- The new endpoints are new — no behavior change on existing endpoints.
- Rollback: revert the new endpoints + remove `placed_by_employee_id` column + remove the granular permissions. Existing orders with `placed_by_employee_id = NULL` survive untouched.

---

## Order of Operations Across All Three Phases

```
Phase 1 (commits 1.1 → 1.7, each in sequence, no pause)
  1.1  Topics consolidation (11 services)
  1.2  Producer consolidation (11 services)
  1.3  Scheduled job migration (13 sites)
  1.4  gRPC bootstrap migration (11 services)
  1.5  transaction-service saga migration (pilot)
  1.6  stock-service saga migration (cross-bank deferred)
  1.7  account-service ledger embedding

Phase 2 (commits 2.1 → 2.5, atomic cutover, no aliases)
  2.1  Catalog + metadata struct
  2.2  RequireAnyPermission middleware + scope helper
  2.3  Route audit + updates (per category sub-commits)
  2.4  Seed role rewrite (replace mode)
  2.5  Spec + docs

Phase 3 (commits 3.1 → 3.4, additive)
  3.1  Bank-system-type holdings + orders.placed_by_employee_id column
  3.2  POST /orders/for-bank endpoint + saga wiring
  3.3  /me/orders query split + GET /orders scope dispatch
  3.4  Bank-account selector endpoint + integration tests
```

Each week is a checkpoint where progress is verifiable and revertible. The plan is paused at any phase boundary if regressions surface.

## Files Touched (Inventory)

### Phase 1
- 11 × `internal/kafka/topics.go` (deleted)
- 11 × `internal/kafka/producer.go` (rewritten as thin adapters)
- 13+ × ad-hoc ticker call sites (rewritten)
- 11 × `cmd/main.go` (gRPC bootstrap simplified)
- transaction-service: `internal/service/saga_helper.go` (deleted), 2 saga call sites rewritten, recovery loop rewritten, recorder adapter added
- stock-service: same shape, larger surface
- account-service: `internal/model/ledger_entry.go` (optional embedding)

### Phase 2
- `user-service/internal/permission/catalog.go` (new — ~95 granular codes with metadata)
- `user-service/internal/service/role_service.go` (catalog reference + seed updates)
- `api-gateway/internal/middleware/auth.go` (alias resolution)
- `api-gateway/internal/router/router_v1.go` and `router_v2.go` (route permission updates)
- `Specification.md` (Section 6 rewrite)
- `docs/api/REST_API_v1.md` (per-route permission name updates)
- `docs/permissions/granular-codes.md` (new catalog reference)
- `docs/permissions/route-mapping.csv` (new audit table)
- new tests in `user-service/internal/permission/`, `api-gateway/internal/middleware/`, `test-app/workflows/`

## Resolved Decisions (per user clarification 2026-04-26)

- **No backwards-compatibility aliases.** Atomic cutover via the role seeder.
- **Permissions never encode amounts.** `EmployeeLimit` already gates approval-by-amount.
- **Cross-bank saga stays as-is.** Out of scope for both Phase 1.6 and Phase 3.
- **Frontend gates by role name, not permission code.** No coordinated frontend cutover required for Phase 2.
- **OTC and funds bank-as-buyer deferred** to the Celina 4–5 branch merge.
- **Bank-as-buyer scope (Phase 3.b)**: employee chooses which bank account to debit/credit; the chosen account must be `is_bank_account = true`.
- **Client `/me/orders`** surfaces both client-self-placed and employee-on-behalf orders.

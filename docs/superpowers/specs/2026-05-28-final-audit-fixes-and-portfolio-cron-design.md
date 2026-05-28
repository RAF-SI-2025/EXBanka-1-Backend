# Final Audit Fixes + Unified Portfolio + Admin Cron Viewer — Design

**Date:** 2026-05-28
**Status:** Approved (pending user review of this written spec)
**Scope:** Pre-submission hardening of Celina 4 + Celina 5 (no stuck states), unified portfolio routes across clients/bank/funds, and an admin-only cron job viewer with full control.

---

## Background

A final audit of the Celina 4 (OTC + investment funds) and Celina 5 (cross-bank protocol) implementation surfaced three blocking gaps:

1. **Loan disbursement** in credit-service has no saga log and no compensation-recovery worker. If borrower credit fails after the bank account is debited, the bank loses money permanently with no automatic recovery.
2. **OTC accept/exercise post-saga steps** (buyer-holding upsert, capital gain row, contract status update, notification publish) run AFTER the money-saga completes and only log failures. Buyer can pay without receiving a holding row; contract can stay "active" instead of "exercised".
3. **No `CHECK_STATUS` peer endpoint.** Celina-5 spec (§"Mehanizam za Retry", line 251) defines a bi-directional status-poll mechanism for stuck cross-bank transactions. Only sender-side `OutboundReplayCron` exists today — if Bank A crashes mid-flight, Bank B cannot resolve the transaction.

In parallel, the user wants:

- A consistent route shape for viewing portfolios across the three possible owners (clients, bank, investment funds), with the unified response surfacing fund positions as investments alongside stocks/options/futures.
- An admin-only endpoint that lists every cron job across services with full control (view + trigger + pause + resume).

This spec covers all three concerns as one cohesive change because they share the same submission window and they each touch the route/handler layer.

---

## Part A — Audit Fixes (Critical/High)

### A1. Loan disbursement saga (credit-service)

**Current state:** `credit-service/internal/service/loan_request_service.go::DisburseLoan` is an inline two-step (debit bank account → credit borrower). Compensation failure is only logged as `"COMPENSATION FAILED for loan %d"`. No saga log, no recovery worker.

**Target state:**

- Adopt the same saga abstraction stock-service uses; the package's exact import path is resolved during plan writing by reading `stock-service/internal/service/*_saga.go` (this is a non-blocking detail — same abstraction either way).
- New `saga_log` table in credit-service's DB, schema mirroring transaction-service's `saga_log`.
- Steps:
  | Step | Action | Compensation |
  |---|---|---|
  | `debit_bank` | Debit bank-owned RSD account by loan principal | `credit_bank` (idempotent via key `loan-{loanId}-debit-comp`) |
  | `credit_borrower` | Credit borrower's selected account | `debit_borrower` (idempotent via key `loan-{loanId}-credit-comp`) |
  | `mark_loan_active` | Set `Loan.Status = "active"` | `mark_loan_failed` (sets status back to `"approved"`) |
- Idempotency: each step is keyed on `loan-{loanId}-{step-name}`. Re-running a completed step is a no-op.
- Wire `StartCompensationRecovery(ctx)` in `credit-service/cmd/main.go` with a 5-minute tick, mirroring `transaction-service/internal/service/transfer_service.go::StartCompensationRecovery`.
- Dead-letter pattern: 10 retries, then publish to `saga.dead-letter` Kafka topic. Notification-service alerts admin.

**Files touched:**
- `credit-service/internal/model/saga_log.go` (new)
- `credit-service/internal/repository/saga_log_repository.go` (new)
- `credit-service/internal/service/loan_disbursement_saga.go` (new)
- `credit-service/internal/service/loan_request_service.go` (refactor `DisburseLoan` to use saga)
- `credit-service/internal/service/compensation_recovery.go` (new)
- `credit-service/cmd/main.go` (wire recovery worker)

**Tests:**
- Unit: saga step execution + compensation under each failure mode (mock account-service client returning error mid-flight).
- Integration in `test-app/workflows/`: simulate borrower-credit failure, observe automatic bank-account refund, verify saga_log row reaches `completed_compensated` status.

### A2. OTC post-saga rollback guards (stock-service)

**Current state:** `stock-service/internal/service/otc_accept_saga.go` and `otc_exercise_saga.go` have four post-saga steps that run AFTER money has moved:
- `upsert_holding` for the buyer
- `record_capital_gain` for the seller
- `update_contract_status`
- `publish_notification`

All four are logged-on-failure with comments like `"CRITICAL"` — no rollback, no retry.

**Target state:** these four become committable steps **inside** the saga.

- Saga ordering:
  1. `reserve_buyer_funds`
  2. `verify_and_reserve_seller_holding`
  3. `settle_premium_or_strike` (irrevocable money move — `Pivot: true`)
  4. `upsert_buyer_holding` (idempotent on `(buyer_id, asset_id, contract_id)`)
  5. `record_capital_gain` (idempotent on `(party_id, source_contract_id, event_kind)`)
  6. `update_contract_status` (`accepted` or `exercised`)
  7. `publish_notification` (best-effort — see below)

- Steps 4–6 have explicit compensations that run if a LATER step fails. Step 7 is intentionally not compensated; on failure it re-enqueues via the durable outbox, which the existing `outbox-drainer` cron retries until success. Notifications are idempotent at the consumer side (notification-service dedupes by `idempotency_key`).
- Bump `stock-service/internal/service/saga_recovery.go::maxSagaRecoveryRetries` from 5 to 10 to match transaction-service.
- After 10 retries: publish to `saga.dead-letter` Kafka topic, do not silently stay in `pending`/`compensating`.

**Files touched:**
- `stock-service/internal/service/otc_accept_saga.go`
- `stock-service/internal/service/otc_exercise_saga.go`
- `stock-service/internal/service/saga_recovery.go`
- `stock-service/internal/kafka/topics.go` (add `saga.dead-letter` to EnsureTopics)

**Tests:**
- Unit: per-step failure injection (e.g. `update_contract_status` returns DB error) — verify saga compensates back to `settle_premium_or_strike` (but not past the pivot, since money is gone) and leaves a consistent state where buyer has holding+CG+notification or none of them.
- Integration: simulate `record_capital_gain` failure during exercise; verify automatic retry succeeds within 10 attempts, no admin action needed.

### A3. CHECK_STATUS peer endpoint

**Current state:** Only sender-side `OutboundReplayCron` retries. No way for the receiving bank to ask the sender about a transaction's state if the sender goes silent.

**Target state:**

- New inbound route `GET /api/v3/interbank/:transactionId/status` exposed by api-gateway, peer-authenticated via the existing `middleware.PeerAuth` (X-Api-Key OR HMAC).
- gRPC method `transaction.PeerTx.GetTxStatus(GetTxStatusRequest) returns (TxStatusResponse)` in transaction-service.
- Status resolution:
  - If the caller's `peer_bank_code` is the **counterparty** of the transaction (we are the sender): look up `outbound_peer_tx` by `transaction_id`, return `{state: pending|committed|rolled_back|dead_letter, last_action_at, our_role: "sender"}`.
  - If the caller is the **originator** (we are the receiver): look up `peer_idempotence_record` joined with `saga_log`, return `{state: prepared|committed|rolled_back|unknown, last_action_at, our_role: "receiver"}`.
  - If no record: `{state: "unknown", our_role: null}`. Peer should treat this as "we never saw it" and is safe to retry.
- New sender-side reconciler `PeerTxReconciler` in transaction-service:
  - On startup AND every 10 minutes, scan `outbound_peer_tx` rows in `pending` state older than 2× the retry interval (default: 60s).
  - For each, call peer's `CHECK_STATUS` via `peer_http_client`.
  - If peer says `committed`: mark local row `committed`.
  - If peer says `unknown` or `rolled_back`: rollback the local row, release sender-side reservation, emit `peer.tx.reconciled` Kafka event.
  - If peer is unreachable after 3 reconcile attempts: leave row `pending` for next cycle (do not give up).

**Files touched:**
- `transaction-service/internal/handler/peer_tx_grpc_handler.go` (add `GetTxStatus` RPC)
- `transaction-service/internal/service/peer_tx_reconciler.go` (new)
- `transaction-service/internal/sitx/peer_http_client.go` (add `CheckStatus` outbound call)
- `api-gateway/internal/handler/peer_tx_status_handler.go` (new inbound REST handler)
- `api-gateway/internal/router/router_v3.go` (register route under existing PeerAuth middleware)
- `contract/proto/transaction.proto` (add RPC + messages)
- `transaction-service/cmd/main.go` (wire reconciler)

**Tests:**
- Unit: status resolution for sender role, receiver role, unknown-transaction.
- Integration (two-bank): Bank A sends NEW_TX, kills its own service mid-flight (force-quit before commit). On restart, reconciler queries Bank B's CHECK_STATUS. If Bank B says `prepared`, Bank A sends COMMIT_TX. If Bank B says `unknown`, Bank A releases reservation. Verify no manual admin action needed in either path.

---

## Part B — Unified Portfolio Model & Routes

### B1. Portfolio identity (no new table)

Portfolio identity is a deterministic, URL-safe encoding of `(owner_type, owner_id)`:

| Encoded `portfolioId` | `owner_type` | `owner_id` |
|---|---|---|
| `client-<n>` | `client` | `<n>` |
| `bank` | `bank` | (singleton) |
| `fund-<n>` | `investment_fund` | `<n>` |

Encoding/decoding lives in `api-gateway/internal/handler/portfolio_id.go` with strict regex validation. Decoder returns 400 `invalid_portfolio_id` on malformed input.

### B2. Routes

All `/me/*` routes use `AnyAuthMiddleware` (accepts client or employee tokens). All non-`/me` portfolio routes use `AuthMiddleware` + ownership enforcement (B3).

```
GET /api/v3/me/portfolio
GET /api/v3/me/watchlist
GET /api/v3/me/favourites

GET /api/v3/portfolio/client/:clientId
GET /api/v3/portfolio/bank
GET /api/v3/portfolio/investment-fund/:fundId
GET /api/v3/portfolio/:portfolioId

GET /api/v3/watchlist/:portfolioId
GET /api/v3/favourites/:portfolioId
```

The `/portfolio/:portfolioId` form is the canonical one; the explicit `/portfolio/client/:clientId` etc. routes are convenience aliases that encode their input and delegate to the same handler. This keeps backwards compatibility (any existing frontend code referencing `/portfolio/client/42` keeps working) while enabling the new generic form.

### B3. Authorization (enforced gateway-side, before gRPC)

| Caller | Allowed `portfolioId` values |
|---|---|
| client principal | `client-<myPrincipalId>` only |
| employee, no special permissions | `bank` only |
| employee with `portfolio.view_client` | any `client-<n>` |
| employee with `portfolio.view_fund` | any `fund-<n>` |
| supervisor managing fund N (lookup at request time) | `fund-N` (even without `portfolio.view_fund`) |
| admin | all |

New permissions seeded in `user-service/internal/service/role_service.go`:
- `portfolio.view_client` → EmployeeSupervisor, EmployeeAdmin
- `portfolio.view_fund` → EmployeeSupervisor, EmployeeAdmin

Authorization is implemented via a new helper `enforcePortfolioAccess(c, resolvedIdentity, portfolioId)` in `api-gateway/internal/handler/validation.go`. Mismatch returns 403 `forbidden` for all portfolio routes (consistent with existing ownership checks in the file). Existence-leak is acceptable here since portfolio owners (clients, funds, bank) are not secret resources — only their contents are.

### B4. Response shape

```json
GET /api/v3/portfolio/client-42
{
  "portfolio_id": "client-42",
  "owner": { "type": "client", "id": 42, "name": "John Doe" },
  "totals": {
    "value_rsd": 1350000,
    "profit_rsd": 50000,
    "profit_pct": 3.85
  },
  "securities": {
    "totals": { "value_rsd": 1100000, "profit_rsd": 40000, "profit_pct": 3.77 },
    "positions": [
      {
        "asset_type": "stock",
        "symbol": "AAPL",
        "qty": 50,
        "avg_cost_rsd": 200,
        "current_price_rsd": 220,
        "current_value_rsd": 11000,
        "p_l_rsd": 1000,
        "p_l_pct": 10.0
      },
      {
        "asset_type": "option",
        "contract_id": 42,
        "underlying_symbol": "AAPL",
        "strike_rsd": 200,
        "qty": 50,
        "settlement_date": "2026-04-05",
        "premium_paid_rsd": 1150,
        "intrinsic_value_rsd": 2500,
        "p_l_rsd": 1350
      },
      {
        "asset_type": "future",
        "contract_id": 99,
        "underlying_symbol": "CORN",
        "qty": 2,
        "settlement_date": "2026-06-30",
        "p_l_rsd": -250
      }
    ]
  },
  "funds": {
    "totals": { "value_rsd": 250000, "profit_rsd": 10000, "profit_pct": 4.17 },
    "positions": [
      {
        "asset_type": "investment_fund",
        "fund_id": 7,
        "name": "Alpha Growth",
        "amount_invested_rsd": 25000,
        "current_value_rsd": 27000,
        "pct_of_fund": 0.5,
        "p_l_rsd": 2000,
        "p_l_pct": 8.0,
        "last_updated": "2026-05-28T10:00:00Z"
      }
    ]
  }
}
```

**Derived field semantics:**
- `current_value_rsd` for stocks: `qty × current_listing_price`. Cross-currency holdings are converted to RSD at the spot rate from exchange-service.
- `current_value_rsd` for fund positions: `fund.total_value × pct_of_fund` where `fund.total_value = fund.liquid_rsd + Σ(holding.qty × current_listing_price_rsd)`.
- `p_l_rsd` for stocks: `(current_price − avg_cost) × qty` (in RSD).
- `p_l_rsd` for fund positions: `current_value_rsd − amount_invested_rsd`.
- `pct_of_fund` for fund positions: `amount_invested_rsd / Σ(all positions' amount_invested_rsd)` for that fund.
- All derived fields are computed on read, not stored. Matches Celina-4(Nova).md prescription.

### B5. Implementation surface

- New gRPC RPC `stock.Portfolio.Get(GetPortfolioRequest) returns (PortfolioResponse)` in `contract/proto/stock.proto`.
- New service file `stock-service/internal/service/portfolio_service.go` with `GetPortfolio(ctx, ownerType, ownerId)` that:
  - Fans out to `holding_repository.ListByOwner(...)` (stocks/options/futures live there today)
  - Fans out to `fund_position_repository.ListByOwner(...)` for fund positions
  - Issues one batched listing-price read per unique symbol
  - Issues one exchange-rate read for cross-currency conversion
  - Computes derived fields and assembles the response
- New gateway handler `api-gateway/internal/handler/portfolio_handler.go` with:
  - `GetMyPortfolio` (resolves identity from JWT, delegates)
  - `GetPortfolio` (decodes `:portfolioId`, enforces ownership, delegates)
  - `GetPortfolioByOwnerType` (handles the convenience aliases by encoding their input)
- Existing fragmented endpoints (`/api/v3/holdings`, `/api/v3/funds/positions`, etc.) remain functional and unchanged — no breaking changes per the API versioning rules. They become thin wrappers calling the unified service if convenient.
- Watchlist and favourites follow the same pattern: existing service-layer logic untouched, only the gateway routes get the new shape with portfolio-id-based authorization.

**Tests:**
- Unit: portfolio service composition for each owner type (client with mix of all asset types; bank with bank-owned holdings; fund with no positions; empty portfolio).
- Integration in `test-app/workflows/`: end-to-end portfolio fetch for client/bank/fund; authorization checks (client cannot view another client; supervisor without `portfolio.view_fund` cannot view a fund they don't manage; admin can view all).

---

## Part C — Admin Cron Viewer

### C1. CronRegistry (per-service, in-memory)

A small shared package `contract/cronreg/registry.go` defines:

```go
type CronInfo struct {
    Name             string
    Service          string
    Description      string
    Interval         time.Duration  // 0 if cron expression
    CronExpression   string         // empty if interval-based
    LastStartedAt    *time.Time     // nil if never run
    LastFinishedAt   *time.Time
    LastError        string
    NextScheduledAt  time.Time
    IsPaused         bool
    PausedByEmployee int64
    PausedAt         *time.Time
    RunCount         int64
    ErrorCount       int64
}

type Registry interface {
    Register(name, description string, interval time.Duration) *Entry
    List() []CronInfo
    Get(name string) (CronInfo, bool)
    Pause(name string, byEmployeeId int64) error
    Resume(name string) error
    Trigger(name string) error
}
```

Every cron loop, on startup, calls `registry.Register(...)` and receives an `*Entry`. Before each tick the loop calls `entry.BeginRun()` (returns false if `IsPaused`); after each run it calls `entry.EndRun(err)`. This is the only invasive change to existing cron implementations.

Skip-while-paused semantics (no catch-up on resume): a paused cron's tick simply returns without doing work; on resume, the next tick fires normally per the original schedule.

### C2. Pause persistence

One small table per service that uses crons:

```sql
CREATE TABLE cron_pause_state (
  name              VARCHAR(100) PRIMARY KEY,
  is_paused         BOOLEAN NOT NULL DEFAULT FALSE,
  paused_by         BIGINT,
  paused_at         TIMESTAMPTZ,
  resumed_by        BIGINT,
  resumed_at        TIMESTAMPTZ
);
```

- At service startup, after constructing the registry but before any cron starts, the registry loads `cron_pause_state` rows and sets `IsPaused` accordingly.
- `Pause(name, employeeId)` and `Resume(name)` write to this table inside a transaction, then flip the in-memory flag.
- All other state (last run times, last error, counts) is in-memory only and resets on restart. That's fine — those fields are observability nice-to-haves, not source-of-truth.

### C3. gRPC AdminCron service (per service)

```proto
service AdminCron {
  rpc ListCrons(ListCronsRequest)   returns (ListCronsResponse);
  rpc GetCron(GetCronRequest)       returns (CronInfo);
  rpc TriggerCron(TriggerRequest)   returns (TriggerResponse);
  rpc PauseCron(PauseRequest)       returns (PauseResponse);
  rpc ResumeCron(ResumeRequest)     returns (ResumeResponse);
}

message TriggerRequest {
  string cron_name = 1;
  bool   force     = 2;  // if true, run even if paused
  int64  triggered_by_employee_id = 3;
}
```

- `Trigger` enqueues a one-shot run on the registry's internal trigger channel. The cron's worker loop checks both the timer AND the trigger channel via `select`. If `force=false` and `IsPaused`, the registry returns `FailedPrecondition` ("cron is paused; pass force=true to override").
- `Pause`/`Resume` are atomic: DB write + in-memory flag update inside a single transaction. Returns `AlreadyExists` if `Pause` is called on an already-paused cron.

Every service that has crons gets this gRPC server registered in its `cmd/main.go` on the existing service-port (alongside its main RPCs). No new ports.

### C4. Gateway aggregation

```
GET    /api/v3/admin/crons
GET    /api/v3/admin/crons/:service/:name
POST   /api/v3/admin/crons/:service/:name/trigger
POST   /api/v3/admin/crons/:service/:name/pause
POST   /api/v3/admin/crons/:service/:name/resume
```

- `:service` is in the URL so the gateway routes the control RPC directly without a registry lookup. Valid services are validated against a static list (`{credit-service, account-service, card-service, stock-service, transaction-service, notification-service, user-service}`).
- `GET /api/v3/admin/crons` uses `errgroup.WithContext` to fan out to every service's `ListCrons` RPC in parallel. If one service is unreachable, its entry in the response is `{ "service": "...", "status": "unreachable", "crons": [] }` rather than failing the whole request.
- The api-gateway holds a gRPC client pool for `AdminCron` per service (added to `api-gateway/internal/grpc/clients.go`).

### C5. Authorization

Three new permissions, all seeded ONLY on `EmployeeAdmin`:

- `admin.crons.view` — required for GET endpoints
- `admin.crons.trigger` — required for the trigger endpoint
- `admin.crons.manage` — required for pause/resume

All admin/cron routes use `AuthMiddleware` + `RequirePermission(...)`. No `/me` variant.

### C6. Audit trail

Every Trigger/Pause/Resume publishes a Kafka event `admin.cron-action`:

```json
{
  "action": "trigger" | "pause" | "resume",
  "service": "stock-service",
  "cron_name": "tax-collection",
  "employee_id": 3,
  "timestamp": "2026-05-28T10:00:00Z",
  "reason": "manual investigation"
}
```

Notification-service consumes this and writes to a new `admin_audit_log` table for future audit-log endpoints. (The audit-log surface itself is out of scope for this spec — but the events are persisted now so the data is available when that endpoint is added.)

### C7. Response shape

```json
GET /api/v3/admin/crons
{
  "services": [
    {
      "service": "credit-service",
      "status": "ok",
      "crons": [
        {
          "name": "credit-installment-collection",
          "description": "Collect due loan installments",
          "interval": "24h",
          "cron_expression": "",
          "last_started_at": "2026-05-28T00:00:00Z",
          "last_finished_at": "2026-05-28T00:00:12Z",
          "last_error": "",
          "next_scheduled_at": "2026-05-29T00:00:00Z",
          "is_paused": false,
          "run_count": 47,
          "error_count": 0
        }
      ]
    },
    {
      "service": "stock-service",
      "status": "ok",
      "crons": [
        {
          "name": "tax-collection",
          "description": "Collect capital-gains tax monthly",
          "cron_expression": "59 23 L * *",
          "next_scheduled_at": "2026-05-31T23:59:00Z",
          "is_paused": true,
          "paused_by_employee_id": 3,
          "paused_at": "2026-05-27T15:00:00Z"
        }
      ]
    },
    {
      "service": "verification-service",
      "status": "unreachable",
      "crons": []
    }
  ]
}
```

### C8. Implementation surface

**New shared code:**
- `contract/cronreg/registry.go` — the `Registry` and `Entry` types.
- `contract/cronreg/grpc_server.go` — generic `AdminCron` gRPC server backed by a `Registry`.
- `contract/proto/admin_cron.proto` — service definition.

**Per-service changes:**
- Each service's `cmd/main.go` constructs a `Registry`, registers `AdminCron` gRPC server, hands the registry to every cron constructor.
- Each existing cron file (e.g. `stock-service/internal/service/tax_cron.go`, `credit-service/internal/service/cron_service.go`, etc.) is patched to call `entry.BeginRun()` / `entry.EndRun(err)` around its work and `entry.IsPaused()` before doing work.
- `cron_pause_state` table added via GORM AutoMigrate in each service.

**Gateway changes:**
- `api-gateway/internal/handler/admin_cron_handler.go`
- `api-gateway/internal/router/router_v3.go` (register routes)
- `api-gateway/internal/grpc/admin_cron_clients.go` (per-service gRPC clients)

**Tests:**
- Unit: registry pause/resume/trigger semantics, including `force=true` override, including resume-on-restart from DB.
- Integration: end-to-end admin pause → cron's next tick is skipped → admin resume → cron's next tick runs. Then admin trigger on a paused cron without force → 412 FailedPrecondition. Admin trigger with force=true → cron runs once.

---

## Spec Update Requirement

After implementation, `docs/Specification.md` must be updated per the project rule:
- Section 6 (permissions): add `portfolio.view_client`, `portfolio.view_fund`, `admin.crons.view`, `admin.crons.trigger`, `admin.crons.manage`.
- Section 11 (gRPC services): add `stock.Portfolio.Get`, `transaction.PeerTx.GetTxStatus`, `<service>.AdminCron.*` per service.
- Section 17 (API routes): add the new portfolio, watchlist, favourites, admin/crons, and interbank/:id/status routes.
- Section 18 (entities): add `cron_pause_state`, credit-service `saga_log`.
- Section 19 (Kafka topics): add `saga.dead-letter`, `admin.cron-action`, `peer.tx.reconciled`.
- Section 21 (business rules): document the saga-recovery guarantees ("no cross-bank, OTC, or loan operation can leave the system in a state requiring admin intervention").

`REST_API_v1.md` must be updated with the new routes, and swagger annotations + regen must be run per CLAUDE.md.

---

## Out of Scope (Tracked for Follow-up)

- Fund-redemption mid-liquidation idempotency (medium-risk audit finding, deferred).
- Spec/code rename to align SI-TX `NEW_TX`/`COMMIT_TX`/`ROLLBACK_TX` with Celina-5's `RESERVE_FUNDS`/`COMMIT_FUNDS`/etc. message names — functionally equivalent today, documentation/naming change deferred.
- Admin audit-log read endpoint (events are persisted now; the read surface comes later).
- Cohort-test of two-bank reconciliation end-to-end with three running banks.

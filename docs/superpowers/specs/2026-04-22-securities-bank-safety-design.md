# Securities Order Bank-Safety Redesign

**Status:** Design approved — pending implementation plans
**Date:** 2026-04-22
**Authors:** discussion with user (brainstorming session)
**Supersedes:** nothing — fixes bugs tracked in `docs/Bugs.txt`
**Related plans:** `docs/superpowers/plans/2026-04-22-unblock-order-flow.md` (to be written), `docs/superpowers/plans/2026-04-22-bank-safe-settlement.md` (to be written)

---

## 1. Motivation

`docs/Bugs.txt` documents four production bugs in the securities trading path of `stock-service`, ranging from a broken order-creation 404 to a critical "buy orders never debit the account" bug and a family of seven bank-safety defects in the fill path.

This design covers the full fix in two implementation phases:

- **Phase 1 — Unblock order flow.** Fixes Bugs #1 (listing-id mapping), #2 (generated-source volume = 0), and #3 (request-context cancellation killing the execution goroutine). After Phase 1 orders run end-to-end again; they are **not yet bank-safe** (money movement still uses the current best-effort path).
- **Phase 2 — Bank-safe settlement.** Fixes Bug #4 (all seven sub-issues 4a–4h): introduces a fund-reservation mechanism on accounts, a saga-log pattern in stock-service for cross-service fill orchestration, proper cross-currency conversion, forex-as-exchange settlement, Kafka-after-saga-commit, and recovery reconciliation.

The user confirmed:
- Forex orders follow the same order pipeline as stocks (reserve funds → partial fills → saga), **but** settlement moves money between two of the user's own accounts (quote-ccy and base-ccy) instead of creating a `holding` row. Stock-service performs the forex conversion itself using the forex listing's own price — it does not delegate to `exchange-service` for forex pairs.
- Reservations must be idempotent and recoverable after a crash; pure in-memory / pure column-only storage is not acceptable.
- Settlements must appear in the account's ledger history exactly like existing transfers/payments.
- Only `direction=buy` is supported for forex (selling would be semantically equivalent to buying the inverse pair and is excluded from scope).

---

## 2. Architecture overview

### Services touched

| Service | Changes |
|---|---|
| **account-service** | New `reserved_balance` column on `accounts`; new tables `account_reservations` and `account_reservation_settlements`; new gRPC RPCs `ReserveFunds`, `ReleaseReservation`, `PartialSettleReservation`, `GetReservation`; `AvailableBalance` recomputed as `Balance - ReservedBalance`; settlement RPCs write the same ledger entries existing debits/credits do, so settlements appear in account transaction history. |
| **stock-service** | New `saga_logs` table mirroring transaction-service; `baseCtx` field on `OrderExecutionEngine` (bug #3); ListingID population in security handlers (bug #1); volume hashing in `generated_source.go` (bug #2); rewritten `portfolio_service.ProcessBuyFill` / `ProcessSellFill` using saga + reservations + currency conversion; forex settlement path (dual ledger entries across user's two accounts; no holding); commission as explicit saga step; `PublishOrderFilled` moves post-saga, no detached goroutine. `holdings.reserved_quantity` + `holding_reservations` + `holding_reservation_settlements` tables for sell-side quantity reservation. |
| **exchange-service** | No code change; stock-service calls existing `Convert` RPC for stocks/futures/options when listing currency differs from account currency. Forex orders do **not** call exchange-service. |
| **api-gateway** | No new routes; `POST /api/v1/me/orders` request body gains optional `base_account_id` (required when `security_type=forex`); new validation; error-path error codes updated. Swagger regenerated. |
| **contract/proto** | `account.proto` adds reservation RPCs and messages. `stock.proto` unchanged (ListingInfo.id field already exists; only handler wiring changes). |

### High-level data flow (buy stock, cross-currency)

1. Client `POST /api/v1/me/orders` → gateway → `stock-service.CreateOrder`.
2. Stock-service runs **placement saga**:
   - `validate_listing` — resolve listing, get listing currency.
   - `validate_account` — ownership + currency.
   - `compute_reservation_amount` — see §6.
   - `convert_currency` — call `exchange-service.Convert(listing_ccy → account_ccy)` if needed.
   - `reserve_funds` — call `account-service.ReserveFunds(account_id, order_id, converted_amount, account_ccy)`.
   - `persist_order` — save order with status `approved`, reservation metadata, `saga_id`.
   - Any step failure → compensating steps in reverse (e.g., persist failure → release reservation).
3. Stock-service returns the order; execution engine kicks off using the long-lived `baseCtx`.
4. Execution engine partial-fills the order. Each fill runs a **fill saga**:
   - `record_transaction` — insert `OrderTransaction`.
   - `convert_amount` — for cross-ccy securities only.
   - `settle_reservation` — `account-service.PartialSettleReservation(order_id, txn_id, converted_amount, memo)`, idempotent on `txn_id`; writes ledger entry.
   - `update_holding` — `holdingRepo.Upsert`; idempotent on PK.
   - `credit_commission` — separate ledger entry on bank's commission account, idempotent on memo.
   - `publish_kafka` — sync, after saga commit.
   - Each step recorded in `saga_logs`; failures trigger compensations per matrix in §5.
5. When the whole order completes (or is cancelled), stock-service calls `ReleaseReservation` for any leftover.

### High-level data flow (buy forex, e.g. EUR/USD)

1. Client provides `account_id` (quote-ccy, USD) and `base_account_id` (base-ccy, EUR).
2. Placement saga reserves the **quote**-currency cost on the USD account. No call to exchange-service.
3. Each fill:
   - `record_transaction`.
   - `settle_reservation_quote` — partial-settle USD reservation (debit).
   - `credit_base` — credit EUR account.
   - `credit_commission` — commission on quote side.
   - `publish_kafka`.
4. No `holding` row is created or touched. No `exchange-service.Convert` call.
5. Release unused USD reservation on completion.

---

## 3. Phase 1 — Unblock order flow

### 3.1 Bug #1 — ListingInfo.Id not populated

**Root cause** (per `Bugs.txt` #1): `toListingInfo` in `stock-service/internal/handler/security_handler.go:298` does not receive or populate the listings-table PK. All callers assemble `StockItem`/`FuturesItem`/`ForexItem` whose top-level `id` is the security-table PK, never the listings.id. Clients thus have no way to discover a listing's actual ID and send the security ID instead, causing `OrderService.CreateOrder` to miss in `listingRepo.GetByID` and return 404.

**Fix:**
- Add leading `listingID uint64` parameter to `toListingInfo` (security_handler.go:298); set `Id: listingID` on the returned `pb.ListingInfo`.
- Update all callers in the same file — `toStockItem`, `toStockDetail`, `toFuturesItem`, `toFuturesDetail`, `toForexPairItem`, `toForexPairDetail`, and their list-batch siblings — to pass in a resolved listing ID.
- Resolution strategy: extend the `ListStocks`/`ListFutures`/`ListForexPairs` repository queries to `JOIN listings ON listings.security_id = <sec>.id AND listings.security_type = '<type>'` and preload the listing ID into the view struct. For single-item detail paths, add `ListingRepo.GetBySecurityIDAndType(id uint64, secType string) (*Listing, error)`.
- Options are already correct and unchanged.

**Tests:** `security_handler_test.go` asserts `resp.Data[0].Listing.Id != 0` for each security type. Regression cases cover: list, get-by-id, search.

### 3.2 Bug #2 — Volume not populated in generated source

**Root cause:** `generated_source.go` never assigns `Volume` on `Stock`/`FuturesContract`/`ForexPair` or their `*WithListing` wrappers. The zero value propagates to DB; `calculateWaitTime` then falls back to `volume=1`, breaking simulated market behavior.

**Fix:**
- Add `hashVolume(seed string, min, max int64) int64` helper in `generated_source.go` using FNV hashing identical in style to the existing `hashPrice`. Deterministic across restarts.
- Ranges:
  - Stocks: 100_000 – 50_000_000
  - Futures: 1_000 – 500_000
  - Forex: 100_000_000 – 10_000_000_000
- Assign volume on all three security types and propagate into wrappers.

**Tests:** new `generated_source_test.go` asserts volume > 0 for every row; determinism test asserts same seed ↔ same volume across two calls.

### 3.3 Bug #3 — Execution context cancellation

**Root cause:** `OrderExecutionEngine.StartOrderExecution` (order_execution.go:73) derives its goroutine's context from the gRPC request ctx. The request ctx is cancelled the moment `CreateOrder` returns, which cancels the goroutine's ctx, causing `executeOrder`'s first `select { case <-ctx.Done(): return }` to exit before any fill runs. Orders get stuck at `status=approved` until a service restart, at which point `Start(ctx)` picks them up using the long-lived main.go ctx — which is why the bug looks intermittent.

**Fix:**
- Add `baseCtx context.Context` field to `OrderExecutionEngine`.
- Change `NewOrderExecutionEngine` to accept `baseCtx` as first parameter.
- In `StartOrderExecution`, change `context.WithCancel(ctx)` to `context.WithCancel(e.baseCtx)`. Drop or ignore the `ctx` parameter at the callsite (it remains on the method signature for gRPC handler ergonomics but is no longer used for cancellation).
- `cmd/main.go` wires the long-lived ctx from main.go:281 into the engine constructor.

**Tests:** new regression test in `order_execution_test.go`:
1. Construct engine with non-cancelled base ctx.
2. Call `StartOrderExecution(cancelledCtx, orderID)` with a pre-cancelled ctx.
3. Assert the goroutine still processes at least one fill (fake fill handler signals via channel).

### 3.4 Phase 1 non-goals

Phase 1 explicitly does **not** fix bug #4. Specifically:
- Reservations, saga logs, currency conversion, forex-as-exchange, commission saga, Kafka-after-commit — all deferred to Phase 2.
- The best-effort `ProcessBuyFill`/`ProcessSellFill` logic remains in place after Phase 1 ships. It still has bugs 4a–4h.
- A warning banner is added to `Specification.md` noting the securities fill path is not bank-safe until Phase 2 ships.

---

## 4. Phase 2 — Account-service reservation system

### 4.1 Schema

> **Note:** The SQL blocks in this document are illustrative. The actual schema is driven by GORM struct tags and `db.AutoMigrate` per CLAUDE.md §Architecture — no separate migration tool. The inline `INDEX (...)` lines indicate *where indexes are needed*, not literal DDL.

```sql
-- Fast-path running totals on the account
ALTER TABLE accounts ADD COLUMN reserved_balance DECIMAL NOT NULL DEFAULT 0;

-- Idempotency + state ledger for reservations; row is immutable except for status/updated_at/version
CREATE TABLE account_reservations (
    id             BIGSERIAL PRIMARY KEY,
    account_id     BIGINT NOT NULL REFERENCES accounts(id),
    order_id       BIGINT NOT NULL UNIQUE,            -- idempotency key
    amount         DECIMAL NOT NULL,                  -- IMMUTABLE after insert
    currency_code  VARCHAR(3) NOT NULL,
    status         VARCHAR(16) NOT NULL,              -- active | released | settled
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    version        BIGINT NOT NULL DEFAULT 0,
    INDEX (account_id, status)
);

-- Append-only children: one row per partial settlement. Fully immutable.
CREATE TABLE account_reservation_settlements (
    id                   BIGSERIAL PRIMARY KEY,
    reservation_id       BIGINT NOT NULL REFERENCES account_reservations(id),
    order_transaction_id BIGINT NOT NULL UNIQUE,      -- idempotency key from stock-service
    amount               DECIMAL NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL,
    INDEX (reservation_id)
);
```

### 4.2 Invariants

- `reserved_balance` equals `SUM(amount - SUM(settlements.amount))` over all `active` reservations on the account.
- `available_balance = balance - reserved_balance` (computed; not stored).
- `amount` on `account_reservations` is never mutated after insert.
- Settlement rows are append-only.
- A reservation can never be over-settled: `SUM(settlements.amount) ≤ reservation.amount`.
- GORM `BeforeUpdate` hook on `account_reservations` enforces version matching per CLAUDE.md §Concurrency.

### 4.3 Proto additions (`contract/proto/account/account.proto`)

```protobuf
service AccountService {
    // ... existing RPCs ...
    rpc ReserveFunds(ReserveFundsRequest) returns (ReserveFundsResponse);
    rpc ReleaseReservation(ReleaseReservationRequest) returns (ReleaseReservationResponse);
    rpc PartialSettleReservation(PartialSettleRequest) returns (PartialSettleResponse);
    rpc GetReservation(GetReservationRequest) returns (GetReservationResponse);
}

message ReserveFundsRequest {
    uint64 account_id = 1;
    uint64 order_id = 2;
    string amount = 3;          // decimal string
    string currency_code = 4;   // must match account.currency_code
}
message ReserveFundsResponse {
    uint64 reservation_id = 1;
    string reserved_balance = 2;
    string available_balance = 3;
}

message ReleaseReservationRequest { uint64 order_id = 1; }
message ReleaseReservationResponse { string released_amount = 1; string reserved_balance = 2; }

message PartialSettleRequest {
    uint64 order_id = 1;
    uint64 order_transaction_id = 2;   // idempotency key
    string amount = 3;
    string memo = 4;                    // written into the ledger entry
}
message PartialSettleResponse {
    string settled_amount = 1;
    string remaining_reserved = 2;
    string balance_after = 3;
    uint64 ledger_entry_id = 4;
}

message GetReservationRequest { uint64 order_id = 1; }
message GetReservationResponse {
    bool exists = 1;
    string status = 2;              // active|released|settled
    string amount = 3;
    string settled_total = 4;
    repeated uint64 settled_txn_ids = 5;   // for stock-service recovery reconciliation
}
```

### 4.4 Behavior

All RPCs run inside `db.Transaction` with `SELECT FOR UPDATE` on the `accounts` row.

- **ReserveFunds(account_id, order_id, amount, currency_code):**
  1. Lock account row.
  2. `FailedPrecondition` if `currency_code != account.currency_code`.
  3. `FailedPrecondition` if `balance - reserved_balance < amount`.
  4. `INSERT ... ON CONFLICT(order_id) DO NOTHING`.
  5. If inserted: `UPDATE accounts SET reserved_balance = reserved_balance + amount`.
  6. If conflicted: re-fetch existing, return its state (idempotent).

- **ReleaseReservation(order_id):**
  1. Load reservation + account FOR UPDATE.
  2. If missing: no-op, return `released_amount=0`.
  3. `remaining = amount - SUM(settlements.amount)`.
  4. If status == `active`: set `released`, decrement `reserved_balance` by `remaining`.
  5. Else: no-op, return 0.

- **PartialSettleReservation(order_id, order_transaction_id, amount, memo):**
  1. Load reservation + account FOR UPDATE.
  2. `FailedPrecondition` if reservation missing or status != `active`.
  3. `FailedPrecondition` if `SUM(settlements.amount) + amount > reservation.amount`.
  4. `INSERT INTO account_reservation_settlements (...) ON CONFLICT(order_transaction_id) DO NOTHING`.
  5. If inserted: decrement `reserved_balance` by `amount`; decrement `balance` by `amount`; write a `ledger_entries` row via existing `DebitWithLock` with the supplied memo.
  6. If now `SUM(settlements.amount) == reservation.amount`: set status `settled`.
  7. Return running totals + ledger entry id.

- **GetReservation(order_id):** read-only. Returns `settled_txn_ids` so stock-service can reconcile after a crash.

### 4.5 Spending limits compatibility

Today's `UpdateBalance` enforces daily/monthly limits atomically inside `SELECT FOR UPDATE` (`docs/Specification.md` §Spending Limits; CLAUDE.md §Spending Limits). `PartialSettleReservation` invokes the same limit-check path (posted money) within its transaction. `ReserveFunds` does **not** count toward spending limits — no money has moved at reservation time, matching real-bank hold vs. posting semantics.

### 4.6 Ledger visibility

Each `PartialSettleReservation` writes a `ledger_entries` row via the existing debit path, so settled fills appear in the user's `/api/v1/me/accounts/{id}/transactions` history exactly like transfers and payments. The memo references the order + fill (e.g., `"Order #123 partial fill (txn #456)"`).

### 4.7 Tests

Unit tests in `account-service/internal/service/reservation_service_test.go`:
- Reserve → balance/reserved_balance deltas match.
- Reserve idempotent on retry with same `order_id`.
- Reserve with insufficient available balance → FailedPrecondition, no DB change.
- Reserve with currency mismatch → FailedPrecondition.
- PartialSettle N times → running totals, settlement rows appended, ledger entries written.
- PartialSettle exceeding reservation → FailedPrecondition, no DB change.
- PartialSettle same `order_transaction_id` twice → second call is no-op.
- Release active → `reserved_balance` decreases by remaining; repeat Release → no-op.
- Release after full settle → no-op.
- Concurrent goroutines reserving on same account racing for exact available funds → one wins, one fails.

Integration tests in `account-service/internal/repository/reservation_repository_test.go` for raw DB invariants (status transitions, SUM constraints, optimistic lock conflicts).

---

## 5. Phase 2 — Stock-service saga log and fill path

### 5.1 `saga_logs` table

Mirrors `transaction-service/internal/model/saga_log.go` 1:1.

```sql
CREATE TABLE saga_logs (
    id                   BIGSERIAL PRIMARY KEY,
    saga_id              UUID NOT NULL,
    order_id             BIGINT NOT NULL,
    order_transaction_id BIGINT,                           -- set for fill-scoped sagas, NULL for placement saga
    step_number          INT NOT NULL,
    step_name            VARCHAR(64) NOT NULL,
    status               VARCHAR(16) NOT NULL,             -- pending | completed | failed | compensating | compensated
    is_compensation      BOOLEAN NOT NULL DEFAULT FALSE,
    compensation_of      BIGINT REFERENCES saga_logs(id),
    amount               DECIMAL,
    currency_code        VARCHAR(3),
    payload              JSONB,
    error_message        TEXT,
    retry_count          INT NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    version              BIGINT NOT NULL DEFAULT 0,
    INDEX (saga_id),
    INDEX (order_id),
    INDEX (status) WHERE status IN ('pending', 'compensating')
);
```

### 5.2 Saga types

- **Placement saga** — one per `CreateOrder` call. Scoped to `order_id`. Steps: `validate_listing` → `validate_account` → `compute_reservation_amount` → `convert_currency` (cross-ccy only) → `reserve_funds` → `persist_order`. Compensation: persist failure triggers `release_reservation`.
- **Fill saga** — one per partial fill inside the execution loop. Scoped to `order_id` + `order_transaction_id`. Steps vary by order type; see §5.4.

### 5.3 Repository + helper

- `SagaLogRepo` with `RecordStep`, `UpdateStatus`, `ListPendingForOrder`, `ListStuckSagas`, `GetByStepName`.
- `SagaExecutor` helper with `RunStep(ctx, name, amount, ccy, payload, fn)` and `RunCompensation(ctx, forStepID, name, fn)`, recording pending → completed/failed transitions. Mirrors transaction-service/internal/service/saga_helper.go.

### 5.4 Fill saga steps by order type

#### Buy — stocks / futures / options (cross-ccy aware)

1. `record_transaction` — insert `OrderTransaction`. No compensation.
2. `convert_amount` — `exchange.Convert(listing_ccy → account_ccy)` if different. Store both amounts + rate on the txn row. No compensation.
3. `settle_reservation` — `PartialSettleReservation(order_id, txn_id, converted_amount, memo)`. Compensation: `accountClient.CreditWithLock(account_id, converted_amount, memo="compensating order #X fill #Y")` — explicit reverse ledger entry; ledger stays append-only.
4. `update_holding` — `holdingRepo.Upsert`; idempotent on PK. Compensation: `holdingRepo.Decrement` under SELECT FOR UPDATE (delete if qty → 0).
5. `credit_commission` — `CreditWithLock(bank_commission_account_id, commission, memo="commission for order #X fill #Y")`. Idempotent via memo existence check. Compensation not triggered from here (see matrix).
6. `publish_kafka` — synchronous call (not a detached goroutine), after saga step commits.

#### Sell — stocks / futures / options

Holding decrement moved *after* credit so shares only leave the user once the cash is safely in their account.

1. `record_transaction`.
2. `convert_amount`.
3. `credit_proceeds` — `CreditWithLock(account_id, converted_amount, memo)` with unique memo. Compensation: `DebitWithLock` reverse.
4. `decrement_holding` — `holdingRepo.Decrement` under SELECT FOR UPDATE; `FailedPrecondition` if under-qty (should not happen because of the holding reservation at placement — see §6.3). Compensation: `DebitWithLock` reverse on the credit.
5. `credit_commission`.
6. `publish_kafka`.

#### Forex (buy only)

No holding. Both legs of the trade are ledger entries on the user's own accounts.

1. `record_transaction`.
2. `settle_reservation_quote` — `PartialSettleReservation(order_id, txn_id, quote_amount, memo)` against the quote-ccy account. Compensation: `CreditWithLock` reverse on quote.
3. `credit_base` — `CreditWithLock(base_account_id, base_amount, memo)`; `base_amount = quote_amount / pair_price` using the fill execution price. Compensation: `DebitWithLock` reverse on base.
4. `credit_commission` — commission on the quote side by convention.
5. `publish_kafka`.

### 5.5 Compensation trigger matrix

| Step that failed | Action |
|---|---|
| record_transaction | none — nothing to undo |
| convert_amount | none |
| settle_reservation / credit_proceeds / settle_reservation_quote | none — reservation still open or no money moved yet; order will retry next iteration |
| update_holding / decrement_holding | compensate the preceding settle/credit |
| credit_base (forex) | compensate settle_reservation_quote |
| credit_commission | log + retry via recovery; do **not** compensate holding/settle — the trade is valid, only the fee is pending |
| publish_kafka | retry via recovery; no compensation |

If a compensation itself fails, the saga step stays `compensating` for human review (monitored / alerted).

### 5.6 Kafka-after-commit

`publish_kafka` is a synchronous saga step. The Kafka key is `"order-fill-{txn_id}"` for consumer-side dedup. The event payload carries `saga_id`, `native_amount`, `native_currency`, `converted_amount`, `account_currency`, `fx_rate` (nullable), `is_done`, `timestamp`. No detached goroutine.

### 5.7 Recovery reconciliation on startup

After `execEngine.Start(ctx)` picks up active approved orders, `sagaRecovery.Reconcile(ctx)` runs:

1. List `saga_logs` rows with status `pending` or `compensating` older than 30s.
2. For each:
   - `settle_reservation` / `settle_reservation_quote`: call `accountClient.GetReservation(order_id)`. If the step's `order_transaction_id` appears in `settled_txn_ids`, mark step `completed`. Else retry (idempotent on `txn_id`).
   - `update_holding` / `decrement_holding`: compare stock-service's own holding state. If already at target, mark completed; else retry.
   - `credit_commission`: existence check on the bank's commission account by memo. If present, mark completed; else retry.
   - `publish_kafka`: always retry (Kafka-side dedup).
3. On 5 consecutive failures for a step: leave in `compensating` for human review with a loud log + metric.

### 5.8 Tests

- Unit: `saga_log_repository_test.go`, `saga_helper_test.go` cover step recording, status updates, compensation recording, optimistic-lock conflicts.
- Unit: `order_service_test.go` covers placement saga happy path + each failure mode + forex-specific validations (missing `base_account_id`, mismatched currency).
- Unit: `portfolio_service_test.go` rewrite covers buy, sell, forex buy, each compensation trigger, cross-ccy path.
- Unit: `order_execution_test.go` adds recovery reconciliation tests.
- Integration (`test-app/workflows/`):
  - `wf_stock_reservation_test.go` — place + cancel, reservation released.
  - `wf_stock_concurrent_orders_test.go` — concurrent orders exceeding balance; exactly one succeeds.
  - `wf_stock_cross_currency_test.go` — USD stock + RSD account; converted debit correct.
  - `wf_stock_fill_failure_test.go` — gRPC interceptor fails PartialSettle once; verify no divergence and recovery retries.
  - `wf_stock_commission_failure_test.go` — commission call fails; main trade valid; recovery retries commission.
  - `wf_forex_test.go` — forex buy EUR/USD with USD+EUR accounts; USD debited, EUR credited, no holding row, no exchange-service call.
  - `wf_forex_validation_test.go` — forex placement without `base_account_id`, currency mismatches — rejected at gateway.
  - `wf_stop_limit_refund_test.go` — stop-limit that never triggers + TIF expiry → reservation released.
  - `wf_stock_buy_sell_test.go` — updated to match new semantics.

---

## 6. Phase 2 — Order placement and reservation sizing

### 6.1 New placement flow (buy)

Replaces the current "immediately approved, no money check" path:

1. `validate_listing` — bug #1 fix already in place from Phase 1.
2. `validate_account` — ownership (JWT `user_id` matches account owner), status `active`.
3. `compute_reservation_amount` — see §6.2.
4. `convert_currency` — only for stocks/futures/options when `listing_ccy != account_ccy`. Forex skips this step (currencies validated instead).
5. `reserve_funds` — on the reservation account.
6. `persist_order` — status `approved`, with `reservation_amount`, `reservation_currency`, `reservation_account_id`, `base_account_id` (forex only), `placement_rate` (audit), `saga_id`.
7. Commit + return. Execution engine starts as before, now using the long-lived `baseCtx`.

Compensation: most common case is `persist_order` failure after `reserve_funds` succeeded → `release_reservation`.

### 6.2 Reservation sizing

For a buy order of quantity `Q`:

- **Limit / stop-limit:** `reserve = Q × limit_price × contract_size + commission_estimate`. No slippage buffer — fills never exceed the limit.
- **Market / stop:** `reserve = Q × listing.High × contract_size × (1 + settings.market_slippage_pct) + commission_estimate`. `market_slippage_pct` default 5%.
- **Commission estimate:** `settings.commission_rate × trade_value`.
- **Forex:** same rules, using the quote-ccy amount.

If `available_balance < reservation`: return `FailedPrecondition`. Gateway maps to HTTP 409 `business_rule_violation` with message `"insufficient available balance"`.

### 6.3 Sell-side holding reservations

Symmetric to funds reservation, living in `stock-service` DB:

```sql
ALTER TABLE holdings ADD COLUMN reserved_quantity BIGINT NOT NULL DEFAULT 0;

CREATE TABLE holding_reservations (
    id         BIGSERIAL PRIMARY KEY,
    holding_id BIGINT NOT NULL REFERENCES holdings(id),
    order_id   BIGINT NOT NULL UNIQUE,
    quantity   BIGINT NOT NULL,            -- IMMUTABLE
    status     VARCHAR(16) NOT NULL,       -- active | released | settled
    created_at, updated_at, version
);

CREATE TABLE holding_reservation_settlements (
    id                     BIGSERIAL PRIMARY KEY,
    holding_reservation_id BIGINT NOT NULL,
    order_transaction_id   BIGINT NOT NULL UNIQUE,
    quantity               BIGINT NOT NULL,
    created_at
);
```

`available_quantity = quantity - reserved_quantity`. Sell placement asserts sufficient `available_quantity`, reserves it, and the fill saga `decrement_holding` step partial-settles this reservation. Release on cancel / expiry. Applies to stocks/futures/options only; forex has no holdings.

### 6.4 Forex placement specifics

`POST /api/v1/me/orders` request body gains two fields, **required for `security_type=forex`**:

```json
{
  "listing_id": 123,
  "direction": "buy",
  "quantity": 100,
  "order_type": "market",
  "account_id": 42,          // quote-ccy (USD for EUR/USD); funds reserved here
  "base_account_id": 55      // base-ccy (EUR); proceeds credited here
}
```

Gateway validations (api-gateway per CLAUDE.md §Input Validation):
- Both accounts owned by the JWT `user_id`.
- `account_id.currency_code == pair.quote_currency`.
- `base_account_id.currency_code == pair.base_currency`.
- `account_id != base_account_id`.
- `direction == "buy"` (sell is rejected with `InvalidArgument`).

Service layer re-validates — defense in depth.

### 6.5 Cancellation and expiry paths

- `CancelOrder` — marks order cancelled; saga step `release_reservation` (+ `release_holding_reservation` for sells); releases remainder.
- Fully filled market order with slippage unused: explicit "trim release" step at `markDone`.
- Stop-limit that never triggers + time-in-force expiry: background reaper marks order expired → releases both reservations.

---

## 7. Phase 2 — Currency conversion integration

### 7.1 When conversion is needed

| Order type | Listing ccy | Account ccy | Action |
|---|---|---|---|
| stocks/futures/options | same | same | no conversion |
| stocks/futures/options | differs | differs | `exchange.Convert(listing_ccy → account_ccy)` |
| forex — quote leg | quote_ccy | quote_account_ccy | must match, validated at placement |
| forex — base leg | base_ccy | base_account_ccy | must match |

### 7.2 Call sites

`exchange-service.Convert(from, to, amount) → (converted, effective_rate, error)` (exchange-service/internal/service/exchange_service.go:155). Already supports two-leg foreign↔foreign conversions via RSD. Stock-service never reinvents conversion.

Three call sites, all inside saga steps:
1. Placement — reservation sizing.
2. Fill — debit amount (buy).
3. Fill — credit amount (sell).

### 7.3 OrderTransaction schema additions

```sql
ALTER TABLE order_transactions
    ADD COLUMN native_amount DECIMAL,         -- in listing currency
    ADD COLUMN native_currency VARCHAR(3),
    ADD COLUMN converted_amount DECIMAL,      -- in account currency; NULL if same ccy
    ADD COLUMN account_currency VARCHAR(3),
    ADD COLUMN fx_rate DECIMAL;               -- NULL if same ccy
```

Existing `total_price` column stays and continues to mean "native total". Additive-only; no breaking change for consumers reading the old column.

### 7.4 Forex conversion math

Stock-service does this internally using the forex listing's fields. No exchange-service call.

For pair `BASE/QUOTE` with listing price `p`:
- Buy 1 unit of the pair = pay `p` QUOTE, receive 1 BASE.
- Order quantity `Q`: reserve `Q × p × (1 + slippage)` QUOTE.
- On fill at execution price `p_exec`: debit `Q × p_exec` QUOTE, credit `Q` BASE.

### 7.5 Rate timing

- Placement rate is a snapshot stored for audit only. Reservation slack (slippage buffer) absorbs normal drift.
- Fill rate is fresh at each partial. Converted amount at fill time is checked against remaining reservation.
- If cumulative fill conversions would exceed the reservation (rare, violent FX move): `PartialSettleReservation` returns `FailedPrecondition`, compensation releases what's left, order transitions to `failed_insufficient_reservation`, Kafka emits `stock.order-failed`. Explicit correctness-over-convenience: auto-topping-up reservations is out of scope.

### 7.6 Commission separation

Two independent fees:
- **Exchange-service commission** — built into `Convert`, accrued to the bank's FX book inside exchange-service. Not a stock-service concern.
- **Order commission** — `settings.commission_rate × native_trade_value`, converted to account currency, credited to the bank's main commission account as a dedicated saga step.

---

## 8. Spec + doc + infra updates

### 8.1 `docs/Specification.md`

- **§17 (API routes):** POST `/api/v1/me/orders` request body — add optional `base_account_id` (required when `security_type=forex`); document new error cases (409 insufficient available balance, 400 forex currency mismatch, 400 forex direction=sell). GET `/api/v1/me/orders/{id}` response — add `reservation_amount`, `reservation_currency`, `native_currency`, `placement_rate`. GET `/api/v1/me/accounts/{id}` response — add `reserved_balance`, `available_balance`.
- **§18 (entities):** add `AccountReservation`, `AccountReservationSettlement`, `HoldingReservation`, `HoldingReservationSettlement`, `SagaLog` (stock-service). Update `Account` (`ReservedBalance`), `OrderTransaction` (native/converted/fx_rate), `Holding` (`ReservedQuantity`).
- **§19 (Kafka topics):** `stock.order-filled` payload fields `saga_id`, `converted_amount`, `account_currency`, `fx_rate`. New topic `stock.order-failed` for the insufficient-reservation / fill-failed path.
- **§20 (enums):** `reservation_status`: `active|released|settled`. `saga_step_status`: `pending|completed|failed|compensating|compensated`.
- **§21 (business rules):**
  - "Buy orders for securities reserve funds at placement in the account currency; reservations are released on cancellation or partially released when filled under the reserved amount."
  - "Forex orders reserve on the quote-currency account and credit the base-currency account on fill; no holdings are created for forex; forex does not use exchange-service."
  - "Sell orders for securities reserve holdings at placement and decrement them at each fill; sells are rejected if available quantity is insufficient."
  - "Securities order commissions are charged to the bank's commission account as a separate saga step per fill."
  - "Kafka fill events are published only after the fill saga step commits."
- **§11 (gRPC):** document `ReserveFunds`, `ReleaseReservation`, `PartialSettleReservation`, `GetReservation`.
- **§3 (gateway client wiring):** no change (stock-service already wires account-service and exchange-service).
- **§6 (permissions):** no change.

### 8.2 `docs/api/REST_API_v1.md`

- POST `/api/v1/me/orders` — document `base_account_id`, forex constraints, new error cases.
- GET `/api/v1/me/orders/{id}` — document new response fields.
- GET `/api/v1/me/accounts/{id}` — document `reserved_balance`, `available_balance`.
- GET `/api/v1/me/accounts/{id}/transactions` — document that settled fills appear in transaction history with order + fill memos.

### 8.3 Swagger

Regenerate `api-gateway/docs/` (`make swagger`). All changed handler annotations updated.

### 8.4 docker-compose

No new services; no new env vars. Account-service + stock-service get new tables via auto-migrate on startup — no compose change needed.

### 8.5 Phase 1 documentation note

At the top of `docs/Specification.md` §Order Settlement: insert a banner "⚠ Securities fill path is best-effort until Phase 2 (`docs/superpowers/plans/2026-04-22-bank-safe-settlement.md`) ships. Reservations and saga-based compensation are not yet active." Removed when Phase 2 ships.

---

## 9. Out of scope

- Front-end work.
- Refactor of transaction-service's existing saga.
- Any change to exchange-service.
- Forex `direction=sell` (rejected at validation).
- Partial-cancel-during-fill (only whole-remaining-order cancellation is supported).
- Auto-topping-up reservations under violent FX moves (order fails instead; documented).
- Cross-user reservation transfers, reservation expiry timers beyond TIF, or reservation holds on non-fungible balances.

---

## 10. Open questions / risks

- `order_transactions.order_transaction_id` as a cross-service idempotency key works only if stock-service never reuses / re-inserts the same id. We rely on the auto-increment PK of `order_transactions`, which is already a uniqueness guarantee in stock-service's DB.
- Recovery assumes `settled_txn_ids` from `GetReservation` is authoritative for "what account-service already processed." This is true because `PartialSettleReservation` commits everything in one DB transaction; there is no window where account-service has written the ledger entry but not the settlement row, or vice versa.
- Forex price semantics: the current listing `Price`/`High`/`Low` for a forex pair must represent the price of 1 base-ccy unit denominated in quote-ccy. The generated source already produces plausible values (Bugs.txt #2 fix adds volume but not price semantics). If anyone changes that convention later, forex settlement math breaks; added as a doc invariant in §21.

---

## 11. Deliverables

Two implementation plans will be written next, following this spec:

1. **`docs/superpowers/plans/2026-04-22-unblock-order-flow.md`** — covers Phase 1 (§3): bugs #1, #2, #3. Output: orders run end-to-end, volume > 0, listing-id returned correctly. Fill path remains best-effort.
2. **`docs/superpowers/plans/2026-04-22-bank-safe-settlement.md`** — covers Phase 2 (§4–§8): reservations, saga log, fill rewrites, currency conversion, forex-as-exchange, commission saga, Kafka-after-commit, spec + REST + Swagger + integration tests.

Both plans must include unit + integration test coverage per CLAUDE.md §Testing Requirement. Neither ships until `make lint` is clean on all touched services (CLAUDE.md §Linting Requirement).

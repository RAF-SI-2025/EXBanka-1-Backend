# Investment Funds Design (Celina 4)

**Status:** Design approved — pending implementation plan
**Date:** 2026-04-24
**Authors:** discussion with user (brainstorming session)
**Supersedes:** nothing — net-new feature
**Related plans:** TBD — implementation plan to be written against this spec
**Cross-spec hooks:** none required; Spec 2 (OTC option contracts), Spec 3 (interbank communication) and Spec 4 (aggregated reporting / TBD) will be written in parallel and may extend this feature

> Note on code blocks: all SQL below is illustrative. The real schema is driven by GORM `AutoMigrate` per CLAUDE.md. Proto snippets are illustrative — `make proto` generates the actual bindings.

---

## 1. Motivation

Celina 4 of the project specification (see `docs/Celina 4(Nova).docx.md`, lines 206–400) introduces **investment funds** as a financial product enabling both clients and the bank itself to invest in securities and diversify portfolios. Each fund:

- Has **exactly one RSD account** ("likvidna sredstva") holding the fund's cash position.
- Is created and managed by a **supervisor**, who is its "manager" (`menadžer`).
- May hold positions of both clients (`ClientFundPosition`) and the bank itself.
- Tracks invest/redeem events per client as `ClientFundTransaction` rows (equivalent to the `FundContribution` entity used throughout this spec).
- Supports supervisor-driven securities purchases executed **on behalf of the fund** (as opposed to on behalf of the bank directly).

The Celina doc also mandates:

1. **Automatic liquidation** of fund holdings when a client's redeem amount exceeds the fund's cash balance (doc line 263, "Napomena").
2. **Conversion fees** charged to client redemptions but not to supervisor withdrawals from the bank's own position (doc line 379, "Napomena 4").
3. When an admin revokes `isSupervisor` from a supervisor that manages funds, ownership of those funds transfers to the admin (doc line 334).
4. A **Profit Banke** portal for supervisors showing (a) each actuary's realized profit in RSD and (b) the bank's positions across funds with manager name, percentage share, RSD value, and realized profit (doc lines 383–400).
5. Derived (non-persisted) aggregates: `VrednostFonda`, `Profit`, `ProcenatFonda`, `TrenutnaVrednostPozicije` (doc lines 237, 276–280).

This spec translates those requirements into a concrete design that reuses the existing Phase 2 bank-safe settlement machinery (account reservations, saga logs, ledger entries, holding reservations) introduced by the `feature/securities-bank-safety` branch.

---

## 2. Architecture overview

### Services touched

| Service | Changes |
|---|---|
| **stock-service** | Owner of funds. New tables: `investment_funds`, `client_fund_positions`, `fund_contributions`, `fund_holdings`. New `InvestmentFundService` gRPC service. Reuses `SagaLog`/`SagaExecutor` for invest/redeem/liquidate sagas and extends the existing placement saga to accept an `on_behalf_of={fund_id|bank}` payload. New Kafka consumer for `user.supervisor-demoted` to reassign funds atomically. |
| **account-service** | No RPC changes. Fund RSD accounts are regular bank-owned `is_bank_account=true` accounts (same machinery as existing `bank` sentinel accounts). Existing `CreateAccount`, `ReserveFunds`, `PartialSettleReservation`, `ReleaseReservation`, `UpdateBalance`, `GetLedgerEntries` RPCs cover all movements. New `memo` and `idempotency_key` values are used to tag fund-related ledger entries and reservations. |
| **exchange-service** | No changes. Existing `Convert` RPC is called for cross-currency invest and cross-currency redeem. |
| **user-service** | Owner of the **demote-supervisor saga driver**. When an admin revokes `isSupervisor` (or the `funds.manage` permission) from a supervisor, user-service publishes `user.supervisor-demoted` to Kafka after the permission-update DB TX commits. Also aggregates actuary realized profit for `/api/v1/actuaries/performance` by calling stock-service RPC (sourcing data from stock-service's capital gains + order transaction history). |
| **api-gateway** | 8 new REST routes (see §6). One existing route is extended: `POST /api/v1/me/orders` gains an optional `on_behalf_of` object. Swagger regenerated; `docs/api/REST_API_v1.md` updated. |
| **contract/proto** | `stock.proto` gets a new `InvestmentFundService` service with ~10 RPCs. `contract/kafka/messages.go` adds 6 topic constants + payload structs. |

### Why stock-service owns funds (not a new service)

A fund's state is dominated by its holdings (`FundHolding`), which are identical in shape and lifecycle to the per-user `holding` rows stock-service already owns. The fund's RSD "likvidnost" lives in account-service (same as any other bank-owned account), so stock-service does not re-implement balance tracking. Putting funds in stock-service lets the existing placement/fill saga be extended in one place to handle "on behalf of fund" orders.

### High-level data flow (client invest, cross-currency, e.g. from EUR account into fund #101)

1. Client `POST /api/v1/investment-funds/101/invest` with `{source_account_id, amount, currency}` → api-gateway → `stock-service.InvestInFund`.
2. Stock-service runs the **invest saga** (full step list in §5.1):
   - `validate_fund` (exists, active, meets `minimum_contribution_rsd` after FX) →
   - `convert_to_rsd` (if source ccy ≠ RSD, call `exchange-service.Convert`) →
   - `reserve_source` (`account-service.ReserveFunds(source_account_id, saga_id, amount_native, source_ccy)`) →
   - `settle_source_debit` (`PartialSettleReservation`, ledger memo `"Invest in fund #101"`, idempotency_key=`saga_id`) →
   - `credit_fund_account` (`UpdateBalance(fund.rsd_account_id, +amount_rsd)`, ledger memo `"Contribution from client #M (saga=...)"`) →
   - `upsert_client_fund_position` (`total_contributed_rsd += amount_rsd`, optimistic-locked on `version`) →
   - `record_contribution` (insert `FundContribution` row with `status=completed`, `direction=invest`) →
   - `publish_kafka` (`stock.fund-invested` AFTER saga commit).
3. Any step failure triggers ordered compensations (see §5.1 matrix).

### High-level data flow (client redeem, fund cash insufficient)

1. Client `POST /api/v1/investment-funds/101/redeem` with `{amount_rsd, target_account_id}` → api-gateway → `stock-service.RedeemFromFund`.
2. **Redeem saga** (§5.2):
   - `validate_fund_and_position` (client has position; `amount_rsd` ≤ `client_position.current_value_rsd`) →
   - `compute_fee` (for `system_type=client`: `fee_rsd = amount_rsd × fund_redemption_fee_pct`; for `system_type=employee` supervisor acting on bank position: `fee_rsd = 0`) →
   - `ensure_liquidity` (fetch `fund.rsd_account.balance`; if `< amount_rsd + fee_rsd`, invoke **liquidate_holdings sub-saga** — see §5.3) →
   - `debit_fund_account` (`UpdateBalance(fund.rsd_account_id, -(amount_rsd))`, ledger memo `"Redemption to client #M"`) →
   - `credit_fee_to_bank` (ledger entry moving `fee_rsd` from fund RSD account to bank RSD account; skipped when `fee_rsd=0`) →
   - `convert_if_needed` (if target account currency ≠ RSD, `exchange-service.Convert(RSD → target_ccy)`) →
   - `credit_target` (`UpdateBalance(target_account_id, +amount_native)`, ledger memo `"Redemption from fund #101"`) →
   - `update_client_fund_position` (decrement `total_contributed_rsd` proportionally — see §4.2 rule) →
   - `record_contribution` (`direction=redeem`, `status=completed`) →
   - `publish_kafka` (`stock.fund-redeemed`).

### High-level data flow (supervisor buy on behalf of fund)

1. Supervisor `POST /api/v1/me/orders` body `{security_id, direction:"buy", quantity, on_behalf_of:{type:"fund", fund_id:101}}` → `stock-service.CreateOrder`.
2. Placement saga (already introduced in `2026-04-22-securities-bank-safety-design.md`) branches at `compute_reservation_target`:
   - If `on_behalf_of.type=fund`: validate that `req.actor_id == fund.manager_employee_id` OR actor has `EmployeeAdmin` role; set `reservation_account_id = fund.rsd_account_id`; set `order.owner_user_id = 1_000_000_000` (bank sentinel) and `order.fund_id = 101`.
   - If `on_behalf_of.type=bank`: existing bank-employee flow (reservation on a bank account of the supervisor's choosing).
   - Otherwise (no `on_behalf_of`): existing client / own-behalf flow.
3. Every fill credits `FundHolding` (not user `holding`) when `order.fund_id` is non-null.

### Cross-service saga idempotency keys (consistency)

All cross-service steps derive idempotency keys from stable IDs:

- Invest saga steps: `invest-<fund_id>-<saga_id>-<step_name>`
- Redeem saga steps: `redeem-<fund_id>-<saga_id>-<step_name>`
- Liquidation sub-saga order: `liquidate-<fund_id>-<saga_id>-<holding_id>`
- On-behalf-of-fund order placement: existing `place-<order_id>-<step_name>`
- Fund-fill settlement: existing `fill-<order_id>-<txn_id>-<step_name>`

---

## 3. Data model

### 3.1 Tables (stock-service DB, `stock_db`)

```sql
-- Illustrative; real schema via GORM AutoMigrate.

CREATE TABLE investment_funds (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  manager_employee_id BIGINT NOT NULL,
  minimum_contribution_rsd NUMERIC(20,4) NOT NULL DEFAULT 0,
  rsd_account_id BIGINT NOT NULL UNIQUE, -- account-service PK of the fund's RSD account
  active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  version BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX ux_investment_funds_name_active ON investment_funds(name) WHERE active = true;
CREATE INDEX ix_investment_funds_manager ON investment_funds(manager_employee_id);

CREATE TABLE client_fund_positions (
  id BIGSERIAL PRIMARY KEY,
  fund_id BIGINT NOT NULL REFERENCES investment_funds(id) ON DELETE RESTRICT,
  user_id BIGINT NOT NULL,
  system_type VARCHAR(10) NOT NULL CHECK (system_type IN ('client','employee')),
  total_contributed_rsd NUMERIC(20,4) NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  version BIGINT NOT NULL DEFAULT 0,
  UNIQUE (fund_id, user_id, system_type)
);
CREATE INDEX ix_client_fund_positions_user ON client_fund_positions(user_id, system_type);

CREATE TABLE fund_contributions (
  id BIGSERIAL PRIMARY KEY,
  fund_id BIGINT NOT NULL REFERENCES investment_funds(id) ON DELETE RESTRICT,
  user_id BIGINT NOT NULL,
  system_type VARCHAR(10) NOT NULL CHECK (system_type IN ('client','employee')),
  direction VARCHAR(8) NOT NULL CHECK (direction IN ('invest','redeem')),
  amount_native NUMERIC(20,4) NOT NULL,
  native_currency VARCHAR(8) NOT NULL,
  amount_rsd NUMERIC(20,4) NOT NULL,
  fx_rate NUMERIC(20,10) NULL,
  fee_rsd NUMERIC(20,4) NOT NULL DEFAULT 0,
  source_or_target_account_id BIGINT NOT NULL,
  saga_id BIGINT NOT NULL REFERENCES saga_logs(id),
  status VARCHAR(12) NOT NULL CHECK (status IN ('pending','completed','failed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ix_fund_contributions_fund ON fund_contributions(fund_id, created_at DESC);
CREATE INDEX ix_fund_contributions_user ON fund_contributions(user_id, system_type, created_at DESC);
CREATE INDEX ix_fund_contributions_saga ON fund_contributions(saga_id);

CREATE TABLE fund_holdings (
  id BIGSERIAL PRIMARY KEY,
  fund_id BIGINT NOT NULL REFERENCES investment_funds(id) ON DELETE RESTRICT,
  security_type VARCHAR(16) NOT NULL, -- 'stock','futures','forex','option'
  security_id BIGINT NOT NULL,
  quantity BIGINT NOT NULL DEFAULT 0,
  reserved_quantity BIGINT NOT NULL DEFAULT 0,
  average_price_rsd NUMERIC(20,4) NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  version BIGINT NOT NULL DEFAULT 0,
  UNIQUE (fund_id, security_type, security_id)
);
CREATE INDEX ix_fund_holdings_fund ON fund_holdings(fund_id);
```

**GORM `BeforeUpdate` hooks** must be added on every table with a `version` column per CLAUDE.md (§Optimistic Locking): `InvestmentFund`, `ClientFundPosition`, `FundHolding`.

### 3.2 Column changes on existing tables

**`orders`** (stock-service):
- Add nullable `fund_id BIGINT REFERENCES investment_funds(id)`. When non-null, the order was placed on behalf of a fund.
- When `fund_id IS NOT NULL`, `owner_user_id = 1_000_000_000` and `system_type = 'employee'` (bank sentinel).

**`order_transactions`**: no change — the transaction is tied to the order, which already carries `fund_id`.

### 3.3 Derived (non-persisted) fields

Computed at read time in the service layer, never stored:

```
fund.value_rsd      = fund_rsd_account.balance
                    + Σ (holding.quantity × current_market_price_rsd(security_id))

total_contribs_all  = Σ over all positions in fund of total_contributed_rsd
                      (includes positions with system_type=employee, user_id=bank_sentinel)

position.current_value_rsd = fund.value_rsd × (position.total_contributed_rsd / total_contribs_all)
position.profit_rsd        = position.current_value_rsd - position.total_contributed_rsd
position.percentage_fund   = position.total_contributed_rsd / total_contribs_all
```

`current_market_price_rsd(security_id)`:
- For securities quoted in a non-RSD listing, apply `exchange.Convert(listing_ccy → RSD)` on the listing's `last_price`.
- For RSD listings, use `last_price` directly.
- Cached per-request in memory to avoid N round-trips; never persisted.

When `total_contribs_all = 0` (empty fund), percentage and current value both resolve to `0`.

### 3.4 System settings

One new row in `system_settings` (stock-service):

| Key | Default | Meaning |
|---|---|---|
| `fund_redemption_fee_pct` | `0.005` | 0.5% fee on client redemptions; supervisors acting on bank position pay 0. |

---

## 4. Business rules

### 4.1 Fund creation (supervisor only)

1. Validate `funds.manage` permission.
2. Validate `name` uniqueness among `active=true` funds (case-insensitive).
3. Validate `minimum_contribution_rsd >= 0`.
4. In one DB transaction:
   - Call `account-service.CreateBankAccount(currency='RSD', purpose=fmt.Sprintf("fund_%d", next_fund_id_reservation))` — account-service returns an `is_bank_account=true` account.
   - Insert `investment_funds` row with `manager_employee_id = actor_employee_id` and `rsd_account_id = returned_account_id`.
5. Publish `stock.fund-created`.

### 4.2 Client fund position update on redeem

Partial redemption proportionally reduces `total_contributed_rsd`:

```
reduced_contrib = position.total_contributed_rsd
                × (amount_rsd / position.current_value_rsd_at_saga_start)
position.total_contributed_rsd -= reduced_contrib
```

Full redemption (`amount_rsd == position.current_value_rsd`): set `total_contributed_rsd = 0`. The `ClientFundPosition` row is retained (not deleted) so history is preserved.

### 4.3 Supervisor can always act on bank's position

When a supervisor invests/redeems via `POST /api/v1/investment-funds/{id}/invest` or `/redeem` using `on_behalf_of=bank`, the `ClientFundPosition` row is `(fund_id, user_id=1_000_000_000, system_type='employee')`. Source/target account must be a bank-owned RSD or foreign-currency account.

### 4.4 Redemption cap

A client may redeem up to **100%** of their `current_value_rsd` (inclusive). Requests exceeding that are rejected with `FailedPrecondition`/`business_rule_violation`. After a 100% redemption, `total_contributed_rsd = 0` and the position row remains.

### 4.5 Auto-liquidation strategy

When `fund_rsd_account.balance < amount_rsd + fee_rsd` at the moment the redeem saga runs:

- **Order of liquidation:** FIFO by `fund_holdings.created_at ASC` (oldest first).
- **Price used for valuation:** `listing.Low` (the conservative bid-side price used by existing sell-order execution for market sells).
- **Quantity per holding:** the smallest integer `q` such that `q × price_per_unit_rsd >= remaining_needed_rsd`. Iterate through holdings in FIFO order until the cumulative sold value meets the deficit.
- **Mechanism:** submit a market sell order per holding via the existing order-placement + execution engine. The order carries `owner_user_id=1_000_000_000`, `system_type='employee'`, `fund_id=<this fund>`. Proceeds credit the fund's RSD account via the existing fill saga, reducing the holding and increasing liquidity atomically. On the redeem saga side, the `ensure_liquidity` step waits (polls with timeout) until cumulative fills cover the deficit.
- **Timeout:** 60 seconds. If partial fills fall short, the redeem saga compensates (release reservation, mark `fund_contribution` as `failed`) and returns `ResourceExhausted`/`liquidation_timeout`. The already-completed liquidation sells remain — no retroactive unwinding of real sells.

### 4.6 On-behalf-of-fund orders

- Valid only for `direction=buy` at spec time. `direction=sell` on behalf of a fund is a **supervisor liquidation** and is a distinct flow in §4.5. (A future spec may permit supervisor-initiated `sell` on a specific `FundHolding`; out of scope here.)
- Actor must either be the fund's `manager_employee_id` or hold the `EmployeeAdmin` role.
- Reservation lives on the fund's RSD account; the order cannot be placed if the account lacks funds.
- Fills credit `fund_holdings` (upsert on `(fund_id, security_type, security_id)`), not user `holding`.

### 4.7 Supervisor demotion

Trigger: admin uses the existing employee-permission endpoint to remove `funds.manage` (or the role containing it) from a supervisor.

In user-service, inside the permission-update TX:

1. Compute `revoked_permissions = previous - new`.
2. If `funds.manage` ∈ `revoked_permissions`, publish a **pending** row in user-service's outbox (to be written in the implementation plan as a lightweight table — or reused from transaction-service's outbox pattern) with payload `{supervisor_id, admin_id = actor.id, revoked_at}`.

After TX commits, an outbox relay publishes `user.supervisor-demoted` to Kafka.

Stock-service consumes and runs the **demote-supervisor saga** (§5.5):

1. In one DB transaction: `UPDATE investment_funds SET manager_employee_id = admin_id, version = version+1 WHERE manager_employee_id = supervisor_id`. Optimistic-lock per-row; retry on conflict.
2. For any in-flight sagas where the supervisor is a step actor (e.g., an invest saga still running), **log a warning** (`SupervisorDemotedWhileSagaActive`) but do not block. The admin inherits any mid-flight obligations; the saga continues with its original metadata.
3. For pending OTC option contracts managed by the supervisor (covered in **Spec 2 — OTC options, to be written**), Spec 2 will extend this consumer to also reassign those.
4. Publish `stock.funds-reassigned` with `{supervisor_id, admin_id, fund_ids[]}`.

### 4.8 Minimum contribution

- Compared **in RSD, after FX**. If the converted RSD amount < `minimum_contribution_rsd`, reject with `FailedPrecondition`/`minimum_contribution_not_met`.
- Minimum applies on every invest event, not just the first.

### 4.9 Supervisor's own-employee invest on a fund

A supervisor using `POST /api/v1/investment-funds/{id}/invest` **without** `on_behalf_of=bank` (i.e., their own personal employee account) is rejected at the handler with `InvalidArgument` — employees cannot hold personal fund positions. Employee fund positions only exist as the bank sentinel row.

---

## 5. Saga breakdowns

Every saga below is persisted via the shared `saga_logs` table (one `SagaLog` row per saga; one `SagaStep` row per step). Steps are executed by `SagaExecutor`; compensations run in reverse order. All cross-service steps use idempotency keys (§2). Kafka events publish after saga commit.

### 5.1 Invest saga

| # | Step | Description | Compensation |
|---|------|-------------|--------------|
| 1 | `validate_fund` | Fund exists, `active=true`. Read only; nothing to compensate. | — |
| 2 | `validate_source_account` | Source account belongs to actor; currency supported. | — |
| 3 | `convert_to_rsd` | If source ccy ≠ RSD, call `exchange-service.Convert`. Store `fx_rate`, `amount_rsd`. | — (pure read) |
| 4 | `validate_minimum` | `amount_rsd >= fund.minimum_contribution_rsd`. | — |
| 5 | `reserve_source` | `account-service.ReserveFunds(source_account_id, idempotency=invest-<fund>-<saga>-reserve, amount_native, ccy)`. | `ReleaseReservation(idempotency)` |
| 6 | `insert_pending_contribution` | Insert `FundContribution` row `status=pending`, `saga_id`. | Mark row `status=failed`. |
| 7 | `settle_source_debit` | `PartialSettleReservation` for the full reserved amount (writes source ledger entry). | No-op on compensate — ledger entries are append-only; step 8's credit is reversed by step 8's own compensation. |
| 8 | `credit_fund_account` | `UpdateBalance(fund.rsd_account_id, +amount_rsd)`, memo `"Contribution from client #M, saga=<id>"`. | Reverse: credit source back via `UpdateBalance(source, +amount_native)` with compensation-memo. |
| 9 | `upsert_position` | Atomic `INSERT … ON CONFLICT (fund_id, user_id, system_type) DO UPDATE SET total_contributed_rsd = total_contributed_rsd + excluded.total_contributed_rsd, version = version+1`. | Decrement by `amount_rsd` (or delete if new and now zero). |
| 10 | `complete_contribution` | Update `FundContribution.status = 'completed'`. | No compensation; redemption of a completed contribution is a separate saga. |
| 11 | `publish_kafka` | AFTER saga commit: publish `stock.fund-invested`. | — |

### 5.2 Redeem saga

| # | Step | Description | Compensation |
|---|------|-------------|--------------|
| 1 | `validate_fund_and_position` | Fund active; position exists. | — |
| 2 | `compute_current_value` | Read `fund.value_rsd` + position's `current_value_rsd` at saga start. | — |
| 3 | `validate_amount` | `0 < amount_rsd <= position.current_value_rsd`. | — |
| 4 | `compute_fee` | Fee rule per §4.2, §4.3. | — |
| 5 | `ensure_liquidity` | If `fund_rsd_account.balance < amount_rsd + fee_rsd`, invoke liquidate_holdings sub-saga (§5.3); wait for completion or timeout. | Sub-saga compensates per §5.3. |
| 6 | `insert_pending_contribution` | `FundContribution status=pending, direction=redeem, saga_id`. | Mark `status=failed`. |
| 7 | `debit_fund_account` | `UpdateBalance(fund.rsd_account_id, -(amount_rsd))`, memo `"Redemption to user #M"`. | Reverse credit with compensation-memo. |
| 8 | `credit_fee_to_bank` | If `fee_rsd > 0`: transfer `fee_rsd` from fund RSD account to bank RSD account (two ledger entries). | Reverse both. |
| 9 | `convert_to_target_ccy` | If target account ccy ≠ RSD, `exchange-service.Convert(RSD → target_ccy)`. | — (pure read) |
| 10 | `credit_target_account` | `UpdateBalance(target_account_id, +amount_native)`. | Reverse debit. |
| 11 | `update_position` | Decrement `total_contributed_rsd` per §4.2 rule; increment `version`. | Re-add the decremented amount. |
| 12 | `complete_contribution` | Mark row `completed`. | — |
| 13 | `publish_kafka` | `stock.fund-redeemed` after saga commit. | — |

### 5.3 Liquidate holdings sub-saga

Invoked from step 5 of redeem saga. Parent saga waits with timeout (§4.5).

| # | Step | Description | Compensation |
|---|------|-------------|--------------|
| 1 | `select_holdings_fifo` | Read fund holdings ordered by `created_at ASC`, filter `quantity - reserved_quantity > 0`. | — |
| 2 | `compute_liquidation_plan` | For each holding, compute qty needed at `listing.Low`; accumulate until plan covers `deficit_rsd`. If plan insufficient (total available < deficit), fail immediately (sub-saga and parent). | — |
| 3..N | `place_sell_order[holding_i]` | For each holding in plan: submit market sell order via existing order-placement saga. Quantity reserved on holding via `HoldingReservationService`. | Release holding reservation + cancel order. |
| N+1 | `await_fills` | Poll `order_transactions` until cumulative RSD credited to fund RSD account ≥ `deficit_rsd` OR timeout. | — (fills can't be un-executed) |

**Compensation note:** sell orders already partially filled are NOT reverted on timeout (real market action). The parent redeem saga fails with `ResourceExhausted` and the fund keeps the now-liquid cash; a supervisor can rerun the redeem on behalf of the client. This is explicitly called out in Bugs/known-issues docs after implementation.

### 5.4 On-behalf-of-fund order placement saga

Extends the **existing** placement saga introduced in `2026-04-22-securities-bank-safety-design.md`. One new conditional branch at step `compute_reservation_target`:

```
if req.on_behalf_of.type == "fund":
  assert actor has funds.manage AND (actor.id == fund.manager_employee_id OR actor has admin role)
  reservation_account_id = fund.rsd_account_id
  order.owner_user_id = 1_000_000_000
  order.system_type   = "employee"
  order.fund_id       = req.on_behalf_of.fund_id
```

Fill saga is extended at step `update_holding`: when `order.fund_id IS NOT NULL`, upsert `fund_holdings` instead of `holdings`. No compensation changes.

### 5.5 Demote-supervisor saga (stock-service consumer)

Consumer listens to `user.supervisor-demoted`. Idempotent on `(supervisor_id, demoted_at)` — same message re-processed twice is a no-op.

| # | Step | Description | Compensation |
|---|------|-------------|--------------|
| 1 | `find_managed_funds` | `SELECT * FROM investment_funds WHERE manager_employee_id = supervisor_id FOR UPDATE`. | — |
| 2 | `reassign_funds` | In one TX, update each fund's `manager_employee_id = admin_id`; bump `version`. On optimistic-lock conflict, retry the single row. | Compensation: reverse (but never in practice — admin inheritance is terminal). |
| 3 | `warn_inflight` | Scan `saga_logs` for unfinished sagas whose payload references any reassigned fund; log structured warning per finding. | — |
| 4 | `publish_kafka` | `stock.funds-reassigned` with `{supervisor_id, admin_id, fund_ids[]}`. | — |

### 5.6 Compensation matrix summary

Every saga follows the CLAUDE.md saga pattern: compensations flagged `compensating` in `saga_logs` and retried by the background `SagaRecovery` worker. Failures in compensation land the saga in `compensation_failed` and emit `stock.saga-dead-letter` (already wired). No new dead-letter topic needed.

---

## 6. REST API surface

All new routes live under `/api/v1/` (v2 fallback is implicit — CLAUDE.md §API Versioning). All error responses use `apiError()` helper and the `{"error":{"code","message","details"}}` shape.

### 6.1 `POST /api/v1/investment-funds`

**Auth:** `AuthMiddleware` + `RequirePermission("funds.manage")`.

**Request:**
```json
{
  "name": "Alpha Growth Fund",
  "description": "IT sector focus.",
  "minimum_contribution_rsd": "1000.00"
}
```

**Validation:** name 1–128 chars unique; description ≤ 2000 chars; minimum ≥ 0.

**Response 201:**
```json
{
  "fund": {
    "id": 101,
    "name": "Alpha Growth Fund",
    "description": "IT sector focus.",
    "minimum_contribution_rsd": "1000.00",
    "manager_employee_id": 25,
    "rsd_account_id": 4812,
    "rsd_account_number": "265-4812-00",
    "active": true,
    "created_at": "2026-04-24T10:00:00Z"
  }
}
```

**Errors:** 400 `validation_error`, 401, 403 `forbidden`, 409 `conflict` (name taken), 500.

### 6.2 `GET /api/v1/investment-funds`

**Auth:** `AnyAuthMiddleware` (client + employee).

**Query:** `page` (default 1), `page_size` (default 20, max 100), `search` (matches `name` ILIKE), `active` (default `true`).

**Response 200:**
```json
{
  "funds": [
    {
      "id": 101,
      "name": "Alpha Growth Fund",
      "description": "IT sector focus.",
      "manager": {"employee_id": 25, "full_name": "Jane Doe"},
      "minimum_contribution_rsd": "1000.00",
      "value_rsd": "2600000.0000",
      "liquid_rsd": "1500000.0000",
      "profit_rsd": "5000000.0000",
      "active": true
    }
  ],
  "page": 1, "page_size": 20, "total": 1
}
```

### 6.3 `GET /api/v1/investment-funds/{id}`

**Auth:** `AnyAuthMiddleware`.

**Response 200** — fund summary (as above) plus:
```json
{
  "fund": { ... },
  "holdings": [
    {
      "security_type": "stock",
      "security_id": 55,
      "ticker": "AAPL",
      "quantity": 120,
      "average_price_rsd": "18500.0000",
      "current_price_rsd": "19000.0000",
      "acquired_at": "2026-03-15T00:00:00Z"
    }
  ]
}
```

Holdings are ordered by `created_at ASC` (matches FIFO liquidation order).

### 6.4 `PUT /api/v1/investment-funds/{id}`

**Auth:** `AuthMiddleware` + `RequirePermission("funds.manage")`.

**Authorization rule:** actor must be the fund's `manager_employee_id` OR hold `EmployeeAdmin`. Otherwise 403.

**Request:** same shape as create (all fields optional; omitted fields unchanged).

**Response 200:** updated fund summary. **Errors:** 400, 401, 403, 404 `not_found`, 409 (name collision, optimistic lock), 500.

### 6.5 `POST /api/v1/investment-funds/{id}/invest`

**Auth:** `AnyAuthMiddleware`.

**Request:**
```json
{
  "source_account_id": 9091,
  "amount": "500.00",
  "currency": "EUR",
  "on_behalf_of": {"type": "bank"}
}
```

- `on_behalf_of` optional. If absent or `{"type":"self"}`: the position is `(user_id = jwt.sub, system_type = jwt.system_type)`. Clients may only use `self`/omit (validated).
- `{"type":"bank"}` requires `funds.manage` permission; position becomes the bank sentinel row.

**Validation:** `amount` > 0; `currency` ∈ supported currencies; `source_account_id` must match actor's ownership (or be a bank account when `on_behalf_of=bank`).

**Response 202 Accepted:**
```json
{
  "contribution": {
    "id": 7721,
    "fund_id": 101,
    "direction": "invest",
    "amount_native": "500.00",
    "native_currency": "EUR",
    "amount_rsd": "58500.00",
    "fx_rate": "117.0000000000",
    "fee_rsd": "0.00",
    "status": "completed",
    "saga_id": 44201
  }
}
```

**Errors:** 400, 401, 403, 404 (fund), 409 (optimistic lock), 422 `business_rule_violation` (minimum not met, insufficient funds), 500.

### 6.6 `POST /api/v1/investment-funds/{id}/redeem`

**Auth:** `AnyAuthMiddleware`.

**Request:**
```json
{
  "amount_rsd": "25000.00",
  "target_account_id": 9091,
  "on_behalf_of": {"type": "bank"}
}
```

- `on_behalf_of` optional; same semantics as §6.5.
- For clients, `amount_rsd` is RSD-denominated. If the target account currency ≠ RSD, conversion to target currency happens inside the saga; the client receives the converted amount on their account.

**Validation:** `amount_rsd` > 0; actor must own the target account or the target must be a bank account when `on_behalf_of=bank`.

**Response 202:** same contribution shape as §6.5 with `direction="redeem"` and `fee_rsd` populated.

**Errors:** 400, 401, 403, 404, 422 (insufficient position, liquidation_timeout), 429 (rate-limited on liquidation), 500.

### 6.7 `GET /api/v1/me/investment-funds`

**Auth:** `AnyAuthMiddleware`.

**Response 200:** list of the caller's positions (scoped by `user_id, system_type` from JWT). For each:

```json
{
  "positions": [
    {
      "fund_id": 101,
      "fund_name": "Alpha Growth Fund",
      "total_contributed_rsd": "25000.00",
      "percentage_fund": "0.00005",
      "current_value_rsd": "27000.00",
      "profit_rsd": "2000.00",
      "last_changed_at": "2026-03-15T00:00:00Z"
    }
  ]
}
```

### 6.8 `GET /api/v1/investment-funds/positions?scope=bank`

**Auth:** `AuthMiddleware` + `RequirePermission("funds.bank-position-read")`.

**Query:** `scope` required, currently only `bank` supported. (Future values like `all` reserved.)

**Response 200:**
```json
{
  "positions": [
    {
      "fund_id": 101,
      "fund_name": "Alpha Growth Fund",
      "manager_full_name": "Jane Doe",
      "bank_contribution_rsd": "500000.00",
      "percentage_fund": "0.1923",
      "current_value_rsd": "520000.00",
      "profit_rsd": "20000.00"
    }
  ]
}
```

### 6.9 `GET /api/v1/actuaries/performance`

**Auth:** `AuthMiddleware` + `RequirePermission("funds.bank-position-read")`.

**Response 200:**
```json
{
  "actuaries": [
    {
      "employee_id": 25,
      "full_name": "Jane Doe",
      "role": "EmployeeSupervisor",
      "realized_profit_rsd": "125000.0000"
    }
  ]
}
```

**Source of `realized_profit_rsd`:** sum of realized gains from `capital_gains` records attributable to each actuary across all bank orders and on-behalf-of-fund orders. Attribution rule: an order's `actor_employee_id` receives credit. Pure-client orders (no acting employee) are excluded.

### 6.10 Extension to `POST /api/v1/me/orders`

Existing route. Request body gains an optional `on_behalf_of` object:

```json
{
  "security_id": 55,
  "security_type": "stock",
  "direction": "buy",
  "quantity": 100,
  "limit_price": null,
  "account_id": 4812,
  "on_behalf_of": {"type": "fund", "fund_id": 101}
}
```

Accepted values:
- omitted / `{"type":"self"}`: existing behavior.
- `{"type":"bank"}`: existing employee-on-behalf-of-client flow renamed (or a new flag — implementation-detail decision deferred to the plan). Must match existing `on_behalf_of_client_id` or supersede it per the plan.
- `{"type":"fund","fund_id":N}`: see §4.6. Requires `funds.manage`.

Response shape unchanged. New error codes: `fund_not_managed_by_actor` (403), `fund_insufficient_funds` (422).

---

## 7. gRPC additions (stock-service `InvestmentFundService`)

Illustrative — real bindings via `make proto`.

```proto
// stock.proto additions
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

message CreateFundRequest {
  int64 actor_employee_id = 1;   // from gateway JWT
  string name = 2;
  string description = 3;
  string minimum_contribution_rsd = 4;
}

message InvestInFundRequest {
  int64 fund_id = 1;
  int64 actor_user_id = 2;
  string actor_system_type = 3; // "client" or "employee"
  int64 source_account_id = 4;
  string amount = 5;             // native amount
  string currency = 6;
  OnBehalfOf on_behalf_of = 7;   // optional
}

message OnBehalfOf {
  string type = 1;  // "self" | "bank" | "fund"
  int64 fund_id = 2; // required iff type == "fund"
}

message ContributionResponse {
  int64 id = 1;
  int64 fund_id = 2;
  string direction = 3;
  string amount_native = 4;
  string native_currency = 5;
  string amount_rsd = 6;
  string fx_rate = 7;
  string fee_rsd = 8;
  string status = 9;
  int64 saga_id = 10;
}

message GetActuaryPerformanceRequest {}
message GetActuaryPerformanceResponse {
  repeated ActuaryPerformance actuaries = 1;
}
message ActuaryPerformance {
  int64 employee_id = 1;
  string realized_profit_rsd = 2;
}
```

### Account-service extensions

**None.** `GetLedgerEntries`, `ReserveFunds`, `PartialSettleReservation`, `ReleaseReservation`, `UpdateBalance`, `CreateBankAccount` already cover all flows. Fund RSD accounts are created by `CreateBankAccount(currency="RSD", purpose="fund_<id>")`.

### User-service extensions

One new RPC:

```proto
rpc ListEmployeeFullNames(ListEmployeeFullNamesRequest) returns (ListEmployeeFullNamesResponse);

message ListEmployeeFullNamesRequest { repeated int64 employee_ids = 1; }
message ListEmployeeFullNamesResponse { map<int64, string> names_by_id = 1; }
```

Used by stock-service to decorate fund-manager and actuary-performance responses. Already partially covered if an equivalent RPC exists — the plan verifies and reuses if so.

---

## 8. Kafka topics

All new topics pre-created via `EnsureTopics` in stock-service and user-service (CLAUDE.md §Kafka Topic Pre-Creation).

| Topic | Producer | Consumer(s) | Payload (illustrative) |
|---|---|---|---|
| `stock.fund-created` | stock-service | (audit / notification) | `{fund_id, name, manager_employee_id, rsd_account_id, created_at}` |
| `stock.fund-updated` | stock-service | (audit) | `{fund_id, changed_fields[], updated_at}` |
| `stock.fund-invested` | stock-service | (audit, notifications) | `{fund_id, user_id, system_type, amount_rsd, amount_native, native_currency, fx_rate, saga_id, contribution_id}` |
| `stock.fund-redeemed` | stock-service | (audit, notifications) | `{fund_id, user_id, system_type, amount_rsd, fee_rsd, target_account_id, saga_id, contribution_id}` |
| `stock.funds-reassigned` | stock-service | (audit) | `{supervisor_id, admin_id, fund_ids[]}` |
| `user.supervisor-demoted` | user-service | stock-service | `{supervisor_id, admin_id, revoked_at}` |

All payloads also include a `message_id` (UUID) + `occurred_at` per the existing `contract/kafka/messages.go` convention.

`stock.saga-dead-letter` is reused from the existing securities-bank-safety design for any saga that lands in `compensation_failed`.

---

## 9. Permissions

| Code | Description | Default roles |
|---|---|---|
| `funds.manage` | Create/update funds; place orders on behalf of funds or bank; invest/redeem on behalf of bank. | `EmployeeSupervisor`, `EmployeeAdmin` (already seeded per current `role_service.go:48`) |
| `funds.bank-position-read` | View the bank's positions across funds; view actuary performance report. | `EmployeeSupervisor`, `EmployeeAdmin` (**new** — added to seed) |

Discovery/read (`GET /api/v1/investment-funds`, `/{id}`, `/me/investment-funds`) requires only authentication (no new permission); clients can browse and view their own positions.

Seed change: add `{"funds.bank-position-read", "View bank fund positions and actuary performance", "funds"}` to `DefaultPermissions` in `user-service/internal/service/role_service.go` and include it in the permission sets for `EmployeeSupervisor` and `EmployeeAdmin`.

---

## 10. Testing strategy

### 10.1 Unit tests (per service, mocked dependencies)

stock-service:
- `investment_fund_service_test.go` — create/update/get/list happy paths and auth failures.
- `fund_invest_saga_test.go` — cross-currency + RSD paths; each step's compensation; minimum-contribution rejection; optimistic-lock retry on `client_fund_positions`.
- `fund_redeem_saga_test.go` — cash-sufficient path; cash-insufficient triggers liquidation; fee-zero for bank redemptions; fee-0.5% for client redemptions; full-redemption resets `total_contributed_rsd` to 0.
- `fund_liquidation_test.go` — FIFO ordering verified; plan insufficient → immediate fail; partial fills + timeout → redeem saga fails.
- `fund_on_behalf_order_test.go` — fund reservation target selection; fund_holdings upsert on fill; manager/admin auth gate.
- `demote_supervisor_consumer_test.go` — idempotent re-consumption; mass reassignment; warning log when in-flight sagas exist.

user-service:
- `supervisor_demotion_outbox_test.go` — permission revocation publishes event after TX commits; no publish on failed TX.

account-service:
- None — uses existing RPCs.

### 10.2 Integration tests (`test-app/workflows/`)

At least six new workflows (file names illustrative):

1. `wf_investment_funds_basic_test.go` — supervisor creates fund → client invests (RSD) → client reads `/me/investment-funds` → redeems partially → verifies RSD account ledger entries on both sides + `value_rsd`, `profit_rsd` derived fields.
2. `wf_investment_funds_cross_ccy_test.go` — client invests from EUR account; verifies FX call, `fx_rate` populated in contribution, `amount_rsd` correct.
3. `wf_investment_funds_liquidation_test.go` — supervisor places 2 buy orders on fund to build 2 holdings → client redeems amount > fund cash → asserts FIFO liquidation order via `stock.fund-redeemed` + sell orders created → redeem completes after fills.
4. `wf_investment_funds_bank_position_test.go` — supervisor invests bank funds → `GET /investment-funds/positions?scope=bank` returns the bank row; bank redemption charges fee_rsd=0.
5. `wf_investment_funds_on_behalf_order_test.go` — supervisor places on-behalf-of-fund buy order → order fills → `fund_holdings` populated; `holdings` table unchanged; non-manager supervisor gets 403.
6. `wf_investment_funds_supervisor_demotion_test.go` — admin revokes `isSupervisor` from a fund-managing supervisor → all their funds reassigned to the admin after the Kafka event propagates (use `waitForKafkaMessage` shared helper).
7. (Bonus) `wf_investment_funds_actuary_performance_test.go` — after a few on-behalf-of-fund orders with realized gains, `GET /actuaries/performance` returns the actor's profit matching `capital_gains` sum.
8. (Bonus) `wf_investment_funds_minimum_contribution_test.go` — invest below minimum (pre-FX and post-FX) returns 422.

Use shared helpers from `test-app/workflows/helpers_test.go` (client setup, JWT minting) and `helpers_orders_test.go` (order placement + fill polling). Do NOT inline Kafka scanning — use the existing `awaitKafkaMessage` helper.

### 10.3 Contract tests

- `contract/proto/stock/stock.proto` compile cleanly via `make proto`.
- Generated `InvestmentFundService` client wired into api-gateway's `internal/grpc/` bundle.

### 10.4 Lint

`make lint` passes on every touched service per CLAUDE.md.

---

## 11. Spec / REST doc updates

Upon implementation the following docs update:

1. **`docs/Specification.md`**
   - §3 (gateway client wiring): add `InvestmentFundServiceClient`.
   - §6 (permissions): add `funds.bank-position-read`.
   - §11 (gRPC service definitions): add `InvestmentFundService` RPC summary.
   - §17 (REST routes): add the 8 new routes from §6 above + the `on_behalf_of` extension on `POST /api/v1/me/orders`.
   - §18 (entities): add `InvestmentFund`, `ClientFundPosition`, `FundContribution`, `FundHolding`; note `orders.fund_id` addition.
   - §19 (Kafka topics): add 6 new topics (§8).
   - §20 (enums): add `on_behalf_of.type ∈ {self, bank, fund}`, `fund_contribution.direction ∈ {invest, redeem}`, `fund_contribution.status ∈ {pending, completed, failed}`.
   - §21 (business rules): add Rules §4.1–§4.9 above.

2. **`docs/api/REST_API_v1.md`** — one section per new route (§6.1–§6.9) in the existing format (auth, params, request body, responses, errors, example). Update `POST /api/v1/me/orders` to document the new optional `on_behalf_of` field.

3. **`api-gateway/docs/swagger.*`** — regenerated via `make swagger` (auto on `make build`).

---

## 12. Out of scope

- **Fund sell-side supervisor action outside of auto-liquidation.** Supervisor cannot `POST /me/orders direction=sell on_behalf_of=fund` in this spec. Only buys on behalf of funds; sells come exclusively through the liquidation sub-saga or future work.
- **Fund-to-fund transfers.** Merging or splitting funds, or moving cash/holdings between funds. Future spec.
- **Per-month fund performance history storage.** Celina 4 mentions "mesečni/kvartalni/godišnji prikaz" (line 316) but does not mandate persisted history; we compute `value_rsd`/`profit_rsd` from current state only. A later aggregation spec may add a daily snapshot table.
- **Deactivating / deleting a fund.** No endpoint in this spec; implicitly supported via `active=false` toggle in `PUT` if the plan deems it needed, but full lifecycle (what happens to open positions on deactivation?) is deferred.
- **Multi-currency fund accounts.** Each fund has exactly one RSD account per Celina 4. A multi-ccy fund is not in scope.
- **Fee structure variation.** Only a single flat `fund_redemption_fee_pct` setting. Per-fund or tiered fees are future work.
- **Notifications to clients on invest/redeem completion.** Kafka events are published, but explicit notification templates / email wiring is deferred to a notification-service spec.
- **Admin UI for overriding fund manager assignment.** Manager reassignment only happens via supervisor demotion (§4.7).
- **Rate limiting on invest/redeem.** Deferred to a cross-cutting rate-limit spec.

---

## 13. Open questions / risks

1. **Liquidation price source.** We use `listing.Low` (conservative bid-side) to match existing market-sell behavior. Risk: funds under-liquidate in volatile markets, leaving insufficient cash even after the sell. Alternative: mid-price or last-price with a safety multiplier. **Decision: use `listing.Low` with a 2% buffer on the liquidation plan (sell 2% more than the deficit requires) to absorb slippage.** Plan must implement the buffer.
2. **FIFO vs proportional liquidation.** FIFO has simpler auditability (Celina doc does not mandate a specific order). Risk: FIFO concentrates liquidation in oldest holdings, which might have the largest unrealized gains (tax event). Alternative: proportional (sell equal % from every holding). **Decision: FIFO.** Revisit if tax-inefficiency is raised.
3. **Full redemption allowed.** Default: yes, client can redeem 100%. Risk: client's position row is retained with `total_contributed_rsd=0`, which correctly yields `profit_rsd=0` derivations but clutters `/me/investment-funds` responses. **Decision: filter positions where `total_contributed_rsd == 0 AND last_redeem_fully_closed = true` from `GET /me/investment-funds` by default; admin queries can include them.**
4. **Bank-side position sentinel.** Default: use `(fund_id, user_id=1_000_000_000, system_type='employee')` leveraging the existing bank-owner sentinel from CLAUDE.md. Risk: sentinel collides with legitimate employee ID 1_000_000_000 (does not — sentinel is reserved). The alternative of storing the bank's contributions on `investment_funds.bank_contributed_rsd` was rejected because it duplicates schema and breaks the "one query for all positions" property needed by the derived aggregates.
5. **In-flight sagas during supervisor demotion.** Warn-and-continue is pragmatic but leaves saga telemetry showing an old `manager_employee_id`. Risk: audit logs may be confusing. **Mitigation:** the `stock.funds-reassigned` Kafka event carries a correlation ID auditors can use to reconcile.
6. **Kafka event ordering between `user.supervisor-demoted` and subsequent fund operations.** If a supervisor places an on-behalf-of-fund order at the same instant their demotion event propagates, stock-service might process the order before reassigning the fund, so the order authorizes against the old manager. This is fine (the order is authorized at the moment of its handler execution; eventual consistency on manager_id is acceptable). **No mitigation required**, but the implementation plan must call this out for reviewers.
7. **Actuary-performance numerator.** We sum `capital_gains` attributed to the actor employee. `capital_gains` today is populated only for sell-side realized gains. Fund invest/redeem flows do not currently hit `capital_gains`. Risk: the report undercounts if the intent was "all employee-driven P&L". **Decision: scope is realized capital gains from securities sales only.** Future spec may extend to fund-level P&L.
8. **FX rounding.** Cross-currency conversion introduces rounding. For invest, we round `amount_rsd` to the currency's minor-unit precision (2 dp for most; per exchange-service convention). For redeem, the client's `amount_rsd` input is rounded likewise; FX target credit is rounded to the target account's minor-unit. Risk: dust amounts may linger on the fund's RSD account (≤ 0.01 RSD per contribution). **Decision: dust is acceptable; periodically swept to bank RSD account by a future ops task (out of scope).**
9. **Holding current price with stale listings.** If a listing's `last_price` is stale (market closed), `current_value_rsd` may misrepresent. The existing stock-service listing cron refreshes prices; we inherit its staleness semantics. No new mitigation.

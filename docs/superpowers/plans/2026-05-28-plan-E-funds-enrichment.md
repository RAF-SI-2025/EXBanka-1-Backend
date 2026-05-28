# Plan E — Investment Funds Enrichment

> **For agentic workers:** Use superpowers:subagent-driven-development.

**Goal:** Complete the investment-funds feature so funds are first-class investable assets with full statistics, traded-on-behalf support, and dividend pass-through.

**Architecture:** Augment the existing fund + fund_holding + client_fund_position infrastructure. Most of the data already exists in the DB — surface it. Add a dividend-payment table + service so dividend events can be persisted and surfaced in P/L + tax.

**Files touched:** stock-service models/service/handler, contract/proto/stock.proto, api-gateway portfolio + fund handlers, docs.

---

## 🔒 INVARIANT — Fund RSD Account Outflow Restriction (added 2026-05-28)

Money in a fund's RSD account may ONLY leave via one of these three paths:

1. **Buy a security on behalf of the fund** — the existing `OnBehalfOfFundID` order path (E2). Settlement debits the fund's RSD account; the resulting holding lands in the fund's portfolio.
2. **Pay dividends pro-rata to fund investors** — when the fund holds dividend-paying securities, dividends flow into the fund's RSD account and then pass through to investors' RSD accounts proportionally (E4).
3. **Redeem an investor's position** — the existing `FundService.Redeem` flow where a client/bank investor withdraws their proportional share. This is the ONLY user-initiated outflow; it's already permitted by the existing spec.

**FORBIDDEN:** An employee CANNOT transfer money from a fund's RSD account to any arbitrary other account via the generic transfer/payment routes. There must be NO route that allows this, and the existing transfer routes (`/me/payments`, `/me/transfers`, employee transfer admin routes, etc.) must reject any source account that is a fund's RSD account.

### Enforcement task — E0 (must run BEFORE E1-E4)

**E0.1: Audit all routes that initiate a transfer / payment / withdrawal.**

For each account-source path:
- `POST /api/v3/me/payments` (own-account → recipient)
- `POST /api/v3/me/transfers` (own-account → own-account)
- Any employee-initiated transfer (search `transaction-service` for handlers that take a source account_id from request)

Find where the source account is loaded. Verify that loaded accounts have a fund-association check.

**E0.2: Add fund-account guard to the transfer/payment service path.**

In `account-service` (where the debit happens) or transaction-service (where the saga starts), add a check:
```go
// Fund RSD accounts are restricted to fund operations only. Reject any
// generic transfer/payment that tries to debit one.
if sourceAccount.OwnerType == "investment_fund" {
    return nil, status.Error(codes.PermissionDenied,
        "fund accounts cannot be used as a transfer source; use fund operations only")
}
```

If accounts don't currently carry an explicit owner_type linking them to a fund, look up the fund via `fund.RSDAccountID == sourceAccount.ID` and reject if matched.

**E0.3: Unit test the rejection.**

Test case: an employee tries `POST /me/payments` with `source_account_id` set to a fund's RSD account → expect 403 PermissionDenied.

**E0.4: Document the invariant in Specification.md §21.**

> **Fund RSD Account Outflow Restriction (Celina-4):** Money in an investment fund's RSD account may only leave via (a) a buy order placed on behalf of the fund, (b) a dividend payout to fund investors, or (c) a redemption by an investor of their position. Employees cannot transfer fund money to arbitrary accounts via the generic transfer/payment routes.

---

## Sub-plan E1: Enrich `GET /investment-funds/:id` response

**Current state:** Returns basic fund fields (name, manager, account, status).
**Target state:** Returns the full fund snapshot.

**New response fields (computed at read time):**

```json
{
  "id": 7,
  "name": "Alpha Growth",
  "description": "...",
  "manager_employee_id": 3,
  "minimum_contribution_rsd": "1000.00",
  "rsd_account_id": 12345,
  "status": "active",

  "investor_count": 42,
  "total_contributed_rsd": "5000000.00",
  "liquid_rsd_balance": "1500000.00",
  "total_holdings_value_rsd": "3500000.00",
  "total_value_rsd": "5000000.00",
  "total_dividends_paid_rsd": "25000.00",
  "profit_rsd": "0.00",
  "profit_pct": "0.00",

  "holdings": [
    {
      "security_type": "stock",
      "ticker": "AAPL",
      "quantity": 100,
      "average_price_rsd": "20000.00",
      "current_price_rsd": "22000.00",
      "current_value_rsd": "2200000.00"
    }
  ]
}
```

### Tasks

**E1.1:** Add a `Statistics(ctx, fundID)` method to `FundService` that returns:
- `investor_count` = `COUNT(*) FROM client_fund_positions WHERE fund_id = ? AND total_contributed_rsd > 0`
- `total_contributed_rsd` = `SUM(total_contributed_rsd) FROM client_fund_positions WHERE fund_id = ?`
- `liquid_rsd_balance` = account-service `GetAccount(fund.RSDAccountID).Balance`
- `total_holdings_value_rsd` = same as `UnifiedPortfolioService.computeFundValue` minus the liquid (just the Σ side)
- `total_value_rsd` = liquid + holdings
- `total_dividends_paid_rsd` = `SUM(amount_rsd) FROM fund_dividend_payments WHERE fund_id = ?` (new table — see E4)
- `profit_rsd` = total_value_rsd − total_contributed_rsd
- `profit_pct` = profit/contributed × 100

**E1.2:** Add `FundHoldingsSnapshot` returning the fund's portfolio for the response. Reuse `UnifiedPortfolioService.composeHoldingPosition` (refactor or duplicate; lean toward extract-to-shared).

**E1.3:** Modify `GetFund(ctx, fundID)` gRPC to return the enriched shape; gateway `h.Fund.GetFund` passes through.

**E1.4:** Tests:
- Unit: zero investors → counts/totals all zero; happy path with 3 investors and 2 holdings.
- Integration: GET /investment-funds/:id after seeded fund returns correct stats.

---

## Sub-plan E2: Buy on behalf of fund

**Current state:** `OnBehalfOfFundID` is already plumbed through `stock-service/internal/service/order_service.go` and the gateway's `stock_order_handler.go`. Holdings land in `fund_holdings` instead of user holdings. Manager-only validation is enforced in the service.

**What's missing:** Verify the end-to-end flow + add tests + document it. Add a similar `on_behalf_of_fund_id` to the OTC-options buy/accept flow (which currently has no on-behalf-of-fund support — see `otc_accept_saga.go`).

### Tasks

**E2.1:** Read `stock-service/internal/service/order_service.go` lines 218–260 (the OnBehalfOfFundID branch) and confirm:
- Funds source = fund's RSD account (not employee's bank-on-behalf account).
- Resulting holding row has `owner_type='investment_fund'`, `owner_id=fund.id`.
- Manager-only check is enforced.
- The fund holding lands in the `fund_holdings` table or the unified `holdings` table — confirm which and document.

**E2.2:** If the holding lands in `holdings` (not `fund_holdings`), confirm `owner_type` CHECK constraint includes `investment_fund`. If only `client`/`bank`, schema migration needed:

```sql
ALTER TABLE holdings DROP CONSTRAINT IF EXISTS holdings_owner_type_check;
ALTER TABLE holdings ADD CONSTRAINT holdings_owner_type_check
  CHECK (owner_type IN ('client', 'bank', 'investment_fund'));
```

(Apply the same to `client_fund_positions` if relevant — though it's the inverse: positions IN funds, not OF funds.)

**E2.3:** Add `on_behalf_of_fund_id` to OTC option accept/exercise flows:
- `OTCAcceptInput`: add `OnBehalfOfFundID *uint64`.
- When set, premium-debit comes from fund's RSD account; minted contract is owned by fund.
- Same for exercise: strike-debit from fund; shares land in fund.
- Manager-only check.
- Gateway exposes via new request field.

**E2.4:** Tests:
- Unit: place market buy with `on_behalf_of_fund_id=X`; verify fund's RSD account is debited, fund's portfolio gains the holding, user's holdings unchanged.
- Unit: non-manager calls → PermissionDenied.
- Integration: end-to-end fund-buys-AAPL flow.

---

## Sub-plan E3: User portfolio fund-position detail

**Current state (after Plan B):** `UnifiedPortfolio.Funds.Positions[*]` already shows `fund_id`, `fund_name`, `amount_invested_rsd`, `current_value_rsd`, `pct_of_fund`, `p_l_rsd`, `p_l_pct`.

**What's missing per user request:** "user needs to see how much money he invested in fonds and what fonds." This is already covered. **Verify** and add any missing fields.

### Tasks

**E3.1:** Add to `PortfolioPosition` proto:
- `dividends_received_rsd` (decimal as string) — caller's pro-rata share of all dividends paid to the fund since the position opened, accrued post-tax.
- `fund_status` (`active`|`fundraising`|`matured`|`liquidated` — passthrough).

**E3.2:** Compute `dividends_received_rsd` in `UnifiedPortfolioService.composeFundPosition` from the new `fund_dividend_payments` table (E4) × the caller's pct_of_fund **at the time of each dividend payment**. (Optimization: cache pre-computed per-investor share at payment time; see E4.)

**E3.3:** Tests update.

---

## Sub-plan E4: Dividends → fund → user P/L + tax

**Current state:** No dividend infrastructure. `Stock.DividendYield` field exists but no payment cron, no payouts.

**Target state:**
- Dividend payments persist as discrete events.
- Each event credits the holder's RSD account (or the fund's RSD account if the holding is fund-owned).
- For fund-owned dividends: the dividend is recorded on the fund and ALSO accrued pro-rata to each investor's `client_fund_position` for tax purposes.
- Capital-gains tax (15% per `tax_service.go`) is computed on dividends received by clients (both direct holdings and fund pass-through) at the next tax cycle.

### Schema (new tables)

```sql
CREATE TABLE dividend_payments (
  id BIGSERIAL PRIMARY KEY,
  security_id BIGINT NOT NULL,
  ticker VARCHAR(30) NOT NULL,
  amount_per_share_rsd NUMERIC(20,4) NOT NULL,
  payment_date DATE NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (security_id, payment_date)
);

CREATE TABLE dividend_payouts (
  id BIGSERIAL PRIMARY KEY,
  dividend_payment_id BIGINT NOT NULL,
  holding_owner_type VARCHAR(20) NOT NULL,  -- client | bank | investment_fund
  holding_owner_id BIGINT,                  -- nullable for bank
  holding_id BIGINT NOT NULL,
  shares INTEGER NOT NULL,
  gross_amount_rsd NUMERIC(20,4) NOT NULL,
  tax_amount_rsd NUMERIC(20,4) NOT NULL DEFAULT 0,
  net_amount_rsd NUMERIC(20,4) NOT NULL,
  credited_account_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  idempotency_key VARCHAR(128) UNIQUE NOT NULL
);

CREATE TABLE fund_dividend_payments (
  id BIGSERIAL PRIMARY KEY,
  dividend_payment_id BIGINT NOT NULL,
  fund_id BIGINT NOT NULL,
  amount_rsd NUMERIC(20,4) NOT NULL,
  per_investor_snapshot JSONB NOT NULL,  -- snapshot of pct_of_fund × amount per investor at payout time
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (dividend_payment_id, fund_id)
);
```

### Tasks

**E4.1:** Add models + AutoMigrate.

**E4.2:** `DividendService` with two flows:
- `RecordDividend(security_id, amount_per_share, payment_date)` — declare a dividend (manual employee action or cron from external data).
- `Payout(dividend_payment_id)` — fan out to every holding of the security, debit a bank-sentinel RSD account (or external counterparty per spec), credit each holder's RSD account. For fund-owned holdings, credit the fund's RSD account and ALSO write a `fund_dividend_payments` snapshot row.
- Idempotency key: `dividend-<payment_id>-<holding_id>`.

**E4.3:** Tax treatment:
- Update `tax_service.go` to include `gross_amount_rsd` from `dividend_payouts` where `holding_owner_type='client'` AND `created_at` within the tax period.
- Pro-rata fund dividends are similarly taxed when the investor's `current_value_rsd` increase is realized (redemption); for the simpler MVP, tax fund-dividend pass-through at redemption time, not at payout time. Document this in §21.

**E4.4:** New gateway routes:
- `POST /api/v3/admin/dividends` (declare) — perm `securities.manage.catalog`.
- `POST /api/v3/admin/dividends/:id/payout` (trigger payout) — same perm.
- `GET /api/v3/me/dividends` — caller's dividend history (read).
- `GET /api/v3/investment-funds/:id/dividends` — fund's dividend history.

**E4.5:** Surface in user portfolio response: `securities.positions[*].dividends_received_rsd` (sum of holder's `dividend_payouts.net_amount_rsd` for that holding). Already added the field in E3.

**E4.6:** Tests + integration test for a full dividend cycle (declare → payout → check balances + tax line).

---

## Plan E commit budget

Roughly 12–15 commits across the four sub-plans. Each sub-plan should be its own contiguous block of commits so an executor can pause at the boundary.

## Out of scope

- Automated dividend scraping from external market data — manual declare for now.
- Fund-of-funds (a fund investing in another fund) — restrict to direct security holdings.
- Special-distribution dividends (e.g., stock splits as dividends).

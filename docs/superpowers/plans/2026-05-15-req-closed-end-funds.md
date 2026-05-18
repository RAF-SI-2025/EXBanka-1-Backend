# Plan — Closed-End Funds (Celina 4)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` + `account-service` (RSD account interactions) + `notification-service`
**Scope:** Extend `investment_funds` with a fund-type and lifecycle so closed-end funds can be created with a fundraising window, an active operational phase, a maturity date that flips to a 7-day liquidation grace window, and a final liquidated phase that distributes net assets pro-rata.

This must land BEFORE the recurring-fund-investments plan, because that plan checks `fund_status == active` before scheduling contributions.

---

## Schema additions

`stock-service/internal/model/investment_fund.go` — add fields:

```go
type FundType string   // "open" | "closed"
type FundStatus string // "open" | "fundraising" | "active" | "matured" | "liquidated"

// Add to InvestmentFund:
FundType         FundType   `gorm:"type:varchar(16);not null;default:'open';index" json:"fund_type"`
FundraisingStart *time.Time `json:"fundraising_start,omitempty"`
FundraisingEnd   *time.Time `json:"fundraising_end,omitempty"`
MaturityDate     *time.Time `json:"maturity_date,omitempty"`
TargetAmountRSD  decimal.Decimal `gorm:"type:numeric(20,4)" json:"target_amount_rsd"`
FundStatus       FundStatus `gorm:"type:varchar(16);not null;default:'open';index" json:"fund_status"`
MaturityGraceEnd *time.Time `json:"maturity_grace_end,omitempty"` // computed = maturity_date + 7d
```

Validation invariants (enforced at create/update):

- If `fund_type==closed`: `fundraising_start < fundraising_end < maturity_date` all required; `target_amount_rsd > 0`.
- If `fund_type==open`: all closed-only fields must be null; `fund_status` is always `open`.

`BeforeUpdate` hook on `InvestmentFund` already enforces version match. No additional changes to that.

## Phase transitions (CLOSED only)

| From | To | Trigger |
|---|---|---|
| `fundraising` | `active` | `now > fundraising_end` |
| `active` | `matured` | `now > maturity_date` |
| `matured` | `liquidated` | All `fund_holdings` for this fund sold (qty=0) AND `now > maturity_grace_end` (or earlier if supervisor finishes manually) |

When entering `matured`, the system records `maturity_grace_end = maturity_date + 7 days` (or use a config-driven `CLOSED_FUND_GRACE_DAYS`).

At `maturity_grace_end`, if positions remain, the cron auto-creates `Sell Market` orders for every remaining holding via the existing `OrderService.PlaceOrder` path (bank-owned orders on behalf of the fund manager). Once all are filled, the fund auto-flips to `liquidated`.

On `liquidated`:

1. Read the fund's RSD account balance.
2. Compute each investor's share = (their `client_fund_position.shares` / total fund shares) × fund net assets.
3. Credit each investor's primary RSD account (via `account-service.Credit` saga step). Use a `FundLiquidation` saga record per investor for retry/compensation safety.
4. Mark each `client_fund_position` as `redeemed`.
5. Publish `FUND_LIQUIDATED` (per-investor `FUND_INVESTOR_PAYOUT`).

## Cron / scheduler

`stock-service/internal/service/fund_lifecycle_cron.go`:

- `FundLifecycleCron{fundSvc, interval}`. Default interval `15m` (configurable).
- Each tick:
  1. List closed funds in `fundraising` → if `now > fundraising_end`, transition to `active`, emit `FUND_FUNDRAISING_CLOSED`.
  2. List closed funds in `active` → if `now > maturity_date`, transition to `matured`, set `maturity_grace_end`, emit `FUND_MATURED`.
  3. List closed funds in `matured` → if `now > maturity_grace_end` AND positions remain → auto-create Sell Markets. If positions == 0 → flip to `liquidated`, run distribution.

All transitions are inside a `db.Transaction` per fund row (`SELECT FOR UPDATE`) so concurrent ticks don't double-fire.

For symmetry, when a closed fund is created with `fundraising_start <= now`, the create handler sets `fund_status=fundraising` immediately and publishes `FUND_FUNDRAISING_STARTED`.

## Invariants enforced in fund_invest_saga

`stock-service/internal/service/fund_invest_saga.go` — `Invest`:

- If `fund.FundType == closed`:
  - Reject unless `fund.FundStatus == "fundraising"`.
  - Reject if `now > fundraising_end`.
  - If `target_amount_rsd > 0`, reject if `current_total + amount > target_amount_rsd`.
- Redeem is rejected when `closed && fund_status != "open"` (closed funds never allow mid-life redemption).

## Notifications (5 new keys)

- `FUND_FUNDRAISING_STARTED` — fan-out to interested clients? MVP: bank/supervisor + an opt-in mechanism is overkill; emit only to fund manager and emit a notification with general `Data` for clients with positions or following the fund. MVP simplification: only emit to the fund manager. Discovery page surfacing is via the existing fund-list read.
- `FUND_FUNDRAISING_CLOSED` — emit to fund manager + every client who has an `active` `client_fund_position` in the fund.
- `FUND_MATURED` — same set as `FUND_FUNDRAISING_CLOSED`.
- `FUND_LIQUIDATED` — same set.
- `FUND_INVESTOR_PAYOUT` — one per investor; Data: `{fund_id, payout_amount_rsd, share_pct, fund_name}`.

`FUND_INVESTOR_PAYOUT` is also surfaced via email per the requirement (email body summarises total return and amount paid). Add to email template too.

## gRPC + Gateway

Proto: add fields to `InvestmentFund` message; add `FundLifecycleEvent` for future read API if needed.

New / updated routes (all under `/api/v3`):

| Method | Path | Auth | Permission | Notes |
|---|---|---|---|---|
| POST | `/api/v3/funds` | AuthMiddleware | `fund.manage.all` | extended body accepts new fields |
| GET | `/api/v3/funds/:id` | AnyAuth | existing | exposes new fields |
| GET | `/api/v3/funds?type=closed&status=fundraising` | AnyAuth | existing | filter by new fields |
| POST | `/api/v3/funds/:id/force-liquidate` | AuthMiddleware | `fund.manage.closed` | supervisor override |

Input validation:

- `fund_type` ∈ `{open, closed}` (`oneOf`).
- `fund_status` ∈ valid set (`oneOf`).
- When `fund_type==closed`: required date fields present, ordered, `target_amount_rsd` positive.
- Update of closed-only fields is allowed only when `fund_status == fundraising`.

## Permissions

- `fund.manage.closed` — `EmployeeSupervisor`, `EmployeeAdmin`.
- Existing `fund.manage.all` remains for OPEN funds and general edits.

## Tests

Unit:

- Fund model validation across both types.
- `fund_invest_saga` rejects invest on closed funds outside fundraising window.
- Cron transitions across all four state edges; idempotent under retry.
- Auto-liquidation: positions remaining → Sell Markets fired; positions zero → flip to liquidated + payouts computed correctly with rounding-down policy and bank-account residual.
- Pro-rata distribution: shares summing across investors equals fund net assets within rounding.

Integration (`test-app/workflows/closed_end_funds_test.go`):

- Supervisor creates a closed fund, fundraising_end = now+1s, maturity_date = now+5s, grace=2s in test config.
- Client invests during fundraising. After fundraising_end tick, status=active and client cannot redeem.
- After maturity tick, status=matured. Auto-liquidation flushes positions, distribution credits client's RSD account, inbox contains `FUND_INVESTOR_PAYOUT`, email is queued.

## Docs

- §17: 1 new route + 1 extended route.
- §18: `investment_funds` extended schema.
- §6: 1 new permission.
- §19+§20: 5 new notification keys (4 push + 1 push+email).
- §21: closed-fund lifecycle invariants + 7-day liquidation rule.
- `docs/api/REST_API_v1.md`: extended Funds section.

## Verification

- `make build`, stock-service tests, `make lint`, integration suite, manual smoke through swagger.

## Commits

1. `feat(stock-service): investment_funds — fund_type/status + closed-end fields`
2. `feat(stock-service): closed-fund invariants in invest/redeem saga`
3. `feat(stock-service): fund_lifecycle_cron with phase transitions + auto-liquidation`
4. `feat(stock-service): pro-rata distribution + payout saga`
5. `feat(api-gateway): /api/v3/funds extended + /:id/force-liquidate`
6. `feat(notification-service): FUND_* templates (push + email for payout)`
7. `feat(user-service): seed fund.manage.closed`
8. `test: closed-end funds workflow integration`
9. `docs: closed-end funds spec + REST_API_v1`

All on `Development`.

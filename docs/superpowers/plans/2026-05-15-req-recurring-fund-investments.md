# Plan — Recurring Fund Investments (Celina 4)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Prerequisite:** Closed-End Funds plan (`2026-05-15-req-closed-end-funds.md`) — this plan reads `fund_status` to gate scheduling.
**Service:** `stock-service`
**Scope:** Client schedules a monthly fixed RSD contribution into a chosen fund (Dollar-Cost Averaging). A scheduler triggers `FundService.Invest` on the configured day. Insufficient-funds skips the run and notifies the client without cancelling the recurrence.

---

## Schema

`stock-service/internal/model/recurring_fund_investment.go`:

```go
type RecurringFundInvestment struct {
    ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
    ClientID    uint64    `gorm:"not null;index:idx_rfi_client" json:"client_id"`
    FundID      uint64    `gorm:"not null;index" json:"fund_id"`
    AmountRSD   decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"amount_rsd"`
    SourceAccountID uint64 `gorm:"not null" json:"source_account_id"`
    DayOfMonth  int       `gorm:"not null" json:"day_of_month"` // 1..28
    Active      bool      `gorm:"not null;default:true;index" json:"active"`
    LastRun     *time.Time `json:"last_run,omitempty"`
    NextRun     time.Time `gorm:"not null;index" json:"next_run"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    Version     int64     `gorm:"not null;default:0" json:"-"`
}
```

`BeforeUpdate` hook enforces version match.

## Repository

- CRUD + `ListDue(now)`.

## Service

`stock-service/internal/service/recurring_fund_service.go`:

- `Create` validates: fund exists, fund_status ∈ `{open, fundraising}` at creation time (otherwise the recurrence can never fire), source account belongs to client, amount > fund's `minimum_contribution_rsd`.
- `Pause` / `Resume` / `Cancel`.
- `RunDue(ctx)`:
  1. SELECT FOR UPDATE one row.
  2. If the fund is no longer eligible (`fund_status not in {open, fundraising}` for closed funds; closed funds outside fundraising window also reject) → emit `FUND_RECURRING_SKIPPED` with reason `fund_not_open`. Advance NextRun.
  3. Call `FundService.Invest` with the same parameters. On success, emit `FUND_RECURRING_EXECUTED`. On `ErrInsufficientFunds`, emit `FUND_RECURRING_SKIPPED` with reason `insufficient_funds`.
  4. Update `last_run`, `next_run` = same day next month (clamped to 28).
  5. Save with version check.

## Cron

`stock-service/internal/service/recurring_fund_cron.go`:

- `RecurringFundCron{recurringSvc, interval}`. Default `1h`.
- Mirrors the recurring-order cron structure; honours `ctx.Done()`.

## Notifications (2 new keys)

- `FUND_RECURRING_EXECUTED` — Data: `{fund_name, amount_rsd, contribution_id}`. RefType `fund`, RefID `fund_id`.
- `FUND_RECURRING_SKIPPED` — Data: `{fund_name, amount_rsd, reason}`.

Push channel. No email by default.

## gRPC + Gateway

Proto `recurring_fund.proto` with `Create / Get / Pause / Resume / Cancel / ListMy`.

Routes:

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/recurring-funds` | AnyAuth | `recurring_fund.read.my` |
| POST | `/api/v3/me/recurring-funds` | AnyAuth | `recurring_fund.write.my` |
| GET | `/api/v3/me/recurring-funds/:id` | AnyAuth | `recurring_fund.read.my` |
| POST | `/api/v3/me/recurring-funds/:id/pause` | AnyAuth | `recurring_fund.write.my` |
| POST | `/api/v3/me/recurring-funds/:id/resume` | AnyAuth | `recurring_fund.write.my` |
| DELETE | `/api/v3/me/recurring-funds/:id` | AnyAuth | `recurring_fund.write.my` |

Input validation:

- `day_of_month` ∈ [1,28].
- `amount_rsd` > 0.
- `source_account_id` exists + belongs to caller; account currency is RSD.
- `fund_id` exists.

Ownership: `RecurringFundInvestment.ClientID` must equal caller's `principal_id`.

## Permissions

`recurring_fund.read.my`, `recurring_fund.write.my` — `client`, `EmployeeBasic+`.

## Tests

Unit:

- Repo CRUD + ListDue.
- Service: creation rejects on bad fund_status, bad account, sub-minimum amount; RunDue happy path; insufficient-funds skip path; fund-no-longer-open skip path; next_run rollover end-of-month edge.
- Cron: ticker + ctx cancel.

Integration (`test-app/workflows/recurring_fund_test.go`):

- Client schedules a monthly 1000 RSD recurring into an open fund. Tick fires; `fund_contributions` row exists; inbox has `FUND_RECURRING_EXECUTED`. Drain account; next tick fires; skip notification appears; recurrence still active.

## Docs

- §17: 6 new routes.
- §18: `recurring_fund_investments` model.
- §6: 2 new permissions.
- §19+§20: 2 new templates.
- §21: skip-on-insufficient + fund-status gate rules.
- `docs/api/REST_API_v1.md`: Recurring Fund Investments section.

## Verification

- `make build`, stock-service tests, `make lint`, integration suite.

## Commits

1. `feat(stock-service): recurring_fund_investments model + repo`
2. `feat(stock-service): recurring-fund service + cron`
3. `feat(stock-service): recurring-fund gRPC + proto`
4. `feat(api-gateway): /api/v3/me/recurring-funds routes`
5. `feat(notification-service): FUND_RECURRING_* templates`
6. `feat(user-service): seed recurring_fund.* permissions`
7. `test: recurring fund integration`
8. `docs: recurring fund spec + REST_API_v1`

All on `Development`.

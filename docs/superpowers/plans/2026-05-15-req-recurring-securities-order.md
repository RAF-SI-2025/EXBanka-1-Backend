# Plan — Recurring Securities Orders (Celina 3)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` (+ gateway)
**Scope:** User configures an auto-recurring Market order on a single listing. A scheduler materializes a real `orders` row on every due tick. If funds are insufficient, the execution is skipped, logged, and the user is notified; the recurring template stays active for the next tick. User can pause/resume/cancel anytime.

---

## Schema

`stock-service/internal/model/recurring_order.go`:

```go
type RecurrenceInterval string // "weekly" | "monthly"

type RecurringOrder struct {
    ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
    OwnerType    OwnerType `gorm:"type:varchar(16);not null;index:idx_recur_owner,priority:1" json:"owner_type"`
    OwnerID      uint64    `gorm:"not null;index:idx_recur_owner,priority:2" json:"owner_id"`
    ListingID    uint64    `gorm:"not null;index" json:"listing_id"`
    Side         OrderSide `gorm:"type:varchar(8);not null" json:"side"` // buy | sell
    Quantity     int64     `gorm:"not null" json:"quantity"`
    AccountID    uint64    `gorm:"not null" json:"account_id"`
    Interval     RecurrenceInterval `gorm:"type:varchar(16);not null" json:"interval"`
    DayOfMonth   *int      `json:"day_of_month,omitempty"` // monthly only, 1..28
    DayOfWeek    *int      `json:"day_of_week,omitempty"`  // weekly only, 0..6 (sun..sat)
    StartDate    time.Time `gorm:"not null" json:"start_date"`
    EndDate      *time.Time `json:"end_date,omitempty"`
    Status       string    `gorm:"type:varchar(16);not null;default:'active';index" json:"status"` // active | paused | cancelled | finished
    LastRun      *time.Time `json:"last_run,omitempty"`
    NextRun      time.Time `gorm:"not null;index" json:"next_run"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
    Version      int64     `gorm:"not null;default:0" json:"-"`
}
```

`BeforeUpdate` enforces version match.

## Repository

- CRUD plus `ListDue(now)` (status=active AND next_run<=now AND (end_date IS NULL OR end_date>now)).

## Service

`stock-service/internal/service/recurring_order_service.go`:

- `Create` validates account ownership (calls `account-service.GetAccount` to check `owner_id` matches), listing exists, interval/day fields consistent. Computes `next_run` from `start_date`.
- `Pause`, `Resume`, `Cancel` — version-checked Save.
- `RunDue(ctx)` — iterates `ListDue`, for each:
  1. Acquire row with `SELECT FOR UPDATE`.
  2. Call existing `OrderService.PlaceOrder` (Market). The order service already does pre-checks and reservation.
  3. If the place call fails with `ErrInsufficientFunds` → publish `RECURRING_ORDER_SKIPPED` notification, advance `next_run` per interval, save.
  4. If it succeeds → publish `RECURRING_ORDER_EXECUTED`, `last_run = now`, advance `next_run`, save.
  5. Any other error → log, do NOT advance, retry next tick.

## Scheduler

`stock-service/internal/service/recurring_order_cron.go`:

- `RecurringOrderCron{recurringSvc, interval time.Duration}` with `Run(ctx)` ticker loop.
- Default `interval = 1h` (configurable via env `RECURRING_ORDER_TICK_INTERVAL`). Cron honours `ctx.Done()` and `defer ticker.Stop()`.
- Started in `cmd/main.go` via `go cron.Run(ctx)`.

## Notifications

Three new template keys (push only, per gap analysis):

- `RECURRING_ORDER_EXECUTED` — Data: `listing_symbol, quantity, side, order_id`. RefType `order`, RefID `order_id`.
- `RECURRING_ORDER_SKIPPED` — Data: `listing_symbol, quantity, reason`.
- `RECURRING_ORDER_PAUSED` — Data: `listing_symbol, reason`. Fired manually if cron auto-pauses (e.g. account closed). Not in MVP scope; defer unless trivial.

Wired via the existing `kafka.Producer.PublishGeneralNotification` path; emitted from the service layer after the relevant action commits.

## gRPC + Gateway

Proto `recurring_order.proto` with `Create / Get / Update / Pause / Resume / Cancel / ListMy`.

Gateway routes on `/api/v3`:

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/recurring-orders` | AnyAuth | `recurring_order.read.my` |
| POST | `/api/v3/me/recurring-orders` | AnyAuth | `recurring_order.write.my` |
| GET | `/api/v3/me/recurring-orders/:id` | AnyAuth | `recurring_order.read.my` |
| POST | `/api/v3/me/recurring-orders/:id/pause` | AnyAuth | `recurring_order.write.my` |
| POST | `/api/v3/me/recurring-orders/:id/resume` | AnyAuth | `recurring_order.write.my` |
| POST | `/api/v3/me/recurring-orders/:id/cancel` | AnyAuth | `recurring_order.write.my` |

Input validation:

- `interval` ∈ `{weekly, monthly}` (`oneOf`).
- `day_of_week` ∈ [0,6] when interval==weekly; required.
- `day_of_month` ∈ [1,28] when interval==monthly; required (28 to avoid Feb edge cases).
- `quantity` > 0; `side` ∈ `{buy, sell}`.
- Ownership: account belongs to caller; listing exists.

## Permissions

`recurring_order.read.my`, `recurring_order.write.my` — `client`, `EmployeeAgent+`.

## Tests

Unit:

- `recurring_order_service_test.go` — `Create` validates account ownership; `RunDue` happy path; insufficient-funds skip; cancelled/paused rows ignored; end-date expiry transitions to `finished`.
- Cron test — ticker fires, calls RunDue, honours ctx cancel.
- Next-run computation tests covering month-rollover and weekly day-of-week math.

Integration (`test-app/workflows/recurring_orders_test.go`):

- Client creates a weekly recurring order. Force tick via small interval in test config. Verify a real `orders` row was created. Pause → verify next tick doesn't fire. Resume + insufficient funds (drain account) → verify `RECURRING_ORDER_SKIPPED` in inbox, template stays active.

## Docs

- §17: 6 new routes.
- §18: `recurring_orders` model.
- §6: 2 new permissions.
- §19: 3 new general-notification keys.
- §20 (template catalogue): 3 entries.
- §21: business rules — insufficient funds skip, day-28 clamp.
- `docs/api/REST_API_v1.md`: Recurring Orders section.

## Verification

- `make build`, `cd stock-service && go test ./...`, `make lint`, integration suite.

## Commits

1. `feat(stock-service): recurring_orders model + repo + service`
2. `feat(stock-service): recurring-order cron + insufficient-funds skip`
3. `feat(stock-service): recurring-order gRPC + proto`
4. `feat(api-gateway): /api/v3/me/recurring-orders routes`
5. `feat(notification-service): RECURRING_ORDER_* templates`
6. `feat(user-service): seed recurring_order.* permissions`
7. `test: recurring-order workflow integration`
8. `docs: recurring orders spec + REST_API_v1`

All on `Development`.

# Plan — Price Alerts (Celina 3)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` (alert eval + storage), `notification-service` (template), `api-gateway` (routes)
**Scope:** A user creates a `PriceAlert` on any listing with an absolute threshold (≥/≤) OR a percentage daily-change rule. On every price refresh tick, active alerts are evaluated; matches fire an in-app notification (and email if user opted in), then transition to `triggered` (single-shot) OR remain active (recurring, with a cooldown).

---

## Schema

`stock-service/internal/model/price_alert.go`:

```go
type PriceAlertCondition string // "gte" | "lte" | "daily_change_pct_gte" | "daily_change_pct_lte"

type PriceAlert struct {
    ID            uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
    OwnerType     OwnerType        `gorm:"type:varchar(16);not null;index:idx_alert_owner,priority:1" json:"owner_type"`
    OwnerID       uint64           `gorm:"not null;index:idx_alert_owner,priority:2" json:"owner_id"`
    ListingID     uint64           `gorm:"not null;index" json:"listing_id"`
    Condition     PriceAlertCondition `gorm:"type:varchar(32);not null" json:"condition"`
    Threshold     decimal.Decimal  `gorm:"type:numeric(20,4);not null" json:"threshold"`
    IsRecurring   bool             `gorm:"not null;default:false" json:"is_recurring"`
    Cooldown      int              `gorm:"not null;default:3600" json:"cooldown_seconds"` // recurring only
    EmailToo      bool             `gorm:"not null;default:false" json:"email_too"`
    Active        bool             `gorm:"not null;default:true;index" json:"active"`
    LastTriggered *time.Time       `json:"last_triggered,omitempty"`
    CreatedAt     time.Time        `json:"created_at"`
    UpdatedAt     time.Time        `json:"updated_at"`
    Version       int64            `gorm:"not null;default:0" json:"-"`
}
```

`Version` + `BeforeUpdate` hook per the optimistic-locking CLAUDE.md rule (alert evaluation reads + flips `Active=false`).

## Repository

`stock-service/internal/repository/price_alert_repository.go`:

- `Create`, `GetByID`, `Update` (Save), `Delete`
- `ListByOwner(ownerType, ownerID)` and `ListActiveByListing(listingID)` for the evaluator loop.

## Service

`stock-service/internal/service/price_alert_service.go`:

CRUD methods + evaluator entry point:

```go
func (s *PriceAlertService) EvaluateForListing(ctx context.Context, listingID uint64, latestPrice, prevClose decimal.Decimal) error {
    // 1. ListActiveByListing
    // 2. For each: check condition; if matched and (not last_triggered OR last_triggered+cooldown < now)
    //    a. Publish PriceAlertTriggered Kafka event (general notification, Data: listing_symbol, price, condition, threshold)
    //    b. If !IsRecurring → Active=false. Else last_triggered=now.
    //    c. db.Save with version check (FOR UPDATE inside transaction).
    // 3. Each match fires one notification per alert.
}
```

The evaluator is invoked from the price-refresh path in `stock-service/internal/source/` after each successful `UpsertDailyPriceInfo`. Add a thin `priceAlertHook PriceAlertHook` interface so the source layer can fan out without depending on the price-alert service directly (avoid circular imports).

Single-shot triggers set `active=false`; recurring alerts honour `cooldown_seconds`.

## Notification

New template: `PRICE_ALERT_TRIGGERED` in `notification-service/internal/template/registry.go`:

- Push: `"{symbol} hit your alert: current price {price}, condition {condition} {threshold}"`
- Email: optional, gated on `email_too` flag in the original alert (passed through `Data["email_too"] == "true"`).

Data payload: `{symbol, listing_id, price, condition, threshold}`. `RefType: "listing"`, `RefID: listing_id`.

The price-alert evaluator publishes via the standard `kafka.Producer.PublishGeneralNotification` path. notification-service consumes from `notification.general` as today.

## gRPC + Gateway

Proto `price_alert.proto` with `Create / Get / Update / Delete / ListMy`.

Gateway routes on `/api/v3`:

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/price-alerts` | AnyAuth | `price_alert.read.my` |
| POST | `/api/v3/me/price-alerts` | AnyAuth | `price_alert.write.my` |
| GET | `/api/v3/me/price-alerts/:id` | AnyAuth | `price_alert.read.my` |
| PUT | `/api/v3/me/price-alerts/:id` | AnyAuth | `price_alert.write.my` |
| DELETE | `/api/v3/me/price-alerts/:id` | AnyAuth | `price_alert.write.my` |

Ownership check via `enforceOwnership` against the alert's `owner_type`/`owner_id` on every per-id route.

Input validation:

- `condition` ∈ `{gte, lte, daily_change_pct_gte, daily_change_pct_lte}` via `oneOf()`.
- `threshold` > 0 via `positive()`.
- `cooldown_seconds` ∈ [60, 86400] via `inRange()`.
- `listing_id` existence-checked server-side.

## Permissions

`price_alert.read.my`, `price_alert.write.my` — `client`, `EmployeeBasic+`.

## Scheduler

No new top-level loop — alerts are evaluated reactively from the existing price-refresh ticker in `stock-service/internal/source/`. Wire as:

```go
// in cmd/main.go
source.SetPriceUpdateHook(func(listingID uint64, latest, prevClose decimal.Decimal) {
    if err := priceAlertSvc.EvaluateForListing(ctx, listingID, latest, prevClose); err != nil {
        log.Printf("WARN: price alert eval failed: %v", err)
    }
})
```

Hook is best-effort (do not block price refresh on alert errors).

## Tests

Unit:

- `price_alert_repository_test.go` — round-trip + optimistic lock.
- `price_alert_service_test.go` — condition matrix (4 conditions × match/no-match × recurring/one-shot × cooldown active/expired); verify Kafka emit + state transitions.
- Gateway handler test — input validation rejects bad condition / negative threshold.

Integration (`test-app/workflows/price_alerts_test.go`):

- Client creates a `gte` alert at $200. Force a price update via a test seam (or sleep+drift). Assert in-app inbox contains `PRICE_ALERT_TRIGGERED`. Assert alert flipped to `active=false` for single-shot.

## Docs

- §17: 5 new routes.
- §18: `price_alerts` model.
- §6: 2 new permissions.
- §19: `notification.general` Data form for `PRICE_ALERT_TRIGGERED`.
- §20 (template catalogue): `PRICE_ALERT_TRIGGERED` push (+ optional email).
- `docs/api/REST_API_v1.md`: Price Alerts section.

## Verification

- `make build` succeeds.
- `cd stock-service && go test ./... -count=1` passes.
- `make lint` clean on stock-service, notification-service template registry, api-gateway.
- Integration test passes.

## Commits

1. `feat(stock-service): price_alerts model + repo + service`
2. `feat(stock-service): wire price-update hook for alert evaluation`
3. `feat(stock-service): price-alert gRPC + proto`
4. `feat(api-gateway): /api/v3/me/price-alerts routes`
5. `feat(notification-service): PRICE_ALERT_TRIGGERED template`
6. `feat(user-service): seed price_alert.* permissions`
7. `test: price alerts integration`
8. `docs: price alerts spec + REST_API_v1`

All on `Development`.

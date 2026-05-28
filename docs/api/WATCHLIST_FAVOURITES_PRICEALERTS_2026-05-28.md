# Watchlist + Favourites + Price-Alerts Verification
_Date: 2026-05-28 | Branch: Development_

---

## §1 Watchlist

### Route table

| Method | Path | Auth | Middleware |
|--------|------|------|-----------|
| `GET`    | `/api/v3/me/watchlist`             | AnyAuthMiddleware | `bankIfEmp` (ResolveIdentity OwnerIsBankIfEmployee) |
| `POST`   | `/api/v3/me/watchlist`             | AnyAuthMiddleware | `bankIfEmp` |
| `DELETE` | `/api/v3/me/watchlist/:listing_id` | AnyAuthMiddleware | `bankIfEmp` |
| `GET`    | `/api/v3/watchlist/:portfolio_id`  | AuthMiddleware (employee only) | `bankIfEmp` |

No `RequirePermission` guard is applied to the `/me/watchlist` routes — they are accessible to any authenticated caller (client or employee), consistent with them being personal data.

The `/api/v3/watchlist/:portfolio_id` route (B7, added alongside the unified portfolio) is employee-only via `AuthMiddleware`. It calls `WatchlistHandler.GetByPortfolioID`, which decodes the portfolio_id string (e.g. `client-42`, `bank`, `fund-7`) and enforces `enforcePortfolioAccess` before forwarding the `ListMy` RPC.

### Service trace

```
Gateway: WatchlistHandler (api-gateway/internal/handler/watchlist_handler.go)
  ↓ gRPC WatchlistService.AddItem / RemoveItem / ListMy
Stock-service: WatchlistHandler (stock-service/internal/handler/watchlist_handler.go)
  ↓ WatchlistService.Add / Remove / List (stock-service/internal/service/watchlist_service.go)
  ↓ WatchlistRepository.Add / Remove / ListWithListings (stock-service/internal/repository/watchlist_repository.go)
    DB table: watchlist_items
    JOIN to: listings (for price + security_type)
    Fan-out: StockRepository / OptionRepository / FuturesRepository / ForexRepository (for ticker symbol)
```

### Proto (contract/proto/stock/stock.proto lines 2244–2284)

`WatchlistService` defines three RPCs: `AddItem`, `RemoveItem`, `ListMy`. All messages carry `owner_type` + `owner_id`; the gateway fills these from `ResolvedIdentity`. No "update" RPC exists — watchlist items have no mutable fields (only presence/absence).

### Ownership enforcement

- **Gateway layer:** Identity resolved from JWT by `ResolveIdentity(OwnerIsBankIfEmployee)`. The resolved `OwnerType`/`OwnerID` are passed directly to the gRPC call; the handler never reads `owner_id` from the request body.
- **Service layer:** `ownerFromRequest` in the gRPC handler validates that `owner_id` is present when `owner_type != "bank"`. `scopeOwner` in the repository always filters `WHERE owner_type = ? AND owner_id = ?` — a caller cannot read or delete another user's items.

### Idempotency

`WatchlistRepository.Add` uses `clause.OnConflict{DoNothing: true}` — a second `AddItem` for the same `(owner, listing)` pair silently succeeds (no error, no duplicate row). The unique index `idx_watchlist_owner_listing` on `(owner_type, owner_id, listing_id)` enforces this at DB level.

`WatchlistService.Remove` returns `ErrWatchlistEntryNotFound` (gRPC `NotFound` → HTTP 404) when the row was not present. Remove is therefore **not** idempotent; clients must handle 404 on double-delete.

### Input validation

- `listing_id = 0` → HTTP 400 at the gateway.
- `listing_type` filter (optional) validated via `oneOf` against `"stock"`, `"option"`, `"futures"`, `"forex"`.
- Non-numeric `listing_id` in path → HTTP 400.
- Listing existence verified in `WatchlistService.Add` before insert (returns `ErrWatchlistListingNotFound` → 404 if missing).

### Tests

| Test | File |
|------|------|
| `TestWatchlist_Add_ListingMissing` | `stock-service/internal/service/watchlist_service_test.go` |
| `TestWatchlist_AddRemoveList` (add + idempotent re-add + list + remove + 404 on re-remove) | same |

**Missing coverage:**
- No test for cross-user privacy (user A cannot see user B's watchlist).
- No integration test (test-app/workflows/).
- No test for `listing_type` filter.

---

## §2 Favourites

**DOES NOT EXIST.**

A codebase-wide search for `favourit`, `favorite`, `Favourit`, `Favorite` returned zero results across all `.go` files, `.proto` files, model files, migration files, router files, and test files. There is no favourites feature anywhere in the backend.

**This is a gap.** If a "favourites" concept is required (distinct from watchlist), it would need to be built from scratch: new model, repository, service, gRPC RPC, gateway handler, and router registration.

---

## §3 Price Alerts

### Route table

| Method | Path | Auth | Middleware |
|--------|------|------|-----------|
| `GET`    | `/api/v3/me/price-alerts`     | AnyAuthMiddleware | `bankIfEmp` |
| `POST`   | `/api/v3/me/price-alerts`     | AnyAuthMiddleware | `bankIfEmp` |
| `GET`    | `/api/v3/me/price-alerts/:id` | AnyAuthMiddleware | `bankIfEmp` |
| `PUT`    | `/api/v3/me/price-alerts/:id` | AnyAuthMiddleware | `bankIfEmp` |
| `DELETE` | `/api/v3/me/price-alerts/:id` | AnyAuthMiddleware | `bankIfEmp` |

No `RequirePermission` guard — accessible to any authenticated caller, consistent with personal-data convention.

### Service trace

```
Gateway: PriceAlertHandler (api-gateway/internal/handler/price_alert_handler.go)
  ↓ gRPC PriceAlertService.CreateAlert / UpdateAlert / GetAlert / DeleteAlert / ListMy
Stock-service: PriceAlertHandler (stock-service/internal/handler/price_alert_handler.go)
  ↓ PriceAlertService.Create / Update / Get / Delete / ListMy
    (stock-service/internal/service/price_alert_service.go)
  ↓ PriceAlertRepository (stock-service/internal/repository/price_alert_repository.go)
    DB table: price_alerts
Background: PriceAlertCron.Run → PriceAlertService.EvaluateForListing (every 30 s)
  fires GeneralNotificationMessage → Kafka → notification-service
```

### Proto (contract/proto/stock/stock.proto lines 2172–2237)

`PriceAlertService` defines five RPCs: `CreateAlert`, `UpdateAlert`, `GetAlert`, `DeleteAlert`, `ListMy`. `GetAlert`, `UpdateAlert`, and `DeleteAlert` all carry `owner_type` + `owner_id` so they can enforce the caller-owns-alert invariant inside the service.

### Ownership enforcement

- **Gateway layer:** `ResolvedIdentity.OwnerType`/`OwnerID` passed in every RPC call. No caller-supplied owner field is accepted.
- **Service layer (Get):** `PriceAlertService.Get` (line 57–68) fetches by `id`, then explicitly checks `a.OwnerType != ownerType || !ownerIDEqual(a.OwnerID, ownerID)` and returns `ErrPriceAlertNotFound` on mismatch — preventing cross-user leakage without exposing existence.
- **Service layer (Delete):** `PriceAlertRepository.Delete` scopes the `WHERE` clause with `scopeOwner` — a caller can only delete their own row.
- **Service layer (ListMy):** `PriceAlertRepository.ListByOwner` scopes with `scopeOwner`.
- **Model layer (BeforeSave):** `ValidateOwner` rejects invalid `(owner_type, owner_id)` combinations before any write reaches the DB.

### Input validation

- `listing_id = 0` or `threshold = ""` → HTTP 400 at gateway.
- `condition` validated via `oneOf` against `"gte"`, `"lte"`, `"daily_change_pct_gte"`, `"daily_change_pct_lte"` — both on `Create` and `Update`.
- `is_recurring && (cooldown_seconds < 60 || cooldown_seconds > 86400)` → HTTP 400 at gateway.
- `threshold` must parse as a decimal string — invalid format returns gRPC `InvalidArgument` from the stock-service handler, mapped to HTTP 400 at the gateway.
- **Model-layer guard (`BeforeSave`):** `threshold.Sign() <= 0` returns an error, preventing zero or negative thresholds from being persisted even if the gateway check is bypassed. `cooldown [60..86400]` is also re-checked at this layer.

### Known issue: threshold > 0 check blocks `daily_change_pct_lte` with negative threshold

`PriceAlert.BeforeSave` (model/price_alert.go line 59) rejects any threshold where `Threshold.Sign() <= 0`. However, for the `daily_change_pct_lte` condition a caller might legitimately want to alert when daily change drops below –5% (threshold = –5). The model silently blocks this with "threshold must be positive", returning a gRPC `Unknown` error that maps to HTTP 500 at the gateway. This is a usability bug (P1). See §4.

### Evaluation / cron

`PriceAlertCron` runs every 30 seconds (default, configurable). It scans all active alerts, groups by `listing_id`, fetches the current price + daily change from the `listings` table, and calls `EvaluateForListing`. Single-shot alerts auto-deactivate; recurring alerts respect the `cooldown_seconds` window.

Notifications are published via Kafka `GeneralNotificationMessage` with `Type = "PRICE_ALERT_TRIGGERED"`. If `OwnerID == nil` (bank-owned alert) the notification is skipped silently.

### Tests

| Test | File |
|------|------|
| `TestPriceAlert_Create_ListingMissing` | `stock-service/internal/service/price_alert_service_test.go` |
| `TestPriceAlert_EvaluateForListing_SingleShotDeactivates` | same |
| `TestPriceAlert_EvaluateForListing_NoMatchNoFire` | same |

**Missing coverage:**
- No test for cross-user privacy (user A cannot get/delete user B's alert).
- No test for `Update` path.
- No test for recurring alert + cooldown enforcement.
- No test for `daily_change_pct_lte` with negative threshold (exposes the P1 bug above).
- No integration test (test-app/workflows/).

---

## §4 Findings and Follow-ups

### P0 — None found.

All three features follow the established ownership pattern. No caller can bypass identity resolution to read or mutate another user's data.

### P1 — Threshold sign check blocks valid negative daily-change-pct thresholds

**File:** `stock-service/internal/model/price_alert.go:59`
**Symptom:** `PriceAlert.BeforeSave` unconditionally rejects `Threshold.Sign() <= 0`. A request to alert when `daily_change_pct_lte –5%` (i.e. threshold = `"-5"`) will fail with a model-layer error ("threshold must be positive") that surfaces at the gateway as HTTP 500 (unmapped `Unknown` gRPC status). The gateway does not gate-keep this; it passes the string `"-5"` through as-is.
**Impact:** `daily_change_pct_lte` and `daily_change_pct_gte` with negative thresholds are unusable. The `gte`/`lte` price-absolute conditions are unaffected (prices are always positive).
**Fix approach:** Allow negative thresholds when the condition is `daily_change_pct_lte` or `daily_change_pct_gte`. The BeforeSave check should be conditioned on the condition type.

### P1 — Favourites feature does not exist

No `favourite`/`favorite` code exists anywhere in the codebase. If the product requires a favourites concept distinct from watchlist, it must be built. No partial implementation or stub was found.

### P2 — No cross-user privacy tests for watchlist or price alerts

Neither feature has a unit test asserting that user A cannot see or modify user B's data. The enforcement logic is correct in code (scoped queries + owner check in `Get`), but it is unverified by tests. Recommend adding:
- Watchlist: a test creating items for owner-A and asserting `List` for owner-B returns empty.
- Price alerts: a test creating an alert for owner-A and asserting `Get` with owner-B returns `ErrPriceAlertNotFound`.

### P2 — No integration tests (test-app/workflows/) for either feature

Zero integration tests exist for watchlist or price alerts. Both features are exercised only by unit tests. An integration test covering the full HTTP → gRPC → DB → response round-trip is missing for: add/list/remove watchlist item; create/get/list/delete price alert; and the alert-fire notification path.

### P2 — `RemoveItem` returns 404 on re-remove (non-idempotent)

`WatchlistService.Remove` returns `ErrWatchlistEntryNotFound` when the row is already gone. This differs from `Add`, which is fully idempotent. If a client retries a delete after a network timeout it will receive HTTP 404. Depending on the intended contract this may be acceptable, but it is worth documenting and testing explicitly.

### P2 — `daily_change_pct_lte` / `gte` conditions with non-obvious threshold semantics not documented

There is no API documentation clarifying what "threshold" means for percent-change conditions (`daily_change_pct_lte –5` vs `daily_change_pct_lte 5`). Combined with the P1 sign-check bug, this is a usability hazard.

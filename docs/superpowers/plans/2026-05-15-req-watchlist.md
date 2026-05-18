# Plan — Watchlist for Securities (Celina 3)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` (+ gateway routes)
**Scope:** Personal list of tracked securities per user (client or employee). Read shows current price + daily change pulled from existing `listing_daily_price_info` and `listings`. No notifications, no schedulers.

---

## Schema (stock-service)

`stock-service/internal/model/watchlist_item.go`:

```go
type WatchlistItem struct {
    ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
    OwnerType  OwnerType `gorm:"type:varchar(16);not null;index:idx_watch_owner,priority:1" json:"owner_type"` // "client" | "employee"
    OwnerID    uint64    `gorm:"not null;index:idx_watch_owner,priority:2" json:"owner_id"`
    ListingID  uint64    `gorm:"not null;index" json:"listing_id"`
    AddedAt    time.Time `gorm:"not null" json:"added_at"`
}
```

Unique index `(owner_type, owner_id, listing_id)` to prevent dups.

Auto-migrate added in `cmd/main.go` alongside other models.

## Repository

`stock-service/internal/repository/watchlist_repository.go`:

- `Add(item *model.WatchlistItem) error` — INSERT with `ON CONFLICT DO NOTHING`.
- `Remove(ownerType, ownerID, listingID) error` — DELETE by composite key.
- `List(ownerType, ownerID, listingType *string) ([]model.WatchlistItem, error)` — JOIN `listings` to optionally filter by listing kind.
- `Exists(ownerType, ownerID, listingID) (bool, error)`.

## Service

`stock-service/internal/service/watchlist_service.go`:

- `AddToWatchlist(ownerType, ownerID, listingID)` — verifies listing exists via existing `ListingRepository`. Returns the inserted record.
- `RemoveFromWatchlist(ownerType, ownerID, listingID)` — 404 if not present.
- `ListWatchlist(ownerType, ownerID, listingType *string)` — returns a slice of `WatchlistEntry { Item, Listing, LatestPrice, DailyChangePercent }` by fanning out to existing `listing_daily_price_info` reads.

No saga, no Kafka emit, no notification (UX-only feature per gap analysis).

## gRPC

New `contract/proto/watchlist.proto` (added to `Makefile` proto target):

```
service WatchlistService {
  rpc AddItem(AddItemRequest) returns (WatchlistItem);
  rpc RemoveItem(RemoveItemRequest) returns (google.protobuf.Empty);
  rpc List(ListRequest) returns (ListResponse);
}
```

`stock-service/internal/handler/watchlist_handler.go` wires `WatchlistService` → `WatchlistGRPC`.

## API Gateway

`api-gateway/internal/handler/watchlist_handler.go` + register in `router_v3.go`:

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/watchlist?listing_type=stock` | `AnyAuthMiddleware` | `watchlist.read.my` |
| POST | `/api/v3/me/watchlist` (body: `{listing_id}`) | `AnyAuthMiddleware` | `watchlist.write.my` |
| DELETE | `/api/v3/me/watchlist/:listing_id` | `AnyAuthMiddleware` | `watchlist.write.my` |

Owner is resolved from JWT via `ResolveIdentity`. No ownership check on watchlist items (they are scoped to the caller's identity by construction); the listing-id input is validated with `inRange()` and existence-checked in service layer.

`listing_type` query param is validated with `oneOf("stock","option","futures","forex")` if present.

## Permissions

`user-service/internal/service/role_service.go`:

- Seed `watchlist.read.my` and `watchlist.write.my`.
- Grant to roles: `client`, `EmployeeBasic`, `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin`.

## Tests

Unit:

- `watchlist_repository_test.go` — Add idempotency (no error on duplicate), Remove returns 0 rows on missing, List with/without filter.
- `watchlist_service_test.go` — listing-not-found → `NotFound`; add+remove+list round-trip.
- `watchlist_handler_test.go` — gRPC handler maps owner identity correctly.

Integration (`test-app/workflows/`):

- `watchlist_test.go` — client adds 3 listings, lists them with prices, removes one, verifies 2 remain.

## Docs

- `docs/Specification.md`: extend §17 routes table with three rows; §18 schema with `watchlist_items`; §6 permissions with two new entries.
- `docs/api/REST_API_v1.md` — add Watchlist section.
- Run `make swagger` in api-gateway, commit `docs/`.

## Verification

- `make build` succeeds.
- `cd stock-service && go test ./... -count=1` passes.
- `make lint` clean on stock-service and api-gateway.
- Integration test in `test-app` passes.

## Commits

1. `feat(stock-service): watchlist model + repo + service`
2. `feat(stock-service): watchlist gRPC handler + proto`
3. `feat(api-gateway): /api/v3/me/watchlist routes`
4. `feat(user-service): seed watchlist.* permissions`
5. `test: watchlist integration coverage`
6. `docs: watchlist spec + REST_API_v1`

All on the `Development` branch.

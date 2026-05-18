# Plan — OTC Negotiation History (Celina 3)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` (read endpoint), `api-gateway` (route)
**Scope:** Surface a read endpoint that lists *terminal* OTC negotiations (accepted, rejected, cancelled, expired) for the caller, with filters by status / date range / counterparty.

`otc_offers` already persists all statuses; this plan adds a focused query path and gateway route. No schema changes, no new emits, no scheduler.

---

## Schema (no change)

We reuse the existing `otc_offers` table. Confirm an index covers `(seller_owner_id, buyer_owner_id, status, created_at desc)` for the common access pattern; add if missing.

## Repository

`stock-service/internal/repository/otc_offer_repository.go` — add:

```go
func (r *OTCOfferRepository) ListTerminalForOwner(
    ownerType model.OwnerType,
    ownerID uint64,
    statusFilter []string,
    since, until *time.Time,
    counterpartyID *uint64,
    limit, offset int,
) ([]model.OTCOffer, int64, error)
```

Default `statusFilter` is the terminal set: `["accepted", "rejected", "cancelled", "expired"]`. Always exclude `pending` from history.

## Service

`stock-service/internal/service/otc_offer_service.go` — add `ListNegotiationHistory(ctx, input)` that wraps the repo call with permission scoping (caller's owner only).

Counterparty filter accepts an `owner_id` that must appear in the *other* side of the offer; map accordingly to `seller_owner_id` / `buyer_owner_id`.

## gRPC

Extend the existing OTC service in `contract/proto/otc.proto`:

```
rpc ListNegotiationHistory(ListNegotiationHistoryRequest) returns (ListNegotiationHistoryResponse);
```

Request fields: `owner_type`, `owner_id`, `statuses[]`, `since`, `until`, `counterparty_id`, `limit`, `offset`.

## Gateway route

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/otc/history` | AnyAuth | existing `otc.read.my` |

Query params: `status` (repeatable), `since=YYYY-MM-DD`, `until=YYYY-MM-DD`, `counterparty_id`, `limit`, `offset`.

Input validation:

- Each `status` value ∈ terminal set via `oneOf()`.
- `since` <= `until`.
- `limit` ∈ [1, 200], default 50.

Identity resolution from JWT — the handler injects `ownerType/ownerID` from `ResolvedIdentity` so callers cannot view other users' history.

## Tests

Unit:

- Repository test: returns rows in `created_at desc`, filters work in combination, excludes `pending` always.
- Service test: caller's id is forced regardless of input; counterparty filter constrains either side.
- Gateway handler test: status validation, date range validation, identity injection.

Integration (`test-app/workflows/otc_history_test.go`):

- Seed three offers (one accepted, one rejected, one pending). Call `/api/v3/me/otc/history` — expect only the first two; filter `?status=accepted` — only the accepted one.

## Docs

- §17: 1 new route.
- §21: include "history excludes pending offers".
- `docs/api/REST_API_v1.md`: OTC negotiation history section.

## Verification

- `make build`, stock-service tests, `make lint`, integration suite.

## Commits

1. `feat(stock-service): ListNegotiationHistory repo + service`
2. `feat(stock-service): ListNegotiationHistory gRPC`
3. `feat(api-gateway): /api/v3/me/otc/history`
4. `test: OTC history integration`
5. `docs: OTC history spec + REST_API_v1`

All on `Development`.

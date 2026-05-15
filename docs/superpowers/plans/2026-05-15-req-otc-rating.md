# Plan — OTC Trader Rating (Celina 3)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `stock-service` + `api-gateway`
**Scope:** After a *terminal* OTC negotiation (status = `accepted`), either side may rate the other once with `score ∈ [1,5]` and an optional comment. The aggregate average is exposed on OTC portal listings and on a trader profile endpoint.

---

## Schema

`stock-service/internal/model/otc_trader_rating.go`:

```go
type OTCTraderRating struct {
    ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
    OfferID       uint64    `gorm:"not null;uniqueIndex:idx_rating_offer_rater" json:"offer_id"`
    RaterOwnerType OwnerType `gorm:"type:varchar(16);not null;uniqueIndex:idx_rating_offer_rater" json:"rater_owner_type"`
    RaterOwnerID  uint64    `gorm:"not null;uniqueIndex:idx_rating_offer_rater" json:"rater_owner_id"`
    RatedOwnerType OwnerType `gorm:"type:varchar(16);not null;index:idx_rating_rated" json:"rated_owner_type"`
    RatedOwnerID  uint64    `gorm:"not null;index:idx_rating_rated" json:"rated_owner_id"`
    Score         int       `gorm:"not null" json:"score"` // 1..5
    Comment       string    `gorm:"type:text" json:"comment"`
    CreatedAt     time.Time `json:"created_at"`
}
```

Unique `(offer_id, rater_owner_type, rater_owner_id)` enforces "one rating per side per offer".

## Repository

- `Create` with `ON CONFLICT DO NOTHING` to be idempotent on double-submit.
- `AvgForRated(ratedType, ratedID)` returns `{avg float64, count int64}`.
- `ListForRated(...)` with cursor pagination for the public profile view.

## Service

`stock-service/internal/service/otc_rating_service.go`:

- `SubmitRating(input)`:
  1. Load the offer; verify status==accepted.
  2. Verify the rater is one of the two parties; the `rated` party is the OTHER side.
  3. Insert via `Create`. Conflict → return 409 `already_rated`.
  4. Publish `OTC_RATING_RECEIVED` notification to the rated party (Data: `score, comment, offer_id, rater_label`).
- `GetTraderProfile(ratedOwnerType, ratedOwnerID)` → returns avg, count, and the most recent N comments.

## gRPC

Proto `otc_rating.proto` with `Submit / GetProfile / ListForRated`.

## Gateway routes

| Method | Path | Auth | Permission |
|---|---|---|---|
| POST | `/api/v3/me/otc/ratings` | AnyAuth | `otc_rating.write.my` |
| GET | `/api/v3/otc/traders/:owner_type/:owner_id/rating` | AuthMiddleware | (public to all employees + clients via AnyAuth on the public-read route) |
| GET | `/api/v3/me/otc/ratings/received` | AnyAuth | own only |

Input validation:

- `score` ∈ [1,5] via `inRange()`.
- `comment` length ≤ 1000.
- `offer_id` required.

OTC listings in the existing portal endpoint join `OTCTraderRating.AvgForRated` (or surface via a separate fetch). To keep this self-contained, the OTC portal extension is OPTIONAL: the trader-profile endpoint is the primary new surface.

## Permissions

`otc_rating.write.my` — `client`, `EmployeeBasic+`.

## Notifications

New template `OTC_RATING_RECEIVED`:

- Push: `"You received a rating of {score}/5 from your OTC counterparty."`
- Data: `{score, offer_id, rater_label}`. RefType `otc_offer`, RefID `offer_id`.

## Tests

Unit:

- Service: reject if offer not accepted; reject if caller not party; double-submit returns 409; avg computed correctly with mixed scores.
- Gateway handler: score range, comment length.

Integration (`test-app/workflows/otc_rating_test.go`):

- Two clients complete an OTC. Each rates the other. Profile endpoint shows avg=correct, count=1 per side. Double-submit returns 409. `OTC_RATING_RECEIVED` appears in inbox.

## Docs

- §17: 3 new routes.
- §18: `otc_trader_ratings` model.
- §6: 1 new permission.
- §19+§20: `OTC_RATING_RECEIVED` template.
- `docs/api/REST_API_v1.md`: OTC Rating section.

## Verification

- `make build`, stock-service tests, `make lint`, integration suite.

## Commits

1. `feat(stock-service): otc_trader_ratings model + repo + service`
2. `feat(stock-service): otc-rating gRPC + proto`
3. `feat(api-gateway): /api/v3 otc-rating routes`
4. `feat(notification-service): OTC_RATING_RECEIVED template`
5. `feat(user-service): seed otc_rating.write.my`
6. `test: otc rating integration`
7. `docs: otc rating spec + REST_API_v1`

All on `Development`.

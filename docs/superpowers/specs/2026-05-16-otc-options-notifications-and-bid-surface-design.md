# OTC Options — Notification Coverage & Best-Bid Surface — Design

**Date:** 2026-05-16
**Status:** Approved
**Scope:** Two related additions to the OTC options marketplace, requested live during a multi-bidder testing session:

- **Part A** — surface aggregated active-chain pricing (best bid / best ask) on listings so a prospective bidder sees the market is moving.
- **Part B** — close the notification gap on the OTC option negotiation lifecycle (counters, cancels, cascade-cancels, status changes).

Builds on the 2026-05-15 notification-coverage-expansion spec (3b): the `notification.general` topic, the `GeneralNotificationMessage{Data}` struct, the `GeneralNotificationConsumer` render path, and the push-template registry are all in place and live in `stock-service`'s producer. Order events already publish. **OTC events do not**, and two OTC types are missing from the registry.

## Problem

Two gaps emerged during the cross-bank Phase 10 testing.

**Gap 1 — bidders fly blind.** The local `/api/v3/otc/options` and peer `/api/v3/public-option-offers` feeds project only the parent `OTCOffer`'s original strike/premium. Active counter-bids on a listing are invisible to other prospective bidders. With three bidders all competing for one option, none of them sees that the going premium has moved from the seller's $40 ask to a current $45 bid — they each start fresh from $40.

**Gap 2 — silent state machine.** A grep across `stock-service` confirms zero `PublishGeneralNotification` calls on the OTC negotiation path. Bidders never learn that the seller countered. Sellers never learn that a new bid landed. Losing bidders in a Phase 10 cross-bank cascade never learn their chain was nuked. The 3b spec covers 8 OTC types but **misses** the user-driven cancel and the cascade-cancel, and is silent on cross-bank routing rules.

## Goals

- Listings carry an optional `best_bid` / `best_ask` / `active_chains_count` derived from active chains. FE renders "3 bids — top $45" instead of "asking $40" when a contest is live.
- The peer protocol extension for those fields is **strictly additive and optional** — banks that don't upgrade keep working unchanged.
- Every OTC option lifecycle event produces an in-app notification (`notification.general` → `general_notifications` row → `GET /api/v3/me/notifications`).
- Cross-bank events are notified by **each bank to its own user only**. No cross-bank notification routing.
- Sibling chains cancelled by a Phase 10 cascade get a distinct event type so the message reads "your bid was cancelled because the seller accepted a competing offer" instead of the bland "your bid was cancelled".

## Non-goals

- Email for these events. In-app only (3b decision).
- Mobile push for these events.
- Changing the SI-TX peer protocol for negotiation lifecycle (counter / accept / reject / cancel POSTs and DELETEs stay as they are). Notification routing is a local concern of each bank.
- Auction-style enforcement (no monotonic bid floor). The best-bid surface is informational; counters still accept any value.

## Key facts established during exploration

- 3b's `notification.general` pipeline is live: `GeneralNotificationMessage{UserID, Type, Data, RefType, RefID}`, the consumer's `Data`-driven render path, the `templates/registry_push.go` 35-entry registry. `credit-service` and `stock-service` (order paths only) already publish.
- `stock-service/internal/kafka/producer.go` already has `PublishGeneralNotification`; `cmd/main.go:252` already lists `notification.general` in `EnsureTopics`. **No wiring work needed in stock-service to start publishing.**
- The OTC types `OTC_OFFER_RECEIVED, OTC_OFFER_COUNTERED, OTC_OFFER_REJECTED, OTC_OFFER_EXPIRED, OTC_CONTRACT_CREATED, OTC_CONTRACT_EXERCISED, OTC_CONTRACT_EXPIRED, OTC_CONTRACT_FAILED` are already in the registry but **no service publishes them**. The new types `OTC_OFFER_CANCELLED` and `OTC_OFFER_CASCADE_CANCELLED` are missing.
- Negotiation rows store the parties as `(BidderOwnerType, BidderOwnerID)` (local row) or `(BuyerRoutingNumber, BuyerID, SellerRoutingNumber, SellerID)` (cross-bank mirror). The seller's identity comes from the parent `OTCOffer`'s `InitiatorOwnerID`. All are directly resolvable to a `UserID` for the `GeneralNotificationMessage`.
- Phase 10's `CascadeCancelSiblings` already loads every cancelled sibling row in stock-service and the gateway already iterates outbound DELETEs to each bidder bank. Both sites are natural publish hooks.
- Peer protocol's `PeerPublicOptionOffer` proto uses non-optional `string` fields. Adding optional fields is wire-compatible: older peers omit them; proto3 treats them as default (empty string / 0) on the receiver side.

## Design

### Part A — Best-bid / best-ask surface

**Aggregation.** A single repository helper joins active chains to their parent offers:

```sql
SELECT parent_offer_id,
       MAX(premium::numeric) AS best_premium_high,  -- sell_initiated parent → best_bid
       MIN(premium::numeric) AS best_premium_low,   -- buy_initiated parent  → best_ask
       COUNT(*)              AS active_chains
FROM otc_negotiations
WHERE parent_offer_id IN (...) AND status IN ('open','countered')
GROUP BY parent_offer_id;
```

One round-trip per `ListUnifiedOptionOffers` call (and per `GetPublicOptionOffers` peer request). No per-row N+1.

**Local routes.** `/api/v3/otc/options` and `/api/v3/me/otc/options/:id` carry three new fields per row, derived from the parent's direction:

| Parent direction | `best_bid` | `best_ask` |
|---|---|---|
| `sell_initiated` (seller posts, buyers bid) | MAX premium of active chains | always null |
| `buy_initiated` (buyer posts, sellers bid) | always null | MIN premium of active chains |

`active_chains_count` is the count regardless of direction. All three are null when no active chains exist.

**Peer protocol.** Extend `PeerPublicOptionOffer`:

```proto
message PeerPublicOptionOffer {
  // ... existing fields ...
  string best_bid             = 10;
  string best_ask             = 11;
  int32  active_chains_count  = 12;
}
```

Pre-Part-A peers omit those fields → proto3 deserializes them as `""` / `0` → our cache projection treats `""` as "unknown / not reported" (null on the JSON wire). The bank that supports it publishes; the bank that doesn't, doesn't. **No coordination with other faculty banks required.**

**Cache invalidation.** The unified-options cache in `stock-service/internal/otccache/cache.go` already refreshes every ~5s. The aggregation runs as part of that refresh — no separate invalidation triggers needed, and a freshly-placed counter shows up within the next refresh tick.

### Part B — Notification coverage

**Event matrix.** One intent per event, one publish per recipient. All intents fire **after** the relevant DB transaction commits.

| Event | Recipient(s) | Type | Trigger site |
|---|---|---|---|
| Bid opened | Listing poster (the other party of the bidder) | `OTC_OFFER_RECEIVED` | `OTCNegotiationService.OpenNegotiation` post-commit |
| Counter placed | The OTHER party in the chain | `OTC_OFFER_COUNTERED` | `OTCNegotiationService.CounterNegotiation` post-commit |
| Rejected | The OTHER party | `OTC_OFFER_REJECTED` | `OTCNegotiationService.RejectNegotiation` post-commit |
| Cancelled by caller | The OTHER party | `OTC_OFFER_CANCELLED` (new) | `OTCNegotiationService.CancelNegotiation` post-commit |
| Cascade-cancelled (Phase 2 intra-bank) | Each losing chain's bidder | `OTC_OFFER_CASCADE_CANCELLED` (new) | per-row inside `AcceptNegotiationChain`'s sibling sweep |
| Cascade-cancelled (Phase 10 cross-bank, seller side) | n/a (seller already gets `OTC_CONTRACT_CREATED`) | — | — |
| Cascade-cancelled (Phase 10 cross-bank, bidder side) | Local bidder whose chain was nuked | `OTC_OFFER_CASCADE_CANCELLED` (new) | `PeerOTCGRPCHandler.DeleteNegotiation` when the inbound DELETE flips a still-`ongoing` row to `cancelled` AND the row's `parent_offer_id` is set (cascade marker) |
| Accepted | Both bidder and seller | `OTC_CONTRACT_CREATED` | `OTCAcceptSaga` after `credit_premium_seller` step commits |
| Expired | Both parties | `OTC_OFFER_EXPIRED` | existing expiry cron — adds a publish hook per row |

**Cross-bank routing rule.** Each bank publishes notifications **only** for its own local users. No cross-bank notification messages. Cross-bank events propagate the *state change* via the existing SI-TX protocol (POST /negotiations counter, DELETE cancel, GET /accept etc.); both banks then independently publish notifications to their own users from the same state-change hook they already process.

- Buyer bank receives a SI-TX counter from seller bank → `PeerOTCGRPCHandler.UpdateNegotiation` flips local mirror → publishes `OTC_OFFER_COUNTERED` to the local buyer.
- Seller bank receives a SI-TX inbound bid → `PeerOTCGRPCHandler.CreateNegotiation` stores mirror → publishes `OTC_OFFER_RECEIVED` to the local seller.
- Bidder bank receives an outbound DELETE driven by the seller's cascade → publishes `OTC_OFFER_CASCADE_CANCELLED` to the local bidder.

This keeps the SI-TX protocol unchanged and gives every bank independent control over its own notification UX.

**Disambiguating cascade vs caller-driven cancel.** The inbound DELETE handler distinguishes the two cases by inspecting the local mirror row's `ParentOfferID`:

- `ParentOfferID != nil` and there is now a sibling row marked `accepted` under the same parent → **cascade**.
- otherwise → **caller-driven cancel**.

Practically: the bidder-bank DELETE handler emits `OTC_OFFER_CASCADE_CANCELLED` whenever `ParentOfferID != nil` (the seller would only DELETE a chain it didn't accept if the cascade fired). Free-form chains (`ParentOfferID == nil`) emit plain `OTC_OFFER_CANCELLED`. This is a heuristic that's correct in all currently-defined flows; if a future flow lets the seller DELETE a discovered-group chain for a reason other than cascade, the type would need an explicit hint on the DELETE.

**`Data` keys per type:**

| Type | `Data` keys |
|---|---|
| `OTC_OFFER_RECEIVED` | `ticker, quantity, strike_price, premium` |
| `OTC_OFFER_COUNTERED` | `ticker, quantity, strike_price, premium` |
| `OTC_OFFER_REJECTED` | `ticker` |
| `OTC_OFFER_CANCELLED` | `ticker` |
| `OTC_OFFER_CASCADE_CANCELLED` | `ticker, accepted_premium` (so the bidder sees what price won) |
| `OTC_OFFER_EXPIRED` | `ticker` |
| `OTC_CONTRACT_CREATED` | `ticker, quantity, strike_price, premium_paid` |

`RefType`/`RefID` = `("otc_negotiation", negotiationID)` for the lifecycle events, `("otc_contract", contractID)` for contract events. FE deep-links into the negotiation chain view.

### Files touched

**Part A (~6):**
- `contract/proto/stock/stock.proto` — `PeerPublicOptionOffer` extension + internal `UnifiedOptionOffer`
- `stock-service/internal/repository/otc_negotiation_repository.go` — `AggregateActiveBidsByOffer(offerIDs []uint64) (map[uint64]ChainAggregate, error)`
- `stock-service/internal/handler/peer_otc_grpc_handler.go` — `GetPublicOptionOffers` populates new fields
- `stock-service/internal/otccache/cache.go` — unified-listing projection
- `api-gateway/internal/handler/portfolio_handler.go` — passes through to JSON
- `docs/api/REST_API_v3.md` — documents new optional fields

**Part B (~9):**
- `notification-service/internal/templates/registry_push.go` — add two new `Definition`s + the `accepted_premium` variable
- `notification-service/internal/templates/registry_test.go` — extend the enumeration test
- `stock-service/internal/service/otc_negotiation_service.go` — publish hooks in `Open/Counter/Reject/Cancel` after commit; per-row publish in the cascade sibling sweep inside `AcceptNegotiationChain`
- `stock-service/internal/service/otc_accept_saga.go` — `OTC_CONTRACT_CREATED` publish after `credit_premium_seller` step
- `stock-service/internal/handler/peer_otc_grpc_handler.go` — publishes in `CreateNegotiation` (inbound bid), `UpdateNegotiation` (inbound counter — confirm exact method name), `DeleteNegotiation` (inbound cancel/cascade), `AcceptNegotiation` (inbound accept seller-side)
- `stock-service/internal/cron/otc_expiry.go` (or wherever expiry lives) — per-row publish
- Tests on each new publish site (each call site has an existing producer-mock pattern from the order-path tests)
- `docs/api/REST_API_v3.md` — short note that `/me/notifications` now covers these events

## Concurrency & safety

- Intents publish AFTER the business transaction commits (CLAUDE.md Kafka rule) — never inside a TX.
- Each intent is best-effort; a Kafka failure logs a warning and does not fail the business action (matches the existing credit-service / order-path pattern).
- The cascade sweep already runs under a single DB transaction; the publish loop runs AFTER the TX commits, iterating the in-memory result.
- Part A's aggregator is a single read query; no locking needed (eventual consistency with the 5s cache tick is acceptable).

## Testing

**Part A:**
- Repo unit: `AggregateActiveBidsByOffer` with mixed-direction parents + mixed-status chains returns the right MAX/MIN/COUNT per parent.
- Handler unit: `GetPublicOptionOffers` populates the new fields when chains exist; returns "" / 0 when none.
- Integration: place two competing bids on one local listing → `/api/v3/otc/options` shows `best_bid` = higher of the two, `active_chains_count` = 2.
- Cross-bank graceful: a mock peer that omits the new fields in its `PeerPublicOptionOffer` JSON yields a unified row with null `best_bid` (no crash, no warning).

**Part B:**
- Per publish site: unit test the producer-mock captures one `GeneralNotificationMessage` with the right `Type`, `UserID`, `Data`, `RefType`, `RefID`. Two-party events assert two captures.
- Cascade: `AcceptNegotiationChain` test asserts one `OTC_OFFER_CASCADE_CANCELLED` per sibling bidder.
- Cross-bank inbound: `PeerOTCGRPCHandler.DeleteNegotiation` test — `ParentOfferID != nil` → `OTC_OFFER_CASCADE_CANCELLED`; `ParentOfferID == nil` → `OTC_OFFER_CANCELLED`.
- Registry: enumeration test catches both new types in `registry_push.go`.
- Integration: open a chain on bank A's listing from bank B, then bank B counters → `/api/v3/me/notifications` on bank A's seller shows `OTC_OFFER_COUNTERED`.

## Docs to update

- `docs/api/REST_API_v3.md` — Part A: document the three optional fields on the listing endpoints + the peer endpoint. Part B: one short paragraph under §47 listing the new notification types.
- Memory: update `project_notification_gaps` to mark the OTC option gap as closed once Part B ships.

## Implementation phasing

**PR 1 — Part B (notifications):** higher user value (state machine is currently silent). 1.5 days. Subphases:
- B.1 — registry: two new types + tests.
- B.2 — stock-service publish hooks for the intra-bank flow (`OTCNegotiationService` + `otc_accept_saga` + expiry cron) + tests.
- B.3 — stock-service publish hooks for the cross-bank inbound flow (`PeerOTCGRPCHandler` create/update/delete/accept) + tests.
- B.4 — integration tests + docs.

**PR 2 — Part A (best-bid surface):** auction polish. ~half day. Subphases:
- A.1 — aggregator repo helper + unit test.
- A.2 — proto extension (`make proto`) + local/peer handler population + cache projection.
- A.3 — integration tests + docs.

Independent — no shared files between the two PRs.

## Open questions

None.

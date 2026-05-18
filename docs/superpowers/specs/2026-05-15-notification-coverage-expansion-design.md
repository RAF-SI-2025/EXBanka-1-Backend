# Notification Coverage Expansion — Design

**Date:** 2026-05-15
**Status:** Approved (brainstorming)
**Scope:** Spec 3b — the final piece of the 2026-05-14 request. Spec 1 (OTC client access), Spec 2 (employee bank-account activity), and Spec 3a (notification template management) are done. 3b builds directly on 3a's template registry.

## Problem

~25 domain Kafka topics are published today (orders, OTC offers/contracts, payments, transfers, loans, account lifecycle, card lifecycle) but **nothing turns them into user notifications**. A client never learns in-app that their order filled, a counter was placed on their OTC offer, money moved into their account, or their loan was approved. The user asked for broad coverage: "order filled (each partial part too), order placed, order cancelled, when money is taken or moved into account, when a counter is placed on a user's offer, when an offer is accepted, …".

## Goals

- Every significant action across the six domains produces an **in-app notification** in the existing persistent feed (`general_notifications`, surfaced by `GET /api/v3/me/notifications`).
- Notification wording is rendered through **3a's template registry** (`push` channel), so the text is admin-editable via the 3a endpoints.
- notification-service stays a **pure sink** — no new outbound gRPC clients, no domain knowledge of event types.

## Non-goals

- **Email** for these events. In-app feed only (decided during brainstorming). Per-partial-fill emails would be spam; notification-service has no way to resolve `user_id → email`; the email surface stays as it is.
- Mobile push delivery for these events (the `notification.mobile-push` path stays verification-only).
- Migrating the existing email flows or auth-service's legacy `password_changed` notification to the new mechanism — 3b is purely additive.
- A `SPENDING_RESET` notification — that event is a bulk daily/monthly cron with no per-user recipient.
- New REST routes — `GET /api/v3/me/notifications` already serves the feed.

## Key facts established during exploration

- Event messages identify recipients inconsistently: orders carry `UserID` (0 when bank-owned); OTC events carry `OTCParty{OwnerType, OwnerID}` per party; **payments/transfers carry only account numbers — no user id**; loans/cards/account-created carry an email or id in varying shapes.
- notification-service has **zero outbound gRPC clients** and currently knows only `user_id` (from `GeneralNotificationMessage`) — it cannot resolve `account# → owner` or `user_id → email`.
- `notification.general` + `GeneralNotificationMessage` + `GeneralNotificationConsumer` already exist; auth-service already publishes one `password_changed` notification through this path. The consumer persists a `GeneralNotification` row.
- Partial fills reuse the `stock.order-filled` topic — stock-service publishes one `OrderEventMessage` per partial settlement; the order's `IsDone` flag distinguishes a final fill. Per-partial-fill notifications need no matching-engine change.
- 3a's registry (`notification-service/internal/templates/registry.go`) holds 13 `email` `Definition`s; the `push` channel is plumbed but empty.

## Design

### 1. Pipeline & contract change

Each publishing service, at a significant action, emits a notification **intent** on the existing `notification.general` topic. notification-service's `GeneralNotificationConsumer` renders it via the 3a registry (`push` channel) and stores a `GeneralNotification`. No new consumers, no new gRPC clients.

`GeneralNotificationMessage` (`contract/kafka/messages.go`) gains one field:

```go
type GeneralNotificationMessage struct {
	UserID  uint64            `json:"user_id"`
	Type    string            `json:"type"`
	Title   string            `json:"title,omitempty"`   // legacy: pre-rendered
	Message string            `json:"message,omitempty"` // legacy: pre-rendered
	Data    map[string]string `json:"data,omitempty"`    // NEW: render Type via the registry
	RefType string            `json:"ref_type,omitempty"`
	RefID   uint64            `json:"ref_id,omitempty"`
}
```

`GeneralNotificationConsumer.handleMessage`:
- `len(Data) > 0` → `templateSvc.Render(Type, "push", Data)` → store the rendered subject/body as the notification's `Title`/`Message`. Unknown type or render error → log and drop (consistent with the email consumer's render-error handling).
- `len(Data) == 0` → legacy path: store `Title`/`Message` as-is (keeps auth-service's `password_changed` working untouched).

The consumer gains a `TemplateService` dependency, wired in `notification-service/cmd/main.go`.

### 2. New `push` template types

35 new `push`-channel registry `Definition`s. Each has a default Title (subject) and Message (body) editable via the 3a admin endpoints. **R** = recipient, **vars** = the `Data` keys the publisher supplies.

**Orders** (stock-service — R: order owner; emitted only when `OwnerType == "client"`):
| Type | vars | trigger |
|---|---|---|
| `ORDER_PLACED` | `direction, quantity, ticker, order_type` | `stock.order-created` |
| `ORDER_APPROVED` | `direction, quantity, ticker` | `stock.order-approved` |
| `ORDER_DECLINED` | `direction, quantity, ticker` | `stock.order-declined` |
| `ORDER_PARTIALLY_FILLED` | `direction, filled_quantity, ticker` | partial `stock.order-filled` |
| `ORDER_FILLED` | `direction, quantity, ticker` | final `stock.order-filled` |
| `ORDER_CANCELLED` | `direction, quantity, ticker` | `stock.order-cancelled` |

**OTC** (stock-service — two-party; one intent per *client* party, `bank` parties skipped):
| Type | R | vars |
|---|---|---|
| `OTC_OFFER_RECEIVED` | named counterparty | `ticker, quantity, strike_price, premium` |
| `OTC_OFFER_COUNTERED` | the party who didn't counter | `ticker, quantity, strike_price, premium` |
| `OTC_OFFER_REJECTED` | the other party | `ticker` |
| `OTC_OFFER_EXPIRED` | initiator (+ counterparty) | `ticker` |
| `OTC_CONTRACT_CREATED` | buyer & seller (= "offer accepted") | `ticker, quantity, strike_price, premium_paid` |
| `OTC_CONTRACT_EXERCISED` | buyer & seller | `ticker, shares_transferred, strike_amount_paid` |
| `OTC_CONTRACT_EXPIRED` | buyer & seller | `ticker` |
| `OTC_CONTRACT_FAILED` | buyer & seller | `failure_reason` |

**Money movement** (transaction-service — resolves account → owner; bank-owned accounts skipped):
| Type | R | vars |
|---|---|---|
| `PAYMENT_SENT` | sender | `amount, to_account` |
| `PAYMENT_RECEIVED` | receiver | `amount, from_account` |
| `PAYMENT_FAILED` | sender | `amount, failure_reason` |
| `TRANSFER_SENT` | sender | `amount, to_account` |
| `TRANSFER_RECEIVED` | receiver | `final_amount, from_account` |
| `TRANSFER_FAILED` | sender | `amount, failure_reason` |

**Loans** (credit-service — R: borrower):
| Type | vars |
|---|---|
| `LOAN_REQUEST_SUBMITTED` | `loan_type, amount` |
| `LOAN_REQUEST_APPROVED` | `loan_type, amount` |
| `LOAN_REQUEST_REJECTED` | `loan_type, amount` |
| `LOAN_DISBURSED` | `loan_number, amount, currency` |
| `INSTALLMENT_COLLECTED` | `amount` |
| `INSTALLMENT_FAILED` | `amount, retry_deadline` |

**Accounts** (account-service — R: account owner; bank accounts skipped):
| Type | vars |
|---|---|
| `ACCOUNT_OPENED` | `account_number, currency` |
| `ACCOUNT_STATUS_CHANGED` | `account_number, new_status` |
| `ACCOUNT_NAME_UPDATED` | `account_number, new_name` |
| `ACCOUNT_LIMITS_UPDATED` | `account_number, daily_limit, monthly_limit` |
| `MAINTENANCE_FEE_CHARGED` | `account_number, amount, currency` |

**Cards** (card-service — R: card/request owner):
| Type | vars |
|---|---|
| `CARD_CREATED` | `card_brand` |
| `CARD_STATUS_CHANGED` | `new_status` |
| `CARD_TEMPORARY_BLOCKED` | `expires_at, reason` |
| `VIRTUAL_CARD_CREATED` | `usage_type` |
| `CARD_REQUEST_CREATED` | `card_brand` |
| `CARD_REQUEST_APPROVED` | `card_brand` |
| `CARD_REQUEST_REJECTED` | `reason` |

### 3. Publishing-service changes

Each of the six services already has a Kafka producer and publishes its domain events from the `service/` layer. For each:

1. Add a `PublishGeneralNotification(ctx, GeneralNotificationMessage) error` helper to `internal/kafka/producer.go` (thin wrapper over the existing generic publish).
2. Add `notification.general` to the service's `EnsureTopics(...)` call in `cmd/main.go` — it now *produces* to it; creation is idempotent.
3. At each significant action in the `service/` layer — next to where the domain event is already published, **after the DB transaction commits** — emit one `GeneralNotificationMessage{UserID, Type, Data, RefType, RefID}` per recipient.
4. Emitting the intent is **best-effort**: a publish failure logs a warning and does not fail the business action (matches the existing account-service email-publish pattern).

**Recipient rules:**
- **Orders** (stock-service) — the order's `(OwnerType, OwnerID)`; emit only when `OwnerType == "client"`. Bank-owned orders produce nothing.
- **OTC** (stock-service) — two-party. For each event, emit one intent per *client* party named in the Section 2 recipient rule; `bank` parties are skipped.
- **Money movement** (transaction-service) — resolves the from/to account → owner client id (transaction-service already calls account-service in the flow). `SENT`/`FAILED` to the sender, `RECEIVED` to the receiver; bank-owned accounts skipped.
- **Loans** (credit-service) — the borrower's client id, from the loan/loan-request entity.
- **Accounts** (account-service) — the account's `OwnerID`; bank accounts skipped.
- **Cards** (card-service) — the card/request's owner client id.

Two-party events (OTC counters/rejections/contracts, transfers, payments) emit **multiple** `GeneralNotificationMessage`s — one per recipient; the consumer renders and stores each separately.

`RefType`/`RefID` are set where natural (`"order"`/orderID, `"otc_offer"`/offerID, `"otc_contract"`/contractID, `"payment"`/paymentID, `"transfer"`/transferID, `"loan"`/loanID, `"account"`/accountID, `"card"`/cardID) so the frontend can deep-link from a feed entry.

**Purely additive** — existing flows untouched: account-created keeps its email *and* gains an `ACCOUNT_OPENED` in-app intent; auth-service's `password_changed` legacy notification keeps working.

### 4. File structure

3a's `registry.go` (13 entries) would grow to ~48. Split it:
- `registry_email.go` — the 13 email `Definition`s (`emailDefs` var).
- `registry_push.go` — the 35 push `Definition`s (`pushDefs` var).
- `registry.go` — keeps the `Variable`/`Definition` types, the combined `registry` slice (`append(emailDefs, pushDefs...)`), and the `All`/`Get`/`KnownVars` helpers.

## Concurrency & safety

- Intents are published from the `service/` layer **after** the business transaction commits (CLAUDE.md's Kafka rule) — a notification is never sent for an action that rolled back.
- Intent publishing is best-effort and never inside the business transaction; a Kafka failure degrades to a missing notification, not a failed action.
- `GeneralNotificationConsumer` writes one `GeneralNotification` row per message via the existing repository `Create` — no new locking concerns.
- No new versioned models, no new sagas.

## Testing

- **contract** — `GeneralNotificationMessage` JSON round-trips with `Data` populated.
- **notification-service** — `GeneralNotificationConsumer`: the `Data`-driven path renders `Type` via the registry `push` channel and stores the rendered Title/Message; the legacy `Title/Message` path still works; unknown type / render error → logged and dropped.
- **registry** — 3a's `TestRegistry_SelfConsistent` already validates every new entry's placeholders against its declared variables; add `TestRegistry_AllPushTypesPresent` enumerating the 35 push types.
- **each publishing service** — per significant action, a unit test that the expected `GeneralNotificationMessage` is emitted (correct `Type`, `UserID`, `Data` keys); two-party events assert both intents; bank-owned cases assert **no** intent. Uses each service's existing producer-mock pattern.
- **integration** (`test-app/workflows/`) — a handful end-to-end: place an order → `GET /api/v3/me/notifications` shows `ORDER_PLACED`; client A counters client B's offer → B's feed shows `OTC_OFFER_COUNTERED`; transfer between two clients → sender sees `TRANSFER_SENT`, receiver sees `TRANSFER_RECEIVED`.

## Docs to update

- `docs/Specification.md` — §19 Kafka (the `Data` field on `GeneralNotificationMessage`; `notification.general` is now produced by all six services), the 35 new notification types, §21 business rules (bank-owned skipped, two-party emits multiple, best-effort publish), the registry split.
- `docs/api/REST_API_v3.md` — no new routes; a short note that `GET /api/v3/me/notifications` now covers these event types.
- No swagger change (no new routes). No `docker-compose.yml` change (no new env/service/port).

## Implementation phasing

One spec → one plan, phased:
- **Phase 1** — contract `Data` field + `GeneralNotificationConsumer` render path + `cmd/main.go` wiring + tests.
- **Phase 2** — registry split + the 35 `push` template types + registry tests.
- **Phases 3–8** — one per domain (orders, OTC, money movement, loans, accounts, cards): the producer helper, the `EnsureTopics` entry, the `service/`-layer call sites, and unit tests. Each phase is independent and shippable.
- **Phase 9** — integration tests + docs.

## Open questions

None. Channel (in-app only), the intent-based pipeline, the six-domain scope, and the skipped `SPENDING_RESET` were settled during brainstorming.

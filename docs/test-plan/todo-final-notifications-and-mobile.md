# TODO_final — Cross-cutting Notification Coverage + Mobile-App Features + Quick Approve

**Source of truth:** `docs/bank-requirements/TODO_final.pdf` (Celina 1–4 notification refinements + the
"Proširenje za mobilne aplikacije" section). Cross-referenced with the live implementation:
`notification-service/`, `contract/kafka/messages.go`, the api-gateway notification/websocket/mobile
handlers, and `docs/api/REST_API_v3.md`.

This file follows the template and ID scheme from
`docs/superpowers/specs/2026-06-07-comprehensive-test-plan-design.md` §4
(`TC-<AREA>-<nnn>`, areas `NOTIF` and `MOBILE`). Per spec §7 every requirement is written as a TC even
when no endpoint implements it — those rows are marked **NO-ENDPOINT** (a real gap to surface, not skip).

---

## 0. Notification architecture (how the three channels actually work)

Understanding the wiring is required to assert side-effects accurately. There are exactly **three**
delivery channels, each a distinct Kafka topic consumed by `notification-service`
(`notification-service/cmd/main.go` wires the consumers):

| Channel | Topic | Payload | Producer pattern | Where the user sees it |
|---|---|---|---|---|
| **Email** | `notification.send-email` | `kafka.SendEmailMessage{To, EmailType, Data}` | a service explicitly calls `producer.SendEmail(...)` | SMTP (`internal/sender`) |
| **In-app inbox** | `notification.general` | `kafka.GeneralNotificationMessage{UserID, Type, Data, RefType, RefID}` | a service calls `PublishGeneralNotification(...)`; `general_notification_consumer` renders `Type` via the push template registry and writes `general_notifications` | `GET /api/v3/me/notifications` |
| **Mobile push** | `notification.mobile-push` | `kafka.MobilePushMessage{UserID, Type, Payload}` | **only** `notification-service/internal/consumer/verification_consumer.go` (verification challenges) | WebSocket `GET /ws` (api-gateway `websocket_handler.go`) |

**Three load-bearing facts that drive most of the matrix below:**

1. **Email is opt-in per call site.** `notification-service` has **no** consumer that translates a domain
   event topic (e.g. `stock.order-filled`, `transaction.payment-completed`, `credit.loan-approved`) into
   an email. An email is sent **iff** a service explicitly publishes a `SendEmailMessage`. The complete
   set of live email call sites is: account-created, card status-changed (block/unblock/deactivate),
   client LIMIT_CHANGED, installment-failed (cron), mobile-activation, account-activation, password-reset,
   activation-confirmation, and the email-channel verification code. **Everything else emits no email.**
2. **In-app inbox is render-by-template.** A `GeneralNotificationMessage` whose `Type` is not in
   `notification-service/internal/templates/registry_push.go` is dropped (not retryable) by the consumer.
   So an in-app item exists iff (a) a service publishes the message AND (b) a matching push template exists.
3. **Mobile push (`notification.mobile-push` → WebSocket) carries verification challenges only.** No
   business event is pushed over the socket; the in-app inbox is delivered by **polling** `/me/notifications`,
   not by WebSocket. Therefore the **push? column is NO for every business event** in the matrix — this is
   a systemic gap relative to the PDF's "email **i mobilne** notifikacije" wording (see TC-NOTIF-023).

**Discrepancy flagged (Celina 1 vs implementation):** the PDF says lockout = "Nakon **5** neuspešnih
pokušaja … **10 minuta**". The code (`auth-service/internal/service/auth_service.go`) locks after
`maxFailedAttempts = 5` for `lockoutDuration = 30m` over a `lockoutWindow = 15m`. Tests below assert the
**implemented** values (5 / 30 min) and note the doc divergence (10 min).

---

## 1. NOTIFICATION COVERAGE MATRIX

`email?` / `in-app?` / `push?` = whether that channel is actually emitted by the current code.
Status: **covered** (every channel the PDF requires is present), **partial** (some required channel
missing), **NO-ENDPOINT** (event emits nothing the PDF asks for, or the event itself is unimplemented).

| Event | Trigger action | email? | in-app? | push? | TC id | Status |
|---|---|---|---|---|---|---|
| **C1** Account locked (5 failed logins) | 5× `POST /api/v3/auth/login` wrong password | **NO** (gap) | NO (user not logged in) | NO | TC-NOTIF-001 | NO-ENDPOINT |
| **C2** Payment executed | `POST /api/v3/me/payments/:id/execute` | **NO** (gap) | YES `PAYMENT_SENT`+`PAYMENT_RECEIVED` | NO | TC-NOTIF-002 | partial |
| **C2** Transfer executed | `POST /api/v3/me/transfers/:id/execute` | **NO** (gap) | YES `TRANSFER_SENT`+`TRANSFER_RECEIVED` | NO | TC-NOTIF-003 | partial |
| **C2** Limit changed | `PUT /api/v3/clients/:id/limits` | YES `LIMIT_CHANGED` | YES `LIMIT_CHANGED` | NO | TC-NOTIF-004 | covered |
| **C2** Card blocked | `POST /api/v3/cards/:id/block` | YES `CARD_STATUS_CHANGED` | YES `CARD_STATUS_CHANGED` | NO | TC-NOTIF-005 | covered |
| **C2** Card unblocked | `POST /api/v3/cards/:id/unblock` | YES `CARD_STATUS_CHANGED` | YES `CARD_STATUS_CHANGED` | NO | TC-NOTIF-006 | covered |
| **C2** Credit created | `POST /api/v3/me/loan-requests` | **NO** (gap) | YES `LOAN_REQUEST_SUBMITTED` | NO | TC-NOTIF-007 | partial |
| **C2** Credit approved | `POST /api/v3/loan-requests/:id/approve` | **NO** (`EmailTypeLoanApproved` defined but unused) | YES `LOAN_REQUEST_APPROVED`(+`LOAN_DISBURSED`) | NO | TC-NOTIF-008 | partial |
| **C3** Order created → Pending | `POST /api/v3/orders` (needs approval) | **NO** (gap) | YES `ORDER_PLACED` | NO | TC-NOTIF-009 | partial |
| **C3** Order approved | `POST /api/v3/orders/:id/approve` | **NO** (gap) | YES `ORDER_APPROVED` | NO | TC-NOTIF-010 | partial |
| **C3** Order rejected | `POST /api/v3/orders/:id/reject` | **NO** (gap) | YES `ORDER_DECLINED` | NO | TC-NOTIF-011 | partial |
| **C3** Order fully executed (isDone) | fill engine reaches qty | **NO** (gap) | YES `ORDER_FILLED` | NO | TC-NOTIF-012 | partial |
| **C3** Order partially filled | fill engine partial | **NO** (gap) | YES `ORDER_PARTIALLY_FILLED` | NO | TC-NOTIF-013 | partial |
| **C3** Order auto-cancelled (settlement) | cancel path | **NO** (gap) | YES `ORDER_CANCELLED` | NO | TC-NOTIF-014 | partial |
| **C3** Price-alert fired | price refresh crosses threshold | **NO** | YES `PRICE_ALERT_TRIGGERED` | NO | TC-NOTIF-015 | partial |
| **C3** Dividend received | quarterly dividend cron | **NO** | **NO** | NO | TC-NOTIF-016 | NO-ENDPOINT |
| **C3** Tax deducted | monthly tax cron / manual trigger | **NO** | **NO** | NO | TC-NOTIF-017 | NO-ENDPOINT |
| **C3** Recurring-order skipped | DCA cron, insufficient funds | **NO** | YES `RECURRING_ORDER_SKIPPED` | NO | TC-NOTIF-018 | partial |
| **C4** OTC counter-offer received | `POST /api/v3/otc/options/:id/counter` | **NO** (gap) | YES `OTC_OFFER_COUNTERED` | NO | TC-NOTIF-019 | partial |
| **C4** OTC offer accepted | `POST .../accept` | **NO** (gap) | YES `OTC_CONTRACT_CREATED` | NO | TC-NOTIF-020 | partial |
| **C4** OTC offer withdrawn | bidder cancels chain | **NO** (gap) | YES `OTC_OFFER_CANCELLED` | NO | TC-NOTIF-021 | partial |
| **C4** Option contract expiring in N days | `otc_expiry_cron` warn pass | **NO** (gap) | YES `OTC_CONTRACT_EXPIRING_SOON` | NO | TC-NOTIF-022 | partial |
| **(systemic)** Mobile push for business events | any of the above | — | — | **NO** (only verification) | TC-NOTIF-023 | NO-ENDPOINT |
| **(bonus, present)** Watchlist ±5% daily move | `notification.watchlist-alert` cron | NO | YES `WATCHLIST_PRICE_MOVE` | NO | TC-NOTIF-024 | covered (in-app) |

---

## 2. Notification-coverage test cases

> **Shared assertion method.** For an **in-app** assertion, after the trigger poll
> `GET /api/v3/me/notifications` (auth = recipient) until an item with the expected `type` appears
> (publish→Kafka→consumer→DB is async; existing tests use a ~20s poll, see
> `test-app/workflows/wf_notification_coverage_test.go`). For an **email** assertion, scan the
> `notification.send-email` Kafka topic for a `SendEmailMessage` with the expected `EmailType`+`To`
> (pattern: `helpers_test.go::scanKafkaForActivationToken`). For a **push** assertion, connect a mobile
> WebSocket and assert a `notification.mobile-push` frame. **A `NO`/gap row asserts the channel is
> *absent*** (negative) — confirm no email/push is observed within the poll window.

---

### Celina 1 — account lock

#### TC-NOTIF-001 · Account-lockout email after 5 failed logins (NEGATIVE — documented gap)
- **Feature:** Bezbednosni mehanizam — email on lock · **Spec:** TODO_final Celina 1 (lock email) · **Existing test:** —
- **Actor:** unauthenticated (login attempts) + the locked account's owner
- **Preconditions:** an active account `victim@exbanka.rs` exists; failed-login counter at 0
- **Request:** `POST /api/v3/auth/login` ×5
  - Auth: none
  - Body: `{"email":"victim@exbanka.rs","password":"WRONG"}`
- **Verification:** n/a
- **Expected:** attempts 1–4 → `401 unauthorized`; attempt 5 → `403 forbidden` (`ErrAccountLocked`) and the
  account is locked for **30 min** (implemented; PDF says 10 min). **Side-effects:** lock row created in
  `auth_db`. **Gap assertion:** scan `notification.send-email` for ~10s — **no** lock-notification email is
  emitted (the PDF requires one; `auth-service` never publishes it). No in-app item (owner is not logged in).
- **Negative siblings:** 6th attempt during lock window → still `403` and counter does not increment past lock;
  after lock expiry a correct password → `200` and counter resets (no email either way).

> **Coverage note:** the lock *mechanism* (5/30-min) and reset-unlocks behavior are tested in
> `celina-1-user-management.md`; this TC asserts only the **missing email channel**.

---

### Celina 2 — money movement, limits, cards, credit

#### TC-NOTIF-002 · Payment executed → in-app to both parties; email absent (gap)
- **Feature:** Notifikacije — "kada izvrši plaćanje" · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** client (sender); recipient client
- **Preconditions:** sender funded RSD account; recipient active RSD account at this bank
- **Request:** `POST /api/v3/me/payments` then `POST /api/v3/me/payments/:id/execute`
  - Auth: `Bearer <client>`
  - Body (create): `{"from_account_number":"<sender>","to_account_number":"<recipient>","amount":"1500.00","currency":"RSD"}`
- **Verification:** full-flow (→ `cross-cutting-verification.md`) or `verification.skip` employee-on-behalf
- **Expected:** `200`; payment `completed`. **Side-effects:** balances move (sender −amount−fees, recipient
  +amount), fees credited to bank RSD account, `transaction.payment-completed` published. **In-app:** sender gets
  `PAYMENT_SENT`, recipient gets `PAYMENT_RECEIVED` in `/me/notifications`. **Gap:** no `notification.send-email`
  emitted for either party (PDF asks for email "na isti email").
- **Negative siblings:** insufficient funds → `409 business_rule_violation`, payment `failed`, sender gets a
  `PAYMENT_FAILED` in-app item, recipient gets nothing.

#### TC-NOTIF-003 · Transfer executed → in-app to owner; email absent (gap)
- **Feature:** Notifikacije — "kada izvrši transfer" · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** client (owns both accounts)
- **Preconditions:** client has a funded RSD account and a second (e.g. EUR) own account
- **Request:** `POST /api/v3/me/transfers` then `POST /api/v3/me/transfers/:id/execute`
  - Auth: `Bearer <client>`
  - Body: `{"from_account_number":"<rsd>","to_account_number":"<eur>","amount":"1000.00"}`
- **Verification:** full-flow / skip
- **Expected:** `200`; transfer `completed`, FX applied, commission to bank. **In-app:** `TRANSFER_SENT` +
  `TRANSFER_RECEIVED`. **Gap:** no email.
- **Negative siblings:** cross-bank `to_account_number` rejected (`400`/`409`, intra-bank validation) → no
  notification; insufficient funds → `TRANSFER_FAILED` in-app only.

#### TC-NOTIF-004 · Limit changed → email + in-app (covered)
- **Feature:** Notifikacije — "kada se promeni limit" · **Spec:** TODO_final Celina 2 · **Existing test:** test-app/workflows/wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange
- **Actor:** employee (admin/supervisor) acting on a client
- **Preconditions:** activated client with an account
- **Request:** `PUT /api/v3/clients/:id/limits`
  - Auth: `Bearer <admin>`
  - Body: `{"daily_limit":"100000.00","monthly_limit":"500000.00","transfer_limit":"50000.00"}`
- **Verification:** n/a
- **Expected:** `200`. **Side-effects:** `client.limits-updated` published; `LIMIT_CHANGED` email
  (`notification.send-email`, `EmailType("LIMIT_CHANGED")`) to the client AND a `LIMIT_CHANGED` in-app item
  in `/me/notifications` (poll). Values clamped to the employee's `MaxClient*Limit`.
- **Negative siblings:** values over the employee's max → clamped (no error), notification still fires with
  clamped values; non-permitted role → `403` and no notification.

#### TC-NOTIF-005 · Card blocked → email + in-app (covered)
- **Feature:** Notifikacije — "blokira kartica" · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** employee (`cards.manage`)
- **Preconditions:** active client-owned card
- **Request:** `POST /api/v3/cards/:id/block`
  - Auth: `Bearer <employee>`
- **Verification:** n/a
- **Expected:** `200`, card `status=blocked`. **Side-effects:** `card.status-changed` published; **email**
  `EmailTypeCardStatusChanged` (with masked last-four + new_status) to the card owner; **in-app**
  `CARD_STATUS_CHANGED` (client-owned cards only; `RefType=card`). Bank-owned card → no in-app (no end user).
- **Negative siblings:** already-blocked → `409 conflict`, no new notification; deactivated card → `409`, no
  notification; card not found → `404`.

#### TC-NOTIF-006 · Card unblocked → email + in-app (covered)
- **Feature:** card status change notify · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** employee (`cards.manage`) — note unblock is **employee-only** (TC-MOBILE-003)
- **Preconditions:** a blocked client-owned card
- **Request:** `POST /api/v3/cards/:id/unblock` · Auth: `Bearer <employee>`
- **Expected:** `200`, `status=active`; **email** + **in-app** `CARD_STATUS_CHANGED` as in TC-NOTIF-005.
- **Negative siblings:** card not currently blocked → `409 conflict` (`ErrCardNotBlocked`), no notification.

#### TC-NOTIF-007 · Credit created → in-app; email absent (gap)
- **Feature:** Notifikacije — "kada se kreira kredit" · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** client (or employee-on-behalf)
- **Preconditions:** client with an eligible account
- **Request:** `POST /api/v3/me/loan-requests`
  - Auth: `Bearer <client>`
  - Body: `{"loan_type":"cash","amount":"500000.00","term_months":24,"account_number":"<acc>","interest_type":"fixed"}`
- **Expected:** `201`; loan request `pending`. **Side-effects:** `credit.loan-requested` published; **in-app**
  `LOAN_REQUEST_SUBMITTED`. **Gap:** no email.
- **Negative siblings:** invalid `loan_type` → `400 validation_error`; amount ≤ 0 → `400`; no notification.

#### TC-NOTIF-008 · Credit approved → in-app (+disbursed); email absent (gap)
- **Feature:** Notifikacije — "kada se odobri kredit" · **Spec:** TODO_final Celina 2 · **Existing test:** —
- **Actor:** employee within `MaxLoanApprovalAmount`
- **Preconditions:** a pending loan request; bank has liquidity
- **Request:** `POST /api/v3/loan-requests/:id/approve` · Auth: `Bearer <employee>`
- **Expected:** `200`; loan created + disbursed (saga). **Side-effects:** `credit.loan-approved` +
  `credit.loan-disbursed` published; balance credited to borrower; **in-app** `LOAN_REQUEST_APPROVED` and
  `LOAN_DISBURSED`. **Gap:** `EmailTypeLoanApproved` is defined in `contract/kafka` but **never published**
  → no approval email; assert its absence.
- **Negative siblings:** amount over approver's `MaxLoanApprovalAmount` → `403`/`409`, no notification;
  reject path `POST /api/v3/loan-requests/:id/reject` → in-app `LOAN_REQUEST_REJECTED`, `EmailTypeLoanRejected`
  also defined-but-unused (email absent).

---

### Celina 3 — orders, alerts, dividends, tax, DCA

> **PDF Celina 3 explicitly asks for "email i mobilne notifikacije" on every order lifecycle event.** The
> implementation delivers **in-app only** for all of them, and **no push** at all — so TC-NOTIF-009..014 are
> each `partial` (email + push gaps).

#### TC-NOTIF-009 · Order created → Pending → in-app `ORDER_PLACED` (email/push gap)
- **Feature:** order needs-approval notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** agent (used+order over daily limit, or `need_approval=true`)
- **Preconditions:** agent with a limit that this order exceeds; exchange open
- **Request:** `POST /api/v3/orders`
  - Auth: `Bearer <agent>`
  - Body: `{"listing_id":<id>,"direction":"buy","order_type":"market","quantity":100,"account_number":"<acc>"}`
- **Expected:** `201`; order `status=pending` (awaiting supervisor). **Side-effects:** `stock.order-created`
  published; **in-app** `ORDER_PLACED` to the agent. **Gap:** no email, no push.
- **Negative siblings:** order **within** limit → auto-approved, fires the fill/approval path instead of Pending
  (still no email); exchange closed → `409` ("Berza je zatvorena"), no notification.

#### TC-NOTIF-010 · Order approved → in-app `ORDER_APPROVED` (email/push gap)
- **Feature:** supervisor approve notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** supervisor
- **Preconditions:** a pending order (TC-NOTIF-009)
- **Request:** `POST /api/v3/orders/:id/approve` · Auth: `Bearer <supervisor>`
- **Expected:** `200`. **Side-effects:** `stock.order-approved` published; **in-app** `ORDER_APPROVED` to the
  order owner. **Gap:** no email/push.
- **Negative siblings:** approving an already-decided order → `409` (illegal transition); settlement date passed
  → decline-only (`409` on approve); neither emits a notification.

#### TC-NOTIF-011 · Order rejected → in-app `ORDER_DECLINED` (email/push gap)
- **Feature:** supervisor decline notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** supervisor
- **Request:** `POST /api/v3/orders/:id/reject` · Auth: `Bearer <supervisor>`
- **Expected:** `200`; `stock.order-declined` published; **in-app** `ORDER_DECLINED`. **Gap:** no email/push.
- **Negative siblings:** double-decline → `409`; no notification.

#### TC-NOTIF-012 · Order fully executed (isDone) → in-app `ORDER_FILLED` (email/push gap)
- **Feature:** order fully-filled notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** client/agent owner (in-app fires for client-owned orders only)
- **Preconditions:** an approved/active order; `testing_mode` on for fast fill (see 00-setup)
- **Request:** (no direct call) the fill engine reaches `quantity` → order `isDone`
- **Expected:** `stock.order-filled` published; **in-app** `ORDER_FILLED` to the owner. Bank-owned orders → no
  in-app item. **Gap:** no email/push.
- **Negative siblings:** bank-owned order fill → no end-user notification (correct); partial fill fires
  `ORDER_PARTIALLY_FILLED` instead (TC-NOTIF-013).

#### TC-NOTIF-013 · Order partially filled → in-app `ORDER_PARTIALLY_FILLED` (email/push gap)
- **Feature:** partial-fill notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** client/agent owner
- **Preconditions:** a limit order that fills in tranches
- **Expected:** one `ORDER_PARTIALLY_FILLED` in-app item **per fill** (with `filled_quantity`), then
  `ORDER_FILLED` on the final tranche. **Gap:** no email/push.
- **Negative siblings:** AON order → never partials (fail/pending instead), so no `ORDER_PARTIALLY_FILLED`.

#### TC-NOTIF-014 · Order auto-cancelled → in-app `ORDER_CANCELLED` (email/push gap; trigger gap)
- **Feature:** auto-cancel notify (settlement expired) · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** order owner
- **Preconditions:** an open/unfilled order
- **Request:** cancel path (`POST /api/v3/orders/:id/cancel` for the unfilled portion)
- **Expected:** `200`; `stock.order-cancelled` published; **in-app** `ORDER_CANCELLED`. **Gap:** no email/push;
  **trigger gap:** no dedicated *settlement-date* auto-cancel cron was found — the PDF's "istekao settlement
  date" auto-cancel maps onto the generic cancel notification only (flag as partial).
- **Negative siblings:** cancelling a fully-filled order → `409`; no notification.

#### TC-NOTIF-015 · Price-alert fired → in-app `PRICE_ALERT_TRIGGERED` (email/push gap)
- **Feature:** Price Alert · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** client/aktuar (alert owner)
- **Preconditions:** a `PriceAlert` (e.g. `condition=price_gte, threshold=200`, or `daily_change_pct_lte=-5`)
  created via the price-alert endpoint; a price refresh that crosses the threshold (`testing_mode` to move price)
- **Request:** (cron) price-refresh evaluates active alerts
- **Expected:** **in-app** `PRICE_ALERT_TRIGGERED` (with `ticker`, `condition`, `threshold`, `price`,
  `daily_change_percent`). **Gap:** no email/push (PDF lists email + mobile as alert delivery types).
- **Negative siblings:** threshold not crossed → no notification; `isActive=false` alert → no notification;
  bank-owned holding (no `OwnerID`) → skipped.

#### TC-NOTIF-016 · Dividend received → NO notification (NO-ENDPOINT gap)
- **Feature:** Isplata dividendi notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** holder of a dividend-paying stock
- **Preconditions:** client holds a stock with `DividendYield>0`; quarterly dividend cron runs
- **Request:** (cron) `dividend_service` pays `Quantity × Price × (Yield/4)` to the buy-account (fallback RSD)
- **Expected:** dividend credited + recorded as capital gain (15% tax tracking). **Gap:** `dividend_service.go`
  publishes **no** `GeneralNotificationMessage` and **no** email — the holder is never notified. Assert: account
  balance increases but `/me/notifications` gains **no** dividend item and no email is scanned.
- **Negative siblings:** bank-held dividends → no 15% tax (goes to bank profit), and likewise no notification.

#### TC-NOTIF-017 · Tax deducted → NO notification (NO-ENDPOINT gap)
- **Feature:** Porez na kapitalnu dobit notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** taxed client
- **Preconditions:** client with realized capital gain in the month; monthly tax cron or
  supervisor manual trigger
- **Request:** (cron) `tax_service` computes 15% and auto-deducts (RSD, no commission)
- **Expected:** RSD account debited; `stock.tax-collected` event published. **Gap:** `tax_service.go` emits
  **no** in-app item and **no** email (only a Prometheus counter + the event topic, which no consumer turns into
  a notification). Assert balance drop with no `/me/notifications` tax item.
- **Negative siblings:** monthly net loss → no tax, no event, no notification.

#### TC-NOTIF-018 · Recurring-order skipped → in-app `RECURRING_ORDER_SKIPPED` (email/push gap)
- **Feature:** DCA insufficient-funds notify · **Spec:** TODO_final Celina 3 · **Existing test:** —
- **Actor:** client/aktuar owner of a `RecurringOrder`
- **Preconditions:** an active recurring order whose source account has insufficient funds at `NextRun`
- **Request:** (cron) recurring-order tick attempts a Market order, fails, advances `NextRun`
- **Expected:** order **not** placed; `NextRun` still advances; **in-app** `RECURRING_ORDER_SKIPPED` (with
  `reason`). **Gap:** no email/push (PDF: "klijent dobija notifikaciju, slično kao promašena rata kredita" —
  installment-failed *does* email, recurring-skip does not).
- **Negative siblings:** sufficient funds → `RECURRING_ORDER_EXECUTED` instead; paused/cancelled order → no tick,
  no notification.

---

### Celina 4 — OTC negotiation + option contracts

> **PDF Celina 4 explicitly asks for email** on OTC counter / accept-or-withdraw / contract-expiring-soon.
> The implementation delivers **in-app only** → TC-NOTIF-019..022 are each `partial` (email gap).

#### TC-NOTIF-019 · OTC counter-offer received → in-app `OTC_OFFER_COUNTERED` (email gap)
- **Feature:** OTC notifikacije — kontraponuda · **Spec:** TODO_final Celina 4 · **Existing test:** —
- **Actor:** two clients (or supervisor on bank behalf) in an option negotiation
- **Preconditions:** an open OTC option offer/negotiation chain between A and B
- **Request:** `POST /api/v3/otc/options/:id/counter` (or the negotiation counter route)
  - Auth: `Bearer <counterparty>`
  - Body: `{"quantity":"100","strike_price":"150.00","premium":"6.00","settlement_date":"2026-09-15"}`
- **Expected:** `200`; chain `status=countered`, revision appended (old→new + who/when). **In-app**
  `OTC_OFFER_COUNTERED` to the **other** party. **Gap:** no email.
- **Negative siblings:** counter after settlement date → `409`; non-participant → `403`; neither notifies.

#### TC-NOTIF-020 · OTC offer accepted → in-app `OTC_CONTRACT_CREATED` (email gap)
- **Feature:** OTC notifikacije — prihvatanje · **Spec:** TODO_final Celina 4 · **Existing test:** —
- **Actor:** accepting party
- **Preconditions:** an open negotiation; acceptor account funded for the premium
- **Request:** `POST /api/v3/otc/options/:id/accept` (first-accept-wins) · Auth: `Bearer <acceptor>`
  - Body: `{"account_id":<acceptorAccount>}`
- **Expected:** `200`; contract minted, premium debited buyer → credited seller, seller shares locked.
  `otc.contract-created` published; **in-app** `OTC_CONTRACT_CREATED` to both parties. **Gap:** no email.
- **Negative siblings:** competing sibling chains auto-cancel → losing bidders get `OTC_OFFER_CASCADE_CANCELLED`
  in-app (a present extra); accepting your own offer → `403` (`ErrOTCAcceptUnauthorized`); no email anywhere.

#### TC-NOTIF-021 · OTC offer withdrawn → in-app `OTC_OFFER_CANCELLED` (email gap)
- **Feature:** OTC notifikacije — odustajanje · **Spec:** TODO_final Celina 4 · **Existing test:** —
- **Actor:** the bidder who withdraws their chain
- **Preconditions:** an open/countered negotiation the actor initiated
- **Request:** the negotiation cancel/withdraw route · Auth: `Bearer <bidder>`
- **Expected:** chain removed for both sides; **in-app** `OTC_OFFER_CANCELLED` to the other party. **Gap:** no email.
- **Negative siblings:** withdrawing an already-accepted chain → `409`; non-owner withdraw → `403`; no notification.

#### TC-NOTIF-022 · Option contract expiring in N days → in-app `OTC_CONTRACT_EXPIRING_SOON` (email gap)
- **Feature:** OTC notifikacije — ugovor ističe za N dana · **Spec:** TODO_final Celina 4 · **Existing test:** —
- **Actor:** both contract parties (buyer + seller)
- **Preconditions:** an active option contract whose `settlement_date` is `warnDays` away
  (`otc_expiry_cron` with `WithExpiryWarning(N)`)
- **Request:** (cron) expiring-soon warning pass
- **Expected:** **in-app** `OTC_CONTRACT_EXPIRING_SOON` (with `ticker`, `settlement_date`, `days_remaining`) to
  buyer AND seller. **Gap:** no email (PDF: "obaveštenje kada opcioni ugovor ističe za N dana").
- **Negative siblings:** contract more than N days out → no warning; already exercised/expired → no warning;
  on the settlement date the cron instead fires `OTC_CONTRACT_EXPIRED`.

---

### Systemic push gap

#### TC-NOTIF-023 · Business events are never delivered by mobile push (NO-ENDPOINT, systemic)
- **Feature:** "mobilne notifikacije" channel · **Spec:** TODO_final Celina 2/3/4 (email **i mobilne**) · **Existing test:** —
- **Actor:** any client with an active mobile device + open WebSocket
- **Preconditions:** mobile device activated; WebSocket `GET /ws?token=<mobileJWT>&device_id=<id>` connected
- **Request:** trigger any business event from TC-NOTIF-002..022
- **Expected (gap):** the WebSocket receives **nothing** — `notification.mobile-push` is published **only** by
  `verification_consumer` for verification challenges. In-app business notifications land in the
  `general_notifications` table and are surfaced by **polling** `/me/notifications`, never pushed over the
  socket. Assert: after the trigger, `/me/notifications` gains the item but no `mobile-push` frame arrives.
- **Negative siblings:** the **one** thing that *does* push — a verification challenge — is covered positively
  in TC-MOBILE-020 and `cross-cutting-verification.md`.

#### TC-NOTIF-024 · Watchlist ±5% daily move → in-app `WATCHLIST_PRICE_MOVE` (present, covered in-app)
- **Feature:** Watchlist daily alert · **Spec:** TODO_final Celina 3 (watchlist) · **Existing test:** —
- **Actor:** client/aktuar with a watchlisted ticker
- **Preconditions:** ticker on the user's watchlist; daily change > ±5%
- **Request:** (cron) watchlist notification pass publishes `notification.watchlist-alert`
- **Expected:** **in-app** `WATCHLIST_PRICE_MOVE` (idempotent per `user_id+ticker+YYYYMMDD` via the partial
  unique index). No email/push (consistent with the in-app-only design).
- **Negative siblings:** |move| ≤ 5% → no alert; duplicate same-day delivery → deduped (idempotency key).

---

## 3. In-app notification API

Routes (`api-gateway/internal/router/router_v3.go`, `AnyAuthMiddleware`):
`GET /api/v3/me/notifications`, `GET /api/v3/me/notifications/unread-count`,
`POST /api/v3/me/notifications/read-all`, `POST /api/v3/me/notifications/:id/read`.

#### TC-NOTIF-030 · List own notifications (paginated, read-filterable) — POSITIVE
- **Feature:** in-app inbox list · **Spec:** TODO_final ("prikazivati i unutar aplikacije") · **Existing test:** test-app/workflows/wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange
- **Actor:** client (or any authenticated principal)
- **Preconditions:** ≥1 notification exists for the caller (e.g. via TC-NOTIF-004)
- **Request:** `GET /api/v3/me/notifications?page=1&page_size=20&read=unread` · Auth: `Bearer <client>`
- **Expected:** `200`; body `{"notifications":[{id,type,title,message,is_read,ref_type,ref_id,created_at}],"total":N}`.
  Items are the caller's own only; `read=unread` returns only `is_read=false`.
- **Negative siblings:** `read=read` returns only read items; no filter returns all; `page_size>100` clamped to 100.

#### TC-NOTIF-031 · Unread count — POSITIVE
- **Feature:** unread badge · **Spec:** TODO_final · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/me/notifications/unread-count` · Auth: `Bearer <client>`
- **Expected:** `200`; `{"unread_count":N}` where N = unread items for this principal. Drops by 1 after
  TC-NOTIF-032 and to 0 after TC-NOTIF-033.
- **Negative siblings:** unauthenticated → `401`.

#### TC-NOTIF-032 · Mark one read — POSITIVE
- **Feature:** mark-read · **Spec:** TODO_final · **Existing test:** —
- **Actor:** client (owner of the notification)
- **Preconditions:** an unread notification `:id` owned by the caller
- **Request:** `POST /api/v3/me/notifications/:id/read` · Auth: `Bearer <client>`
- **Expected:** `200` `{"success":true}`; that item `is_read=true`; unread-count decremented by 1.
- **Negative siblings:** non-numeric id → `400 validation_error`; already-read id → `200` (idempotent).

#### TC-NOTIF-033 · Mark all read — POSITIVE
- **Feature:** mark-all-read · **Spec:** TODO_final · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/notifications/read-all` · Auth: `Bearer <client>`
- **Expected:** `200` `{"success":true,"count":K}`; unread-count → 0; only the caller's items affected.
- **Negative siblings:** no unread items → `200` `count:0`.

#### TC-NOTIF-034 · Ownership — cannot read/mark another user's notification (NEGATIVE)
- **Feature:** inbox ownership · **Spec:** Resource Ownership requirement · **Existing test:** —
- **Actor:** client B
- **Preconditions:** notification `:id` belongs to client A
- **Request:** `POST /api/v3/me/notifications/:id/read` · Auth: `Bearer <clientB>`
- **Expected:** `404 not_found` (ownership scoped by JWT `principal_id`; existence not leaked — the `UserId`
  in the gRPC mark-read filter never matches B). B's `read-all` (TC-NOTIF-033) leaves A's items untouched.
- **Negative siblings:** B's `GET /me/notifications` never includes A's items; unauthenticated → `401`.

---

## 4. Mobile-app features ("Proširenje za mobilne aplikacije")

> Per PDF, these are "Ako timovima ostane dovoljno vremena". Test whatever the backend exposes; mark
> **NO-ENDPOINT** otherwise. Mobile auth (`POST /api/v3/mobile/auth/request-activation` →
> `/activate` → `/refresh`, `X-Device-ID` header, `system_type:"mobile"`) is exercised by
> `test-app/workflows/mobile_auth_test.go`; these feature TCs reuse a mobile token but the same routes are
> reachable with a browser client token under `AnyAuthMiddleware`.

#### TC-MOBILE-001 · View all own cards — POSITIVE
- **Feature:** Pregled kartica · **Spec:** TODO_final mobile §1 · **Existing test:** —
- **Actor:** client
- **Preconditions:** client has ≥1 card
- **Request:** `GET /api/v3/me/cards` (and `GET /api/v3/me/cards/:id` for detail) · Auth: `Bearer <client/mobile>`
- **Expected:** `200`; only the caller's cards (ownership from JWT); each carries status/brand/masked number.
- **Negative siblings:** `GET /api/v3/me/cards/:id` for a card owned by another client → `404`; unauth → `401`.

#### TC-MOBILE-002 · Block own card from mobile — POSITIVE
- **Feature:** blokiranje kartice (klijent) · **Spec:** TODO_final mobile §1 · **Existing test:** —
- **Actor:** client
- **Preconditions:** an active client-owned card
- **Request:** `POST /api/v3/me/cards/:id/temporary-block` · Auth: `Bearer <client/mobile>`
  - Body: `{"duration_hours":24,"reason":"lost phone"}`
- **Verification:** n/a (client self-service block)
- **Expected:** `200`; a `CardBlock` row with `ExpiresAt`; **in-app** `CARD_TEMPORARY_BLOCKED`; auto-unblock by
  the background goroutine after expiry. (This is the client-facing block; the permanent
  `POST /api/v3/cards/:id/block` is employee-only.)
- **Negative siblings:** another client's card → `403`/`404`; invalid duration → `400`; already-blocked → `409`.

#### TC-MOBILE-003 · Unblock NOT available from mobile (NEGATIVE — by design)
- **Feature:** "Deblokiranje … jedino bankarski službenik ili lično" · **Spec:** TODO_final mobile §1 · **Existing test:** —
- **Actor:** client
- **Request:** there is **no** `/api/v3/me/cards/:id/unblock` route; unblock lives at employee-only
  `POST /api/v3/cards/:id/unblock` (`AuthMiddleware` + `cards.manage`)
- **Expected:** a client token on `POST /api/v3/cards/:id/unblock` → `403 forbidden` (employee permission
  required), and no `/me` unblock route exists → confirms unblock is banker-only, matching the PDF.
- **Negative siblings:** temporary-block auto-expiry is the only client path back to active.

#### TC-MOBILE-004 · View all accounts + balances — POSITIVE
- **Feature:** Pregled računa i stanja · **Spec:** TODO_final mobile §2 · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/me/accounts` (and `GET /api/v3/me/accounts/:id`) · Auth: `Bearer <client/mobile>`
- **Expected:** `200`; only the caller's accounts, each with `available_balance` + currency.
- **Negative siblings:** `GET /api/v3/me/accounts/:id` for a non-owned account → `403`; unauth → `401`.

#### TC-MOBILE-005 · Transaction history (per account, paginated) — POSITIVE
- **Feature:** Istorija transakcija · **Spec:** TODO_final mobile §3 · **Existing test:** —
- **Actor:** client
- **Preconditions:** an account with ledger activity
- **Request:** `GET /api/v3/me/accounts/:id/activity?page=1&page_size=20` · Auth: `Bearer <client/mobile>`
- **Expected:** `200`; reverse-chronological ledger entries (`entry_type`, `amount`, `balance_before/after`,
  `reference_type` ∈ {order, tax, transfer, payment, …}), `total_count`; paginated (max page_size 200).
- **Negative siblings:** non-owned account → `403`; invalid id → `400`; account not found → `404`.

#### TC-MOBILE-006 · Per-card transaction history (NO-ENDPOINT)
- **Feature:** "istorijat za svaku karticu po stranici" · **Spec:** TODO_final mobile §3 · **Existing test:** —
- **Actor:** client
- **Request:** there is **no** `GET /api/v3/me/cards/:id/transactions` (or `/activity`) endpoint
- **Expected (gap):** the PDF asks for per-card transaction history paginated by card; the backend exposes
  history per **account** (TC-MOBILE-005) only. Card-scoped history → **NO-ENDPOINT**.
- **Negative siblings:** the account-activity route does not filter by `card_id`.

#### TC-MOBILE-007 · Menjačnica — calculate + all currencies — POSITIVE
- **Feature:** Menjačnica · **Spec:** TODO_final mobile §4 · **Existing test:** —
- **Actor:** client (public-ish read)
- **Request:** `POST /api/v3/exchange/calculate` with `{"from":"EUR","to":"RSD","amount":"100"}`; and
  `GET /api/v3/exchange/rates` for all available currency pairs · Auth: `Bearer <client/mobile>`
- **Expected:** `200`; calculator returns converted amount (RSD-pivoted, spread applied); rates list includes
  every bank-supported currency (RSD/EUR/CHF/USD/GBP/JPY/CAD/AUD pairs).
- **Negative siblings:** unknown currency → `400`/`404`; amount ≤ 0 → `400`.

#### TC-MOBILE-008 · Kursna lista — current rates — POSITIVE
- **Feature:** Kursna lista · **Spec:** TODO_final mobile §5 · **Existing test:** —
- **Actor:** client / public
- **Request:** `GET /api/v3/exchange/rates` (and `GET /api/v3/exchange/rates/:from/:to`)
- **Expected:** `200`; current buy/sell per pair, with rate source/timestamp.
- **Negative siblings:** unknown pair on `:from/:to` → `404`.

#### TC-MOBILE-009 · Kursna lista — last-30-days history (NO-ENDPOINT)
- **Feature:** "pregled kursne liste u zadnjih mesec dana" · **Spec:** TODO_final mobile §5 · **Existing test:** —
- **Actor:** client
- **Request:** there is **no** historical exchange-rate endpoint (`/api/v3/exchange/rates/history` or per-pair
  `?from=&to=&days=30`). `exchange-service` stores only the current rate per pair (versioned upsert), not a
  daily series.
- **Expected (gap):** 30-day kursna-lista history → **NO-ENDPOINT**. (Securities *do* have history at
  `GET /api/v3/securities/.../history`, but FX rates do not.)
- **Negative siblings:** —

#### TC-MOBILE-010 · Upcoming loan-installment view — POSITIVE
- **Feature:** Prikaz rate kredita · **Spec:** TODO_final mobile §6 · **Existing test:** —
- **Actor:** client with an active loan
- **Preconditions:** disbursed loan with a generated installment schedule
- **Request:** `GET /api/v3/me/loans` then `GET /api/v3/me/loans/:id/installments` · Auth: `Bearer <client/mobile>`
- **Expected:** `200`; installment schedule with each row's amount + due date + status; the next-due installment
  is identifiable (earliest unpaid) — satisfies "koliko je predstojeća rata".
- **Negative siblings:** another client's loan → `403`/`404`; loan with no schedule yet → empty list.

---

## 5. Quick Approve

> **PDF mobile §7:** "klijent može da odobri akciju direktno sa notifikacije umesto da kuca kod. Ako klijent
> ne reaguje u roku od 5 minuta, zahtev ističe. Quick Approve se primenjuje na sve akcije koje zahtevaju
> verifikaciju." See `cross-cutting-verification.md` for the full challenge mechanism. Mapping to the
> implementation: the closest live primitive is **biometric approval** —
> `POST /api/v3/mobile/verifications/:id/biometric` — which verifies a challenge **without typing a code**.
> The 5-minute window is the standard `VERIFICATION_CHALLENGE_EXPIRY=5m`. There is **no** dedicated
> "approve-from-push-button" endpoint (TC-MOBILE-023).

#### TC-MOBILE-020 · Quick Approve via biometric (approve without typing a code) — POSITIVE
- **Feature:** Quick Approve · **Spec:** TODO_final mobile §7 · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile
- **Actor:** client on an activated mobile device (biometrics enabled)
- **Preconditions:** a gated action pending verification (e.g. a payment created via `POST /api/v3/me/payments`
  in `pending_verification`); a challenge created via `POST /api/v3/verifications`; the challenge appears in
  `GET /api/v3/mobile/verifications/pending` (delivered via `notification.mobile-push` over WebSocket)
- **Request:** `POST /api/v3/mobile/verifications/:id/biometric` · Auth: `Bearer <mobileJWT>` + `X-Device-ID`
- **Verification:** this IS the verification step
- **Expected:** `200`; challenge `verified`; `verification.challenge-verified` published; the pending payment
  becomes executable (`POST /api/v3/me/payments/:id/execute`) → funds move. No 6-digit code was ever typed.
- **Negative siblings:** biometrics disabled on the device → `403`/`400`; wrong device id → `403` device
  mismatch; challenge already verified/cancelled → `409`.

#### TC-MOBILE-021 · Quick Approve expires after 5 minutes of no response (NEGATIVE)
- **Feature:** 5-minute expiry · **Spec:** TODO_final mobile §7 · **Existing test:** —
- **Actor:** client (does nothing)
- **Preconditions:** a pending challenge created at T0 with `VERIFICATION_CHALLENGE_EXPIRY=5m`
- **Request:** wait > 5 min, then `GET /api/v3/verifications/:id/status`
- **Expected:** challenge `status=expired`; `verification.challenge-failed` published with
  `reason="expired"`; the gated action (payment/transfer) is cancelled (transaction-service consumes the
  failure). No approval occurs.
- **Negative siblings:** the challenge no longer appears in `GET /api/v3/mobile/verifications/pending`.

#### TC-MOBILE-022 · Approve after expiry is rejected (NEGATIVE)
- **Feature:** approve-after-expiry guard · **Spec:** TODO_final mobile §7 · **Existing test:** —
- **Actor:** client on mobile
- **Preconditions:** the challenge from TC-MOBILE-021 (already expired)
- **Request:** `POST /api/v3/mobile/verifications/:id/biometric` (or `/submit` with the right code) · Auth: mobile
- **Expected:** `409 business_rule_violation` (or `400`) — expired challenge cannot be approved; the gated
  action stays cancelled; balances unchanged.
- **Negative siblings:** submitting a code to an expired challenge → same rejection; exceeding
  `VERIFICATION_MAX_ATTEMPTS=3` before expiry → challenge fails (`max_attempts_exceeded`), action cancelled.

#### TC-MOBILE-023 · Dedicated "approve directly from push" action button (NO-ENDPOINT / partial)
- **Feature:** "odobri akciju direktno sa notifikacije" · **Spec:** TODO_final mobile §7 · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending
- **Actor:** client on mobile
- **Request:** there is **no** single push-payload-embedded "Approve" endpoint distinct from the verification
  flow. The push frame (`notification.mobile-push`) carries the challenge; approval still goes through
  `POST /api/v3/mobile/verifications/:id/biometric` (no code) or `/submit` (code). `POST .../:id/ack` only
  marks the inbox item delivered — it does **not** approve.
- **Expected (partial):** "approve without typing a code" is satisfied by biometric (TC-MOBILE-020), and the
  5-minute expiry holds — but a one-tap "Quick Approve" button bound to the push notification itself is
  **NO-ENDPOINT**; `ack` is delivery-only, not approval.
- **Negative siblings:** calling `ack` and expecting the action to proceed → action stays
  `pending_verification` (ack ≠ approve).

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| C1 account-locked email | TC-NOTIF-001 | — | NO-ENDPOINT |
| C2 payment-executed notify (email+inapp+push) | TC-NOTIF-002 | — | partial (in-app only) |
| C2 transfer-executed notify | TC-NOTIF-003 | — | partial (in-app only) |
| C2 limit-changed notify | TC-NOTIF-004 | wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange | covered (email+in-app) |
| C2 card-blocked notify | TC-NOTIF-005 | — | covered (email+in-app) |
| C2 card-unblocked notify | TC-NOTIF-006 | — | covered (email+in-app) |
| C2 credit-created notify | TC-NOTIF-007 | — | partial (in-app only) |
| C2 credit-approved notify | TC-NOTIF-008 | — | partial (in-app only; LoanApproved email defined-but-unused) |
| C3 order-created→Pending notify | TC-NOTIF-009 | — | partial (in-app only) |
| C3 order-approved notify | TC-NOTIF-010 | — | partial (in-app only) |
| C3 order-rejected notify | TC-NOTIF-011 | — | partial (in-app only) |
| C3 order-fully-executed notify | TC-NOTIF-012 | — | partial (in-app only) |
| C3 order-partially-filled notify | TC-NOTIF-013 | — | partial (in-app only) |
| C3 order-auto-cancelled notify | TC-NOTIF-014 | — | partial (in-app only; no settlement-expiry auto-cancel cron) |
| C3 price-alert-fired notify | TC-NOTIF-015 | — | partial (in-app only) |
| C3 dividend-received notify | TC-NOTIF-016 | — | NO-ENDPOINT |
| C3 tax-deducted notify | TC-NOTIF-017 | — | NO-ENDPOINT |
| C3 recurring-order-skipped notify | TC-NOTIF-018 | — | partial (in-app only) |
| C4 OTC counter-offer notify | TC-NOTIF-019 | — | partial (in-app only) |
| C4 OTC offer-accepted notify | TC-NOTIF-020 | — | partial (in-app only) |
| C4 OTC offer-withdrawn notify | TC-NOTIF-021 | — | partial (in-app only) |
| C4 option-contract-expiring-N-days notify | TC-NOTIF-022 | — | partial (in-app only) |
| Mobile push for business events (systemic) | TC-NOTIF-023 | — | NO-ENDPOINT |
| Watchlist ±5% daily-move notify (bonus) | TC-NOTIF-024 | — | covered (in-app) |
| In-app notifications: list | TC-NOTIF-030 | wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange | covered |
| In-app notifications: unread-count | TC-NOTIF-031 | — | covered |
| In-app notifications: mark-read | TC-NOTIF-032 | — | covered |
| In-app notifications: mark-all-read | TC-NOTIF-033 | — | covered |
| In-app notifications: ownership | TC-NOTIF-034 | — | covered |
| Mobile: view own cards | TC-MOBILE-001 | — | covered |
| Mobile: block own card | TC-MOBILE-002 | — | covered (temporary-block) |
| Mobile: unblock not allowed (banker-only) | TC-MOBILE-003 | — | covered (by-design) |
| Mobile: view accounts + balances | TC-MOBILE-004 | — | covered |
| Mobile: transaction history (per account) | TC-MOBILE-005 | — | covered |
| Mobile: per-card transaction history | TC-MOBILE-006 | — | NO-ENDPOINT |
| Mobile: menjačnica calculate + currencies | TC-MOBILE-007 | — | covered |
| Mobile: kursna lista current | TC-MOBILE-008 | — | covered |
| Mobile: kursna lista 30-day history | TC-MOBILE-009 | — | NO-ENDPOINT |
| Mobile: upcoming loan-installment view | TC-MOBILE-010 | — | covered |
| Quick Approve: approve without code (biometric) | TC-MOBILE-020 | wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile | covered |
| Quick Approve: 5-minute expiry | TC-MOBILE-021 | — | covered |
| Quick Approve: approve-after-expiry rejected | TC-MOBILE-022 | — | covered |
| Quick Approve: dedicated push-button approve | TC-MOBILE-023 | wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending | partial (biometric only; ack≠approve) |
```

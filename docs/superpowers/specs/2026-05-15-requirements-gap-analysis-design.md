# Requirements Gap Analysis — `docs/requirements.docx`

**Date:** 2026-05-15
**Source:** `docs/requirements.docx`
**Scope:** Identify every feature in the requirements document that is NOT yet implemented in the EXBanka backend, then propose a plan grouping for each.

This document is the parent design for nine follow-up plans located in `docs/superpowers/plans/2026-05-15-req-*.md`.

---

## Source document inventory

`docs/requirements.docx` contains nine concrete feature proposals across two cohort cells:

| # | Feature | Cell | Source ¶ |
|---|---|---|---|
| 1 | Watchlist for securities | Celina 3 | "Watchlist", §1 |
| 2 | System audit log | Celina 3 | "Audit log" |
| 3 | Recurring securities orders | Celina 3 | "Recurring order" |
| 4 | OTC negotiation history | Celina 3 | "Istorija pregovora" |
| 5 | OTC trader rating system | Celina 3 | "Rating sistem za OTC trgovce" |
| 6 | Real-time inter-bank transaction status | Celina 4 | "Status međubankarske transakcije..." |
| 7 | Price alerts | Celina 3 | §2 "Sistem cenovnih obaveštenja" |
| 8 | Recurring fund investments (DCA) | Celina 4 | §1 "Automatsko ponavljano investiranje" |
| 9 | Closed-End Funds | Celina 4 | "Zatvoreni fondovi" |

---

## Current state — what is and isn't implemented

### 1. Watchlist — NOT implemented

No `Watchlist`/`watchlists` table, no `watchlist_items` table, no endpoint, no gRPC. Closest existing entities are `holdings` (actual owned position) and the listings catalogue. We need a separate join table per user keyed on `listing_id` (which covers stocks, options, futures, forex pairs).

### 2. System audit log — partially exists

`changelogs` tables already exist per service (`accounts`, `cards`, `clients`, `loans`, `employees`) with read endpoints at `/api/v3/{resource}/:id/changelog`. What's missing relative to the requirement:

- **Agent limit changes**: `employee_limits` updates are not yet recorded in any changelog.
- **usedLimit resets**: `used_limit` decrements/resets on employee limit are not logged.
- **Order approve/reject**: order state changes are not in `changelogs`.
- **Manual tax run trigger**: who/when invoked the tax-collection endpoint is not recorded.
- **Aggregated admin endpoint**: there is no single endpoint for admins/supervisors to filter the union of audit events by `actor_id` / `action_type` / date range across services. Each changelog endpoint is per-entity.

### 3. Recurring securities orders — NOT implemented

No `recurring_orders` table, no scheduler in `stock-service`, no endpoint.

### 4. OTC negotiation history — partially possible

`otc_offers` rows persist with statuses (`pending`, `accepted`, `rejected`, `cancelled`, `expired`). What's missing is a dedicated "history" endpoint surfacing only terminal statuses with filters by status / date / counterparty. The current list endpoint defaults to active offers.

### 5. OTC trader rating — NOT implemented

No `otc_trader_ratings` table, no endpoints, no average display on portal.

### 6. Real-time SI-TX status — partially exists

`outbound_peer_tx` stores SI-TX state (`pending`, `completed`, `failed`, `compensating`, …) and the saga log records phase transitions. What's missing:

- A client-facing read endpoint exposing the status of a specific outbound transfer (currently the only client-facing transfer endpoints return `transfers.status` which collapses SI-TX progress).
- In-app notification (or WS push) on each significant transition (Initiated → Pending → Completed / Failed).

The notification-coverage work (Plans B1-B5) already wired `TRANSFER_SENT` / `TRANSFER_RECEIVED` / `TRANSFER_FAILED`, but those fire only at terminal commit on the local side. The cross-bank intermediate states aren't surfaced.

### 7. Price alerts — NOT implemented

No `price_alerts` table, no condition checker, no scheduler hook into price refresh.

### 8. Recurring fund investments — NOT implemented

No `recurring_fund_investments` table, no scheduler creating `fund_contributions` on schedule.

### 9. Closed-End Funds — NOT implemented

`investment_funds.go` has no `fund_type`, no `fundraising_start`, `fundraising_end`, `maturity_date`, `target_amount`, no `fund_status` lifecycle. Today every fund is implicitly open-ended.

---

## Cross-cutting concerns

### Notifications

Every new feature surfaces user-visible events. Each plan must include in-app notification emits via the new `Data` form on `notification.general` (see the notification-coverage design doc):

| Feature | Notification keys |
|---|---|
| Watchlist | — (UX-only) |
| Audit log | — (read-only) |
| Recurring order | `RECURRING_ORDER_EXECUTED`, `RECURRING_ORDER_SKIPPED`, `RECURRING_ORDER_PAUSED` |
| Negotiation history | — (read-only) |
| OTC rating | `OTC_RATING_RECEIVED` |
| SI-TX status | `SI_TX_INITIATED`, `SI_TX_PENDING`, `SI_TX_COMPLETED`, `SI_TX_FAILED` |
| Price alerts | `PRICE_ALERT_TRIGGERED` |
| Recurring fund invest | `FUND_RECURRING_EXECUTED`, `FUND_RECURRING_SKIPPED` |
| Closed-End Funds | `FUND_FUNDRAISING_STARTED`, `FUND_FUNDRAISING_CLOSED`, `FUND_MATURED`, `FUND_LIQUIDATED`, `FUND_INVESTOR_PAYOUT` |

All new template keys need entries in `notification-service/internal/template/registry.go` (push channel at minimum, email where the requirement explicitly says "e-mail").

### Schedulers

Three new background loops:

- **Recurring securities orders** — tick once per day (configurable), materialize due rows from `recurring_orders`.
- **Recurring fund investments** — tick once per day, fire on configured day-of-month.
- **Closed-End Fund phase transitions** — tick once per day, transition `fundraising → active → matured → liquidated` based on dates; enforce 7-day liquidation grace window with auto-Sell Markets at the end.

A fourth loop is implicit in price alerts but it piggybacks on the existing price-refresh path in `stock-service/internal/source/`.

All background goroutines follow the CLAUDE.md pattern: `context.Context` cancellation, `defer ticker.Stop()`, no bare `time.Sleep`.

### Permissions

New permissions to seed in `user-service/internal/service/role_service.go`:

| Permission | Roles |
|---|---|
| `watchlist.read.my` / `watchlist.write.my` | `client`, `EmployeeBasic+` |
| `audit_log.read.all` | `EmployeeSupervisor`, `EmployeeAdmin` |
| `recurring_order.read.my` / `recurring_order.write.my` | `client`, `EmployeeAgent+` |
| `otc_rating.write.my` | `client`, `EmployeeBasic+` |
| `price_alert.read.my` / `price_alert.write.my` | `client`, `EmployeeBasic+` |
| `recurring_fund.read.my` / `recurring_fund.write.my` | `client`, `EmployeeBasic+` |
| `fund.manage.closed` | `EmployeeSupervisor`, `EmployeeAdmin` |

### Spec / docs updates

Every plan ends with the mandatory Specification.md update (Sections 6/17/18/19/20/21 as applicable per CLAUDE.md), Swagger regen, REST_API_v1.md additions, and v3-only route registration (per the standing user memory).

---

## Plan grouping

Nine sibling plans, all under `docs/superpowers/plans/2026-05-15-req-*.md`. They are mostly independent and can be executed in any order, with the exception of "Closed-End Funds" which should land before "Recurring Fund Investments" (so the new lifecycle constraints are respected by the scheduler).

Suggested ordering by risk/value (lowest risk, highest standalone value first):

1. `req-watchlist.md`
2. `req-otc-negotiation-history.md`
3. `req-otc-rating.md`
4. `req-price-alerts.md`
5. `req-sitx-realtime-status.md`
6. `req-recurring-securities-order.md`
7. `req-audit-log.md`
8. `req-closed-end-funds.md` (must precede #9)
9. `req-recurring-fund-investments.md`

Each plan is self-contained: spec rules, schema, service wiring, handler, gateway routes, notifications, scheduler (if any), tests, doc updates, Specification.md sections to bump.

---

## Out of scope (explicitly)

- UI work on the mobile/web frontend. We expose the APIs and notifications; frontend is a separate cohort effort.
- Push notification provider integration beyond the existing in-app inbox + WS path.
- Cross-bank surfacing of these new features to peer banks (no SI-TX wire-format extension needed — these are within-bank features).
- "Quick order from watchlist" — pure frontend convenience; nothing to add on the backend beyond watchlist read.
- Visual badging of closed-end funds on Discovery page — frontend concern. We expose `fund_type` and `fund_status` and any aggregate counters; client renders.

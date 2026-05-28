# New and Updated Routes — Session 2026-05-28 → 2026-05-29

Branch: Development. Commit range: `117e0cd..fb03f95`. Total session commits: ~94.

## Quick summary

- 28 new routes
- 9 existing routes with changed shape or behavior
- 10 removed routes (replaced by canonical `/cross-bank-protocol/` prefix)

---

## New routes (by domain)

### Admin / Cron viewer (Plan C — commits `55b9f1b`…`c88921a`)

| Method | Path | Auth | Permission | Purpose | Commit |
|---|---|---|---|---|---|
| GET | /api/v3/admin/crons | AuthMiddleware | admin.crons.view | List all crons across all services (fan-out) | c88921a |
| GET | /api/v3/admin/crons/:service/:name | AuthMiddleware | admin.crons.view | Get one cron's state and history | c88921a |
| POST | /api/v3/admin/crons/:service/:name/trigger | AuthMiddleware | admin.crons.trigger | Manually trigger a cron immediately | c88921a |
| POST | /api/v3/admin/crons/:service/:name/pause | AuthMiddleware | admin.crons.manage | Pause a cron (persisted across restarts) | c88921a |
| POST | /api/v3/admin/crons/:service/:name/resume | AuthMiddleware | admin.crons.manage | Resume a paused cron | c88921a |

All five require EmployeeAdmin (`admin.crons.*` permissions seeded in commit `29898a1`). Trigger/pause/resume actions are written to the admin audit log (Kafka → notification-service consumer, commit `f1278dc`).

### Admin / Audit logs (Plan D — commits `3312f35`…`85ca0aa`)

| Method | Path | Auth | Permission | Purpose | Commit |
|---|---|---|---|---|---|
| GET | /api/v3/admin/audit/clients-changelog | AuthMiddleware | admin.audit.view | Full changelog from client-service | 85ca0aa |
| GET | /api/v3/admin/audit/accounts-changelog | AuthMiddleware | admin.audit.view | Full changelog from account-service | 85ca0aa |
| GET | /api/v3/admin/audit/cards-changelog | AuthMiddleware | admin.audit.view | Full changelog from card-service | 85ca0aa |
| GET | /api/v3/admin/audit/loans-changelog | AuthMiddleware | admin.audit.view | Full changelog from credit-service | 85ca0aa |
| GET | /api/v3/admin/audit/employees-changelog | AuthMiddleware | admin.audit.view | Full changelog from user-service | 85ca0aa |
| GET | /api/v3/admin/audit/cron-actions | AuthMiddleware | admin.audit.view | Cron trigger/pause/resume audit log | 85ca0aa |

All six require EmployeeAdmin (`admin.audit.view` permission seeded in commit `3312f35`). Common query params: `page`, `page_size`, `since`, `until`, `actor_id`, `action`. See REST_API_v3.md §50 for response shapes.

### Cross-bank protocol — canonical prefix (Plan F + H — commits `6abf95a`…`4c723de`)

All routes were previously mounted at `/api/v3/interbank`, `/api/v3/public-stock`, etc. Plan F dual-mounted them under `/api/v3/cross-bank-protocol/…`; Plan H (commit `4c723de`) removed the legacy aliases entirely.

| Method | Path | Auth | Purpose | Commit |
|---|---|---|---|---|
| POST | /api/v3/cross-bank-protocol/interbank | PeerAuth | SI-TX NEW_TX / COMMIT_TX / ROLLBACK_TX envelope | 6abf95a |
| GET | /api/v3/cross-bank-protocol/interbank/:transaction_id/status | PeerAuth | CHECK_STATUS — query cross-bank TX state | af7dcf9 |
| GET | /api/v3/cross-bank-protocol/public-stock | PeerAuth | Peer-facing OTC stock discovery | 6abf95a |
| GET | /api/v3/cross-bank-protocol/public-option-offers | PeerAuth | Peer-facing OTC option discovery | 6abf95a |
| POST | /api/v3/cross-bank-protocol/negotiations | PeerAuth | Peer initiates a cross-bank OTC negotiation | 6abf95a |
| PUT | /api/v3/cross-bank-protocol/negotiations/:rid/:id | PeerAuth | Counter-offer on existing negotiation | 6abf95a |
| GET | /api/v3/cross-bank-protocol/negotiations/:rid/:id | PeerAuth | Read negotiation state | 6abf95a |
| DELETE | /api/v3/cross-bank-protocol/negotiations/:rid/:id | PeerAuth | Cancel negotiation | 6abf95a |
| GET | /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept | PeerAuth | Accept negotiation (triggers 4-posting SI-TX) | 6abf95a |
| GET | /api/v3/cross-bank-protocol/user/:rid/:id | PeerAuth | Counterparty user identity lookup | 6abf95a |

PeerAuth = `X-Api-Key` or HMAC bundle (`X-Bank-Code` + `X-Bank-Signature` + `X-Timestamp` + `X-Nonce`).

### Dividends (Plan E — commits `dbd3436`…`0322ec2`)

| Method | Path | Auth | Permission | Purpose | Commit |
|---|---|---|---|---|---|
| POST | /api/v3/admin/dividends | AuthMiddleware | securities.manage.catalog | Declare a dividend for a security | 944c087 |
| POST | /api/v3/admin/dividends/:id/payout | AuthMiddleware | securities.manage.catalog | Fan-out dividend payout to all holders | 944c087 |
| GET | /api/v3/me/dividends | AnyAuthMiddleware | (any valid JWT) | Caller's paginated dividend payout history | 944c087 |
| GET | /api/v3/investment-funds/:id/dividends | AnyAuthMiddleware | (any valid JWT) | Paginated fund_dividend_payments for a fund | 944c087 |

### Unified Portfolio (Plan B — commits `81f4234`…`c5a6ee0`)

| Method | Path | Auth | Permission | Purpose | Commit |
|---|---|---|---|---|---|
| GET | /api/v3/portfolio/:portfolio_id | AuthMiddleware | portfolio.view.client / portfolio.view.fund | Generic portfolio by `client-<n>`, `bank`, or `fund-<n>` | 39a900e |
| GET | /api/v3/portfolio/bank | AuthMiddleware | (any employee) | Bank portfolio shortcut | 39a900e |
| GET | /api/v3/portfolio/client/:client_id | AuthMiddleware | portfolio.view.client | Client portfolio shortcut | 39a900e |
| GET | /api/v3/portfolio/investment-fund/:fund_id | AuthMiddleware | portfolio.view.fund | Fund portfolio shortcut | 39a900e |
| GET | /api/v3/watchlist/:portfolio_id | AuthMiddleware | portfolio.view.client / portfolio.view.fund | Watchlist for any portfolio owner | 5cce4fd |

### Negotiation history (Plan C14 — commits `8bbd558`…`de8c2a7`)

| Method | Path | Auth | Permission | Purpose | Commit |
|---|---|---|---|---|---|
| GET | /api/v3/me/otc/options/negotiations/:nid/revisions | AnyAuthMiddleware | (party to chain) | Full revision chain (bid/counter/accept/reject) for one negotiation | de8c2a7 |

---

## Updated routes (existing route, changed shape or behavior)

| Method | Path | What changed | Commits |
|---|---|---|---|
| GET | /api/v3/me/portfolio | Now returns unified grouped shape: `{ portfolio_id, owner_type, securities: { positions }, funds: { positions } }` with P/L totals. Old flat `{ holdings: [...] }` shape is retired. | 39a900e, c5a6ee0 |
| GET | /api/v3/me/otc/options/negotiations | `OTCNegotiationResponse` now includes `minted_contract_id` (uint64, non-zero on accepted rows that successfully formed a contract) | 87c32ff, f5f18e8 |
| GET | /api/v3/me/peer-otc/negotiations | `PeerNegotiationListItem` now includes `local_contract_id` (uint64, non-zero on accepted rows where a contract was minted on this bank's side) | 5e8b219 |
| GET | /api/v3/investment-funds/:id | Response enriched with `investor_count`, `total_contributed_rsd`, `liquid_rsd_balance`, `total_holdings_value_rsd`, `total_value_rsd`, `total_dividends_paid_rsd`, `profit_rsd`, `profit_pct`, and a `holdings` array | 4c5cb47, b28808e |
| POST | /api/v3/me/otc/options/:id/negotiations/:nid/accept | Accepts optional `on_behalf_of_fund_id` — when non-zero, places the accept on behalf of an investment fund; `acceptor_account_id` must equal the fund's RSD account; acting employee must be the fund manager | 545ab6c |
| POST | /api/v3/otc/contracts/:id/exercise | Accepts optional `on_behalf_of_fund_id` — shares land in `fund_holdings` instead of personal holdings when set; fund manager validation applies | 545ab6c |
| GET | /api/v3/me/portfolio (and /portfolio/*) | Each `PortfolioPosition` now carries `dividends_received_rsd` (cumulative net dividend received) and `fund_status` (for fund positions: `open`/`fundraising`/`active`/`matured`/`liquidated`) | f4cf9f5 |
| POST | /api/v3/me/payments | Now rejects source accounts with `account_category=investment_fund` — returns 403 `fund_account_outflow_restricted`. Fund cash must exit via fund operations only. | 437fe23 |
| POST | /api/v3/me/transfers | Same fund account outflow restriction as payments | 437fe23 |

---

## Removed routes

| Method | Path | Replacement | Commit |
|---|---|---|---|
| POST | /api/v3/interbank | /api/v3/cross-bank-protocol/interbank | 4c723de |
| GET | /api/v3/interbank/:transaction_id/status | /api/v3/cross-bank-protocol/interbank/:transaction_id/status | 4c723de |
| GET | /api/v3/public-stock | /api/v3/cross-bank-protocol/public-stock | 4c723de |
| GET | /api/v3/public-option-offers | /api/v3/cross-bank-protocol/public-option-offers | 4c723de |
| POST | /api/v3/negotiations | /api/v3/cross-bank-protocol/negotiations | 4c723de |
| PUT | /api/v3/negotiations/:rid/:id | /api/v3/cross-bank-protocol/negotiations/:rid/:id | 4c723de |
| GET | /api/v3/negotiations/:rid/:id | /api/v3/cross-bank-protocol/negotiations/:rid/:id | 4c723de |
| DELETE | /api/v3/negotiations/:rid/:id | /api/v3/cross-bank-protocol/negotiations/:rid/:id | 4c723de |
| GET | /api/v3/negotiations/:rid/:id/accept | /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept | 4c723de |
| GET | /api/v3/user/:rid/:id | /api/v3/cross-bank-protocol/user/:rid/:id | 4c723de |

---

## Notes for frontend / cohort banks

### Cross-bank protocol prefix (BREAKING)

Cohort banks must re-register this bank with `base_url` ending in `/api/v3/cross-bank-protocol`. The outbound client appends only the leaf paths (`/interbank`, `/public-stock`, `/negotiations`, `/user`), so `base_url` must be the full prefix, e.g.:

```
base_url = "http://host:8080/api/v3/cross-bank-protocol"
```

Legacy paths (`/api/v3/interbank`, `/api/v3/public-stock`, etc.) now return 404.

### Unified portfolio shape (BREAKING for consumers of GET /me/portfolio)

`GET /api/v3/me/portfolio` no longer returns `{ holdings: [...] }`. The new shape is:

```json
{
  "portfolio_id":    "client-42",
  "owner_type":      "client",
  "owner_id":        42,
  "total_value_rsd": "...",
  "securities":      { "positions": [...] },
  "funds":           { "positions": [...] }
}
```

Existing clients that index into `response.holdings` will break. Update to read from `response.securities.positions` and `response.funds.positions`.

### Fund account outflow restriction (NEW 403 case)

`POST /me/payments` and `POST /me/transfers` now return `403 fund_account_outflow_restricted` when the source account belongs to an investment fund. Fund cash can only leave via:
- `POST /api/v3/investment-funds/:id/orders` (buy on behalf of fund)
- `POST /api/v3/admin/dividends/:id/payout` (dividend payout)
- `POST /api/v3/investment-funds/:id/redeem` (investor redemption)

This restriction is enforced in transaction-service, not the gateway, so it appears as a `PermissionDenied` gRPC error mapped to 403 at the HTTP layer.

### Dividend fields on portfolio positions

`GET /me/portfolio` (and all `GET /portfolio/*` variants) now include two optional fields per position:
- `dividends_received_rsd` — cumulative net dividends received on this position (after 15% tax for clients; no tax for bank/fund)
- `fund_status` — lifecycle status for fund positions (`open`, `fundraising`, `active`, `matured`, `liquidated`). Empty string for non-fund positions.

### Price alerts and watchlist notifications

- `PRICE_ALERT_TRIGGERED` push notification template is now live (previously missing — alerts fired but delivered nothing).
- A new daily cron (`watchlist-price-move`) sends notifications when any watchlisted ticker moves ±5% within the day (commit `89649fc`).

### Negotiation revision history

`GET /api/v3/me/otc/options/negotiations/:nid/revisions` returns the full ordered revision chain for one negotiation. Both the bidder and the listing poster can call it; third parties receive 403. Useful for a "negotiation timeline" UI component.

### minted_contract_id and local_contract_id

After a negotiation is accepted and the contract formation saga completes:
- `GET /me/otc/options/negotiations` — `minted_contract_id` is populated on `status=accepted` rows (the intra-bank `OptionContract.id`)
- `GET /me/peer-otc/negotiations` — `local_contract_id` is populated on `status=accepted` rows on the relevant side (seller side: contract ID from seller's bank; buyer side: from buyer's bank)

Use these to deep-link from a negotiation list to the contract detail view without a separate lookup.

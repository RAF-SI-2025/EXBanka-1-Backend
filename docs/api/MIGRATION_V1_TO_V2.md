# Migration: `/api/v1/*` → `/api/v2/*`

Short, frontend-facing list of every behavior change between v1 and v2. Any route not mentioned below is identical. Backward-compat is preserved — v1 keeps working — but new features are v2-only.

## TL;DR

1. Switch the base URL prefix from `/api/v1` to `/api/v2`. Every v1 route has an equivalent v2 route.
2. The only **new** routes are two options endpoints and one account-activity endpoint — see §1.
3. The most common **changed** response is `/me/portfolio` — holdings now trimmed to quantity only; pull per-lot history from the new `/me/holdings/{id}/transactions` endpoint.
4. The most common **changed** request is `/me/orders` — `holding_id` is no longer accepted; `listing_id` is required on both buy and sell.

---

## 1. New routes (v2-only)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v2/options/{option_id}/orders` | Place an option order addressing the option by ID (the server resolves `option_id → listing_id`). Preferred over `POST /me/orders` with the option's listing_id. |
| `POST` | `/api/v2/options/{option_id}/exercise` | Exercise an option by ID. The server auto-resolves the caller's option holding — **no request body** required. |
| `GET` | `/api/v2/me/accounts/{id}/activity` | Per-account event feed: buys/sells, commission, tax, transfers, payments, interest. Returns `{entries, total_count}` with `description`, `amount`, `balance_before`, `balance_after`, `reference_type`, `occurred_at`. |
| `GET` | `/api/v2/me/holdings/{id}/transactions` | Per-holding lot history (native amount, account currency, fx rate, commission). Pairs with the trimmed `/me/portfolio`. |

## 2. Changed requests

**`POST /api/v2/me/orders`** and **`POST /api/v2/orders`** (on-behalf):

- `holding_id` — **removed** from the request body (was ignored post-holdings-rollup).
- `listing_id` — now **required for both buy and sell** (was buy-only in v1). It names the execution venue; holdings aggregate per `(user, security)` so the user picks which listing to sell on.
- Response object now includes two new fields: `state` (derived: `pending` \| `approved` \| `filling` \| `filled` \| `cancelled` \| `declined`) and `filled_quantity` (= `quantity − remaining_portions`). Raw `status` kept for compatibility.

**`POST /api/v2/options/{option_id}/exercise`**:

- Request body is now **none** (was `{"holding_id": …}` in v1 — server resolves the holding automatically).

## 3. Changed response shapes

**`GET /api/v2/me/portfolio`**:

Holding object trimmed to quantity view. **Removed**: `average_price`, `current_price`, `profit`. Use `GET /me/holdings/{id}/transactions` for per-lot detail. Still present: `id`, `security_type`, `ticker`, `name`, `quantity`, `public_quantity`, `account_id`, `last_modified`.

**`GET /api/v2/me/portfolio/summary`**:

Extended with richer P&L + tax breakdown. New fields (all strings unless marked):

- `unrealized_profit` — current holdings P&L in listing's native currency
- `realized_profit_this_month_rsd`
- `realized_profit_this_year_rsd`
- `realized_profit_lifetime_rsd`
- `tax_unpaid_total_rsd` — lifetime owed (not just current month)
- `open_positions_count` (int)
- `closed_trades_this_year` (int)

`total_profit` / `total_profit_rsd` now combine realised-lifetime + unrealised so they're meaningful even after all positions close.

**Every securities listing response** (`/securities/stocks`, `/securities/futures`, `/securities/forex`, `/securities/options`, and their `/{id}` variants):

- New field `listing.currency` (one of `RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`) so prices can be rendered with the right unit.

## 4. Behavior changes that are NOT shape changes

These don't require frontend work but are worth knowing:

- Holdings aggregate per `(user, security)`. Buying the same stock via two different accounts produces one row, not two.
- Commissions are now debited from the user's account and credited to the bank's RSD account (previously the bank credit had no source — the user's account didn't reflect it). Shows up on the new activity feed.
- Capital-gains tax is collected to the state/country account; bank fees go to the bank's own RSD account.
- Residual reservation (slippage+commission buffer) is released back to available balance after a buy fills.
- Order placement resolves the security's ticker and stamps it on the order/holding — `/me/portfolio` now always returns `ticker` for holdings.
- `EmployeeAgent` orders only require supervisor approval once the agent's limit has been fully spent (`used_limit >= limit`). A limit of 0 means "not configured yet" and lets orders through.
- Employee-on-behalf orders attribute the holding to the client (`system_type="client"`), so the client sees it in their own `/me/portfolio`.
- Source-switch and price-refresh on the admin side now respect the active source (generated / simulator / external). Selecting `generated` no longer lets the external AV refresh loop overwrite prices with 0s.

## 5. What didn't change

- Auth flow (`/auth/login`, `/auth/refresh`, etc.)
- `/me` identity endpoint
- `/me/transfers`, `/me/payments`, `/me/cards` shapes
- Loans, credits, mobile auth endpoints
- Employee / admin / limits / tax endpoints

v1 remains supported indefinitely — existing clients continue to work unchanged.

# REST API v2

> **Note:** Any path not listed here transparently falls back to `/api/v1`.
> See `REST_API_v1.md` for the full v1 surface.
>
> v2 is **additive only** — it introduces new routes for features that would be awkward to add at v1 without breaking existing clients. Everything else continues to work under v2 URLs because of the fallback handler in the api-gateway.

Base URL: `/api/v2`

---

## Options

### Create an order for an option contract

```
POST /api/v2/options/:option_id/orders
```

**Authentication:** Any JWT (employee or client) + permission `securities.trade`.

**Path parameters:**

| Parameter   | Type | Description                                                     |
|-------------|------|-----------------------------------------------------------------|
| `option_id` | uint | The option's ID from `GET /api/v1/securities/options?stock_id=...` |

**Request body:**

```json
{
  "direction": "buy | sell",
  "order_type": "market | limit | stop | stop_limit",
  "quantity": 1,
  "limit_value": "5.75",
  "stop_value": "6.00",
  "all_or_none": false,
  "margin": true,
  "account_id": 42,
  "holding_id": 0
}
```

| Field         | Type    | Required                                | Notes                                                      |
|---------------|---------|-----------------------------------------|------------------------------------------------------------|
| `direction`   | string  | yes                                     | `buy` or `sell`                                            |
| `order_type`  | string  | yes                                     | `market`, `limit`, `stop`, or `stop_limit`                 |
| `quantity`    | int     | yes                                     | Must be > 0                                                |
| `limit_value` | string  | when `order_type ∈ {limit, stop_limit}` | Decimal string                                             |
| `stop_value`  | string  | when `order_type ∈ {stop, stop_limit}`  | Decimal string                                             |
| `all_or_none` | bool    | no                                      | Default false                                              |
| `margin`      | bool    | no                                      | Options typically use margin                               |
| `account_id`  | uint    | yes                                     | The user's account used for collateral and settlement     |
| `holding_id`  | uint    | no                                      | For closing an existing position; omit or 0 to open a new |

**Response 201:**

Returns the full `Order` object (same shape as `POST /api/v1/orders`):

```json
{
  "id": 999,
  "user_id": 42,
  "listing_id": 77,
  "security_type": "option",
  "ticker": "AAPL260116C00200000",
  "direction": "buy",
  "order_type": "market",
  "quantity": 1,
  "status": "pending",
  ...
}
```

**Error responses:**

- `400 validation_error` — invalid direction, order_type, quantity, or missing limit/stop for the requested order_type
- `401 unauthorized` — missing or invalid JWT
- `403 forbidden` — missing `securities.trade` permission
- `404 not_found` — option_id does not exist
- `409 business_rule_violation` — option has no listing_id (not tradeable); or stock-service rejected the order
- `500 internal_error` — unexpected failure downstream

**Implementation note:** the gateway resolves `option_id → listing_id` via `GetOption(option_id)` and then calls the existing `CreateOrder` RPC. If the option has no listing (e.g., legacy data from before the unification migration), the endpoint returns 409.

---

### Exercise an option

```
POST /api/v2/options/:option_id/exercise
```

**Authentication:** Any JWT (employee or client) + permission `securities.trade`.

**Path parameters:**

| Parameter   | Type | Description                          |
|-------------|------|--------------------------------------|
| `option_id` | uint | The option's ID                      |

**Request body (optional):**

```json
{ "holding_id": 0 }
```

| Field        | Type | Required | Notes                                                                                  |
|--------------|------|----------|----------------------------------------------------------------------------------------|
| `holding_id` | uint | no       | Specific holding to exercise. Omit or 0 to auto-resolve the user's oldest long position. |

If `holding_id` is omitted or 0, stock-service finds the user's oldest long option holding (`security_type='option' AND security_id=<option_id> AND quantity > 0 ORDER BY created_at ASC`) and exercises that.

**Response 200:**

Returns the `ExerciseResult` shape from the existing v1 exercise path:

```json
{
  "id": 1,
  "option_ticker": "AAPL260116C00200000",
  "exercised_quantity": 1,
  "shares_affected": 100,
  "profit": "42.00"
}
```

**Error responses:**

- `400 validation_error` — invalid `option_id`
- `401 unauthorized` — missing or invalid JWT
- `403 forbidden` — missing `securities.trade` permission
- `404 not_found` — no long option holding found for this user + option (either because it doesn't exist or the position is already 0)
- `409 business_rule_violation` — option expired, compensation triggered downstream, or other business rule failure
- `500 internal_error` — unexpected failure downstream

**Comparison with v1:** the v1 exercise route `POST /api/v1/me/portfolio/:id/exercise` takes a holding ID directly. The v2 route takes an option ID and resolves the holding internally — more natural for UIs that browse the options chain without needing to know about underlying holding bookkeeping.

---

## Fallback to v1

Any path under `/api/v2` that does **not** match one of the routes above is transparently rewritten to `/api/v1` and re-dispatched. This means:

- `GET /api/v2/securities/stocks` → `GET /api/v1/securities/stocks` (identical response)
- `POST /api/v2/orders` → `POST /api/v1/orders`
- `GET /api/v2/me/portfolio` → `GET /api/v1/me/portfolio`

Clients that prefer to use v2 URLs for consistency can do so — the gateway handles the redirect internally without a client-visible 3xx. The method (GET/POST/PUT/DELETE), body, and headers are preserved.

A path that matches no route at either v2 or v1 returns a genuine `404` with body `{"error": {"code": "not_found", "message": "route not found"}}`.

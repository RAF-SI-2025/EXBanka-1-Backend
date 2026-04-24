# EXBanka REST API v2 Documentation

**Base URL:** `http://localhost:8080`
**Content-Type:** `application/json`
**Swagger UI:** `http://localhost:8080/swagger/index.html`

---

## Version Notes

All endpoints in this document are served under the `/api/v2/` prefix. v2 is now a **first-class API surface**: every route registered at v1 is registered at v2 too, with identical handlers, middleware, validation, and response shapes. v2 additionally exposes a small set of v2-only routes (currently the option-trading routes under `/api/v2/options/*`).

**Key differences from v1:**

1. **No fallback rewrite.** Earlier revisions of v2 forwarded unknown `/api/v2/*` paths to `/api/v1/*`. That fallback is removed. v2 now registers the full route set natively; there is nothing to forward.
2. **v2-only extensions** (e.g. `/api/v2/options/:option_id/orders`, `/api/v2/options/:option_id/exercise`) provide an option-first trading API that is awkward to express at v1 without breaking changes.
3. **Backwards compatibility:** v1 remains fully functional. `/api/v1/*` and `/api/v2/*` use the same handler code and produce identical responses for every shared route. Adding a new field to a v2 response is allowed (and is a non-breaking change) provided v1 clients that ignore unknown fields continue to work; the spec's "API Versioning Compatibility Requirement" governs breaking vs. non-breaking changes.

**Alias:** `/api/latest/` is aliased to the newest version currently considered stable (today: v1). Clients that want to pin to v2 must use `/api/v2/*` explicitly.

**v3:** A `/api/v3` prefix is reserved (registered with zero routes) for future additive versions. Any `/api/v3/*` request currently returns `404 not_found`.

---

## Authentication

Most endpoints require a JWT bearer token in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

There are two token types:
- **Employee token** — issued via `POST /api/v2/auth/login` when the principal is an employee, required for employee-protected routes.
- **Client token** — issued via `POST /api/v2/auth/login` when the principal is a client, required for `/api/v2/me/*` routes.

The unified login endpoint auto-detects whether the principal is an employee or a client based on the stored account record in auth-service and issues the appropriate JWT (`system_type: "employee"` or `system_type: "client"`).

Employee routes additionally require specific permissions (see per-endpoint notes). Client routes require `role="client"` in the JWT. Access tokens expire after 15 minutes; use the refresh token to obtain a new pair.

---

## Table of Contents

1. [Core routes (shared with v1)](#1-core-routes-shared-with-v1)
2. [v2-only extensions](#2-v2-only-extensions)
   - [Options](#options)
     - [POST /api/v2/options/:option_id/orders](#post-apiv2optionsoption_idorders)
     - [POST /api/v2/options/:option_id/exercise](#post-apiv2optionsoption_idexercise)
3. [Error Response Format](#error-response-format)
4. [Password Requirements](#password-requirements)
5. [Notes for Frontend Developers](#notes-for-frontend-developers)

---

## 1. Core routes (shared with v1)

Every route listed in `REST_API_v1.md` is also available at v2 with the prefix `/api/v2/...` instead of `/api/v1/...`. The request/response shapes, authentication rules, permission requirements, and validation rules are **identical**. A quick index of the top-level route groups:

| Group                | v2 base path                              | Description                                                     |
|----------------------|-------------------------------------------|-----------------------------------------------------------------|
| Auth                 | `/api/v2/auth/*`                          | Login, refresh, password reset, activation, logout              |
| Employees            | `/api/v2/employees/*`                     | CRUD, limits, roles, permissions, changelog                     |
| Roles & Permissions  | `/api/v2/roles/*`, `/api/v2/permissions`  | Manage roles and permission assignments                         |
| Clients              | `/api/v2/clients/*`                       | CRUD, limits, changelog                                         |
| Accounts             | `/api/v2/accounts/*`                      | Bank-client accounts, name/limits/status updates, changelog     |
| Bank Accounts        | `/api/v2/bank-accounts/*`                 | Bank-owned accounts (admin)                                     |
| Cards                | `/api/v2/cards/*`                         | Physical + authorized person cards, PIN, block/unblock, changelog, requests |
| Payments             | `/api/v2/payments/*`                      | Employee view of client payments                                |
| Transfers            | `/api/v2/transfers/*`                     | Employee view of client transfers                               |
| Payment Recipients   | `/api/v2/payment-recipients/*`            | CRUD (scoped by client)                                         |
| Exchange Rates       | `/api/v2/exchange/*`                      | Public: rates, conversion                                       |
| Loans                | `/api/v2/loans/*`, `/api/v2/loans/requests/*` | Read loans, approve/reject requests, changelog             |
| Interest Rate Tiers  | `/api/v2/interest-rate-tiers/*`           | Admin: tier CRUD + apply                                        |
| Bank Margins         | `/api/v2/bank-margins/*`                  | Admin: list + update                                            |
| Limits               | `/api/v2/limits/templates`, `.../limits`  | Employee + client limit management                              |
| Blueprints           | `/api/v2/blueprints/*`                    | Limit blueprint management                                      |
| Fees                 | `/api/v2/fees/*`                          | Transfer fee rule CRUD                                          |
| Me (self-service)    | `/api/v2/me/*`                            | Client-facing accounts, cards, payments, transfers, loans, orders, portfolio, tax, sessions, notifications |
| Mobile auth          | `/api/v2/mobile/auth/*`                   | Device activation + refresh                                     |
| Mobile device        | `/api/v2/mobile/device/*`                 | Device management + biometrics                                  |
| Mobile verification  | `/api/v2/mobile/verifications/*`          | Pending, submit, ack, biometric                                 |
| Verification         | `/api/v2/verifications/*`                 | Browser-side flows                                              |
| QR Verify            | `/api/v2/verify/:challenge_id`            | QR-scan verification endpoint                                   |
| Stock Exchanges      | `/api/v2/stock-exchanges/*`               | Market browsing + testing-mode (admin)                          |
| Securities           | `/api/v2/securities/*`                    | Stocks, futures, forex, options listings + candles              |
| Orders               | `/api/v2/orders/*`                        | Employee-on-behalf order creation + approve/decline             |
| OTC                  | `/api/v2/otc/*`                           | OTC offer browsing; client buy; employee-on-behalf buy          |
| Actuaries            | `/api/v2/actuaries/*`                     | Supervisor-only actuary management                              |
| Tax                  | `/api/v2/tax/*`                           | Tax records + collection                                        |
| Admin stock-source   | `/api/v2/admin/stock-source`              | Data-source switching (destructive)                             |
| Sessions & History   | `/api/v2/me/sessions/*`, `/me/login-history` | Active sessions + login audit                                |
| Notifications        | `/api/v2/me/notifications/*`              | Persistent inbox                                                |

For the precise request body, response shape, authentication mode, permission requirements, and error conditions of any of these routes, see the corresponding section in `docs/api/REST_API_v1.md` and substitute the `/api/v1/` prefix with `/api/v2/`. The semantics are bit-for-bit identical because both prefixes are wired to the same handler via `RegisterCoreRoutes` in `api-gateway/internal/router/router_v1.go`.

---

## 2. v2-only extensions

The routes below exist only at v2.

### Options

The v2 options surface lets UIs address option contracts by their option ID directly, rather than having to first resolve the option's listing ID or holding ID. This aligns with the way the options chain is browsed in the client app.

---

### POST /api/v2/options/:option_id/orders

Create an order for an option contract, addressing the contract by its option ID.

Internally this resolves `option_id → listing_id` via `GetOption(option_id)` and then delegates to the standard `CreateOrder` RPC. If the option has no listing (e.g., legacy data), the endpoint returns 409.

**Authentication:** Any JWT (client or employee)
**Required permission:** `securities.trade`

**Path parameters:**

| Parameter   | Type | Required | Constraints     |
|-------------|------|----------|-----------------|
| `option_id` | uint | yes      | non-zero        |

**Request body:**

| Field         | Type    | Required                                | Constraints                                                                 |
|---------------|---------|-----------------------------------------|-----------------------------------------------------------------------------|
| `direction`   | string  | yes                                     | one of `buy`, `sell`                                                        |
| `order_type`  | string  | yes                                     | one of `market`, `limit`, `stop`, `stop_limit`                              |
| `quantity`    | int64   | yes                                     | > 0                                                                         |
| `limit_value` | string  | yes if `order_type ∈ {limit, stop_limit}` | decimal string (e.g., `"5.75"`)                                           |
| `stop_value`  | string  | yes if `order_type ∈ {stop, stop_limit}`  | decimal string (e.g., `"6.00"`)                                           |
| `all_or_none` | bool    | no                                      | default `false`                                                             |
| `margin`      | bool    | no                                      | options typically use margin; default `false`                               |
| `account_id`  | uint64  | yes                                     | non-zero; the account used for collateral and settlement                    |
| `holding_id`  | uint64  | no                                      | For closing an existing long position; omit or `0` to open a new position   |

**Example request:**

```json
{
  "direction": "buy",
  "order_type": "market",
  "quantity": 1,
  "all_or_none": false,
  "margin": true,
  "account_id": 42
}
```

**201 Created response:**

Returns the full `Order` object (same shape as `POST /api/v2/orders`). Fields:

| Field           | Type    | Always present | Notes                                                     |
|-----------------|---------|----------------|-----------------------------------------------------------|
| `id`            | uint64  | yes            | Server-assigned order ID                                  |
| `user_id`       | uint64  | yes            | Owner of the order                                        |
| `listing_id`    | uint64  | yes            | Underlying listing (resolved from option)                 |
| `security_type` | string  | yes            | `"option"` for this endpoint                              |
| `ticker`        | string  | yes            | OCC-style option ticker (e.g., `AAPL260116C00200000`)     |
| `direction`     | string  | yes            | echoes request                                            |
| `order_type`    | string  | yes            | echoes request                                            |
| `quantity`      | int64   | yes            | echoes request                                            |
| `status`        | string  | yes            | typically `pending` or `approved`                         |
| `all_or_none`   | bool    | yes            | echoes request                                            |
| `margin`        | bool    | yes            | echoes request                                            |
| `limit_value`   | string  | when applicable | present for limit / stop_limit orders                    |
| `stop_value`    | string  | when applicable | present for stop / stop_limit orders                     |
| `created_at`    | string  | yes            | RFC 3339 timestamp                                        |

**Error responses:**

| Status | Code                      | When                                                               |
|--------|---------------------------|--------------------------------------------------------------------|
| 400    | `validation_error`        | invalid direction / order_type / quantity / missing limit or stop / invalid option_id |
| 401    | `unauthorized`            | missing or invalid JWT                                             |
| 403    | `forbidden`               | missing `securities.trade` permission                              |
| 404    | `not_found`               | option_id does not exist                                           |
| 409    | `business_rule_violation` | option has no listing_id (not tradeable), or stock-service rejected the order |
| 500    | `internal_error`          | unexpected failure downstream                                      |

---

### POST /api/v2/options/:option_id/exercise

Exercise an option contract. In contrast to the v1 `POST /api/v1/me/portfolio/:id/exercise` route — which takes a **holding** ID — this v2 route takes the **option** ID, and the server resolves the holding for the caller. If `holding_id` is omitted or `0`, the stock-service finds the caller's oldest long option holding for that option and exercises that one.

**Authentication:** Any JWT (client or employee)
**Required permission:** `securities.trade`

**Path parameters:**

| Parameter   | Type | Required | Constraints |
|-------------|------|----------|-------------|
| `option_id` | uint | yes      | non-zero    |

**Request body (optional):**

| Field        | Type   | Required | Constraints                                                                          |
|--------------|--------|----------|--------------------------------------------------------------------------------------|
| `holding_id` | uint64 | no       | specific holding to exercise; omit or `0` to auto-resolve oldest long holding        |

Example:

```json
{ "holding_id": 0 }
```

**200 OK response:**

| Field                | Type   | Always present | Notes                                                |
|----------------------|--------|----------------|------------------------------------------------------|
| `id`                 | uint64 | yes            | Exercise event ID                                    |
| `option_ticker`      | string | yes            | OCC-style option ticker                              |
| `exercised_quantity` | int64  | yes            | Number of contracts exercised                        |
| `shares_affected`    | int64  | yes            | Resulting share delta (contract × 100 for standard) |
| `profit`             | string | yes            | Realised P/L in RSD (decimal string)                 |

**Error responses:**

| Status | Code                      | When                                                                              |
|--------|---------------------------|-----------------------------------------------------------------------------------|
| 400    | `validation_error`        | invalid `option_id`                                                               |
| 401    | `unauthorized`            | missing or invalid JWT                                                            |
| 403    | `forbidden`               | missing `securities.trade` permission                                             |
| 404    | `not_found`               | no long option holding found for this user + option (the position does not exist or is already zero) |
| 409    | `business_rule_violation` | option expired / compensation triggered downstream / other business-rule failure  |
| 500    | `internal_error`          | unexpected failure downstream                                                     |

---

## Error Response Format

All error responses follow this format:

```json
{
  "error": {
    "code": "snake_case_error_code",
    "message": "Human-readable error message",
    "details": { "...": "optional, endpoint-specific" }
  }
}
```

`code` is a stable machine-readable string; `message` is human-readable; `details` is an optional object that some endpoints populate with structured context.

**Common error codes:**

| `code`                    | HTTP Status | Meaning                                                       |
|---------------------------|-------------|---------------------------------------------------------------|
| `validation_error`        | 400         | Request body or query param validation failed                 |
| `invalid_input`           | 400         | Malformed or out-of-range value                               |
| `unauthorized`            | 401         | Missing or invalid bearer token                               |
| `forbidden`               | 403         | Authenticated but insufficient permissions                    |
| `not_found`               | 404         | Requested resource does not exist                             |
| `conflict`                | 409         | Duplicate / already-exists                                    |
| `business_rule_violation` | 409         | Operation violates a business rule (e.g., card already blocked) |
| `rate_limited`            | 429         | Too many requests                                             |
| `not_implemented`         | 501         | Endpoint planned but not yet available                        |
| `internal_error`          | 500         | Unexpected server-side failure                                |

---

## Password Requirements

Passwords for both employees and clients must satisfy:

- 8 to 32 characters
- At least 2 digits
- At least 1 uppercase letter
- At least 1 lowercase letter

---

## Notes for Frontend Developers

1. **v1 remains supported.** v2 is additive. A client that targets `/api/v1/*` today will keep working indefinitely; switching to `/api/v2/*` changes nothing for the shared routes.
2. **Token expiry:** Access tokens expire after 15 minutes. Implement automatic refresh using the refresh token before expiry.
3. **Client vs Employee routes:** Employee routes require an employee JWT with specific permissions. Client self-service routes are under `/api/v2/me/*` and accept any valid JWT (employee or client). Do not use a client token to call employee-only endpoints.
4. **Error format:** All error responses are structured objects: `{"error": {"code": "...", "message": "..."}}`. Parse `error.code` for programmatic error handling and `error.message` for display.
5. **Pagination:** All list endpoints support `page` (1-based) and `page_size` query parameters. Default page size is 20; max is usually 100.
6. **Date fields:** `date_of_birth` is a Unix timestamp in seconds. Other timestamps are RFC 3339 strings.
7. **Account numbers:** Account numbers follow the format `265-XXXXXXXXXXX-YY` (Serbian bank account format with control digits).
8. **Card numbers:** The full card number and CVV are only returned in the create-card response. Subsequent reads return a masked card number.
9. **JMBG:** The 13-digit Serbian national ID. Validated server-side for exact length and uniqueness.
10. **CORS:** The API Gateway allows all origins with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` methods and `Authorization`, `Content-Type` headers.
11. **Mobile auth flow:** Mobile devices use a separate auth flow (`POST /api/v2/mobile/auth/request-activation` → `POST /api/v2/mobile/auth/activate`). Mobile JWT tokens include `system_type: "mobile"` and require `X-Device-ID` header for all authenticated requests.
12. **Verification flow:** Payments and transfers require two-factor verification. Create the transaction, then create a verification challenge, wait for mobile approval, then execute. Users with `verification.skip` permission bypass this flow.
13. **v2 options routes:** Prefer `/api/v2/options/:option_id/orders` and `/api/v2/options/:option_id/exercise` over the legacy listing-id / holding-id routes; the v2 URLs match how UIs naturally browse the options chain.

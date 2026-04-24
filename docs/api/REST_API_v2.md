# EXBanka REST API v2

**Base URL:** `http://<gateway>/api/v2`
**Content-Type:** `application/json`
**Swagger UI:** `http://<gateway>/swagger/index.html`

**Authentication:** Bearer JWT in `Authorization` header. Tokens come from `POST /api/v2/auth/login` (employee or client — auto-detected). Access tokens are JWTs with 15-minute expiry; refresh tokens are opaque 64-hex strings with 168h expiry and single-use rotation. Mobile flows use `POST /api/v2/mobile/auth/...` endpoints and `X-Device-ID` headers.

**Common response shape:**

- Errors use `{"error": {"code": "...", "message": "...", "details": {...}}}` with standard codes: `validation_error, invalid_input, unauthorized, forbidden, not_found, conflict, business_rule_violation, rate_limited, not_implemented, internal_error`.
- List endpoints return `{"data": [...], "total": N}` or `{"<entity>s": [...], "total_count": N}` depending on the endpoint (documented per-route).

**Common types:**

- Decimal fields are JSON strings (e.g., `"100.0000"`) to avoid FP precision loss. Server uses 4-decimal fixed precision.
- Timestamps are ISO-8601 / RFC 3339 UTC (e.g., `"2026-04-24T14:32:51Z"`). `date_of_birth` is a Unix timestamp in seconds.
- Account numbers follow `265-XXXXXXXXXXX-YY` (Serbian bank account format with control digits).
- JMBG is the 13-digit Serbian national ID (exact length, unique server-side).

**Password policy (employees and clients):** 8-32 chars, at least 2 digits, 1 uppercase, 1 lowercase.

**Versioning:** v2 is a strict superset of v1. `/api/v1/*` and `/api/v2/*` share the same handlers for the 181 core routes; v2 adds 2 option-first routes (`/api/v2/options/:option_id/...`). `/api/v3` is reserved (404). `/api/latest` aliases the newest stable version (today: v1).

---

## Table of Contents

1. [Authentication](#1-authentication)
2. [Exchange Rates (public)](#2-exchange-rates-public)
3. [Me (self-service)](#3-me-self-service)
4. [Stock Exchanges](#4-stock-exchanges)
5. [Securities (market data)](#5-securities-market-data)
6. [OTC Offers](#6-otc-offers)
7. [Mobile Auth & Device](#7-mobile-auth--device)
8. [Mobile Verifications](#8-mobile-verifications)
9. [QR Verification](#9-qr-verification)
10. [Browser Verifications](#10-browser-verifications)
11. [Employees](#11-employees)
12. [Roles & Permissions](#12-roles--permissions)
13. [Limits](#13-limits)
14. [Blueprints](#14-blueprints)
15. [Clients](#15-clients)
16. [Accounts & Currencies & Companies](#16-accounts--currencies--companies)
17. [Bank Accounts](#17-bank-accounts)
18. [Cards](#18-cards)
19. [Card Requests (employee)](#19-card-requests-employee)
20. [Payments (employee)](#20-payments-employee)
21. [Transfers (employee)](#21-transfers-employee)
22. [Transfer Fees](#22-transfer-fees)
23. [Loans (employee)](#23-loans-employee)
24. [Loan Requests (employee)](#24-loan-requests-employee)
25. [Interest Rate Tiers & Bank Margins](#25-interest-rate-tiers--bank-margins)
26. [Stock Exchange Admin](#26-stock-exchange-admin)
27. [Admin Stock Source](#27-admin-stock-source)
28. [Orders (employee)](#28-orders-employee)
29. [OTC On-Behalf (employee)](#29-otc-on-behalf-employee)
30. [Actuaries](#30-actuaries)
31. [Tax (employee)](#31-tax-employee)
32. [Changelogs](#32-changelogs)
33. [v2-only: Options](#33-v2-only-options)

---

## 1. Authentication

All `/api/v2/auth/*` routes are public (no middleware). The `Login` endpoint auto-detects whether the principal is an employee or a client based on the account record in `auth-service` and issues the appropriate JWT (`system_type: "employee"` or `system_type: "client"`).

### POST /api/v2/auth/login

Authenticate with email and password; returns access + refresh tokens.

**Auth:** none

**Request body:**

| Field    | Type   | Required | Constraints                                                |
|----------|--------|----------|------------------------------------------------------------|
| email    | string | yes      | valid email format                                         |
| password | string | yes      | non-empty; server-side password rules apply during hash compare |

**200 response:**

| Field         | Type   | Always present | Notes                                                   |
|---------------|--------|----------------|---------------------------------------------------------|
| access_token  | string | yes            | JWT, 15-minute expiry; claims include `user_id`, `roles`, `permissions`, `system_type` |
| refresh_token | string | yes            | opaque 64-hex token, 168h expiry; single-use rotation   |

**Error responses:**

| Status | Code             | When                                     |
|--------|------------------|------------------------------------------|
| 400    | validation_error | missing/malformed email or password      |
| 401    | unauthorized     | bad credentials                          |
| 429    | rate_limited     | too many failed attempts within the lockout window |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@admin.com","password":"AdminAdmin2026!."}'
```

### POST /api/v2/auth/refresh

Exchange a valid refresh token for a new access+refresh token pair. The submitted refresh token is revoked (rotation).

**Auth:** none

**Request body:**

| Field         | Type   | Required | Constraints |
|---------------|--------|----------|-------------|
| refresh_token | string | yes      | non-empty   |

**200 response:**

| Field         | Type   | Always present | Notes                                |
|---------------|--------|----------------|--------------------------------------|
| access_token  | string | yes            | new JWT                              |
| refresh_token | string | yes            | new refresh token (old one revoked)  |

**Error responses:**

| Status | Code             | When                                              |
|--------|------------------|---------------------------------------------------|
| 400    | validation_error | missing refresh_token                             |
| 401    | unauthorized     | refresh token not found, already used, or expired |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/refresh \
  -H "Content-Type: application/json" \
  -d '{"refresh_token":"<rt>"}'
```

### POST /api/v2/auth/logout

Revoke the refresh token to end the session. Always returns 200.

**Auth:** none

**Request body:**

| Field         | Type   | Required | Constraints |
|---------------|--------|----------|-------------|
| refresh_token | string | yes      | non-empty   |

**200 response:**

| Field   | Type   | Always present | Notes                         |
|---------|--------|----------------|-------------------------------|
| message | string | yes            | `"logged out successfully"`   |

**Error responses:**

| Status | Code             | When                  |
|--------|------------------|-----------------------|
| 400    | validation_error | missing refresh_token |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/logout \
  -H "Content-Type: application/json" -d '{"refresh_token":"<rt>"}'
```

### POST /api/v2/auth/password/reset-request

Send a password reset link to the user's email via the notification-service. Always returns 200 regardless of whether the email exists (to avoid user enumeration).

**Auth:** none

**Request body:**

| Field | Type   | Required | Constraints        |
|-------|--------|----------|--------------------|
| email | string | yes      | valid email format |

**200 response:**

| Field   | Type   | Always present | Notes                                                        |
|---------|--------|----------------|--------------------------------------------------------------|
| message | string | yes            | `"if the email exists, a reset link has been sent"`          |

**Error responses:**

| Status | Code             | When                         |
|--------|------------------|------------------------------|
| 400    | validation_error | missing/malformed email      |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/password/reset-request \
  -H "Content-Type: application/json" -d '{"email":"user@example.com"}'
```

### POST /api/v2/auth/password/reset

Reset password using a one-time token from the reset email link.

**Auth:** none

**Request body:**

| Field            | Type   | Required | Constraints                    |
|------------------|--------|----------|--------------------------------|
| token            | string | yes      | reset token from email, 1h TTL |
| new_password     | string | yes      | 8-32 chars, >=2 digits, >=1 upper, >=1 lower |
| confirm_password | string | yes      | must equal `new_password`      |

**200 response:**

| Field   | Type   | Always present | Notes                             |
|---------|--------|----------------|-----------------------------------|
| message | string | yes            | `"password reset successfully"`   |

**Error responses:**

| Status | Code             | When                                         |
|--------|------------------|----------------------------------------------|
| 400    | validation_error | weak password, mismatch, missing fields      |
| 404    | not_found        | token invalid, already used, or expired      |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/password/reset \
  -H "Content-Type: application/json" \
  -d '{"token":"<t>","new_password":"NewPass12","confirm_password":"NewPass12"}'
```

### POST /api/v2/auth/activate

Activate a new account using the activation token from the activation email link.

**Auth:** none

**Request body:**

| Field            | Type   | Required | Constraints                          |
|------------------|--------|----------|--------------------------------------|
| token            | string | yes      | activation token from email, 24h TTL |
| password         | string | yes      | 8-32 chars, >=2 digits, >=1 upper, >=1 lower |
| confirm_password | string | yes      | must equal `password`                |

**200 response:**

| Field   | Type   | Always present | Notes                              |
|---------|--------|----------------|------------------------------------|
| message | string | yes            | `"account activated successfully"` |

**Error responses:**

| Status | Code             | When                                          |
|--------|------------------|-----------------------------------------------|
| 400    | validation_error | weak password, mismatch, missing fields       |
| 404    | not_found        | token invalid or expired                      |
| 409    | conflict         | account already activated                     |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/activate \
  -H "Content-Type: application/json" \
  -d '{"token":"<t>","password":"Pass12aa","confirm_password":"Pass12aa"}'
```

### POST /api/v2/auth/resend-activation

Resend the activation email for a pending account. Always returns 200 (no-op if already activated or email unknown).

**Auth:** none

**Request body:**

| Field | Type   | Required | Constraints        |
|-------|--------|----------|--------------------|
| email | string | yes      | valid email format |

**200 response:**

| Field   | Type   | Always present | Notes                                                                                    |
|---------|--------|----------------|------------------------------------------------------------------------------------------|
| message | string | yes            | `"if the email is registered and pending activation, a new activation email has been sent"` |

**Error responses:**

| Status | Code             | When                   |
|--------|------------------|------------------------|
| 400    | validation_error | missing/malformed email|

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/auth/resend-activation \
  -H "Content-Type: application/json" -d '{"email":"user@example.com"}'
```

---

## 2. Exchange Rates (public)

All three exchange-rate routes are public (no middleware). Supported currencies: `RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`.

### GET /api/v2/exchange/rates

List all current buy/sell rates against RSD.

**Auth:** none

**200 response:**

| Field | Type   | Always present | Notes                                                                 |
|-------|--------|----------------|-----------------------------------------------------------------------|
| rates | array  | yes            | Each item: `{from_currency, to_currency, buy_rate, sell_rate, updated_at}` |

**Error responses:**

| Status | Code           | When                                 |
|--------|----------------|--------------------------------------|
| 500    | internal_error | upstream exchange-service failure    |

**Example:** `curl -sS http://localhost:8080/api/v2/exchange/rates`

### GET /api/v2/exchange/rates/{from}/{to}

Buy/sell rates for a specific currency pair. `from`/`to` are case-insensitive and normalised to upper-case.

**Auth:** none

**Path parameters:**

| Field | Type   | Required | Constraints                       |
|-------|--------|----------|-----------------------------------|
| from  | string | yes      | 3-letter ISO code (e.g., EUR)     |
| to    | string | yes      | 3-letter ISO code (e.g., RSD)     |

**200 response:**

| Field         | Type   | Always present | Notes                       |
|---------------|--------|----------------|-----------------------------|
| from_currency | string | yes            |                             |
| to_currency   | string | yes            |                             |
| buy_rate      | string | yes            | decimal string              |
| sell_rate     | string | yes            | decimal string              |
| updated_at    | string | yes            | RFC 3339 timestamp          |

**Error responses:**

| Status | Code      | When                          |
|--------|-----------|-------------------------------|
| 404    | not_found | unknown currency pair         |

**Example:** `curl -sS http://localhost:8080/api/v2/exchange/rates/EUR/RSD`

### POST /api/v2/exchange/calculate

Calculate a currency conversion with bank sell rate + commission applied. Informational only — does not create a transaction.

**Auth:** none

**Request body:**

| Field        | Type   | Required | Constraints                                  |
|--------------|--------|----------|----------------------------------------------|
| fromCurrency | string | yes      | one of RSD/EUR/CHF/USD/GBP/JPY/CAD/AUD       |
| toCurrency   | string | yes      | one of RSD/EUR/CHF/USD/GBP/JPY/CAD/AUD       |
| amount       | string | yes      | positive decimal (normalised to 4 decimals)  |

**200 response:**

| Field            | Type   | Always present | Notes                                         |
|------------------|--------|----------------|-----------------------------------------------|
| from_currency    | string | yes            |                                               |
| to_currency      | string | yes            |                                               |
| input_amount     | string | yes            | echoes input (4-decimal normalised)           |
| converted_amount | string | yes            | decimal string, net of commission             |
| commission_rate  | string | yes            | decimal string, e.g., `"0.005"`               |
| effective_rate   | string | yes            | bank-applied rate incl. spread                |

**Error responses:**

| Status | Code             | When                                      |
|--------|------------------|-------------------------------------------|
| 400    | validation_error | missing field, non-positive amount, unsupported currency |
| 404    | not_found        | rate for this pair unavailable            |
| 500    | internal_error   | upstream failure                          |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/exchange/calculate \
  -H "Content-Type: application/json" -d '{"fromCurrency":"EUR","toCurrency":"RSD","amount":"100.00"}'
```

---

## 3. Me (self-service)

All `/api/v2/me/*` routes use `AnyAuthMiddleware` (accepts client + employee tokens). A subset require `RequireClientToken()` (marked below). Ownership is always derived from the JWT `user_id`; routes never trust body-supplied `client_id` fields.

### GET /api/v2/me

Profile of the authenticated user. Shape differs by `system_type` (client vs. employee).

**Auth:** JWT required; client JWT required (`RequireClientToken`).

**200 response (client):**

| Field         | Type    | Always present | Notes                              |
|---------------|---------|----------------|------------------------------------|
| id            | uint64  | yes            |                                    |
| first_name    | string  | yes            |                                    |
| last_name     | string  | yes            |                                    |
| email         | string  | yes            |                                    |
| phone         | string  | no             |                                    |
| address       | string  | no             |                                    |
| date_of_birth | int64   | yes            | Unix seconds                       |
| gender        | string  | no             |                                    |
| jmbg          | string  | yes            |                                    |
| active        | bool    | yes            | resolved via auth-service          |

**200 response (employee):** similar shape plus `username, position, department, role, roles, permissions`.

**Error responses:**

| Status | Code         | When                                           |
|--------|--------------|------------------------------------------------|
| 401    | unauthorized | bad/missing token or invalid claims            |
| 403    | forbidden    | `system_type` is neither "client" nor "employee", or client token required |

**Example:** `curl -sS http://localhost:8080/api/v2/me -H "Authorization: Bearer $T"`

### GET /api/v2/me/accounts

List the authenticated client's accounts.

**Auth:** JWT

**Query parameters:** `page` (default 1), `page_size` (default 20).

**200 response:**

| Field    | Type  | Always present | Notes                                     |
|----------|-------|----------------|-------------------------------------------|
| accounts | array | yes            | each: account object (see section 16)     |
| total    | int64 | yes            |                                           |

**Error responses:** 401 unauthorized, 500 internal_error.

**Example:** `curl -sS http://localhost:8080/api/v2/me/accounts -H "Authorization: Bearer $T"`

### GET /api/v2/me/accounts/{id}

Get one of the authenticated user's accounts by ID (ownership enforced).

**Auth:** JWT

**200 response:** account object (see section 16).

**Error responses:** 401, 403 forbidden (not owner), 404 not_found.

**Example:** `curl -sS http://localhost:8080/api/v2/me/accounts/42 -H "Authorization: Bearer $T"`

### GET /api/v2/me/cards

List the authenticated user's cards.

**Auth:** JWT

**200 response:** `{"cards": [...], "total": N}`. Card objects include `id, card_number (masked), card_brand, owner_id, owner_type, account_number, status, is_virtual, usage_type, created_at` etc.

**Error responses:** 401, 500.

**Example:** `curl -sS http://localhost:8080/api/v2/me/cards -H "Authorization: Bearer $T"`

### GET /api/v2/me/cards/{id}

Get one of the authenticated user's cards. Ownership is enforced.

**Auth:** JWT

**200 response:** card object (masked `card_number`).

**Error responses:** 401, 403, 404.

### POST /api/v2/me/cards/{id}/pin

Set the 4-digit PIN for a card (first-time setup). Ownership enforced.

**Auth:** client JWT (`RequireClientToken`)

**Request body:**

| Field | Type   | Required | Constraints    |
|-------|--------|----------|----------------|
| pin   | string | yes      | exactly 4 digits |

**200 response:** `{"success": true}` plus card echo.

**Error responses:** 400 validation_error (pin format), 401, 403, 404.

### POST /api/v2/me/cards/{id}/verify-pin

Verify a card PIN (client-side gate). Card is locked after 3 consecutive failed attempts.

**Auth:** client JWT

**Request body:**

| Field | Type   | Required | Constraints    |
|-------|--------|----------|----------------|
| pin   | string | yes      | exactly 4 digits |

**200 response:** `{"success": bool, "remaining_attempts": int}`.

**Error responses:** 400, 401, 403, 404, 409 business_rule_violation (card locked).

### POST /api/v2/me/cards/{id}/temporary-block

Block the card temporarily. Auto-unblocked after `duration_hours` by background goroutine.

**Auth:** client JWT

**Request body:**

| Field          | Type   | Required | Constraints                     |
|----------------|--------|----------|---------------------------------|
| duration_hours | int32  | yes      | 1..720                          |
| reason         | string | no       | free text                       |

**200 response:** card object with updated block status.

**Error responses:** 400, 401, 403, 404, 500.

### POST /api/v2/me/cards/virtual

Create a virtual card for one of the caller's accounts. `owner_id` in the body is ignored — the gateway derives the owner from the JWT.

**Auth:** JWT

**Request body:**

| Field          | Type   | Required | Constraints                                          |
|----------------|--------|----------|------------------------------------------------------|
| account_number | string | yes      | caller-owned account                                 |
| card_brand     | string | yes      | one of `visa, mastercard, dinacard, amex`            |
| usage_type     | string | yes      | one of `single_use, multi_use`                       |
| max_uses       | int32  | cond.    | required when `usage_type == multi_use`; `>= 2`      |
| expiry_months  | int32  | yes      | 1..3                                                 |
| card_limit     | string | yes      | decimal string (RSD-equivalent spend cap)            |

**201 response:** virtual card object incl. `card_number` and `cvv` (returned only once).

**Error responses:** 400, 401, 403 (account not owned), 404, 500.

### POST /api/v2/me/cards/requests

Client submits a card-issuance request. Processed by an employee via `/api/v2/cards/requests/:id/approve`.

**Auth:** client JWT

**Request body:**

| Field          | Type   | Required | Constraints                                      |
|----------------|--------|----------|--------------------------------------------------|
| account_number | string | yes      | caller-owned account                             |
| card_brand     | string | yes      | one of `visa, mastercard, dinacard, amex`        |
| card_type      | string | no       | free text (e.g., `"debit"`)                      |
| card_name      | string | no       | display name                                     |

**201 response:** card-request object `{id, client_id, account_number, card_brand, status:"pending", created_at}`.

**Error responses:** 400, 401, 500.

### GET /api/v2/me/cards/requests

List the authenticated client's card requests.

**Auth:** client JWT

**Query:** `page`, `page_size`.

**200 response:** `{"requests": [...], "total": N}`.

### POST /api/v2/me/payments

Create a pending payment. A verification challenge (email code) is created automatically.

**Auth:** JWT

**Request body:**

| Field                | Type    | Required | Constraints                                |
|----------------------|---------|----------|--------------------------------------------|
| from_account_number  | string  | yes      | caller-owned account                       |
| to_account_number    | string  | yes      | must differ from from_account_number       |
| amount               | float64 | yes      | > 0                                        |
| recipient_name       | string  | no       |                                            |
| payment_code         | string  | no       | Serbian payment code                       |
| reference_number     | string  | no       |                                            |
| payment_purpose      | string  | no       |                                            |

**201 response:** payment object `{id, client_id, from_account_number, to_account_number, amount, status:"pending", verification_code_expires_at, created_at, ...}`.

**Error responses:** 400, 401, 403 (source not owned), 500.

### GET /api/v2/me/payments

List the caller's payments.

**Auth:** JWT

**Query:** `page`, `page_size`.

**200 response:** `{"payments": [...], "total": N}`.

### GET /api/v2/me/payments/{id}

Get one payment (ownership enforced).

**Auth:** JWT

**200 response:** payment object.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/me/payments/{id}/execute

Execute a pending payment after verification.

**Auth:** JWT

**Request body:**

| Field             | Type   | Required | Constraints                                 |
|-------------------|--------|----------|---------------------------------------------|
| verification_code | string | cond.    | required unless `verification.skip` grants  |
| challenge_id      | uint64 | cond.    | mobile verification challenge id (optional) |

**200 response:** updated payment object (`status` now `completed` or `failed`).

**Error responses:** 400, 401, 409 business_rule_violation (bad code, expired, insufficient balance), 500.

### POST /api/v2/me/transfers

Create a pending transfer between two accounts (same or different currency).

**Auth:** JWT

**Request body:**

| Field                | Type    | Required | Constraints                        |
|----------------------|---------|----------|------------------------------------|
| from_account_number  | string  | yes      | caller-owned account               |
| to_account_number    | string  | yes      | differs from `from_account_number` |
| amount               | float64 | yes      | > 0                                |

**201 response:** transfer object (incl. `verification_code_expires_at, fee_breakdown, exchange_rate` when cross-currency).

**Error responses:** 400, 401, 403, 500.

### POST /api/v2/me/transfers/preview

Preview fees + exchange rate for a transfer without creating it.

**Auth:** JWT

**Request body:** same as `POST /api/v2/me/transfers`.

**200 response:**

| Field         | Type   | Always present | Notes                                                    |
|---------------|--------|----------------|----------------------------------------------------------|
| from_currency | string | yes            |                                                          |
| to_currency   | string | yes            |                                                          |
| input_amount  | string | yes            | normalised 4-decimal                                     |
| total_fee     | string | yes            | sum of applied fees                                      |
| fee_breakdown | array  | yes            | per-rule fee line items                                  |
| converted_amount | string | when cross-currency | from exchange-service                            |
| effective_rate   | string | when cross-currency |                                                   |

**Error responses:** 400, 401, 500.

### GET /api/v2/me/transfers

List the caller's transfers.

**Auth:** JWT

**Query:** `page`, `page_size`.

**200 response:** `{"transfers": [...], "total": N}`.

### GET /api/v2/me/transfers/{id}

Get one transfer (ownership enforced).

**Auth:** JWT

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/me/transfers/{id}/execute

Execute a pending transfer after verification.

**Auth:** JWT

**Request body:**

| Field             | Type   | Required | Constraints                                |
|-------------------|--------|----------|--------------------------------------------|
| verification_code | string | cond.    | required unless `verification.skip` grants |
| challenge_id      | uint64 | cond.    | mobile challenge id                        |

**200 response:** updated transfer.

**Error responses:** 400, 401, 409, 500.

### POST /api/v2/me/payment-recipients

Create a payment recipient scoped to the authenticated client.

**Auth:** JWT

**Request body:**

| Field          | Type   | Required | Constraints     |
|----------------|--------|----------|-----------------|
| recipient_name | string | yes      |                 |
| account_number | string | yes      |                 |

**201 response:** recipient object `{id, client_id, recipient_name, account_number, created_at}`.

### GET /api/v2/me/payment-recipients

List the caller's payment recipients.

**Auth:** JWT

**200 response:** `{"recipients": [...]}`.

### PUT /api/v2/me/payment-recipients/{id}

Update a recipient (ownership enforced).

**Auth:** JWT

**Request body (all optional):**

| Field          | Type   | Required | Constraints |
|----------------|--------|----------|-------------|
| recipient_name | string | no       |             |
| account_number | string | no       |             |

**200 response:** updated recipient.

**Error responses:** 400, 401, 403, 404.

### DELETE /api/v2/me/payment-recipients/{id}

Delete a recipient (ownership enforced).

**Auth:** JWT

**200 response:** `{"success": true}`.

### POST /api/v2/me/loan-requests

Create a loan application for the authenticated client. `client_id` in the body is ignored.

**Auth:** JWT

**Request body:**

| Field             | Type    | Required | Constraints                                                             |
|-------------------|---------|----------|-------------------------------------------------------------------------|
| loan_type         | string  | yes      | one of `cash, housing, auto, refinancing, student`                      |
| interest_type     | string  | yes      | one of `fixed, variable`                                                |
| amount            | float64 | yes      | > 0                                                                     |
| currency_code     | string  | yes      |                                                                         |
| purpose           | string  | no       |                                                                         |
| monthly_salary    | float64 | no       |                                                                         |
| employment_status | string  | no       |                                                                         |
| employment_period | int32   | no       | months                                                                  |
| repayment_period  | int32   | yes      | cash/auto/refi/student: {12,24,36,48,60,72,84}; housing: {60,120,...,360} |
| phone             | string  | no       |                                                                         |
| account_number    | string  | yes      |                                                                         |

**201 response:** loan-request object (status `pending`).

**Error responses:** 400, 401, 500.

### GET /api/v2/me/loan-requests

List the caller's loan requests.

**Auth:** JWT

**200 response:** `{"requests": [...], "total": N}`.

### GET /api/v2/me/loans

List the caller's active/approved loans.

**Auth:** JWT

**200 response:** `{"loans": [...], "total": N}`.

### GET /api/v2/me/loans/{id}

Get one loan (ownership enforced).

**Auth:** JWT

**Error responses:** 400, 401, 403, 404.

### GET /api/v2/me/loans/{id}/installments

List installments for one of the caller's loans.

**Auth:** JWT

**200 response:** `{"installments": [...]}`. Each item: `{id, loan_id, amount, interest_rate, currency_code, expected_date, actual_date, status}`.

### POST /api/v2/me/orders

Create a securities order for the authenticated user.

**Auth:** JWT

**Request body:**

| Field          | Type    | Required | Constraints                                                                 |
|----------------|---------|----------|-----------------------------------------------------------------------------|
| security_type  | string  | no       | one of `stock, futures, forex, option`; auto-derived from listing when absent |
| listing_id     | uint64  | cond.    | required for buy orders                                                     |
| direction      | string  | yes      | one of `buy, sell`                                                          |
| order_type     | string  | yes      | one of `market, limit, stop, stop_limit`                                    |
| quantity       | int64   | yes      | > 0                                                                         |
| limit_value    | string  | cond.    | required for `limit, stop_limit`                                            |
| stop_value     | string  | cond.    | required for `stop, stop_limit`                                             |
| all_or_none    | bool    | no       |                                                                             |
| margin         | bool    | no       |                                                                             |
| account_id     | uint64  | yes      | proceeds destination / debit source; caller-owned                           |
| base_account_id| uint64  | cond.    | required when `security_type == forex`; must differ from `account_id`       |

**201 response:** order object.

**Order object fields (also returned by GET /api/v2/me/orders and GET /api/v2/me/orders/{id}):**

| Field               | Type    | Notes                                                                                     |
|---------------------|---------|-------------------------------------------------------------------------------------------|
| id                  | uint64  |                                                                                           |
| user_id             | uint64  |                                                                                           |
| listing_id          | uint64  |                                                                                           |
| security_type       | string  | `stock` \| `futures` \| `forex` \| `option`                                                |
| ticker              | string  |                                                                                           |
| direction           | string  | `buy` \| `sell`                                                                            |
| order_type          | string  | `market` \| `limit` \| `stop` \| `stop_limit`                                              |
| quantity            | int64   | total quantity requested                                                                  |
| filled_quantity     | int64   | `quantity - remaining_portions` — how much of the order has executed                      |
| contract_size       | int64   |                                                                                           |
| price_per_unit      | string  | decimal                                                                                   |
| approximate_price   | string  |                                                                                           |
| commission          | string  |                                                                                           |
| status              | string  | raw status kept for backwards compatibility                                                |
| state               | string  | derived: `pending` \| `approved` \| `filling` \| `filled` \| `cancelled` \| `declined`     |
| approved_by         | string  |                                                                                           |
| is_done             | bool    |                                                                                           |
| remaining_portions  | int64   |                                                                                           |
| after_hours         | bool    |                                                                                           |
| all_or_none         | bool    |                                                                                           |
| margin              | bool    |                                                                                           |
| account_id          | uint64  |                                                                                           |
| acting_employee_id  | uint64  | non-zero when an employee placed the order on behalf                                       |
| limit_value         | string  | present for `limit` / `stop_limit`                                                         |
| stop_value          | string  | present for `stop` / `stop_limit`                                                          |
| last_modification   | string  | RFC3339                                                                                   |
| created_at          | string  | RFC3339                                                                                   |

**Error responses:** 400, 401, 403, 404, 409 business_rule_violation (insufficient available balance), 500.

### GET /api/v2/me/orders

List the caller's orders.

**Auth:** JWT

**Query:** `page` (default 1), `page_size` (default 10), `status`.

**200 response:** `{"orders": [...], "total_count": N}`.

### GET /api/v2/me/orders/{id}

Get one order (ownership enforced).

**Auth:** JWT

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/me/orders/{id}/cancel

Cancel an open order belonging to the caller.

**Auth:** JWT

**200 response:** updated order (status `cancelled`).

**Error responses:** 400, 401, 403, 404, 409 (already filled/cancelled).

### GET /api/v2/me/portfolio

List the caller's holdings (stock/futures/options). Holdings are rolled up per `(user_id, system_type, security_type, security_id)` — buying the same security from two different accounts produces one holding row, not two.

**Auth:** JWT

**Query:** `page` (default 1), `page_size` (default 10), `security_type` (optional; one of `stock, futures, option`).

**200 response:** `{"holdings": [...], "total_count": N}`.

**Holding object fields (trimmed quantity-only view; use the per-holding transactions endpoint for price/FX/commission history):**

| Field            | Type   | Notes                                                            |
|------------------|--------|------------------------------------------------------------------|
| id               | uint64 | holding ID                                                       |
| security_type    | string | `stock` \| `futures` \| `option`                                   |
| ticker           | string |                                                                  |
| name             | string |                                                                  |
| quantity         | int64  | total units held (aggregated across accounts)                    |
| public_quantity  | int64  | portion exposed as OTC-offerable via `/me/portfolio/{id}/make-public` |
| account_id       | uint64 | most recent account used to acquire/settle this security          |
| last_modified    | string | RFC3339                                                          |

### GET /api/v2/me/holdings/{id}/transactions

List the executed order-transactions that contributed to a holding. Replaces the per-purchase detail that was removed from `/me/portfolio` during the aggregation rollup: use this to answer "when did each buy/sell happen, at what price, in what currency, through which account."

**Auth:** JWT (ownership enforced)

**Path params:** `id` — holding ID

**Query:** `direction` (optional; one of `buy, sell`), `page` (default 1), `page_size` (default 10).

**200 response:** `{"transactions": [...], "total_count": N}`.

**Transaction object fields:**

| Field            | Type   | Notes                                                        |
|------------------|--------|--------------------------------------------------------------|
| id               | uint64 | order_transactions.id                                        |
| order_id         | uint64 |                                                              |
| executed_at      | string | RFC3339                                                      |
| direction        | string | `buy` \| `sell`                                                |
| quantity         | int64  |                                                              |
| price_per_unit   | string | decimal, in native listing currency                          |
| native_amount    | string | quantity × price_per_unit in native currency                  |
| native_currency  | string | listing currency (one of the 8 supported: RSD/EUR/CHF/USD/GBP/JPY/CAD/AUD) |
| converted_amount | string | amount debited/credited in `account_currency`                 |
| account_currency | string | currency of the settlement account                            |
| fx_rate          | string | exchange rate applied; equals `1` when native == account       |
| commission       | string | in account currency                                          |
| account_id       | uint64 | the specific account this transaction hit                     |
| ticker           | string |                                                              |

**Error responses:** 400 (invalid `direction`), 401, 404 (holding not found or not owned by caller).

### GET /api/v2/me/portfolio/summary

Aggregate value + P/L across the caller's portfolio.

**Auth:** JWT

**200 response:** summary object (total market value, realised/unrealised P/L, per-security-type breakdown).

### POST /api/v2/me/portfolio/{id}/make-public

Expose a portion of a holding as OTC-offerable. `id` is the holding ID.

**Auth:** JWT

**Request body:**

| Field    | Type  | Required | Constraints |
|----------|-------|----------|-------------|
| quantity | int64 | yes      | > 0         |

**200 response:** updated holding.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/me/portfolio/{id}/exercise

Exercise an option holding (by holding ID). Prefer the v2 `/api/v2/options/:option_id/exercise` where possible.

**Auth:** JWT

**200 response:** `{id, option_ticker, exercised_quantity, shares_affected, profit}`.

**Error responses:** 400, 401, 403, 404, 409.

### GET /api/v2/me/tax

List the caller's capital-gains tax records and current-month balance.

**Auth:** JWT

**Query:** `page`, `page_size`.

**200 response:**

| Field                  | Type   | Always present | Notes |
|------------------------|--------|----------------|-------|
| records                | array  | yes            | per-trade tax lines |
| total_count            | int64  | yes            |       |
| tax_paid_this_year     | string | yes            | decimal |
| tax_unpaid_this_month  | string | yes            | decimal |
| collections            | array  | yes            | per-month collection events |

### GET /api/v2/me/sessions

List the caller's active sessions (refresh tokens + metadata).

**Auth:** JWT

**200 response:** `{"sessions": [{id, user_role, ip_address, user_agent, device_id, system_type, last_active_at, created_at, is_current}, ...]}`.

### POST /api/v2/me/sessions/revoke

Revoke one specific session by ID (caller must own the session).

**Auth:** JWT

**Request body:**

| Field      | Type  | Required | Constraints |
|------------|-------|----------|-------------|
| session_id | int64 | yes      | non-zero    |

**200 response:** `{"message": "session revoked successfully"}`.

**Error responses:** 400, 401, 404.

### POST /api/v2/me/sessions/revoke-others

Revoke every session except the current one.

**Auth:** JWT

**Request body:**

| Field                 | Type   | Required | Constraints                            |
|-----------------------|--------|----------|----------------------------------------|
| current_refresh_token | string | yes      | refresh token of the session to keep   |

**200 response:** `{"message": "all other sessions revoked successfully"}`.

### GET /api/v2/me/login-history

Recent login attempts (successful + failed) for the caller.

**Auth:** JWT

**Query:** `limit` (default 50, max 100).

**200 response:** `{"entries": [{id, ip_address, user_agent, device_type, success, created_at}, ...]}`.

### GET /api/v2/me/notifications

List persistent in-app notifications for the caller.

**Auth:** JWT

**Query:** `page` (default 1), `page_size` (default 20, max 100), `read` (`read` | `unread` | omit for all).

**200 response:** `{"notifications": [{id, type, title, message, is_read, ref_type, ref_id, created_at}, ...], "total": N}`.

### GET /api/v2/me/notifications/unread-count

Unread notifications count for the caller.

**Auth:** JWT

**200 response:** `{"unread_count": N}`.

### POST /api/v2/me/notifications/read-all

Mark all unread notifications as read.

**Auth:** JWT

**200 response:** `{"success": true, "count": N}`.

### POST /api/v2/me/notifications/{id}/read

Mark a single notification as read.

**Auth:** JWT

**200 response:** `{"success": true}`.

**Error responses:** 400, 401, 404.

---

## 4. Stock Exchanges

Browse exchanges. Admin "testing mode" toggles (Section 26) are in a separate group.

### GET /api/v2/stock-exchanges

List stock exchanges.

**Auth:** JWT (`AnyAuthMiddleware`)

**Query:** `page` (default 1), `page_size` (default 10), `search`.

**200 response:** `{"exchanges": [...], "total_count": N}`.

### GET /api/v2/stock-exchanges/{id}

Get one stock exchange.

**Auth:** JWT

**200 response:** exchange object `{id, name, acronym, mic_code, country, currency_code, timezone, open_time, close_time, created_at}`.

**Error responses:** 400, 401, 404.

---

## 5. Securities (market data)

Browsable by any authenticated user (`AnyAuthMiddleware`). Every security item (`stocks[]`, `futures[]`, `forex_pairs[]`, `options[]`) carries the same embedded `listing` object so clients can read price data uniformly.

**Embedded listing object (present on every security response):**

| Field                | Type   | Notes                                                                                   |
|----------------------|--------|-----------------------------------------------------------------------------------------|
| id                   | uint64 | listing ID                                                                              |
| exchange_id          | uint64 |                                                                                         |
| exchange_acronym     | string |                                                                                         |
| currency             | string | listing currency, one of `RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`. Tells the client whether `price: 155.0000` is in USD, EUR, etc. |
| price                | string | decimal                                                                                 |
| high                 | string | decimal                                                                                 |
| low                  | string | decimal                                                                                 |
| change               | string | decimal                                                                                 |
| change_percent       | string | decimal                                                                                 |
| volume               | int64  |                                                                                         |
| initial_margin_cost  | string | decimal; total margin a buy order would reserve per unit                                |
| last_refresh         | string | RFC3339                                                                                 |

### GET /api/v2/securities/stocks

List stocks.

**Auth:** JWT

**Query:** `page`, `page_size`, `search`, `exchange_acronym`, `min_price`, `max_price`, `min_volume`, `max_volume`, `sort_by` (one of `price, volume, change, margin`), `sort_order` (`asc|desc`, default `asc`).

**200 response:** `{"stocks": [...], "total_count": N}`. Each stock: `{id, ticker, name, sector, industry, listing: {…see embedded listing object above…}}`.

### GET /api/v2/securities/stocks/{id}

Get one stock listing.

**200 response:** same stock item shape as the list endpoint, including the embedded `listing` (with `currency`).

**Error responses:** 400, 401, 404.

### GET /api/v2/securities/stocks/{id}/history

Historical price history for a stock.

**Query:** `period` (`day, week, month, year, 5y, all`, default `month`), `page`, `page_size`.

**200 response:** `{"history": [...], "total_count": N}`.

### GET /api/v2/securities/futures

List futures contracts.

**Query:** `page`, `page_size`, `search`, `exchange_acronym`, `min_price`, `max_price`, `min_volume`, `max_volume`, `settlement_date_from`, `settlement_date_to`, `sort_by`, `sort_order`.

**200 response:** `{"futures": [...], "total_count": N}`.

### GET /api/v2/securities/futures/{id}

Get one futures contract.

### GET /api/v2/securities/futures/{id}/history

Historical price history for a futures contract. Same query shape as stocks history.

### GET /api/v2/securities/forex

List forex pairs.

**Query:** `page`, `page_size`, `search`, `base_currency`, `quote_currency`, `liquidity` (`high|medium|low`), `sort_by`, `sort_order`.

**200 response:** `{"forex_pairs": [...], "total_count": N}`.

### GET /api/v2/securities/forex/{id}

Get one forex pair.

### GET /api/v2/securities/forex/{id}/history

Historical price history for a forex pair.

### GET /api/v2/securities/options

List option contracts for a given stock.

**Query:** `stock_id` (required), `option_type` (`call|put`), `settlement_date`, `min_strike`, `max_strike`, `page`, `page_size`.

**200 response:** `{"options": [...], "total_count": N}`.

**Error responses:** 400 (missing `stock_id`), 401.

### GET /api/v2/securities/options/{id}

Get one option contract.

### GET /api/v2/securities/candles

OHLCV candles for a listing from InfluxDB.

**Query:** `listing_id` (required), `interval` (one of `1m, 5m, 15m, 1h, 4h, 1d`; default `1h`), `from` (RFC3339, required), `to` (RFC3339, required).

**200 response:** `{"candles": [{time, open, high, low, close, volume}, ...], "count": N}`.

**Error responses:** 400, 401, 500.

---

## 6. OTC Offers

Browsing uses `AnyAuthMiddleware`; buying requires `securities.trade`. The admin buy-on-behalf route is in Section 29.

### GET /api/v2/otc/offers

List public OTC offers.

**Auth:** JWT

**Query:** `page` (default 1), `page_size` (default 10), `security_type` (`stock|futures`), `ticker`.

**200 response:** `{"offers": [...], "total_count": N}`.

### POST /api/v2/otc/offers/{id}/buy

Buy an OTC offer with one of the caller's own accounts.

**Auth:** JWT + `securities.trade`

**Request body:**

| Field      | Type   | Required | Constraints                        |
|------------|--------|----------|------------------------------------|
| quantity   | int64  | yes      | > 0                                |
| account_id | uint64 | yes      | caller-owned settlement account    |

**200 response:** OTC purchase result.

**Error responses:** 400, 401, 403, 404, 409.

---

## 7. Mobile Auth & Device

Mobile flows use a separate JWT with `system_type: "mobile"` and require the `X-Device-ID` header for authenticated requests. Mobile access tokens typically last longer than web access tokens; refresh tokens default to 90 days (`MOBILE_REFRESH_EXPIRY`).

### POST /api/v2/mobile/auth/request-activation

Request an activation code to be emailed to the user. Used when registering a new device.

**Auth:** none

**Request body:**

| Field | Type   | Required | Constraints        |
|-------|--------|----------|--------------------|
| email | string | yes      | valid email format |

**200 response:** `{"success": bool, "message": string}`.

### POST /api/v2/mobile/auth/activate

Exchange the emailed activation code for a mobile JWT + device credentials.

**Auth:** none

**Request body:**

| Field       | Type   | Required | Constraints              |
|-------------|--------|----------|--------------------------|
| email       | string | yes      | valid email              |
| code        | string | yes      | exactly 6 digits         |
| device_name | string | yes      | free text                |

**200 response:**

| Field         | Type   | Always present | Notes                               |
|---------------|--------|----------------|-------------------------------------|
| access_token  | string | yes            | mobile JWT                          |
| refresh_token | string | yes            | mobile refresh token (long-lived)   |
| device_id     | string | yes            | stable device identifier            |
| device_secret | string | yes            | HMAC secret for signed requests     |

**Error responses:** 400 validation_error, 404 not_found (bad code / email / expired).

### POST /api/v2/mobile/auth/refresh

Refresh mobile access token.

**Auth:** none; requires `X-Device-ID` header.

**Request body:**

| Field         | Type   | Required | Constraints |
|---------------|--------|----------|-------------|
| refresh_token | string | yes      | non-empty   |

**200 response:** `{access_token, refresh_token}`.

**Error responses:** 400 missing device id / body, 401.

### GET /api/v2/mobile/device

Info about the current mobile device.

**Auth:** `MobileAuthMiddleware`

**200 response:** `{device_id, device_name, status, activated_at, last_seen_at}`.

### POST /api/v2/mobile/device/deactivate

Deactivate the current device.

**Auth:** mobile JWT

**200 response:** `{"success": true}`.

### POST /api/v2/mobile/device/transfer

Deactivate the current device and email a fresh activation code to the given address (device-transfer flow).

**Auth:** mobile JWT

**Request body:**

| Field | Type   | Required | Constraints |
|-------|--------|----------|-------------|
| email | string | yes      | valid email |

**200 response:** `{success, message}`.

### POST /api/v2/mobile/device/biometrics

Enable/disable biometric verification for the device. Requires device signature (`RequireDeviceSignature`).

**Auth:** mobile JWT + device signature

**Request body:**

| Field   | Type | Required | Constraints |
|---------|------|----------|-------------|
| enabled | bool | yes      |             |

**200 response:** `{"success": true}`.

### GET /api/v2/mobile/device/biometrics

Read current biometrics setting.

**Auth:** mobile JWT + device signature

**200 response:** `{"enabled": bool}`.

---

## 8. Mobile Verifications

All routes require `MobileAuthMiddleware + RequireDeviceSignature`.

### GET /api/v2/mobile/verifications/pending

Pending verification inbox items for the current device.

**200 response:** `{"items": [{id, challenge_id, method, display_data, expires_at}, ...]}`.

### POST /api/v2/mobile/verifications/{id}/submit

Submit a mobile verification response (e.g., typed code, selected number).

**Path:** `id` = challenge ID.

**Request body:**

| Field    | Type   | Required | Constraints                                    |
|----------|--------|----------|------------------------------------------------|
| response | string | yes      | depends on method (code / selected number / …) |

**200 response:** `{"success": bool, "remaining_attempts": int}`.

**Error responses:** 400, 401, 404, 409 (expired/already verified).

### POST /api/v2/mobile/verifications/{id}/ack

Acknowledge delivery of an inbox item so it no longer appears in subsequent polls.

**Path:** `id` = inbox item ID.

**200 response:** `{"success": true}`.

**Error responses:** 400, 404.

### POST /api/v2/mobile/verifications/{id}/biometric

Verify a challenge using device biometrics. No body — the device signature is the proof.

**Path:** `id` = challenge ID.

**200 response:** `{"success": true}`.

**Error responses:** 400, 401, 403 (biometrics not enabled), 409 (expired/already verified).

---

## 9. QR Verification

Mobile-scan endpoint that completes a QR-method challenge initiated by a browser.

### POST /api/v2/verify/{challenge_id}

Submit the QR token scanned from the browser.

**Auth:** `MobileAuthMiddleware + RequireDeviceSignature`

**Query:** `token` (required) — the token encoded in the QR image.

**200 response:** `{"success": true}`.

**Error responses:** 400 (missing token / invalid id), 401, 404, 409.

---

## 10. Browser Verifications

Create/poll/submit for browser-initiated challenges.

### POST /api/v2/verifications

Create a challenge.

**Auth:** JWT

**Request body:**

| Field          | Type   | Required | Constraints                            |
|----------------|--------|----------|----------------------------------------|
| source_service | string | yes      | e.g., `"transaction-service"`          |
| source_id      | uint64 | yes      | referenced entity id                   |
| method         | string | no       | currently only `code_pull` (default)   |

**200 response:** `{challenge_id, challenge_data, expires_at}`.

### GET /api/v2/verifications/{id}/status

Poll challenge status.

**Auth:** JWT

**200 response:** `{status, method, verified_at, expires_at}`.

### POST /api/v2/verifications/{id}/code

Submit a code (pull method) from the browser.

**Auth:** JWT

**Request body:**

| Field | Type   | Required | Constraints |
|-------|--------|----------|-------------|
| code  | string | yes      |             |

**200 response:** `{"success": bool, "remaining_attempts": int}`.

**Error responses:** 400, 401, 404, 409.

---

## 11. Employees

Employee management is protected by role-based permissions. Admin employees (`EmployeeAdmin`) cannot be modified via `PUT /employees/:id` — the handler returns 403 when targeting an admin.

### GET /api/v2/employees

List employees.

**Auth:** JWT + `employees.read`

**Query:** `page` (default 1), `page_size` (default 20), `email_filter`, `name_filter`.

**200 response:** `{"employees": [...], "total": N}`. Each employee has `id, first_name, last_name, email, username, position, department, role, roles, permissions, active, jmbg, ...`.

**Error responses:** 401, 403, 500.

### GET /api/v2/employees/{id}

Get one employee.

**Auth:** JWT + `employees.read`

**200 response:** employee object.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/employees

Create an employee. Triggers an activation email via Kafka; the account is created in "pending" status and requires activation before first login.

**Auth:** JWT + `employees.create`

**Request body:**

| Field          | Type   | Required | Constraints                                     |
|----------------|--------|----------|-------------------------------------------------|
| first_name     | string | yes      |                                                 |
| last_name      | string | yes      |                                                 |
| date_of_birth  | int64  | yes      | Unix seconds                                    |
| gender         | string | no       |                                                 |
| email          | string | yes      | valid, unique                                   |
| phone          | string | no       |                                                 |
| address        | string | no       |                                                 |
| jmbg           | string | yes      | exactly 13 digits, unique                       |
| username       | string | yes      | unique                                          |
| position       | string | no       |                                                 |
| department     | string | no       |                                                 |
| role           | string | yes      | one of `EmployeeBasic, EmployeeAgent, EmployeeSupervisor, EmployeeAdmin` |

**201 response:** employee object (`active: false`).

**Error responses:** 400, 401, 403, 409 conflict (email/jmbg/username duplicate), 500.

### PUT /api/v2/employees/{id}

Partial update of an employee (non-admin targets only).

**Auth:** JWT + `employees.update`

**Request body (all optional):**

| Field      | Type   | Required | Constraints                      |
|------------|--------|----------|----------------------------------|
| last_name  | string | no       |                                  |
| gender     | string | no       |                                  |
| phone      | string | no       |                                  |
| address    | string | no       |                                  |
| jmbg       | string | no       | 13 digits, unique                |
| position   | string | no       |                                  |
| department | string | no       |                                  |
| role       | string | no       | one of the role names above      |
| active     | bool   | no       | toggles auth-service account status |

**200 response:** updated employee.

**Error responses:** 400, 401, 403 (cannot edit admin), 404, 500.

### GET /api/v2/employees/{id}/limits

Read an employee's transaction/approval limits.

**Auth:** JWT + `limits.manage`

**200 response:**

| Field                      | Type   | Always present | Notes                                  |
|----------------------------|--------|----------------|----------------------------------------|
| employee_id                | int64  | yes            |                                        |
| max_loan_approval_amount   | string | yes            | decimal                                |
| max_single_transaction     | string | yes            | decimal                                |
| max_daily_transaction      | string | yes            | decimal                                |
| max_client_daily_limit     | string | yes            | decimal                                |
| max_client_monthly_limit   | string | yes            | decimal                                |

**Error responses:** 400, 401, 403, 500.

### PUT /api/v2/employees/{id}/limits

Set an employee's limits.

**Auth:** JWT + `limits.manage`

**Request body (all decimal strings, all optional):**

| Field                      | Type   | Required | Constraints   |
|----------------------------|--------|----------|---------------|
| max_loan_approval_amount   | string | no       | decimal       |
| max_single_transaction     | string | no       | decimal       |
| max_daily_transaction      | string | no       | decimal       |
| max_client_daily_limit     | string | no       | decimal       |
| max_client_monthly_limit   | string | no       | decimal       |

**200 response:** updated limits.

**Error responses:** 400, 401, 403, 500.

### POST /api/v2/employees/{id}/limits/template

Apply a named limit template to an employee.

**Auth:** JWT + `limits.manage`

**Request body:**

| Field         | Type   | Required | Constraints        |
|---------------|--------|----------|--------------------|
| template_name | string | yes      | e.g., `BasicTeller`|

**200 response:** updated limits.

**Error responses:** 400, 401, 403, 404 (template not found).

---

## 12. Roles & Permissions

All routes require `employees.permissions`.

### GET /api/v2/roles

List all roles.

**Auth:** JWT + `employees.permissions`

**200 response:** `{"roles": [{id, name, description, permissions: [<code>, ...]}, ...]}`.

### GET /api/v2/roles/{id}

Get one role.

**200 response:** role object.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/roles

Create a role.

**Request body:**

| Field            | Type     | Required | Constraints       |
|------------------|----------|----------|-------------------|
| name             | string   | yes      | unique            |
| description      | string   | no       |                   |
| permission_codes | []string | no       | each must exist   |

**201 response:** role object.

**Error responses:** 400, 401, 403, 409, 500.

### PUT /api/v2/roles/{id}/permissions

Replace the permission set for a role.

**Request body:**

| Field            | Type     | Required | Constraints     |
|------------------|----------|----------|-----------------|
| permission_codes | []string | yes      | each must exist |

**200 response:** updated role.

**Error responses:** 400, 401, 403, 404, 500.

### GET /api/v2/permissions

List all known permissions.

**200 response:** `{"permissions": [{id, code, description, category}, ...]}`.

### PUT /api/v2/employees/{id}/roles

Replace the roles assigned to an employee.

**Request body:**

| Field      | Type     | Required | Constraints                 |
|------------|----------|----------|-----------------------------|
| role_names | []string | yes      | each must be an existing role |

**200 response:** updated employee.

**Error responses:** 400, 401, 403, 404.

### PUT /api/v2/employees/{id}/permissions

Replace the per-employee additional permissions.

**Request body:**

| Field            | Type     | Required | Constraints       |
|------------------|----------|----------|-------------------|
| permission_codes | []string | yes      | each must exist   |

**200 response:** updated employee.

**Error responses:** 400, 401, 403, 404.

---

## 13. Limits

Endpoints live under `/employees/:id/limits`, `/limits/templates`, and `/clients/:id/limits`. All require `limits.manage`.

### GET /api/v2/limits/templates

List limit templates.

**Auth:** JWT + `limits.manage`

**200 response:** `{"templates": [...]}`.

### POST /api/v2/limits/templates

Create a limit template.

**Request body:**

| Field                      | Type   | Required | Constraints     |
|----------------------------|--------|----------|-----------------|
| name                       | string | yes      | unique          |
| description                | string | no       |                 |
| max_loan_approval_amount   | string | no       | decimal         |
| max_single_transaction     | string | no       | decimal         |
| max_daily_transaction      | string | no       | decimal         |
| max_client_daily_limit     | string | no       | decimal         |
| max_client_monthly_limit   | string | no       | decimal         |

**201 response:** created template.

### GET /api/v2/clients/{id}/limits

Read a client's transaction limits.

**Auth:** JWT + `limits.manage`

**200 response:** `{client_id, daily_limit, monthly_limit, transfer_limit, ...}`.

**Error responses:** 400, 401, 403, 500.

### PUT /api/v2/clients/{id}/limits

Set a client's limits. Server constrains the values to the setting employee's own `max_client_daily_limit` and `max_client_monthly_limit`.

**Auth:** JWT + `limits.manage`

**Request body:**

| Field          | Type   | Required | Constraints |
|----------------|--------|----------|-------------|
| daily_limit    | string | yes      | decimal     |
| monthly_limit  | string | yes      | decimal     |
| transfer_limit | string | yes      | decimal     |

**200 response:** updated client limits.

**Error responses:** 400 (exceeds setting employee's cap), 401, 403, 500.

---

## 14. Blueprints

Limit-blueprint CRUD. Blueprints are named parameter bundles that can be applied to employees, actuaries, or clients. All routes require `limits.manage`.

### GET /api/v2/blueprints

List blueprints.

**Auth:** JWT + `limits.manage`

**Query:** `type` (optional; one of `employee, actuary, client`).

**200 response:** `{"blueprints": [...]}`.

**Error responses:** 400 (bad type), 401, 403.

### POST /api/v2/blueprints

Create a blueprint.

**Request body:**

| Field       | Type         | Required | Constraints                                 |
|-------------|--------------|----------|---------------------------------------------|
| name        | string       | yes      |                                             |
| description | string       | no       |                                             |
| type        | string       | yes      | one of `employee, actuary, client`          |
| values      | JSON object  | yes      | type-specific payload                       |

**201 response:** blueprint object.

**Error responses:** 400, 401, 403, 409 (duplicate name+type), 500.

### GET /api/v2/blueprints/{id}

Get one blueprint.

**200 response:** blueprint object.

**Error responses:** 400, 401, 403, 404, 500.

### PUT /api/v2/blueprints/{id}

Update a blueprint.

**Request body (all optional):**

| Field       | Type         | Required | Constraints |
|-------------|--------------|----------|-------------|
| name        | string       | no       |             |
| description | string       | no       |             |
| values      | JSON object  | no       |             |

**200 response:** updated blueprint.

### DELETE /api/v2/blueprints/{id}

Delete a blueprint (does not affect already-applied limits).

**204 response:** empty.

**Error responses:** 400, 401, 403, 404, 500.

### POST /api/v2/blueprints/{id}/apply

Copy blueprint values onto a target entity (the target type is determined by the blueprint's own `type`).

**Request body:**

| Field     | Type  | Required | Constraints      |
|-----------|-------|----------|------------------|
| target_id | int64 | yes      | > 0              |

**200 response:** `{"message": "blueprint applied successfully"}`.

**Error responses:** 400, 401, 403, 404, 500.

---

## 15. Clients

Client CRUD. Ownership for `/me/*` is derived from JWT; these routes are employee-facing.

### GET /api/v2/clients

List clients.

**Auth:** JWT + `clients.read`

**Query:** `page` (default 1), `page_size` (default 20), `email_filter`, `name_filter`.

**200 response:** `{"clients": [...], "total": N}`. Each client has `id, first_name, last_name, email, date_of_birth, gender, phone, address, jmbg, active, created_at`.

### GET /api/v2/clients/{id}

Get one client.

**Auth:** JWT + `clients.read`

**200 response:** client object.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/clients

Create a client.

**Auth:** JWT + `clients.create`

**Request body:**

| Field          | Type   | Required | Constraints                           |
|----------------|--------|----------|---------------------------------------|
| first_name     | string | yes      |                                       |
| last_name      | string | yes      |                                       |
| date_of_birth  | int64  | yes      | Unix seconds                          |
| gender         | string | no       |                                       |
| email          | string | yes      | valid, unique                         |
| phone          | string | no       |                                       |
| address        | string | no       |                                       |
| jmbg           | string | yes      | 13 digits, unique                     |

**201 response:** client object (`active: false`).

**Error responses:** 400, 401, 403, 409, 500.

### PUT /api/v2/clients/{id}

Partial update of a client.

**Auth:** JWT + `clients.update`

**Request body (all optional):**

| Field         | Type   | Required | Constraints        |
|---------------|--------|----------|--------------------|
| first_name    | string | no       |                    |
| last_name     | string | no       |                    |
| date_of_birth | int64  | no       | Unix seconds       |
| gender        | string | no       |                    |
| email         | string | no       | valid              |
| phone         | string | no       |                    |
| address       | string | no       |                    |
| active        | bool   | no       | toggles auth status|

**200 response:** updated client.

**Error responses:** 400, 401, 403, 404, 500.

---

## 16. Accounts & Currencies & Companies

### GET /api/v2/currencies

List supported currencies.

**Auth:** JWT (any authenticated employee; no specific permission)

**200 response:** `{"currencies": [{code, name, symbol}, ...]}`.

### GET /api/v2/accounts

List accounts. Supports `?client_id=X` filter.

**Auth:** JWT + `accounts.read`

**Query:** `page` (default 1), `page_size` (default 20), `client_id`, `name_filter`, `account_number_filter`, `type_filter`.

**200 response:** `{"accounts": [...], "total": N}`. Account object fields: `id, owner_id, account_number, account_name, account_kind (current|foreign), account_type, account_category, currency_code, balance, available_balance, daily_limit, monthly_limit, transfer_limit, status (active|inactive), is_bank_account, company_id, created_at`.

### GET /api/v2/accounts/{id}

Get one account by ID.

**Auth:** JWT + `accounts.read`

**Error responses:** 400, 401, 403, 404.

### GET /api/v2/accounts/by-number/{account_number}

Get one account by its account number.

**Auth:** JWT + `accounts.read`

**200 response:** account object.

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/accounts

Create a new account for a client. Optionally auto-creates a card.

**Auth:** JWT + `accounts.create`

**Request body:**

| Field            | Type    | Required | Constraints                                          |
|------------------|---------|----------|------------------------------------------------------|
| owner_id         | uint64  | yes      | client id                                            |
| account_kind     | string  | yes      | one of `current, foreign`                            |
| account_type     | string  | yes      | free text / service-specific                         |
| account_category | string  | no       | one of `personal, business`                          |
| currency_code    | string  | yes      | 3-letter ISO                                         |
| employee_id      | uint64  | no       | acting employee                                      |
| initial_balance  | float64 | no       | >= 0                                                 |
| create_card      | bool    | no       | also create a card                                   |
| card_brand       | string  | no       | one of `visa, mastercard, dinacard, amex`            |
| company_id       | uint64  | no       | link to company                                      |

**201 response:** account object (plus `card` when `create_card`, or `card_error` on card-creation failure).

**Error responses:** 400, 401, 403, 500.

### PUT /api/v2/accounts/{id}/name

Change the display name of an account.

**Auth:** JWT + `accounts.update`

**Request body:**

| Field     | Type   | Required | Constraints |
|-----------|--------|----------|-------------|
| new_name  | string | yes      |             |
| client_id | uint64 | no       |             |

**200 response:** updated account.

### PUT /api/v2/accounts/{id}/limits

Update daily/monthly limits on an account.

**Auth:** JWT + `accounts.update`

**Request body (all optional):**

| Field         | Type    | Required | Constraints |
|---------------|---------|----------|-------------|
| daily_limit   | float64 | no       | >= 0        |
| monthly_limit | float64 | no       | >= 0        |

**200 response:** updated account.

### PUT /api/v2/accounts/{id}/status

Activate/deactivate an account.

**Auth:** JWT + `accounts.update`

**Request body:**

| Field  | Type   | Required | Constraints               |
|--------|--------|----------|---------------------------|
| status | string | yes      | one of `active, inactive` |

**200 response:** updated account.

### POST /api/v2/companies

Create a company record.

**Auth:** JWT + `accounts.create`

**Request body:**

| Field               | Type   | Required | Constraints                 |
|---------------------|--------|----------|-----------------------------|
| company_name        | string | yes      |                             |
| registration_number | string | yes      | unique                      |
| tax_number          | string | no       |                             |
| activity_code       | string | no       | validated format            |
| address             | string | no       |                             |
| owner_id            | uint64 | yes      | client id                   |

**201 response:** company object.

**Error responses:** 400, 401, 403, 409, 500.

---

## 17. Bank Accounts

Admin-only management of bank-owned accounts. The bank must retain at least one RSD and one foreign-currency account at all times.

### GET /api/v2/bank-accounts

List bank-owned accounts.

**Auth:** JWT + `bank-accounts.manage`

**200 response:** bank-accounts list (each with the same shape as a normal account but `is_bank_account=true` and `owner_id=1000000000`).

### POST /api/v2/bank-accounts

Create a bank-owned account.

**Auth:** JWT + `bank-accounts.manage`

**Request body:**

| Field         | Type   | Required | Constraints                   |
|---------------|--------|----------|-------------------------------|
| currency_code | string | yes      | 3-letter ISO                  |
| account_kind  | string | yes      | one of `current, foreign`     |
| account_name  | string | no       | display name                  |

**201 response:** new bank account.

**Error responses:** 400, 401, 403, 500.

### DELETE /api/v2/bank-accounts/{id}

Delete a bank-owned account.

**Auth:** JWT + `bank-accounts.manage`

**200 response:** `{"success": true}`.

**Error responses:** 400 (last RSD or last foreign), 401, 403, 404, 500.

---

## 18. Cards

Employee-facing card management. Client-side card routes live under `/api/v2/me/cards/*` (Section 3).

### GET /api/v2/cards

List cards.

**Auth:** JWT + `cards.read`

**Query:** `page`, `page_size`, `client_id`, `account_number`, `status`.

**200 response:** `{"cards": [...], "total": N}`.

### GET /api/v2/cards/{id}

Get one card.

**Auth:** JWT + `cards.read`

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/cards

Issue a new physical card for a client or authorized person.

**Auth:** JWT + `cards.create`

**Request body:**

| Field          | Type   | Required | Constraints                               |
|----------------|--------|----------|-------------------------------------------|
| account_number | string | yes      |                                           |
| owner_id       | uint64 | yes      |                                           |
| owner_type     | string | yes      | one of `client, authorized_person`        |
| card_brand     | string | no       | one of `visa, mastercard, dinacard, amex` |

**201 response:** card object — includes full `card_number` and `cvv` (only returned here, then masked afterwards).

**Error responses:** 400, 401, 403, 500.

### POST /api/v2/cards/authorized-person

Create an authorized-person record (later attachable to a card).

**Auth:** JWT + `cards.create`

**Request body:**

| Field         | Type   | Required | Constraints   |
|---------------|--------|----------|---------------|
| first_name    | string | yes      |               |
| last_name     | string | yes      |               |
| date_of_birth | int64  | no       | Unix seconds  |
| gender        | string | no       |               |
| email         | string | no       |               |
| phone         | string | no       |               |
| address       | string | no       |               |
| account_id    | uint64 | yes      |               |

**201 response:** authorized-person object.

### POST /api/v2/cards/{id}/block

Block a card.

**Auth:** JWT + `cards.update`

**200 response:** updated card.

**Error responses:** 400, 401, 403, 404, 500.

### POST /api/v2/cards/{id}/unblock

Unblock a card.

**Auth:** JWT + `cards.update`

**200 response:** updated card.

### POST /api/v2/cards/{id}/deactivate

Permanently deactivate a card.

**Auth:** JWT + `cards.update`

**200 response:** updated card.

---

## 19. Card Requests (employee)

Approve/reject card-issuance requests filed by clients via `POST /api/v2/me/cards/requests`.

### GET /api/v2/cards/requests

List card requests.

**Auth:** JWT + `cards.approve`

**Query:** `page`, `page_size`, `status` (`pending|approved|rejected`).

**200 response:** `{"requests": [...], "total": N}`.

### GET /api/v2/cards/requests/{id}

Get one card request.

**Auth:** JWT + `cards.approve`

**Error responses:** 400, 401, 403, 404.

### POST /api/v2/cards/requests/{id}/approve

Approve a pending card request; creates the card.

**Auth:** JWT + `cards.approve`

**200 response:** `{"request": <request>, "card": <card>}` — the card includes the newly-issued `card_number` and `cvv`.

**Error responses:** 400, 401, 403, 404, 409 (already processed).

### POST /api/v2/cards/requests/{id}/reject

Reject a pending card request.

**Auth:** JWT + `cards.approve`

**Request body:**

| Field  | Type   | Required | Constraints |
|--------|--------|----------|-------------|
| reason | string | yes      |             |

**200 response:** updated request (`status: "rejected"`, `rejection_reason`).

**Error responses:** 400, 401, 403, 404, 409.

---

## 20. Payments (employee)

### GET /api/v2/payments

List payments. Supports exactly one of `?client_id=X` or `?account_number=X` (both → 400).

**Auth:** JWT + `payments.read`

**Query:** `page` (default 1), `page_size` (default 20), `client_id`, `account_number`.

**200 response:** `{"payments": [...], "total": N}`.

**Error responses:** 400 (both filters present / invalid id), 401, 403, 500.

### GET /api/v2/payments/{id}

Get one payment.

**Auth:** JWT + `payments.read`

**200 response:** payment object.

---

## 21. Transfers (employee)

### GET /api/v2/transfers

List transfers. Same filtering convention as payments.

**Auth:** JWT + `payments.read`

**Query:** `page`, `page_size`, `client_id`, `account_number`.

**200 response:** `{"transfers": [...], "total": N}`.

### GET /api/v2/transfers/{id}

Get one transfer.

**Auth:** JWT + `payments.read`

---

## 22. Transfer Fees

Configurable fee rules. Multiple matching rules are cumulative (stack).

### GET /api/v2/fees

List fee rules.

**Auth:** JWT + `fees.manage`

**200 response:** `{"fees": [...]}`.

### POST /api/v2/fees

Create a fee rule.

**Auth:** JWT + `fees.manage`

**Request body:**

| Field            | Type   | Required | Constraints                                   |
|------------------|--------|----------|-----------------------------------------------|
| name             | string | yes      |                                               |
| fee_type         | string | yes      | one of `percentage, fixed`                    |
| fee_value        | string | yes      | decimal (rate or flat amount)                 |
| min_amount       | string | no       | decimal threshold                             |
| max_fee          | string | no       | decimal cap                                   |
| transaction_type | string | yes      | one of `payment, transfer, all`               |
| currency_code    | string | no       | applies only for this currency; empty = any   |

**201 response:** fee object.

**Error responses:** 400, 401, 403, 500.

### PUT /api/v2/fees/{id}

Update a fee rule.

**Auth:** JWT + `fees.manage`

**Request body (all optional):**

| Field            | Type   | Required | Constraints         |
|------------------|--------|----------|---------------------|
| name             | string | no       |                     |
| fee_type         | string | no       | `percentage, fixed` |
| fee_value        | string | no       | decimal             |
| min_amount       | string | no       | decimal             |
| max_fee          | string | no       | decimal             |
| transaction_type | string | no       | `payment, transfer, all` |
| currency_code    | string | no       |                     |
| active           | bool   | no       |                     |

**200 response:** updated fee.

### DELETE /api/v2/fees/{id}

Deactivate (soft-delete) a fee rule.

**Auth:** JWT + `fees.manage`

**200 response:** `{"success": true}`.

**Error responses:** 400, 401, 403, 404, 500.

---

## 23. Loans (employee)

### GET /api/v2/loans

List loans. Supports `?client_id=X`.

**Auth:** JWT + `credits.read`

**Query:** `page`, `page_size`, `client_id`, `loan_type_filter`, `account_number_filter`, `status_filter`.

**200 response:** `{"loans": [...], "total": N}`.

### GET /api/v2/loans/{id}

Get one loan.

**Auth:** JWT + `credits.read`

**Error responses:** 400, 401, 403, 404.

### GET /api/v2/loans/{id}/installments

List installments for a loan.

**Auth:** JWT + `credits.read`

**200 response:** `{"installments": [...]}`.

---

## 24. Loan Requests (employee)

### GET /api/v2/loan-requests

List loan requests. Supports `?client_id=X`.

**Auth:** JWT + `credits.read`

**Query:** `page`, `page_size`, `client_id`, `loan_type_filter`, `account_number_filter`, `status_filter`.

**200 response:** `{"requests": [...], "total": N}`.

### GET /api/v2/loan-requests/{id}

Get one loan request.

**Auth:** JWT + `credits.read`

### POST /api/v2/loan-requests/{id}/approve

Approve a pending loan request. Creates the loan; approving employee's `max_loan_approval_amount` is enforced downstream.

**Auth:** JWT + `credits.approve`

**200 response:** updated loan request (status `approved`) + linked loan id.

**Error responses:** 400, 401, 403 (exceeds employee's approval limit), 404, 409, 500.

### POST /api/v2/loan-requests/{id}/reject

Reject a pending loan request.

**Auth:** JWT + `credits.approve`

**200 response:** updated loan request (status `rejected`).

---

## 25. Interest Rate Tiers & Bank Margins

Admin-only configuration for variable-rate loan calculation.

### GET /api/v2/interest-rate-tiers

List interest rate tiers.

**Auth:** JWT + `interest-rates.manage`

**200 response:** `{"tiers": [...]}`.

### POST /api/v2/interest-rate-tiers

Create an interest rate tier.

**Auth:** JWT + `interest-rates.manage`

**Request body:**

| Field         | Type    | Required | Constraints |
|---------------|---------|----------|-------------|
| amount_from   | float64 | no       | >= 0        |
| amount_to     | float64 | no       | >= 0        |
| fixed_rate    | float64 | yes      | >= 0        |
| variable_base | float64 | yes      | >= 0        |

**201 response:** tier object.

### PUT /api/v2/interest-rate-tiers/{id}

Update a tier.

**Auth:** JWT + `interest-rates.manage`

**Request body:** same shape as create.

**200 response:** updated tier.

### DELETE /api/v2/interest-rate-tiers/{id}

Delete a tier.

**Auth:** JWT + `interest-rates.manage`

**200 response:** `{"success": true}`.

### POST /api/v2/interest-rate-tiers/{id}/apply

Recompute all variable-rate loans against the current tier+margin configuration.

**Auth:** JWT + `interest-rates.manage`

**200 response:** `{"updated_loans": N}`.

### GET /api/v2/bank-margins

List bank margin configurations.

**Auth:** JWT + `interest-rates.manage`

**200 response:** `{"margins": [...]}`.

### PUT /api/v2/bank-margins/{id}

Update a bank margin.

**Auth:** JWT + `interest-rates.manage`

**Request body:**

| Field  | Type    | Required | Constraints |
|--------|---------|----------|-------------|
| margin | float64 | yes      |             |

**200 response:** updated margin.

---

## 26. Stock Exchange Admin

Supervisor-only testing-mode toggles. Enabling testing mode forces exchanges "open" regardless of schedule.

### POST /api/v2/stock-exchanges/testing-mode

Enable or disable testing mode globally.

**Auth:** JWT + `exchanges.manage`

**Request body:**

| Field   | Type | Required | Constraints |
|---------|------|----------|-------------|
| enabled | bool | yes      |             |

**200 response:** `{"testing_mode": bool}`.

### GET /api/v2/stock-exchanges/testing-mode

Read the current testing-mode flag.

**Auth:** JWT + `exchanges.manage`

**200 response:** `{"testing_mode": bool}`.

---

## 27. Admin Stock Source

**DESTRUCTIVE.** Switching the source wipes all stock-service securities, listings, holdings, orders, and daily price history, then reseeds from the new source.

### POST /api/v2/admin/stock-source

Switch the active stock data source.

**Auth:** JWT + `securities.manage`

**Request body:**

| Field  | Type   | Required | Constraints                                  |
|--------|--------|----------|----------------------------------------------|
| source | string | yes      | one of `external, generated, simulator`      |

**202 response:** `{source, status, started_at, last_error}`.

**Error responses:** 400, 401, 403, 500.

### GET /api/v2/admin/stock-source

Read the current source and seeding status.

**Auth:** JWT + `securities.manage`

**200 response:** `{source, status, started_at, last_error}`.

---

## 28. Orders (employee)

### POST /api/v2/orders

Create an order on behalf of a client.

**Auth:** JWT + `securities.manage`

**Request body:**

| Field            | Type    | Required | Constraints                                                           |
|------------------|---------|----------|-----------------------------------------------------------------------|
| client_id        | uint64  | yes      | non-zero                                                              |
| account_id       | uint64  | cond.    | required for buy/sell (proceeds or debit account, must belong to client) |
| security_type    | string  | no       | one of `stock, futures, forex, option`                                |
| listing_id       | uint64  | cond.    | required for buy orders                                               |
| direction        | string  | yes      | one of `buy, sell`                                                    |
| order_type       | string  | yes      | one of `market, limit, stop, stop_limit`                              |
| quantity         | int64   | yes      | > 0                                                                   |
| limit_value      | string  | cond.    | required for limit/stop_limit                                         |
| stop_value       | string  | cond.    | required for stop/stop_limit                                          |
| all_or_none      | bool    | no       |                                                                       |
| margin           | bool    | no       |                                                                       |
| base_account_id  | uint64  | cond.    | required when `security_type == forex`; must differ from `account_id` and belong to client |

**201 response:** order object.

**Error responses:** 400, 401, 403 (account does not belong to client), 404, 409, 500.

### GET /api/v2/orders

List orders (supervisor view).

**Auth:** JWT + `orders.approve`

**Query:** `page` (default 1), `page_size` (default 10), `status`, `agent_email`, `direction`, `order_type`.

**200 response:** `{"orders": [...], "total_count": N}`.

### POST /api/v2/orders/{id}/approve

Approve an order that needs supervisor approval.

**Auth:** JWT + `orders.approve`

**200 response:** updated order.

**Error responses:** 400, 401, 403, 404, 409.

### POST /api/v2/orders/{id}/decline

Decline an order that needs supervisor approval.

**Auth:** JWT + `orders.approve`

**200 response:** updated order.

---

## 29. OTC On-Behalf (employee)

### POST /api/v2/otc/admin/offers/{id}/buy

Buy an OTC offer on behalf of a named client. The gateway verifies the named account belongs to the named client before forwarding to stock-service.

**Auth:** JWT + `securities.manage`

**Request body:**

| Field      | Type   | Required | Constraints |
|------------|--------|----------|-------------|
| client_id  | uint64 | yes      | non-zero    |
| account_id | uint64 | yes      | must belong to `client_id` |
| quantity   | int64  | yes      | > 0         |

**200 response:** OTC purchase result.

**Error responses:** 400, 401, 403 (account not owned by named client), 404, 409.

---

## 30. Actuaries

Supervisor-only management of actuary accounts. All routes require `agents.manage`.

### GET /api/v2/actuaries

List actuaries.

**Auth:** JWT + `agents.manage`

**Query:** `page`, `page_size`, `search`, `position`.

**200 response:** `{"actuaries": [...], "total_count": N}`.

### PUT /api/v2/actuaries/{id}/limit

Set an actuary's trading limit.

**Auth:** JWT + `agents.manage`

**Request body:**

| Field | Type   | Required | Constraints |
|-------|--------|----------|-------------|
| limit | string | yes      | decimal, non-empty |

**200 response:** updated actuary.

**Error responses:** 400, 401, 403, 404, 500.

### POST /api/v2/actuaries/{id}/reset-limit

Reset the actuary's used (consumed) limit counter.

**Auth:** JWT + `agents.manage`

**200 response:** updated actuary (`used_limit: 0`).

### PUT /api/v2/actuaries/{id}/approval

Set whether this actuary's orders require supervisor approval.

**Auth:** JWT + `agents.manage`

**Request body:**

| Field         | Type | Required | Constraints |
|---------------|------|----------|-------------|
| need_approval | bool | yes      |             |

**200 response:** updated actuary.

---

## 31. Tax (employee)

All routes require `tax.manage`.

### GET /api/v2/tax

List per-user capital gains tax debt for the current month.

**Auth:** JWT + `tax.manage`

**Query:** `page` (default 1), `page_size` (default 10), `user_type` (`client|actuary`), `search`.

**200 response:** `{"tax_records": [...], "total_count": N}`.

**Error responses:** 400, 401, 403, 500.

### POST /api/v2/tax/collect

Collect tax from all users for the current month.

**Auth:** JWT + `tax.manage`

**200 response:**

| Field               | Type   | Always present | Notes                      |
|---------------------|--------|----------------|----------------------------|
| collected_count     | int64  | yes            | number of users paid       |
| total_collected_rsd | string | yes            | decimal total in RSD       |
| failed_count        | int64  | yes            | users with failed collection |

**Error responses:** 401, 403, 500.

---

## 32. Changelogs

All five changelog endpoints are registered but currently return **501 `not_implemented`** (a placeholder pending the changelog-service implementation). The permission gates below are already wired.

### GET /api/v2/accounts/{id}/changelog

**Auth:** JWT + `accounts.read` · **Status:** 501 not_implemented

### GET /api/v2/employees/{id}/changelog

**Auth:** JWT + `employees.read` · **Status:** 501 not_implemented

### GET /api/v2/clients/{id}/changelog

**Auth:** JWT + `clients.read` · **Status:** 501 not_implemented

### GET /api/v2/cards/{id}/changelog

**Auth:** JWT + `cards.read` · **Status:** 501 not_implemented

### GET /api/v2/loans/{id}/changelog

**Auth:** JWT + `credits.read` · **Status:** 501 not_implemented

**Response body (all five):**

```json
{ "error": { "code": "not_implemented", "message": "this endpoint is coming in a future release" } }
```

---

## 33. v2-only: Options

The v2 options surface addresses option contracts by their option ID directly, rather than by listing ID or holding ID. This aligns with how UIs naturally browse the options chain. Both routes require `securities.trade`.

### POST /api/v2/options/{option_id}/orders

Create an order for an option contract, addressing it by its option ID. Internally resolves `option_id → listing_id` via `GetOption(option_id)` and delegates to the standard `CreateOrder` RPC. Returns 409 if the option has no listing (e.g., legacy data).

**Auth:** JWT + `securities.trade`

**Path parameters:**

| Field     | Type | Required | Constraints |
|-----------|------|----------|-------------|
| option_id | uint | yes      | non-zero    |

**Request body:**

| Field        | Type   | Required | Constraints                                                    |
|--------------|--------|----------|----------------------------------------------------------------|
| direction    | string | yes      | one of `buy, sell`                                             |
| order_type   | string | yes      | one of `market, limit, stop, stop_limit`                       |
| quantity     | int64  | yes      | > 0                                                            |
| limit_value  | string | cond.    | required for `limit, stop_limit`; decimal string               |
| stop_value   | string | cond.    | required for `stop, stop_limit`; decimal string                |
| all_or_none  | bool   | no       | default false                                                  |
| margin       | bool   | no       | options typically use margin; default false                    |
| account_id   | uint64 | yes      | non-zero; caller-owned collateral/settlement account           |

**201 response (Order object):**

| Field         | Type    | Always present | Notes                                                 |
|---------------|---------|----------------|-------------------------------------------------------|
| id            | uint64  | yes            | server-assigned                                       |
| user_id       | uint64  | yes            | owner of the order                                    |
| listing_id    | uint64  | yes            | resolved from option                                  |
| security_type | string  | yes            | `"option"` for this endpoint                          |
| ticker        | string  | yes            | OCC-style option ticker (e.g., `AAPL260116C00200000`) |
| direction     | string  | yes            | echoes request                                        |
| order_type    | string  | yes            | echoes request                                        |
| quantity      | int64   | yes            | echoes request                                        |
| status        | string  | yes            | typically `pending` or `approved`                     |
| all_or_none   | bool    | yes            | echoes request                                        |
| margin        | bool    | yes            | echoes request                                        |
| limit_value   | string  | when applicable | for limit / stop_limit orders                        |
| stop_value    | string  | when applicable | for stop / stop_limit orders                         |
| created_at    | string  | yes            | RFC 3339 timestamp                                    |

**Error responses:**

| Status | Code                    | When                                                                       |
|--------|-------------------------|----------------------------------------------------------------------------|
| 400    | validation_error        | bad direction / order_type / quantity / missing limit or stop / invalid option_id |
| 401    | unauthorized            | missing or invalid JWT                                                     |
| 403    | forbidden               | missing `securities.trade` permission                                      |
| 404    | not_found               | option_id does not exist                                                   |
| 409    | business_rule_violation | option has no listing_id (not tradeable) or stock-service rejected         |
| 500    | internal_error          | unexpected downstream failure                                              |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/options/123/orders \
  -H "Authorization: Bearer $T" -H "Content-Type: application/json" \
  -d '{"direction":"buy","order_type":"market","quantity":1,"margin":true,"account_id":42}'
```

### POST /api/v2/options/{option_id}/exercise

Exercise an option contract. The server resolves the caller's long option holding for that option automatically (holdings are aggregated per user+option, so there is one per option).

**Auth:** JWT + `securities.trade`

**Path parameters:**

| Field     | Type | Required | Constraints |
|-----------|------|----------|-------------|
| option_id | uint | yes      | non-zero    |

**Request body:** none.

**200 response:**

| Field              | Type   | Always present | Notes                                          |
|--------------------|--------|----------------|------------------------------------------------|
| id                 | uint64 | yes            | exercise event id                              |
| option_ticker      | string | yes            | OCC-style option ticker                        |
| exercised_quantity | int64  | yes            | number of contracts exercised                  |
| shares_affected    | int64  | yes            | resulting share delta (contracts × 100 typical)|
| profit             | string | yes            | realised P/L in RSD (decimal string)           |

**Error responses:**

| Status | Code                    | When                                                                 |
|--------|-------------------------|----------------------------------------------------------------------|
| 400    | validation_error        | invalid option_id                                                    |
| 401    | unauthorized            | missing or invalid JWT                                               |
| 403    | forbidden               | missing `securities.trade` permission                                |
| 404    | not_found               | no long option holding found for this user + option                  |
| 409    | business_rule_violation | option expired / compensation triggered / other business-rule fail   |
| 500    | internal_error          | unexpected downstream failure                                        |

**Example:**

```bash
curl -sS -X POST http://localhost:8080/api/v2/options/123/exercise \
  -H "Authorization: Bearer $T"
```

---

## Appendix: Password Requirements

Passwords for both employees and clients must satisfy:

- 8 to 32 characters
- At least 2 digits
- At least 1 uppercase letter
- At least 1 lowercase letter

## Appendix: Notes for Frontend Developers

1. **v1 remains supported.** v2 is additive. A client that targets `/api/v1/*` today will keep working indefinitely; switching to `/api/v2/*` changes nothing for the shared routes.
2. **Token expiry:** access tokens expire after 15 minutes. Implement automatic refresh using the refresh token before expiry.
3. **Client vs. employee routes:** employee routes require an employee JWT with specific permissions. Client self-service routes are under `/api/v2/me/*` and accept any valid JWT (employee or client). Do not use a client token to call employee-only endpoints.
4. **Error format:** all error responses are structured objects — `{"error": {"code": "...", "message": "..."}}`. Parse `error.code` for programmatic handling and `error.message` for display.
5. **Pagination:** list endpoints that paginate use `page` (1-based) and `page_size`. Default `page_size` varies (typically 10 or 20; sometimes 50 — see per-route docs).
6. **Date fields:** `date_of_birth` is a Unix timestamp in seconds. Other timestamps are RFC 3339 / ISO-8601 strings.
7. **Account numbers:** format `265-XXXXXXXXXXX-YY`.
8. **Card numbers:** the full card number and CVV are returned only in the create-card response. Subsequent reads return a masked number.
9. **JMBG:** 13-digit Serbian national ID; validated for length and uniqueness server-side.
10. **CORS:** the API Gateway allows all origins with `GET, POST, PUT, PATCH, DELETE, OPTIONS` methods and `Authorization, Content-Type` headers.
11. **Mobile auth flow:** `POST /api/v2/mobile/auth/request-activation` → `POST /api/v2/mobile/auth/activate` → receive `device_id, device_secret`. Mobile JWTs include `system_type: "mobile"` and require `X-Device-ID` on all authenticated requests.
12. **Verification flow:** payments and transfers require two-factor verification. Create the transaction → create a verification challenge (or rely on the auto-generated code from the transaction) → wait for mobile approval or submit code → execute. Users with `verification.skip` permission bypass this flow.
13. **v2 options routes:** prefer `/api/v2/options/:option_id/orders` and `/api/v2/options/:option_id/exercise` over the legacy listing-id / holding-id routes; the v2 URLs match how UIs naturally browse the options chain.
14. **Listing currency:** every `listing` object on stocks/futures/forex/options carries a `currency` field (one of `RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`). Use it to format price/volume for display and to know which currency the order will convert from.
15. **Holdings are aggregated:** `/api/v2/me/portfolio` rolls up per `(user, security)` — you won't see separate rows for the same stock bought via two different accounts. For per-purchase history (when each lot was bought, at what price, in what currency, from which account), use `GET /api/v2/me/holdings/{id}/transactions`.
16. **Order state field:** every order response now includes a derived `state` — `pending | approved | filling | filled | cancelled | declined` — and `filled_quantity` so you can render "3 of 10 filled" without a second call. The raw `status` field is preserved for backwards compatibility.

---

## Changelog (v2 additions since last publish)

- **2026-04-24** — `listing.currency` surfaced on every security response (stocks/futures/forex/options).
- **2026-04-24** — `/me/portfolio` trimmed to quantity-only view; per-lot history moved to new endpoint `GET /me/holdings/{id}/transactions`.
- **2026-04-24** — order response includes derived `state` + `filled_quantity` fields.
- **2026-04-24** — bank commissions now route to the bank's RSD account (resolved dynamically); capital-gains tax continues to route to the state account.
- **2026-04-24** — enforced supported-currency set (`RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`) on every persisted stock_exchange and forex_pair row.

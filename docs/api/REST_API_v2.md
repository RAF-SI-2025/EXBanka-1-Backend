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
| holding_id     | uint64  | no       | optional for sell (post-rollup)                                             |
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

List the caller's holdings (stock/futures/options).

**Auth:** JWT

**Query:** `page` (default 1), `page_size` (default 10), `security_type` (optional; one of `stock, futures, option`).

**200 response:** `{"holdings": [...], "total_count": N}`.

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

Browsable by any authenticated user (`AnyAuthMiddleware`).

### GET /api/v2/securities/stocks

List stocks.

**Auth:** JWT

**Query:** `page`, `page_size`, `search`, `exchange_acronym`, `min_price`, `max_price`, `min_volume`, `max_volume`, `sort_by` (one of `price, volume, change, margin`), `sort_order` (`asc|desc`, default `asc`).

**200 response:** `{"stocks": [...], "total_count": N}`.

### GET /api/v2/securities/stocks/{id}

Get one stock listing.

**200 response:** stock listing object.

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

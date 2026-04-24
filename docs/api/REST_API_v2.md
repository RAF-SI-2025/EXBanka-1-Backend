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

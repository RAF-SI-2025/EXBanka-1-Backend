# EXBanka REST API Documentation

**Base URL:** `http://localhost:8080`
**Content-Type:** `application/json`
**Swagger UI:** `http://localhost:8080/swagger/index.html`

---

## Version Notes

All endpoints in this document are served under the `/api/v3/` prefix. v3 is the only live API version after plan E (route consolidation, 2026-04-27); it merged the entire surfaces of the retired v1 and v2 routers plus the v3-only investment-funds, OTC-options, and inter-bank-transfer additions.

**Sunset of v1 and v2:** the prior `/api/v1/` and `/api/v2/` prefixes have been retired. Any request to `/api/v1/*` or `/api/v2/*` returns HTTP 404. Clients must update their base path to `/api/v3/`.

**Adding a v4 (future):** when a breaking change is required, a new explicit `router_v4.go` will be added alongside `router_v3.go`. The two will run side-by-side — there is no transparent fallback. See `api-gateway/internal/router/router_versioning.md` for the per-version pattern and sunset policy.

---

## Authentication

Most endpoints require a JWT bearer token in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

There are two token types:
- **Employee token** -- issued via `POST /api/v3/auth/login` when the principal is an employee, required for employee-protected routes
- **Client token** -- issued via `POST /api/v3/auth/login` when the principal is a client, required for client-protected routes

The unified login endpoint auto-detects whether the principal is an employee or a client based on the stored account record in auth-service and issues the appropriate JWT (`system_type: "employee"` or `system_type: "client"`).

Employee routes additionally require specific permissions (see per-endpoint notes). Client routes require `role="client"` in the JWT.

Access tokens are **ES256-signed JWTs** (15 min). The gateway verifies them
locally against auth-service's published public keys (no per-request validation
hop) and consults a revocation denylist. Two distinct 401 outcomes tell the
client what to do:

- **`401 token_expired`** — the access token is past `exp`, OR its claims are
  stale (permissions/roles/account state changed). **Refresh** the token (the
  refresh token is still valid) and retry — do **not** log the user out.
- **`401 unauthorized`** — the token is invalid/malformed, or the session was
  revoked (logout / revoke-session / revoke-all). The refresh token is also
  dead; the client must **re-authenticate** (log in again).

Use the refresh token (`POST /api/v3/auth/refresh`) to obtain a new pair.

---

## Rate Limiting

The gateway applies Redis-backed fixed-window rate limits (per client IP):

- **Global ceiling** — a generous per-IP cap across **all** routes
  (`RATE_LIMIT_GLOBAL_PER_MIN`, default **3000/min**). It is sized well above
  normal frontend polling and only trips for runaway/abusive clients.
- **`POST /api/v3/auth/login`** — strict per-IP bucket
  (`RATE_LIMIT_LOGIN_PER_5MIN`, default **20 per 5 min**).
- **`POST /api/v3/auth/password/reset-request`** — strict per-IP bucket
  (`RATE_LIMIT_RESET_PER_5MIN`, default **5 per 5 min**).

Exceeding any bucket returns **HTTP 429** with body
`{"error":{"code":"rate_limited","message":"too many requests, slow down"}}` and a
`Retry-After` header (seconds). This is an additive failure mode — no success-path
contract changed. The limiter **fails open**: if Redis is unavailable, requests are
allowed through rather than blocked.

Every response carries an **`X-Request-Id`** header (echoing an inbound
`X-Request-Id` if present, else a fresh UUID) for log correlation.

---

## Table of Contents

1. [Auth](#1-auth)
2. [Employees](#2-employees)
3. [Roles & Permissions](#3-roles--permissions)
4. [Clients](#4-clients)
5. [Accounts](#5-accounts)
6. [Cards](#6-cards)
7. [Payments](#7-payments)
8. [Transfers](#8-transfers)
9. [Payment Recipients](#9-payment-recipients)
10. [Exchange Rates](#10-exchange-rates)
11. [Loans](#11-loans)
12. [Loan Requests](#12-loan-requests)
13. [Limits](#13-limits)
14. [Bank Accounts](#14-bank-accounts)
15. [Notification Templates](#15-notification-templates)
16. [Transfer Fees](#16-transfer-fees)
17. [Interest Rate Tiers](#17-interest-rate-tiers)
18. [Bank Margins](#18-bank-margins)
19. [Card Requests](#19-card-requests)
20. [Me (Self-Service)](#20-me-self-service)
21. [Mobile Auth](#21-mobile-auth)
22. [Mobile Device Management](#22-mobile-device-management)
23. [Mobile Device Settings](#23-mobile-device-settings)
24. [Verification](#24-verification)
25. [Stock Exchanges](#25-stock-exchanges)
26. [Securities](#26-securities)
27. [Orders](#27-orders)
28. [Portfolio](#28-portfolio)
29. [OTC Stocks Marketplace](#29-otc-stocks-marketplace)
30. [OTC Option Contracts](#30-otc-option-contracts)
31. [Investment Funds (Celina 4)](#31-investment-funds-celina-4)
32. [Actuaries](#32-actuaries)
33. [Tax](#33-tax)
34. [Blueprints](#34-blueprints)
35. [Changelog (Audit Trail)](#35-changelog-audit-trail)
36. [Sessions & Login History](#36-sessions--login-history)
37. [Notifications](#37-notifications)
38. [Stock Data Source](#38-stock-data-source)
39. [Peer Banks (Admin) — SI-TX cross-bank registry (Celina 5)](#39-peer-banks-admin--si-tx-cross-bank-registry-celina-5)
40. [Watchlist (Celina 3)](#40-watchlist-celina-3)
41. [OTC Negotiation History (Celina 3)](#41-otc-negotiation-history-celina-3)
42. [OTC Trader Ratings (Celina 3)](#42-otc-trader-ratings-celina-3)
43. [Price Alerts (Celina 3)](#43-price-alerts-celina-3)
44. [Transfer Status (Celina 4 / SI-TX)](#44-transfer-status-celina-4--si-tx)
45. [Recurring Securities Orders (Celina 3)](#45-recurring-securities-orders-celina-3)
46. [Recurring Fund Investments (Celina 4)](#46-recurring-fund-investments-celina-4)
47. [OTC Marketplace](#47-otc-marketplace)
48. [Unified Portfolio Routes (2026-05-28)](#48-unified-portfolio-routes-2026-05-28)
49. [Admin / Cron Management (C10 — 2026-05-28)](#49-admin--cron-management-c10--2026-05-28)
50. [Admin / Audit Logs (D4 — 2026-05-28)](#50-admin--audit-logs-d4--2026-05-28)
51. [Dividends (E4 — 2026-05-28)](#51-dividends-e4--2026-05-28)
52. [Cross-Bank Protocol](#cross-bank-protocol-cross-bank-protocol)
53. [System Version](#53-system-version)
54. [Error Response Format](#error-response-format)
55. [Password Requirements](#password-requirements)
56. [Notes for Frontend Developers](#notes-for-frontend-developers)

---

## 1. Auth

### POST /api/v3/auth/login

Authenticate an employee or bank client with email and password. The endpoint auto-detects whether the principal is an employee or a client based on the stored account record in auth-service and issues the appropriate JWT. Employees receive a token with `system_type: "employee"` and their roles/permissions; clients receive a token with `system_type: "client"` and `role: "client"`.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Email address of the employee or client |
| `password` | string | Yes | Account password |

**Example Request (employee):**
```json
{
  "email": "john.doe@exbanka.com",
  "password": "Secur3Pass99"
}
```

**Example Request (client):**
```json
{
  "email": "jane.smith@example.com",
  "password": "ClientPass12"
}
```

**Response 200:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "a3f8c2d1e9b4..."
}
```

**Response 400:** `{"error": "validation error"}`
**Response 401:** `{"error": "invalid credentials"}`

---

### POST /api/v3/auth/refresh

Exchange a valid refresh token for a new access/refresh token pair.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `refresh_token` | string | Yes | Valid refresh token |

**Example Request:**
```json
{
  "refresh_token": "a3f8c2d1e9b4..."
}
```

**Response 200:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "c9d2e5f1a8b3..."
}
```

**Response 400:** `{"error": "refresh_token is required"}`
**Response 401:** `{"error": "invalid refresh token"}`

---

### POST /api/v3/auth/logout

Revoke the current refresh token to end the session.

**Authentication:** None (public -- the refresh token itself is the credential)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `refresh_token` | string | Yes | Refresh token to revoke |

**Example Request:**
```json
{
  "refresh_token": "a3f8c2d1e9b4..."
}
```

**Response 200:**
```json
{
  "message": "logged out successfully"
}
```

---

### POST /api/v3/auth/password/reset-request

Send a password reset link to the given email. Always returns 200 to avoid leaking whether an email is registered.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Email address to send the reset link to |

**Example Request:**
```json
{
  "email": "john.doe@exbanka.com"
}
```

**Response 200:**
```json
{
  "message": "if the email exists, a reset link has been sent"
}
```

---

### POST /api/v3/auth/password/reset

Reset the password using a token received in the reset email link.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `token` | string | Yes | Reset token from the email link |
| `new_password` | string | Yes | New password (8-32 chars, 2+ digits, 1+ uppercase, 1+ lowercase) |
| `confirm_password` | string | Yes | Must match `new_password` |

**Example Request:**
```json
{
  "token": "d4e7f2a1b9c3...",
  "new_password": "NewPass12",
  "confirm_password": "NewPass12"
}
```

**Response 200:**
```json
{
  "message": "password reset successfully"
}
```

**Response 400:** `{"error": "passwords do not match"}` or `{"error": "invalid or expired token"}`

---

### POST /api/v3/auth/activate

Activate a new employee account by setting a password using the token from the activation email.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `token` | string | Yes | Activation token from the email link |
| `password` | string | Yes | Password to set (8-32 chars, 2+ digits, 1+ uppercase, 1+ lowercase) |
| `confirm_password` | string | Yes | Must match `password` |

**Example Request:**
```json
{
  "token": "e5f1c8a2d9b4...",
  "password": "MyFirst12Pass",
  "confirm_password": "MyFirst12Pass"
}
```

**Response 200:**
```json
{
  "message": "account activated successfully"
}
```

**Response 400:** `{"error": "invalid or expired activation token"}`

---

### POST /api/v3/auth/resend-activation

Resend the activation email for a pending account. Always returns 200 to avoid leaking whether an email is registered and pending activation.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Email address to resend the activation email to |

**Example Request:**
```json
{
  "email": "ana.petrovic@exbanka.com"
}
```

**Response 200:**
```json
{
  "message": "if the email is registered and pending activation, a new activation email has been sent"
}
```

**Response 400:** `{"error": {"code": "validation_error", "message": "..."}}`

---

## 2. Employees

All employee endpoints require an employee JWT. Read endpoints require `employees.read` permission; create requires `employees.create`; update requires `employees.update`.

**Roles and permissions:**
- `EmployeeAdmin` -- can manage employees + all other permissions
- `EmployeeSupervisor` -- agents/OTC/funds management
- `EmployeeAgent` -- securities trading
- `EmployeeBasic` -- clients/accounts/cards/credits

---

### GET /api/v3/employees

List all employees with optional filters.

**Authentication:** Employee JWT + `employees.read` permission

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default: 1) |
| `page_size` | int | No | Items per page (default: 20) |
| `email` | string | No | Filter by email (partial match) |
| `name` | string | No | Filter by first/last name (partial match) |
| `position` | string | No | Filter by position (partial match) |

**Response 200:**
```json
{
  "employees": [
    {
      "id": 1,
      "first_name": "John",
      "last_name": "Doe",
      "date_of_birth": 631152000,
      "gender": "M",
      "email": "john.doe@exbanka.com",
      "phone": "+381601234567",
      "address": "Bulevar Oslobođenja 1, Novi Sad",
      "jmbg": "0101990710123",
      "username": "jdoe",
      "position": "Loan Officer",
      "department": "Retail Banking",
      "active": true,
      "role": "EmployeeBasic",
      "permissions": ["clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage"]
    }
  ],
  "total_count": 42
}
```

---

### POST /api/v3/employees

Create a new employee. Triggers an activation email to the employee's address.

**Authentication:** Employee JWT + `employees.create` permission (EmployeeAdmin only)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `first_name` | string | Yes | First name |
| `last_name` | string | Yes | Last name |
| `date_of_birth` | int64 | Yes | Unix timestamp (seconds) |
| `gender` | string | No | Gender (e.g., "M", "F") |
| `email` | string | Yes | Email address (must be unique) |
| `phone` | string | No | Phone number |
| `address` | string | No | Residential address |
| `jmbg` | string | Yes | 13-digit national ID number (unique) |
| `username` | string | Yes | Login username (unique) |
| `position` | string | No | Job position/title |
| `department` | string | No | Department name |
| `role` | string | Yes | One of: `EmployeeBasic`, `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin` |
| `active` | bool | No | Whether the account is active (default: false until activated) |

**Example Request:**
```json
{
  "first_name": "Ana",
  "last_name": "Petrovic",
  "date_of_birth": 946684800,
  "gender": "F",
  "email": "ana.petrovic@exbanka.com",
  "phone": "+381641234567",
  "address": "Trg Slobode 3, Novi Sad",
  "jmbg": "0101200071012",
  "username": "apetrovic",
  "position": "Account Manager",
  "department": "Retail Banking",
  "role": "EmployeeBasic",
  "active": false
}
```

**Response 201:** Employee object (same shape as GET response item)

**Response 400:** `{"error": "validation error"}`
**Response 401/403:** Unauthorized or insufficient permissions

---

### GET /api/v3/employees/:id

Get a single employee by ID.

**Authentication:** Employee JWT + `employees.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Response 200:** Employee object
**Response 404:** `{"error": "employee not found"}`

---

### PUT /api/v3/employees/:id

Partially update an employee. Cannot edit EmployeeAdmin employees.

**Authentication:** Employee JWT + `employees.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Request Body** (all fields optional):

| Field | Type | Description |
|---|---|---|
| `last_name` | string | New last name |
| `gender` | string | Gender |
| `phone` | string | Phone number |
| `address` | string | Residential address |
| `jmbg` | string | 13-digit national ID |
| `position` | string | Job position |
| `department` | string | Department |
| `role` | string | New role |
| `active` | bool | Active status |

**Response 200:** Updated employee object
**Response 403:** `{"error": "cannot edit admin employees"}`
**Response 404:** `{"error": "employee not found"}`

---

## 3. Roles & Permissions

Role and permission management endpoints require an employee JWT with `employees.permissions` permission (EmployeeAdmin).

---

### GET /api/v3/roles

List all roles with their associated permissions.

**Authentication:** Employee JWT + `employees.permissions` permission

**Response 200:**
```json
{
  "roles": [
    {
      "id": 1,
      "name": "EmployeeBasic",
      "description": "EmployeeBasic default role",
      "permissions": ["clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage"]
    }
  ]
}
```

---

### GET /api/v3/roles/:id

Get a single role by ID.

**Authentication:** Employee JWT + `employees.permissions` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Role ID |

**Response 200:** Role object with permissions array
**Response 404:** `{"error": "role not found"}`

---

### POST /api/v3/roles

Create a new role with the given permission codes.

**Authentication:** Employee JWT + `employees.permissions` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Unique role name |
| `description` | string | No | Role description |
| `permission_codes` | string[] | No | Permission codes to assign |

**Example Request:**
```json
{
  "name": "CustomRole",
  "description": "A custom role",
  "permission_codes": ["clients.read", "accounts.read"]
}
```

**Response 201:** Created role object

---

### PUT /api/v3/roles/:id/permissions

Replace all permissions for a role.

**Authentication:** Employee JWT + `employees.permissions` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Role ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `permission_codes` | string[] | Yes | New permission codes (replaces all existing) |

**Response 200:** Updated role object

---

### POST /api/v3/roles/:id/permissions

Grant a single permission to a role (granular). The permission code is validated against the codegened catalog (`contract/permissions/catalog.yaml`); unknown codes are rejected with 400. Idempotent — granting a permission already held is a no-op success.

**Authentication:** Employee JWT + `roles.permissions.assign` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Role ID (numeric) |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `permission` | string | Yes | Permission code to grant (e.g. `clients.read.all`) |

**Response 204:** No content

**Error Responses:**
- `400` — `id` is not a valid integer, `permission` missing from request body, or permission not found in catalog
- `401` — missing or invalid JWT
- `403` — caller lacks `roles.permissions.assign`
- `404` — role with the given ID does not exist

---

### DELETE /api/v3/roles/:id/permissions/:permission

Revoke a single permission grant from a role (granular). Idempotent — revoking a permission not currently held is a no-op success.

**Authentication:** Employee JWT + `roles.permissions.revoke` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Role ID (numeric) |
| `permission` | string | Permission code to revoke (e.g. `clients.read.all`) |

**Response 204:** No content

**Error Responses:**
- `400` — `id` is not a valid integer
- `401` — missing or invalid JWT
- `403` — caller lacks `roles.permissions.revoke`
- `404` — role with the given ID does not exist

---

### GET /api/v3/permissions

List all available permission codes in the system.

**Authentication:** Employee JWT + `employees.permissions` permission

**Response 200:**
```json
{
  "permissions": [
    { "id": 1, "code": "clients.read", "description": "View client profiles", "category": "clients" },
    { "id": 2, "code": "accounts.create", "description": "Create bank accounts", "category": "accounts" }
  ]
}
```

---

### PUT /api/v3/employees/:id/roles

Set (replace) all roles for an employee.

**Authentication:** Employee JWT + `employees.permissions` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `role_names` | string[] | Yes | Role names to assign (e.g. `["EmployeeBasic", "EmployeeAgent"]`) |

**Response 200:** Updated employee object
**Response 404:** `{"error": "employee not found"}`

---

### PUT /api/v3/employees/:id/permissions

Set (replace) the additional per-employee permissions. These are granted on top of the employee's role-based permissions.

**Authentication:** Employee JWT + `employees.permissions` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `permission_codes` | string[] | Yes | Additional permission codes (e.g. `["securities.trade"]`) |

**Response 200:** Updated employee object
**Response 404:** `{"error": "employee not found"}`

---

## 4. Clients

Client management endpoints require an employee JWT with `clients.read` permission (EmployeeBasic+). Clients can view their own profile via a client JWT.

---

### POST /api/v3/clients

Create a new bank client.

**Authentication:** Employee JWT + `clients.create` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `first_name` | string | Yes | First name |
| `last_name` | string | Yes | Last name |
| `date_of_birth` | int64 | Yes | Unix timestamp (seconds) |
| `gender` | string | No | Gender (e.g., "M", "F") |
| `email` | string | Yes | Email address (must be unique) |
| `phone` | string | No | Phone number |
| `address` | string | No | Residential address |
| `jmbg` | string | Yes | 13-digit national ID number (unique) |

**Example Request:**
```json
{
  "first_name": "Marko",
  "last_name": "Jovanovic",
  "date_of_birth": 820454400,
  "gender": "M",
  "email": "marko.jovanovic@email.com",
  "phone": "+381611234567",
  "address": "Jovana Subotica 12, Beograd",
  "jmbg": "0506960710123"
}
```

**Response 201:**
```json
{
  "id": 1,
  "first_name": "Marko",
  "last_name": "Jovanovic",
  "date_of_birth": 820454400,
  "gender": "M",
  "email": "marko.jovanovic@email.com",
  "phone": "+381611234567",
  "address": "Jovana Subotica 12, Beograd",
  "jmbg": "0506960710123",
  "active": false,
  "created_at": "2026-03-13T10:00:00Z"
}
```

---

### GET /api/v3/clients

List clients with optional filters.

**Authentication:** Employee JWT + `clients.read` permission

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default: 1) |
| `page_size` | int | No | Items per page (default: 20) |
| `email_filter` | string | No | Filter by email (partial match) |
| `name_filter` | string | No | Filter by name (partial match) |

**Response 200:**
```json
{
  "clients": [ /* array of client objects */ ],
  "total": 150
}
```

---

### GET /api/v3/clients/:id

Get a single client by ID.

**Authentication:** Employee JWT + `clients.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Response 200:** Client object
**Response 404:** `{"error": "client not found"}`

---

### PUT /api/v3/clients/:id

Partially update a client record.

**Authentication:** Employee JWT + `clients.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Request Body** (all fields optional):

| Field | Type | Description |
|---|---|---|
| `first_name` | string | First name |
| `last_name` | string | Last name |
| `date_of_birth` | int64 | Unix timestamp |
| `gender` | string | Gender |
| `email` | string | Email address |
| `phone` | string | Phone number |
| `address` | string | Residential address |

**Response 200:** Updated client object

---

## 5. Accounts

Account endpoints require an employee JWT with `accounts.read` permission (EmployeeBasic+). Clients can look up accounts by number.

---

### POST /api/v3/accounts

Create a new bank account.

**Authentication:** Employee JWT + `accounts.create` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `owner_id` | uint64 | Yes | Client ID who owns the account |
| `account_kind` | string | Yes | `"current"` or `"foreign"` (case-insensitive) |
| `account_type` | string | Yes | Free-form account type (e.g., `"standard"`, `"savings"`, `"pension"`) |
| `account_category` | string | No | `"personal"` or `"business"` (case-insensitive) |
| `currency_code` | string | Yes | ISO 4217 code (e.g., `"RSD"`, `"EUR"`, `"USD"`) |
| `employee_id` | uint64 | No | Employee who created the account |
| `initial_balance` | float64 | No | Starting balance (must be >= 0, default: 0) |
| `create_card` | bool | No | Auto-create a debit card for this account |
| `card_brand` | string | No | Card brand if `create_card` is true: `"visa"`, `"mastercard"`, `"dinacard"`, `"amex"` (default: `"visa"`) |
| `company_id` | uint64 | No | Associated company ID (for business accounts) |

**Example Request:**
```json
{
  "owner_id": 1,
  "account_kind": "current",
  "account_type": "standard",
  "account_category": "personal",
  "currency_code": "RSD",
  "employee_id": 5,
  "initial_balance": 10000.00,
  "create_card": true,
  "card_brand": "visa"
}
```

**Response 201:** Account object

---

### GET /api/v3/clients/:id/accounts

List all accounts belonging to a specific client. Replaces the former `GET /api/v3/accounts?client_id=X` pattern.

**Authentication:** Employee JWT + `accounts.read.all` or `accounts.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "accounts": [ /* array of account objects */ ],
  "total": 3
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — client not found

---

### GET /api/v3/accounts

List all accounts with optional filters. This is the supervisor's "find any account" view. To look up accounts belonging to a specific client, use `GET /api/v3/clients/:id/accounts` instead. Clients looking for their own accounts should use `GET /api/v3/me/accounts`.

**Authentication:** Employee JWT + `accounts.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `name_filter` | string | Filter by account name (partial match) |
| `account_number` | string | Look up a single account by exact account number; returns an array of 0 or 1 items (never 404) |
| `type_filter` | string | Filter by account type |

> **Note:** `account_number` is mutually exclusive with `name_filter` and `type_filter`. Providing more than one filter at the same time returns 400. `client_id` is no longer accepted on this endpoint — use `GET /api/v3/clients/:id/accounts` for client-scoped lookups.

**Response 200:**
```json
{
  "accounts": [
    {
      "id": 1,
      "account_number": "265-1234567890123-56",
      "account_name": "My Current Account",
      "owner_id": 1,
      "owner_name": "Marko Jovanovic",
      "balance": "15000.5000",
      "available_balance": "14500.0000",
      "employee_id": 5,
      "created_at": "2026-03-13T10:00:00Z",
      "expires_at": "2031-03-13T10:00:00Z",
      "currency_code": "RSD",
      "status": "active",
      "account_kind": "current",
      "account_type": "standard",
      "account_category": "personal",
      "maintenance_fee": "220.0000",
      "daily_limit": "1000000.0000",
      "monthly_limit": "10000000.0000",
      "daily_spending": "0.0000",
      "monthly_spending": "0.0000",
      "company_id": null
    }
  ],
  "total": 300
}
```

---

### GET /api/v3/accounts/:id

Get a single account by ID.

**Authentication:** Employee JWT + `accounts.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:** Account object
**Response 404:** `{"error": "account not found"}`

---

<!-- NOTE: GET /api/v3/accounts/by-number/:account_number was removed in the v3 route
     standardization (2026-04-28). Use GET /api/v3/accounts?account_number=<number> instead.
     That endpoint returns an array of 0 or 1 items; it never returns 404 for a missing account.
-->



### PUT /api/v3/accounts/:id/name

Update the display name of an account.

**Authentication:** Employee JWT + `accounts.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `new_name` | string | Yes | New account display name |
| `client_id` | uint64 | No | Client ID for ownership verification |

**Response 200:** Updated account object

---

### PUT /api/v3/accounts/:id/limits

Update the daily/monthly spending limits of an account. Requires a verification code for authorization.

**Authentication:** Employee JWT + `accounts.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `daily_limit` | float64 | No | New daily spending limit (must be >= 0) |
| `monthly_limit` | float64 | No | New monthly spending limit (must be >= 0) |
| `verification_code` | string | Yes | Verification code for authorization |

> **Note:** At least one of `daily_limit` or `monthly_limit` should be provided. The `verification_code` is validated against the transaction service before the limits are applied.

**Example Request:**
```json
{
  "daily_limit": 500000.00,
  "monthly_limit": 5000000.00,
  "verification_code": "847291"
}
```

**Response 200:** Updated account object

| Status | Description |
|---|---|
| 200 | Limits updated |
| 400 | Invalid input or invalid verification code |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### POST /api/v3/accounts/:id/activate

Activate a previously inactive account. No request body required.

**Authentication:** Employee JWT + `accounts.deactivate.any` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:**
```json
{ "status": "active" }
```

**Error Responses:**
- `400` — invalid account ID
- `401` — missing or invalid JWT
- `403` — caller lacks `accounts.deactivate.any`
- `404` — account not found

---

### POST /api/v3/accounts/:id/deactivate

Deactivate an active account. No request body required.

**Authentication:** Employee JWT + `accounts.deactivate.any` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:**
```json
{ "status": "inactive" }
```

**Error Responses:**
- `400` — invalid account ID
- `401` — missing or invalid JWT
- `403` — caller lacks `accounts.deactivate.any`
- `404` — account not found

---

### GET /api/v3/currencies

List all supported currencies.

**Authentication:** Employee JWT (any role)

**Response 200:**
```json
{
  "currencies": [
    {
      "code": "RSD",
      "name": "Serbian Dinar",
      "symbol": "din"
    },
    {
      "code": "EUR",
      "name": "Euro",
      "symbol": "EUR"
    }
  ]
}
```

---

### POST /api/v3/companies

Create a new company record.

**Authentication:** Employee JWT + `accounts.create` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `company_name` | string | Yes | Legal company name |
| `registration_number` | string | Yes | Company registration number (unique) |
| `tax_number` | string | No | Tax identification number |
| `activity_code` | string | No | Industry activity code |
| `address` | string | No | Registered address |
| `owner_id` | uint64 | Yes | Client ID of the company owner |

**Response 201:**
```json
{
  "id": 1,
  "company_name": "EX Tech d.o.o.",
  "registration_number": "12345678",
  "tax_number": "987654321",
  "activity_code": "6201",
  "address": "Bulevar Oslobodjenja 1, Novi Sad",
  "owner_id": 1
}
```

---

## 6. Cards

Card endpoints require specific employee permissions (see per-endpoint notes). Creating cards requires `cards.create`; blocking, unblocking, and deactivating require `cards.update`; approving/rejecting card requests requires `cards.approve`. Clients can read their own cards.

---

### POST /api/v3/cards

Issue a new payment card linked to an account.

**Authentication:** Employee JWT + `cards.create` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `account_number` | string | Yes | Account to link the card to |
| `owner_id` | uint64 | Yes | ID of the card owner (client or authorized person) |
| `owner_type` | string | Yes | `"CLIENT"` or `"AUTHORIZED_PERSON"` |
| `card_brand` | string | No | `"VISA"`, `"MASTERCARD"`, `"DINA"` (auto-assigned if omitted) |

**Example Request:**
```json
{
  "account_number": "265-1234567890123-56",
  "owner_id": 1,
  "owner_type": "CLIENT",
  "card_brand": "VISA"
}
```

**Response 201:**
```json
{
  "id": 1,
  "card_number": "**** **** **** 4242",
  "card_number_full": "4111111111114242",
  "card_type": "DEBIT",
  "card_name": "MARKO JOVANOVIC",
  "card_brand": "VISA",
  "created_at": "2026-03-13T10:00:00Z",
  "expires_at": "2031-03-01T00:00:00Z",
  "account_number": "265-1234567890123-56",
  "cvv": "123",
  "card_limit": 100000.00,
  "status": "ACTIVE",
  "owner_type": "CLIENT",
  "owner_id": 1
}
```

> **Note:** `card_number_full` and `cvv` are only returned at card creation time and in the card verification email. Subsequent reads return masked values.

---

### GET /api/v3/cards/:id

Get a card by ID.

**Authentication:** Employee JWT only (`AuthMiddleware` + `cards.read` permission). Clients must use `GET /api/v3/me/cards/:id` instead.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object (with masked card number)
**Response 404:** `{"error": "card not found"}`

---

<!-- NOTE: GET /api/v3/cards (bare collection) was removed in the v3 route standardization
     (2026-04-28). Card lists are now scoped to a parent resource:
       - By client:  GET /api/v3/clients/:id/cards   (requires cards.read.all or cards.read.own)
       - By account: GET /api/v3/accounts/:id/cards  (requires cards.read.all or cards.read.own)
     See the "Client-Scoped Sub-Collections" and "Account-Scoped Sub-Collections" sections below.
-->

### GET /api/v3/clients/:id/cards

List all cards belonging to a specific client.

**Authentication:** Employee JWT + `cards.read.all` or `cards.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ],
  "total": 5
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — client not found

---

### GET /api/v3/accounts/:id/cards

List all cards linked to a specific account (identified by account numeric ID).

**Authentication:** Employee JWT + `cards.read.all` or `cards.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID (numeric, not account number string) |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ],
  "total": 5
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — account not found

---

### POST /api/v3/cards/:id/block

Block a card (e.g., reported as lost or stolen).

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "BLOCKED"`

**Response 404:** `{"error": {"code": "not_found", "message": "card not found"}}`

---

### POST /api/v3/cards/:id/unblock

Unblock a previously blocked card. Only employees can unblock cards.

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "ACTIVE"`

---

### POST /api/v3/cards/:id/deactivate

Permanently deactivate a card.

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "DEACTIVATED"`

---

### POST /api/v3/cards/authorized-persons

Create an authorized person who can also hold a card linked to an existing account.

**Authentication:** Employee JWT + `cards.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `first_name` | string | Yes | First name |
| `last_name` | string | Yes | Last name |
| `date_of_birth` | int64 | No | Unix timestamp |
| `gender` | string | No | Gender |
| `email` | string | No | Email address |
| `phone` | string | No | Phone number |
| `address` | string | No | Residential address |
| `account_id` | uint64 | Yes | Account ID to authorize this person on |

**Response 201:**
```json
{
  "id": 1,
  "first_name": "Ana",
  "last_name": "Jovanovic",
  "date_of_birth": 946684800,
  "gender": "F",
  "email": "ana.j@email.com",
  "phone": "+381651234567",
  "address": "Trg Slobode 5, Novi Sad",
  "account_id": 1
}
```

---

### POST /api/v3/me/cards/virtual

Create a virtual card for a client account. Virtual cards can be single-use or multi-use and expire after 1-3 months. The card owner is derived from the JWT — the `owner_id` field in the request body is ignored; the JWT `user_id` is used as the owner.

**Authentication:** Any JWT (AnyAuthMiddleware -- identity scoped to the token principal)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `account_number` | string | Yes | Account to link the virtual card to |
| `owner_id` | uint64 | Yes | Client ID of the card owner |
| `card_brand` | string | Yes | `"visa"`, `"mastercard"`, `"dinacard"`, or `"amex"` |
| `usage_type` | string | Yes | `"single_use"` or `"multi_use"` |
| `max_uses` | int32 | No | Max uses (required for `multi_use`, must be >= 2; ignored for `single_use`) |
| `expiry_months` | int32 | Yes | Expiry duration in months: 1, 2, or 3 |
| `card_limit` | string | Yes | Card spending limit as decimal string (e.g. `"100000.0000"`) |

**Example Request:**
```json
{
  "account_number": "265-0000000001-00",
  "owner_id": 1,
  "card_brand": "visa",
  "usage_type": "multi_use",
  "max_uses": 5,
  "expiry_months": 1,
  "card_limit": "5000.0000"
}
```

**Response 201:**
```json
{
  "id": 10,
  "card_number": "**** **** **** 9876",
  "card_number_full": "4111111111119876",
  "card_type": "debit",
  "card_brand": "visa",
  "account_number": "265-0000000001-00",
  "cvv": "456",
  "card_limit": "5000.0000",
  "status": "active",
  "owner_type": "client",
  "owner_id": 1,
  "expires_at": "2026-04-19T00:00:00Z",
  "created_at": "2026-03-19T10:00:00Z"
}
```

| Status | Description |
|---|---|
| 201 | Virtual card created |
| 400 | Invalid input (bad usage_type, expiry_months, max_uses, or card_limit) |
| 401 | Unauthorized |

---

### POST /api/v3/me/cards/:id/pin

Set the 4-digit PIN for a card. Ownership is derived from the JWT — if the card does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `pin` | string | Yes | Exactly 4 numeric digits |

**Example Request:**
```json
{
  "pin": "1234"
}
```

**Response 200:**
```json
{
  "success": true,
  "message": "PIN set successfully"
}
```

| Status | Description |
|---|---|
| 200 | PIN set |
| 400 | Invalid PIN format (must be exactly 4 digits) |
| 401 | Unauthorized |
| 500 | Internal error |

---

### POST /api/v3/me/cards/:id/verify-pin

Verify the 4-digit PIN for a card. The card is permanently blocked after 3 consecutive failed attempts. Ownership is derived from the JWT — if the card does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `pin` | string | Yes | 4-digit PIN to verify |

**Example Request:**
```json
{
  "pin": "1234"
}
```

**Response 200:**
```json
{
  "valid": true,
  "message": "PIN verified"
}
```

| Status | Description |
|---|---|
| 200 | Verification result (check `valid` field) |
| 400 | Invalid input |
| 401 | Unauthorized |
| 500 | Internal error |

---

### POST /api/v3/me/cards/:id/temporary-block

Temporarily block a card for a specified duration in hours. The card is automatically unblocked by a background job when the duration expires. Ownership is derived from the JWT — if the card does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `duration_hours` | int32 | Yes | Block duration in hours (1-720) |
| `reason` | string | No | Reason for blocking (e.g. "Lost card") |

**Example Request:**
```json
{
  "duration_hours": 24,
  "reason": "Suspicious activity"
}
```

**Response 200:** Updated card object with `"status": "blocked"`

| Status | Description |
|---|---|
| 200 | Card temporarily blocked |
| 400 | Invalid input or card not found |
| 401 | Unauthorized |
| 404 | Card not found |

---

## 7. Payments

Payments send money from one account to **another person** — a different client, at this bank or at another (peer) bank — with optional payment metadata. (To move money between your **own** accounts, use transfers.)

---

### POST /api/v3/me/payments

Initiate a new payment from a client account. The destination may be at this bank (intra-bank) or at a registered peer bank (cross-bank, dispatched via SI-TX — see the inter-bank note below).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | float64 | Yes | Payment amount (in source currency) |
| `currency` | string | No | Cross-bank only: the SI-TX posting currency. Optional — defaults to the **sender's** account currency (resolved from account-service). |
| `recipient_name` | string | No | Recipient display name |
| `payment_code` | string | No | Payment code (e.g., `"289"`) |
| `reference_number` | string | No | Reference/model number |
| `payment_purpose` | string | No | Description or purpose of payment |

> **Inter-bank dispatch (SI-TX):** When `to_account_number`'s 3-digit prefix differs from this bank's `OWN_BANK_CODE`, the request is dispatched to `PeerTxService.InitiateOutboundTx` and returns `202 Accepted` with `{transaction_id, poll_url, status}`; poll the returned URL for SI-TX completion. **If the destination bank code is not a registered, active peer bank, the request is rejected with `404 not_found` ("peer bank XXX not registered") before any funds move.** Intra-bank receivers (own prefix) keep the `201 Created` shape below.

**Example Request:**
```json
{
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-9876543210987-12",
  "amount": 5000.00,
  "recipient_name": "EX Tech d.o.o.",
  "payment_code": "289",
  "reference_number": "97 123456789",
  "payment_purpose": "Invoice #INV-2026-001"
}
```

**Response 201:**
```json
{
  "id": 1,
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-9876543210987-12",
  "initial_amount": 5000.00,
  "final_amount": 5000.00,
  "commission": 0.00,
  "recipient_name": "EX Tech d.o.o.",
  "payment_code": "289",
  "reference_number": "97 123456789",
  "payment_purpose": "Invoice #INV-2026-001",
  "status": "pending_verification",
  "timestamp": "2026-03-13T10:00:00Z"
}
```

> **Note:** Payment is created in `pending_verification` status. The browser must create a verification challenge via `POST /api/v3/verifications` and then poll `GET /api/v3/verifications/:id/status` until verified. Once verified, call `POST /api/v3/me/payments/:id/execute` with the `challenge_id`. Users with `verification.skip` permission skip verification entirely.

> **Fund account restriction (E0, Plan E 2026-05-28):** The source account (`from_account_number`) must NOT belong to an investment fund's RSD account (`account_category = "investment_fund"`). Fund cash may only exit via dedicated fund operations (buy on behalf of fund, dividend payout, investor redemption). Using a fund account as the source returns `403 forbidden` with code `fund_account_outflow_restricted`.

---

### GET /api/v3/payments/:id

Get a payment by ID.

**Authentication:** Employee JWT + `payments.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Response 200:** Payment object
**Response 404:** `{"error": "payment not found"}`

---

<!-- NOTE: GET /api/v3/payments?client_id=X and GET /api/v3/payments?account_number=X were
     removed in the v3 route standardization (2026-04-28). Use the scoped endpoints:
       - By client:  GET /api/v3/clients/:id/payments
       - By account: GET /api/v3/accounts/:id/payments
-->

### GET /api/v3/clients/:id/payments

List all payments where the specified client's accounts appear as sender or recipient.

**Authentication:** Employee JWT + `accounts.read.all` or `accounts.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `page` | integer | 1 | Page number |
| `page_size` | integer | 20 | Items per page |

**Response 200:**
```json
{
  "payments": [ /* array of payment objects */ ],
  "total": 87
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — client not found

---

### GET /api/v3/accounts/:id/payments

List payments for a specific account, identified by account numeric ID. Supports rich date, status, and amount filters.

**Authentication:** Employee JWT + `accounts.read.all` or `accounts.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID (numeric, not account number string) |

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `page` | integer | 1 | Page number |
| `page_size` | integer | 20 | Items per page |
| `date_from` | string | -- | Start date filter (RFC3339 or YYYY-MM-DD) |
| `date_to` | string | -- | End date filter (RFC3339 or YYYY-MM-DD) |
| `status_filter` | string | -- | Filter by status (e.g., `"COMPLETED"`, `"PENDING"`) |
| `amount_min` | float64 | -- | Minimum amount filter |
| `amount_max` | float64 | -- | Maximum amount filter |

**Response 200:**
```json
{
  "payments": [ /* array of payment objects */ ],
  "total": 87
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — account not found

---

### POST /api/v3/me/payments/preview

Preview what a payment would cost **before** creating it, so the frontend can show the fee and total. Payments are single-currency (no exchange): the fee is computed in the sender's account currency and debited on top of the amount, so `total_debit = input_amount + total_fee` and the recipient receives `input_amount`. Works for both intra-bank and cross-bank destinations (the fee is sender-side; the recipient account is not looked up).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | float64 | Yes | Payment amount (in source currency) |

**Response 200:**
```json
{
  "currency": "RSD",
  "input_amount": "1000.0000",
  "total_fee": "10.0000",
  "fee_breakdown": [ { "fee_type": "percentage", "amount": "10.0000" } ],
  "total_debit": "1010.0000",
  "amount_received": "1000.0000"
}
```

**Error Responses:**
- `400` — invalid body or non-positive amount
- `401` — missing or invalid JWT
- `500` — fee or account lookup failed

---

### GET /api/v3/me/payments/:id/status

Lightweight status of a payment — mirrors `GET /api/v3/me/transfers/:id/status` so the frontend can poll payments and transfers separately. The `:id` may be either:
- a **numeric** payment id → intra-bank payment status (`404` if not owned by the caller); or
- a **UUID** SI-TX transaction id (the `transaction_id` / `poll_url` returned by a cross-bank payment's `202`) → the outbound SI-TX status.

`GET /api/v3/me/payments/:id` accepts the same two id forms. (The cross-bank UUID is unguessable and is only ever handed to the initiator, so knowing it authorizes reading its status.)

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200 (intra-bank, numeric id):**
```json
{ "payment_id": 99, "status": "completed" }
```

**Response 200 (cross-bank, UUID id):**
```json
{ "transaction_id": "1111-...-5555", "status": "committed", "role": "sender", "last_action_at": "2026-05-30T00:00:00Z", "last_error": "" }
```

---

### POST /api/v3/me/payments/:id/execute

Execute a pending payment after verification. The payment must have been created previously via `POST /api/v3/me/payments`. Verification is handled by the verification-service -- pass the `challenge_id` from the completed verification challenge.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | The verification challenge ID (must have status `verified`) |
| `verification_code` | string | No | Deprecated -- kept for backwards compatibility |

**Example Request:**
```json
{
  "challenge_id": 123
}
```

**Response 200:**
```json
{
  "id": 1,
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-9876543210987-12",
  "initial_amount": 5000.00,
  "final_amount": 5000.00,
  "commission": 0.00,
  "recipient_name": "EX Tech d.o.o.",
  "payment_code": "289",
  "reference_number": "97 123456789",
  "payment_purpose": "Invoice #INV-2026-001",
  "status": "completed",
  "timestamp": "2026-03-13T10:00:00Z"
}
```

| Status | Description |
|---|---|
| 200 | Payment executed |
| 400 | Invalid input or invalid payment ID |
| 401 | Unauthorized |
| 409 | Verification not completed |
| 500 | Internal server error |

---

## 8. Transfers

Transfers are inter-account currency exchanges (can be same currency or cross-currency).

---

### POST /api/v3/me/transfers

Initiate a currency transfer between accounts.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | float64 | Yes | Amount to transfer (in source currency) |

**Example Request:**
```json
{
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-1234500000EUR-78",
  "amount": 1000.00
}
```

**Response 201:**
```json
{
  "id": 1,
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-1234500000EUR-78",
  "initial_amount": 1000.00,
  "final_amount": 8.53,
  "exchange_rate": 117.23,
  "commission": 0.50,
  "timestamp": "2026-03-13T10:00:00Z",
  "status": "pending_verification"
}
```

> **Note:** Transfer is created in `pending_verification` status. The browser must create a verification challenge via `POST /api/v3/verifications` and then poll `GET /api/v3/verifications/:id/status` until verified. Once verified, call `POST /api/v3/me/transfers/:id/execute` with the `challenge_id`. Users with `verification.skip` permission skip verification entirely.

> **Intra-bank only:** Transfers are between accounts of the **same client** within this bank (e.g. your own RSD → EUR account, with FX). Both `from_account_number` and `to_account_number` must belong to the same client and to this bank. To send money to **another person or another bank**, use **payments** (`POST /api/v3/me/payments`), which is where cross-bank (SI-TX) dispatch lives. A cross-bank `to_account_number` here is rejected (intra-client/intra-bank validation fails).

> **Fund account restriction (E0, Plan E 2026-05-28):** The source account (`from_account_number`) must NOT belong to an investment fund's RSD account (`account_category = "investment_fund"`). Fund cash may only exit via dedicated fund operations (buy on behalf of fund, dividend payout, investor redemption). Using a fund account as the source returns `403 forbidden` with code `fund_account_outflow_restricted`.

---

### POST /api/v3/me/transfers/preview

**v1-only endpoint.** Preview transfer costs (fees and exchange rate) without creating the transfer.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | number | Yes | Transfer amount (must be positive) |

**Example Request:**

```json
{
  "from_account_number": "1234567890",
  "to_account_number": "0987654321",
  "amount": 5000.00
}
```

**Response 200:**

```json
{
  "from_currency": "RSD",
  "to_currency": "EUR",
  "input_amount": "5000.0000",
  "total_fee": "255.0000",
  "fee_breakdown": [
    {
      "name": "Basic commission",
      "fee_type": "percentage",
      "fee_value": "0.1000",
      "calculated_amount": "5.0000"
    },
    {
      "name": "Transfer commission",
      "fee_type": "percentage",
      "fee_value": "5.0000",
      "calculated_amount": "250.0000"
    }
  ],
  "converted_amount": "42.5532",
  "exchange_rate": "117.4500",
  "exchange_commission_rate": "0.0050"
}
```

For same-currency transfers, `converted_amount` equals `input_amount`, `exchange_rate` is `"1.0000"`, and `exchange_commission_rate` is `"0.0000"`.

| Status | Code | Description |
|---|---|---|
| 200 | -- | Preview returned |
| 400 | `validation_error` | Missing or invalid fields |
| 401 | `unauthorized` | Missing or invalid token |
| 404 | `not_found` | Account not found |

---

### GET /api/v3/transfers/:id

Get a transfer by ID.

**Authentication:** Employee JWT + `payments.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Response 200:** Transfer object
**Response 404:** `{"error": "transfer not found"}`

---

<!-- NOTE: GET /api/v3/transfers?client_id=X was removed in the v3 route standardization
     (2026-04-28). Use GET /api/v3/clients/:id/transfers instead.
-->

### GET /api/v3/clients/:id/transfers

List all currency transfers where the specified client's accounts appear as sender or recipient.

**Authentication:** Employee JWT + `accounts.read.all` or `accounts.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "transfers": [ /* array of transfer objects */ ],
  "total": 12
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — client not found

---

### POST /api/v3/me/transfers/:id/execute

Execute a pending transfer after verification. The transfer must have been created previously via `POST /api/v3/me/transfers`. Verification is handled by the verification-service -- pass the `challenge_id` from the completed verification challenge.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | The verification challenge ID (must have status `verified`) |
| `verification_code` | string | No | Deprecated -- kept for backwards compatibility |

**Example Request:**
```json
{
  "challenge_id": 123
}
```

**Response 200:**
```json
{
  "id": 1,
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-1234500000EUR-78",
  "initial_amount": 1000.00,
  "final_amount": 8.53,
  "exchange_rate": 117.23,
  "commission": 0.50,
  "timestamp": "2026-03-13T10:00:00Z",
  "status": "completed"
}
```

| Status | Description |
|---|---|
| 200 | Transfer executed |
| 400 | Invalid input or invalid transfer ID |
| 401 | Unauthorized |
| 409 | Verification not completed |
| 500 | Internal server error |

---

## 9. Payment Recipients

Saved payment recipients (favorites) for a client.

---

### POST /api/v3/me/payment-recipients

Save a new payment recipient.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | ID of the client saving this recipient |
| `recipient_name` | string | Yes | Display name for the recipient |
| `account_number` | string | Yes | Recipient's account number |

**Example Request:**
```json
{
  "client_id": 1,
  "recipient_name": "Mama",
  "account_number": "265-9876543210987-12"
}
```

**Response 201:**
```json
{
  "id": 1,
  "client_id": 1,
  "recipient_name": "Mama",
  "account_number": "265-9876543210987-12",
  "created_at": "2026-03-13T10:00:00Z"
}
```

---

### GET /api/v3/me/payment-recipients

List all saved recipients for the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**
```json
{
  "recipients": [ /* array of recipient objects */ ]
}
```

---

### PUT /api/v3/me/payment-recipients/:id

Update a saved recipient. Ownership is derived from the JWT — if the recipient does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Recipient ID |

**Request Body** (at least one required):

| Field | Type | Description |
|---|---|---|
| `recipient_name` | string | New display name |
| `account_number` | string | New account number |

**Response 200:** Updated recipient object

---

### DELETE /api/v3/me/payment-recipients/:id

Delete a saved recipient. Ownership is derived from the JWT — if the recipient does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Recipient ID |

**Response 200:** `{"success": true}`

---

## 10. Exchange Rates

Public endpoints -- no authentication required.

Supported currencies: `RSD`, `EUR`, `USD`, `CHF`, `GBP`, `JPY`, `CAD`, `AUD`.

---

### GET /api/v3/exchange/rates

List all current exchange rates.

**Authentication:** None (public)

**Response 200:**
```json
{
  "rates": [
    {
      "from_currency": "EUR",
      "to_currency": "RSD",
      "buy_rate": "116.5000",
      "sell_rate": "117.8000",
      "updated_at": "2026-03-13T08:00:00Z"
    }
  ]
}
```

---

### GET /api/v3/exchange/rates/:from/:to

Get the exchange rate between two specific currencies.

**Authentication:** None (public)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `from` | string | Source currency code (e.g., `EUR`) |
| `to` | string | Target currency code (e.g., `RSD`) |

**Response 200:**
```json
{
  "from_currency": "EUR",
  "to_currency": "RSD",
  "buy_rate": "116.5000",
  "sell_rate": "117.8000",
  "updated_at": "2026-03-13T08:00:00Z"
}
```

**Response 404:** `{"error": {"code": "not_found", "message": "exchange rate not found"}}`

---

### POST /api/v3/exchange/calculate

Calculate a currency conversion including the bank's commission. Informational only -- no transaction is created.

**Authentication:** None (public)

**Request body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `fromCurrency` | string | yes | Source currency code (e.g. `EUR`) |
| `toCurrency` | string | yes | Target currency code (e.g. `RSD`) |
| `amount` | string | yes | Amount to convert (must be positive decimal) |

**Example request:**
```json
{
  "fromCurrency": "EUR",
  "toCurrency": "RSD",
  "amount": "100.00"
}
```

**Response 200:**
```json
{
  "from_currency": "EUR",
  "to_currency": "RSD",
  "input_amount": "100.0000",
  "converted_amount": "11700.0000",
  "commission_rate": "0.005",
  "effective_rate": "117.3000"
}
```

| Code | Description |
|---|---|
| 200 | Conversion result |
| 400 | Validation error (missing fields, invalid amount, unsupported currency) |
| 404 | Exchange rate not found for the requested pair |
| 500 | Internal error |

---

## 11. Loans

Loan management endpoints. Employees can view all loans and approve/reject loan requests. Clients should use `GET /api/v3/me/loans` and related `/api/v3/me/*` routes to view and manage their own loans.

---

### POST /api/v3/me/loan-requests

Submit a new loan application. The `client_id` field in the request body is ignored; the JWT `user_id` is used as the applicant. Ownership is enforced at the gateway.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | No | Ignored — JWT `user_id` is used as the applicant |
| `loan_type` | string | Yes | `"PERSONAL"`, `"MORTGAGE"`, `"AUTO"`, `"STUDENT"`, `"BUSINESS"` |
| `interest_type` | string | Yes | `"FIXED"` or `"VARIABLE"` |
| `amount` | float64 | Yes | Requested loan amount |
| `currency_code` | string | Yes | Currency of the loan (e.g., `"RSD"`, `"EUR"`) |
| `purpose` | string | No | Purpose/reason for the loan |
| `monthly_salary` | float64 | No | Applicant's monthly salary |
| `employment_status` | string | No | `"EMPLOYED"`, `"SELF_EMPLOYED"`, `"UNEMPLOYED"`, `"RETIRED"` |
| `employment_period` | int32 | No | Years of current employment |
| `repayment_period` | int32 | Yes | Loan term in months |
| `phone` | string | No | Contact phone number |
| `account_number` | string | Yes | Account number for loan disbursement |

**Example Request:**
```json
{
  "client_id": 1,
  "loan_type": "PERSONAL",
  "interest_type": "FIXED",
  "amount": 500000.00,
  "currency_code": "RSD",
  "purpose": "Home renovation",
  "monthly_salary": 120000.00,
  "employment_status": "EMPLOYED",
  "employment_period": 5,
  "repayment_period": 60,
  "phone": "+381611234567",
  "account_number": "265-1234567890123-56"
}
```

**Response 201:**
```json
{
  "id": 1,
  "client_id": 1,
  "loan_type": "PERSONAL",
  "interest_type": "FIXED",
  "amount": 500000.00,
  "currency_code": "RSD",
  "purpose": "Home renovation",
  "monthly_salary": 120000.00,
  "employment_status": "EMPLOYED",
  "employment_period": 5,
  "repayment_period": 60,
  "phone": "+381611234567",
  "account_number": "265-1234567890123-56",
  "status": "PENDING",
  "created_at": "2026-03-13T10:00:00Z"
}
```

---

<!-- NOTE: GET /api/v3/loans?client_id=X was removed in the v3 route standardization
     (2026-04-28). Use GET /api/v3/clients/:id/loans instead.
-->

### GET /api/v3/clients/:id/loans

List all loans belonging to a specific client.

**Authentication:** Employee JWT + `credits.read.all` or `credits.read.own` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "loans": [ /* array of loan objects */ ],
  "total": 12
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing required permission
- `404` — client not found

---

### GET /api/v3/loans

List all loans (employee view). This endpoint no longer accepts `?client_id` — use `GET /api/v3/clients/:id/loans` for client-scoped lookups. Clients should use `GET /api/v3/me/loans`.

**Authentication:** Employee JWT + `credits.read.all` or `credits.read.own` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `loan_type_filter` | string | Filter by loan type |
| `account_number_filter` | string | Filter by account number |
| `status_filter` | string | Filter by status |

**Response 200:**
```json
{
  "loans": [ /* array of loan objects */ ],
  "total": 145
}
```

---

### GET /api/v3/loans/:id

Get a single loan by ID (employee view). Clients should use `GET /api/v3/me/loans/:id` instead.

**Authentication:** Employee JWT + `credits.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Loan object
**Response 404:** `{"error": "loan not found"}`

---

### GET /api/v3/loans/:id/installments

Get all installment records for a loan.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:**
```json
{
  "installments": [
    {
      "id": 1,
      "loan_id": 1,
      "amount": 9755.50,
      "interest_rate": 6.5,
      "currency_code": "RSD",
      "expected_date": "2026-04-13",
      "actual_date": null,
      "status": "PENDING"
    }
  ]
}
```

---

## 12. Loan Requests

Loan request management endpoints (employee-facing). Clients should use the `/api/v3/me/loan-requests` routes instead.

---

### GET /api/v3/loan-requests

List all loan requests (employee view).

**Authentication:** Employee JWT + `credits.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `loan_type_filter` | string | Filter by loan type |
| `account_number_filter` | string | Filter by account number |
| `status_filter` | string | Filter by status (`"PENDING"`, `"APPROVED"`, `"REJECTED"`) |
| `client_id` | int | Filter loan requests for a specific client |

**Response 200:**
```json
{
  "requests": [ /* array of loan request objects */ ],
  "total": 23
}
```

---

### GET /api/v3/loan-requests/:id

Get a single loan request by ID.

**Authentication:** Employee JWT + `credits.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Loan request object
**Response 404:** `{"error": {"code": "not_found", "message": "loan request not found"}}`

---

### POST /api/v3/loan-requests/:id/approve

Approve a loan request. Creates a loan and sends an approval email to the client.

**Authentication:** Employee JWT + `credits.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Created loan object:
```json
{
  "id": 1,
  "loan_number": "LOAN-2026-000001",
  "loan_type": "PERSONAL",
  "account_number": "265-1234567890123-56",
  "amount": 500000.00,
  "repayment_period": 60,
  "nominal_interest_rate": 6.5,
  "effective_interest_rate": 6.73,
  "contract_date": "2026-03-13",
  "maturity_date": "2031-03-13",
  "next_installment_amount": 9755.50,
  "next_installment_date": "2026-04-13",
  "remaining_debt": 500000.00,
  "currency_code": "RSD",
  "status": "ACTIVE",
  "interest_type": "FIXED",
  "created_at": "2026-03-13T10:00:00Z"
}
```

**Response 409:** `{"error": {"code": "business_rule_violation", "message": "loan amount 500000.00 exceeds your approval limit of 100000.00"}}`

> **Note:** The approving employee's `MaxLoanApprovalAmount` limit is enforced. If the loan request amount exceeds the employee's configured limit, the approval is rejected with `409 Conflict`.

---

### POST /api/v3/loan-requests/:id/reject

Reject a loan request. Sends a rejection email to the client.

**Authentication:** Employee JWT + `credits.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Updated loan request object with `"status": "REJECTED"`

---

## 13. Limits

Manage transaction and approval limits for employees, and transaction limits for bank clients.

All monetary values are decimal strings (e.g., `"50000.0000"`).

**Authentication:** All endpoints require a valid employee Bearer token.

**Required permission:** `limits.manage`

---

### GET /api/v3/employees/:id/limits

Retrieve the current transaction and approval limits for an employee.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Employee ID |

**Example request:**
```
GET /api/v3/employees/42/limits
Authorization: Bearer <token>
```

**Example response:**
```json
{
  "id": 1,
  "employee_id": 42,
  "max_loan_approval_amount": "50000.0000",
  "max_single_transaction": "100000.0000",
  "max_daily_transaction": "500000.0000",
  "max_client_daily_limit": "250000.0000",
  "max_client_monthly_limit": "2500000.0000"
}
```

| Status | Description |
|--------|-------------|
| 200 | Employee limits returned |
| 400 | Invalid employee ID |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### PUT /api/v3/employees/:id/limits

Set or update transaction and approval limits for an employee. If no limits exist for this employee, they are created.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Employee ID |

**Request body:**
```json
{
  "max_loan_approval_amount": "50000.0000",
  "max_single_transaction": "100000.0000",
  "max_daily_transaction": "500000.0000",
  "max_client_daily_limit": "250000.0000",
  "max_client_monthly_limit": "2500000.0000"
}
```

**Example response:**
```json
{
  "id": 1,
  "employee_id": 42,
  "max_loan_approval_amount": "50000.0000",
  "max_single_transaction": "100000.0000",
  "max_daily_transaction": "500000.0000",
  "max_client_daily_limit": "250000.0000",
  "max_client_monthly_limit": "2500000.0000"
}
```

| Status | Description |
|--------|-------------|
| 200 | Limits updated |
| 400 | Invalid input |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### POST /api/v3/employees/:id/limits/template

Apply a named limit template to an employee. Copies the template's values to the employee's limit record.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Employee ID |

**Request body:**
```json
{
  "template_name": "BasicTeller"
}
```

**Example response:**
```json
{
  "id": 1,
  "employee_id": 42,
  "max_loan_approval_amount": "50000.0000",
  "max_single_transaction": "100000.0000",
  "max_daily_transaction": "500000.0000",
  "max_client_daily_limit": "250000.0000",
  "max_client_monthly_limit": "2500000.0000"
}
```

| Status | Description |
|--------|-------------|
| 200 | Template applied |
| 400 | Invalid input or template not found |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### GET /api/v3/limits/templates

List all predefined and custom limit templates.

**Example request:**
```
GET /api/v3/limits/templates
Authorization: Bearer <token>
```

**Example response:**
```json
{
  "templates": [
    {
      "id": 1,
      "name": "BasicTeller",
      "description": "Default teller limits",
      "max_loan_approval_amount": "50000.0000",
      "max_single_transaction": "100000.0000",
      "max_daily_transaction": "500000.0000",
      "max_client_daily_limit": "250000.0000",
      "max_client_monthly_limit": "2500000.0000"
    }
  ]
}
```

| Status | Description |
|--------|-------------|
| 200 | Templates returned |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### POST /api/v3/limits/templates

Create a new named limit template.

**Request body:**
```json
{
  "name": "SeniorAgent",
  "description": "Senior agent limits",
  "max_loan_approval_amount": "500000.0000",
  "max_single_transaction": "1000000.0000",
  "max_daily_transaction": "5000000.0000",
  "max_client_daily_limit": "1000000.0000",
  "max_client_monthly_limit": "10000000.0000"
}
```

**Example response:**
```json
{
  "id": 4,
  "name": "SeniorAgent",
  "description": "Senior agent limits",
  "max_loan_approval_amount": "500000.0000",
  "max_single_transaction": "1000000.0000",
  "max_daily_transaction": "5000000.0000",
  "max_client_daily_limit": "1000000.0000",
  "max_client_monthly_limit": "10000000.0000"
}
```

| Status | Description |
|--------|-------------|
| 201 | Template created |
| 400 | Invalid input |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### GET /api/v3/clients/:id/limits

Retrieve the current transaction limits for a client.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Client ID |

**Example request:**
```
GET /api/v3/clients/7/limits
Authorization: Bearer <token>
```

**Example response:**
```json
{
  "id": 1,
  "client_id": 7,
  "daily_limit": "100000.0000",
  "monthly_limit": "1000000.0000",
  "transfer_limit": "50000.0000",
  "set_by_employee": 42
}
```

| Status | Description |
|--------|-------------|
| 200 | Client limits returned |
| 400 | Invalid client ID |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### PUT /api/v3/clients/:id/limits

Set or update transaction limits for a client. The employee's own limits constrain the maximum values they may assign (daily and monthly). Requires the authenticated employee's `max_client_daily_limit` >= requested `daily_limit` and `max_client_monthly_limit` >= requested `monthly_limit`.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Client ID |

**Request body:**
```json
{
  "daily_limit": "100000.0000",
  "monthly_limit": "1000000.0000",
  "transfer_limit": "50000.0000"
}
```

**Example response:**
```json
{
  "id": 1,
  "client_id": 7,
  "daily_limit": "100000.0000",
  "monthly_limit": "1000000.0000",
  "transfer_limit": "50000.0000",
  "set_by_employee": 42
}
```

| Status | Description |
|--------|-------------|
| 200 | Client limits updated |
| 400 | Invalid input or limit exceeds employee's authority |
| 401 | Unauthorized |
| 500 | Internal server error |

---

## 14. Bank Accounts

Bank account management endpoints allow administrators to manage internal bank-owned accounts used for fee collection and loan repayments. The bank must always maintain at least one RSD account and at least one foreign currency account.

**Authentication:** Employee token with `bank-accounts.manage` permission

---

### GET /api/v3/bank-accounts

List all bank-owned accounts.

**Authentication:** Employee token with `bank-accounts.manage` permission

**Response 200:**
```json
{
  "accounts": [
    {
      "id": 1,
      "account_number": "265-1234567890123-45",
      "account_name": "EX Banka RSD Account",
      "owner_id": 1000000000,
      "owner_name": "EX Banka",
      "balance": "0.0000",
      "available_balance": "0.0000",
      "currency_code": "RSD",
      "status": "active",
      "account_kind": "current",
      "account_type": "bank"
    }
  ]
}
```

**Response 401:** `{"error": "unauthorized"}`
**Response 500:** `{"error": "internal server error"}`

---

### GET /api/v3/bank-accounts/:id/activity

List the ledger activity (debits/credits) for a bank-owned account.

**Authentication:** Employee token with `bank-accounts.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Bank account ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default 1) |
| `page_size` | int | Page size (default 20, max 200) |

**Response 200:**
```json
{
  "entries": [
    {
      "id": 1,
      "entry_type": "credit",
      "amount": "100.00",
      "currency": "RSD",
      "balance_before": "0.00",
      "balance_after": "100.00",
      "description": "Transfer fee collection",
      "reference_id": "...",
      "reference_type": "transfer",
      "occurred_at": 1747000000
    }
  ],
  "total_count": 1
}
```

**Error Responses:**
- `400` — invalid id
- `403` — missing `bank_accounts.manage.any`
- `404` — account not found, or the id is not a bank account

---

### POST /api/v3/bank-accounts

Create a new bank-owned account.

**Authentication:** Employee token with `bank-accounts.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `currency_code` | string | Yes | ISO 4217 currency code (e.g., `RSD`, `EUR`, `USD`) |
| `account_kind` | string | Yes | Account kind: `current` or `foreign` |
| `account_name` | string | No | Human-readable name for the account |

**Example Request:**
```json
{
  "currency_code": "EUR",
  "account_kind": "foreign",
  "account_name": "EX Banka EUR Account"
}
```

**Response 201:**
```json
{
  "id": 2,
  "account_number": "265-9876543210987-12",
  "account_name": "EX Banka EUR Account",
  "owner_id": 1000000000,
  "owner_name": "EX Banka",
  "balance": "0.0000",
  "available_balance": "0.0000",
  "currency_code": "EUR",
  "status": "active",
  "account_kind": "foreign",
  "account_type": "bank"
}
```

**Response 400:** `{"error": "account_kind must be 'current' or 'foreign'"}`
**Response 401:** `{"error": "unauthorized"}`

---

### DELETE /api/v3/bank-accounts/:id

Delete a bank-owned account by ID.

**Authentication:** Employee token with `bank-accounts.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | integer | Bank account ID |

**Business rules:**
- The account must be a bank account (returns 400 if not).
- Deletion fails if it would leave the bank with zero RSD accounts.
- Deletion fails if it would leave the bank with zero foreign currency accounts.

**Response 200:**
```json
{
  "success": true,
  "message": "bank account deleted"
}
```

**Response 400:** `{"error": "cannot delete: bank must maintain at least one RSD account"}`
**Response 401:** `{"error": "unauthorized"}`
**Response 404:** `{"error": "bank account not found"}`

---

## 15. Notification Templates

Notification template management endpoints allow administrators to customize the subject and body text of notification messages (emails and push notifications).

Each notification template **type** has a fixed, code-defined set of `{{variable}}` placeholders it supports (the registry). Admins customize only the text; the set of supported variables and the template types themselves cannot be changed via the API. A customized template is stored as a DB override; if no override exists, the code-defined registry default is used. The discovery endpoint below lists every template type together with the `{{variables}}` it supports, so a frontend can show which placeholders are valid before saving.

Placeholder substitution syntax is `{{variable_name}}`. At send time, each `{{token}}` is replaced with the corresponding value from the publisher's data map; an unknown or absent token renders as an empty string.

The `channel` path/query value must be `email` or `push`.

**Authentication:** Employee token with `notifications.templates.manage` permission

---

### GET /api/v3/notification-templates

List all notification template types. This is the **discovery endpoint**: for every template type it returns the `{{variables}}` it supports (with descriptions and examples), the code-defined default subject/body, and the current (possibly customized) subject/body.

**Authentication:** Employee token with `notifications.templates.manage` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `channel` | string | Optional. Filter by channel: `email` or `push`. |

**Response 200:**
```json
{
  "templates": [
    {
      "type": "CONFIRMATION",
      "channel": "email",
      "description": "Sent to a client when an action is confirmed",
      "variables": [
        {
          "name": "first_name",
          "description": "Client's first name",
          "example": "Marko"
        }
      ],
      "default_subject": "Confirmation",
      "default_body": "Hello {{first_name}}, your action is confirmed.",
      "current_subject": "Confirmation",
      "current_body": "Hello {{first_name}}, your action is confirmed.",
      "is_customized": false
    }
  ]
}
```

**Response 400:** `{"error": "channel must be 'email' or 'push'"}`
**Response 401:** `{"error": "unauthorized"}`
**Response 403:** `{"error": "forbidden"}`

---

### GET /api/v3/notification-templates/:channel/:type

Return a single notification template.

**Authentication:** Employee token with `notifications.templates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `channel` | string | `email` or `push` |
| `type` | string | Template type (e.g., `CONFIRMATION`) |

**Response 200:** A single template object with the same shape as one element of the `templates` array above:
```json
{
  "type": "CONFIRMATION",
  "channel": "email",
  "description": "Sent to a client when an action is confirmed",
  "variables": [
    {
      "name": "first_name",
      "description": "Client's first name",
      "example": "Marko"
    }
  ],
  "default_subject": "Confirmation",
  "default_body": "Hello {{first_name}}, your action is confirmed.",
  "current_subject": "Confirmation",
  "current_body": "Hello {{first_name}}, your action is confirmed.",
  "is_customized": false
}
```

**Response 400:** `{"error": "channel must be 'email' or 'push'"}`
**Response 401:** `{"error": "unauthorized"}`
**Response 403:** `{"error": "forbidden"}`
**Response 404:** `{"error": "unknown template type"}`

---

### PUT /api/v3/notification-templates/:channel/:type

Customize a notification template's subject and body. The new text may only reference `{{variables}}` that the template type supports — referencing an unknown variable is rejected with `400`.

**Authentication:** Employee token with `notifications.templates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `channel` | string | `email` or `push` |
| `type` | string | Template type (e.g., `CONFIRMATION`) |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `subject` | string | Yes | Customized subject text. May contain `{{variable}}` placeholders. Must not be empty. |
| `body` | string | Yes | Customized body text. May contain `{{variable}}` placeholders. Must not be empty. |

**Example Request:** customizing `email/CONFIRMATION`:
```json
{
  "subject": "Hi {{first_name}}!",
  "body": "Welcome {{first_name}}."
}
```

**Response 200:** The updated template object (same shape as `GET /api/v3/notification-templates/:channel/:type`) with `is_customized: true` and the new `current_subject` / `current_body`:
```json
{
  "type": "CONFIRMATION",
  "channel": "email",
  "description": "Sent to a client when an action is confirmed",
  "variables": [
    {
      "name": "first_name",
      "description": "Client's first name",
      "example": "Marko"
    }
  ],
  "default_subject": "Confirmation",
  "default_body": "Hello {{first_name}}, your action is confirmed.",
  "current_subject": "Hi {{first_name}}!",
  "current_body": "Welcome {{first_name}}.",
  "is_customized": true
}
```

**Response 400:** `{"error": "unknown variable {{account_number}} for template type CONFIRMATION"}` — also returned if `subject` or `body` is empty.
**Response 401:** `{"error": "unauthorized"}`
**Response 403:** `{"error": "forbidden"}`
**Response 404:** `{"error": "unknown template type"}`

---

### DELETE /api/v3/notification-templates/:channel/:type

Revert a notification template to its code-defined default by removing the DB override.

**Authentication:** Employee token with `notifications.templates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `channel` | string | `email` or `push` |
| `type` | string | Template type (e.g., `CONFIRMATION`) |

**Response 200:** The template object with `is_customized: false` and `current_subject` / `current_body` back to the registry defaults:
```json
{
  "type": "CONFIRMATION",
  "channel": "email",
  "description": "Sent to a client when an action is confirmed",
  "variables": [
    {
      "name": "first_name",
      "description": "Client's first name",
      "example": "Marko"
    }
  ],
  "default_subject": "Confirmation",
  "default_body": "Hello {{first_name}}, your action is confirmed.",
  "current_subject": "Confirmation",
  "current_body": "Hello {{first_name}}, your action is confirmed.",
  "is_customized": false
}
```

**Response 400:** `{"error": "channel must be 'email' or 'push'"}`
**Response 401:** `{"error": "unauthorized"}`
**Response 403:** `{"error": "forbidden"}`
**Response 404:** `{"error": "unknown template type"}`

---

## 16. Transfer Fees

Configurable fee rules applied to payments and transfers. Multiple active fee rules can apply to the same transaction -- they stack additively. For example, a percentage fee AND a fixed fee can both apply to the same transaction.

Fee calculation is DB-backed: if the fee service is unavailable, the transaction is rejected. If no rules match (e.g., amount below threshold), zero fee is charged (not an error).

**Authentication:** Employee token with `fees.manage` permission

**Fee types:**
- `percentage` -- charged as a percentage of the transaction amount (e.g., `0.1` = 0.1%)
- `fixed` -- a flat fee regardless of amount

---

### GET /api/v3/fees

List all transfer fee rules.

**Authentication:** Employee JWT with `fees.manage` permission

**Response 200:**
```json
{
  "fees": [
    {
      "id": 1,
      "name": "Standard Payment Fee",
      "fee_type": "percentage",
      "fee_value": "0.1000",
      "min_amount": "1000.0000",
      "max_fee": "0.0000",
      "transaction_type": "all",
      "currency_code": "",
      "active": true
    }
  ]
}
```

**Response 401:** `{"error": "unauthorized"}`
**Response 500:** `{"error": "..."}`

---

### POST /api/v3/fees

Create a new transfer fee rule.

**Authentication:** Employee JWT with `fees.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Human-readable name (e.g., `"Standard Payment Fee"`) |
| `fee_type` | string | Yes | `"percentage"` or `"fixed"` |
| `fee_value` | string | Yes | Fee value as a decimal string. For percentage: `"0.1"` means 0.1%. For fixed: amount in the account's currency. |
| `min_amount` | string | No | Minimum transaction amount for the rule to apply. `"0"` or omitted means always applies. |
| `max_fee` | string | No | Maximum fee cap. `"0"` or omitted means uncapped. |
| `transaction_type` | string | Yes | `"payment"`, `"transfer"`, or `"all"` |
| `currency_code` | string | No | ISO 4217 currency code to restrict the rule (e.g., `"EUR"`). Empty string or omitted applies to all currencies. |

**Example Request:**
```json
{
  "name": "Standard Payment Fee",
  "fee_type": "percentage",
  "fee_value": "0.1",
  "min_amount": "1000.0000",
  "max_fee": "0.0000",
  "transaction_type": "all",
  "currency_code": ""
}
```

**Response 201:** Created fee rule object
**Response 400:** `{"error": "..."}`
**Response 401:** `{"error": "unauthorized"}`

---

### PUT /api/v3/fees/:id

Update an existing fee rule.

**Authentication:** Employee JWT with `fees.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | integer | Fee rule ID |

**Request Body** (all fields optional; omit to keep existing value):

| Field | Type | Description |
|---|---|---|
| `name` | string | New display name |
| `fee_type` | string | `"percentage"` or `"fixed"` |
| `fee_value` | string | New fee value as decimal string |
| `min_amount` | string | New minimum amount threshold |
| `max_fee` | string | New cap (set to `"0"` to remove cap) |
| `transaction_type` | string | `"payment"`, `"transfer"`, or `"all"` |
| `currency_code` | string | New currency restriction |
| `active` | bool | Set to `false` to deactivate, `true` to reactivate |

**Response 200:** Updated fee rule object
**Response 400:** `{"error": "invalid input"}`
**Response 401:** `{"error": "unauthorized"}`
**Response 404:** `{"error": "fee not found"}`

---

### DELETE /api/v3/fees/:id

Deactivate a fee rule. The rule is not deleted from the database -- it is soft-deactivated and will no longer apply to new transactions. It can be reactivated via `PUT /api/v3/fees/{id}` with `"active": true`.

**Authentication:** Employee JWT with `fees.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | integer | Fee rule ID |

**Response 200:**
```json
{
  "success": true,
  "message": "fee deactivated"
}
```

**Response 401:** `{"error": "unauthorized"}`
**Response 500:** `{"error": "..."}`

---

## 17. Interest Rate Tiers

Interest rate tier management for loan interest rate configuration. Each tier defines the fixed and variable base rates for a loan amount range.

**Authentication:** Employee token with `interest-rates.manage` permission

---

### GET /api/v3/interest-rate-tiers

List all interest rate tiers.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Response 200:**
```json
{
  "tiers": [
    {
      "id": 1,
      "amount_from": "0.0000",
      "amount_to": "500000.0000",
      "fixed_rate": "6.5000",
      "variable_base": "3.2500",
      "active": true,
      "created_at": "2026-03-13T10:00:00Z",
      "updated_at": "2026-03-13T10:00:00Z"
    }
  ]
}
```

| Status | Description |
|---|---|
| 200 | Tiers returned |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### POST /api/v3/interest-rate-tiers

Create a new interest rate tier.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `amount_from` | float64 | No | Lower bound of the loan amount range (must be >= 0) |
| `amount_to` | float64 | No | Upper bound of the loan amount range (must be >= 0) |
| `fixed_rate` | float64 | Yes | Fixed interest rate for this tier (must be >= 0) |
| `variable_base` | float64 | Yes | Variable base rate for this tier (must be >= 0) |

**Example Request:**
```json
{
  "amount_from": 0,
  "amount_to": 500000,
  "fixed_rate": 6.5,
  "variable_base": 3.25
}
```

**Response 201:**
```json
{
  "id": 1,
  "amount_from": "0.0000",
  "amount_to": "500000.0000",
  "fixed_rate": "6.5000",
  "variable_base": "3.2500",
  "active": true,
  "created_at": "2026-03-13T10:00:00Z",
  "updated_at": "2026-03-13T10:00:00Z"
}
```

| Status | Description |
|---|---|
| 201 | Tier created |
| 400 | Invalid input |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### PUT /api/v3/interest-rate-tiers/:id

Update an existing interest rate tier.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Tier ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `amount_from` | float64 | No | Lower bound of the loan amount range (must be >= 0) |
| `amount_to` | float64 | No | Upper bound of the loan amount range (must be >= 0) |
| `fixed_rate` | float64 | Yes | Fixed interest rate (must be >= 0) |
| `variable_base` | float64 | Yes | Variable base rate (must be >= 0) |

**Example Request:**
```json
{
  "amount_from": 0,
  "amount_to": 1000000,
  "fixed_rate": 7.0,
  "variable_base": 3.5
}
```

**Response 200:** Updated tier object

| Status | Description |
|---|---|
| 200 | Tier updated |
| 400 | Invalid input |
| 401 | Unauthorized |
| 404 | Tier not found |
| 500 | Internal server error |

---

### DELETE /api/v3/interest-rate-tiers/:id

Delete an interest rate tier.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Tier ID |

**Response 200:**
```json
{
  "success": true
}
```

| Status | Description |
|---|---|
| 200 | Tier deleted |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 404 | Tier not found |
| 500 | Internal server error |

---

### POST /api/v3/interest-rate-tiers/:id/apply

Apply a variable rate update to all active variable-rate loans whose amount falls within this tier's range. This recalculates the interest rate for affected loans based on the tier's current `variable_base` plus the bank margin.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Interest Rate Tier ID |

**Response 200:**
```json
{
  "affected_loans": 15
}
```

| Status | Description |
|---|---|
| 200 | Rate update applied; `affected_loans` indicates how many loans were updated |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 404 | Tier not found |
| 500 | Internal server error |

---

## 18. Bank Margins

Bank margin management for loan interest rate calculation. Each loan type has a configurable margin that is added to the variable base rate from the interest rate tier.

**Authentication:** Employee token with `interest-rates.manage` permission

---

### GET /api/v3/bank-margins

List all bank margins.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Response 200:**
```json
{
  "margins": [
    {
      "id": 1,
      "loan_type": "cash",
      "margin": "2.5000",
      "active": true,
      "created_at": "2026-03-13T10:00:00Z",
      "updated_at": "2026-03-13T10:00:00Z"
    },
    {
      "id": 2,
      "loan_type": "housing",
      "margin": "1.5000",
      "active": true,
      "created_at": "2026-03-13T10:00:00Z",
      "updated_at": "2026-03-13T10:00:00Z"
    }
  ]
}
```

| Status | Description |
|---|---|
| 200 | Margins returned |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### PUT /api/v3/bank-margins/:id

Update the margin for a specific loan type.

**Authentication:** Employee JWT with `interest-rates.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Margin ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `margin` | float64 | Yes | New margin value (must be >= 0) |

**Example Request:**
```json
{
  "margin": 3.0
}
```

**Response 200:**
```json
{
  "id": 1,
  "loan_type": "cash",
  "margin": "3.0000",
  "active": true,
  "created_at": "2026-03-13T10:00:00Z",
  "updated_at": "2026-03-20T14:00:00Z"
}
```

| Status | Description |
|---|---|
| 200 | Margin updated |
| 400 | Invalid input |
| 401 | Unauthorized |
| 404 | Margin not found |
| 500 | Internal server error |

---

## 19. Card Requests

Card requests allow clients to request a card for one of their accounts. Employees with `cards.approve` permission can approve or reject these requests.

---

### POST /api/v3/me/cards/requests

Client submits a request to obtain a card for one of their accounts.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `account_number` | string | Yes | Account number to attach the card to |
| `card_brand` | string | Yes | Card brand: `visa`, `mastercard`, `dinacard`, `amex` |
| `card_type` | string | No | Card type (default: `debit`) |
| `card_name` | string | No | Custom name for the card |

**Example Request:**
```json
{
  "account_number": "265-0000000001-00",
  "card_brand": "visa",
  "card_type": "debit",
  "card_name": "My Main Card"
}
```

**Response 201:**
```json
{
  "id": 1,
  "client_id": 42,
  "account_number": "265-0000000001-00",
  "card_brand": "visa",
  "card_type": "debit",
  "card_name": "My Main Card",
  "status": "pending",
  "reason": "",
  "approved_by": 0,
  "created_at": "2026-03-23T10:00:00Z",
  "updated_at": "2026-03-23T10:00:00Z"
}
```

| Status | Description |
|---|---|
| 201 | Card request created |
| 400 | Invalid input (bad brand, missing required fields) |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### GET /api/v3/me/cards/requests

Returns all card requests submitted by the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Page size (default: 20) |

**Response 200:**
```json
{
  "requests": [...],
  "total": 3
}
```

| Status | Description |
|---|---|
| 200 | List of card requests |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### GET /api/v3/me/cards/requests/:id

Get a single card request the authenticated client submitted. The `/me` self-version of the employee route `GET /api/v3/cards/requests/:id`, so a client can track one of their own requests without an employee permission. Ownership is enforced from the JWT — a request belonging to another client returns `404`.

**Authentication:** Client JWT (RequireClientToken)

**Response 200:** A single card request object (same shape as the items in `GET /api/v3/me/cards/requests`).

**Response 404:** Not found, or the request is not owned by the caller.

---

### GET /api/v3/cards/requests

Returns all card requests, optionally filtered by status.

**Authentication:** Employee JWT with `cards.approve` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `status` | string | Filter: `pending`, `approved`, `rejected` |
| `page` | int | Page number (default: 1) |
| `page_size` | int | Page size (default: 20) |

**Response 200:**
```json
{
  "requests": [...],
  "total": 10
}
```

| Status | Description |
|---|---|
| 200 | List of card requests |
| 400 | Invalid status filter |
| 401 | Unauthorized |
| 403 | Forbidden (missing permission) |
| 500 | Internal server error |

---

### GET /api/v3/cards/requests/:id

Returns a single card request by ID.

**Authentication:** Employee JWT with `cards.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card request ID |

**Response 200:** Card request object

| Status | Description |
|---|---|
| 200 | Card request found |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 403 | Forbidden |
| 404 | Card request not found |
| 500 | Internal server error |

---

### POST /api/v3/cards/requests/:id/approve

Employee approves a pending card request. This creates the actual card.

**Authentication:** Employee JWT with `cards.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card request ID |

**Response 200:**
```json
{
  "request": { "id": 1, "status": "approved", ... },
  "card": { "id": 10, "card_number": "**** **** **** 4242", ... }
}
```

| Status | Description |
|---|---|
| 200 | Request approved and card created |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 403 | Forbidden (missing permission) |
| 404 | Card request not found |
| 409 | Request already processed (not pending) |
| 500 | Internal server error |

---

### POST /api/v3/cards/requests/:id/reject

Employee rejects a pending card request with a reason.

**Authentication:** Employee JWT with `cards.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card request ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `reason` | string | Yes | Reason for rejection |

**Example Request:**
```json
{
  "reason": "Insufficient account history"
}
```

**Response 200:** Updated card request with status `rejected`

| Status | Description |
|---|---|
| 200 | Request rejected |
| 400 | Invalid input or ID |
| 401 | Unauthorized |
| 403 | Forbidden (missing permission) |
| 404 | Card request not found |
| 409 | Request already processed (not pending) |
| 500 | Internal server error |

---

## 20. Me (Self-Service)

The `/api/v3/me/*` route group provides self-service access for both employees and bank clients. All routes in this group are protected by `AnyAuthMiddleware`, which accepts any valid JWT (employee or client). Results are automatically scoped to the authenticated principal -- no `client_id` path segment is needed.

---

### GET /api/v3/me

Get the currently authenticated principal's profile.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200 (client):** Client profile object
**Response 200 (employee):** Employee profile object
**Response 401:** `{"error": {"code": "not_authenticated", "message": "..."}}`

---

### GET /api/v3/me/accounts

List accounts belonging to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "accounts": [ /* array of account objects */ ],
  "total": 3
}
```

---

### GET /api/v3/me/accounts/:id

Get a single account by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:** Account object. Key balance fields:

| Field | Type | Description |
|---|---|---|
| `balance` | string (decimal) | Total on-account balance |
| `reserved_balance` | string (decimal) | Amount held by active securities-order reservations (Phase 2) |
| `available_balance` | string (decimal) | Stored field; equals `balance - reserved_balance` after every reservation mutation |

**Response 404:** `{"error": {"code": "not_found", "message": "account not found"}}`

---

### GET /api/v3/me/accounts/:id/activity

Returns every balance-affecting ledger entry on the account in reverse-chronological order. Includes securities buys/sells (`reference_type=order`), tax collection (`reference_type=tax`), commission debits, transfers, payments, and interest. Ownership is enforced against the JWT.

**Authentication:** Any JWT (AnyAuthMiddleware) — account must belong to the caller

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `page` | int | 1 | Page number |
| `page_size` | int | 20 | Items per page (max 200) |

**Response 200:**
```json
{
  "entries": [
    {
      "id": 5021,
      "entry_type": "debit",
      "amount": "150.00",
      "currency": "RSD",
      "balance_before": "10000.00",
      "balance_after": "9850.00",
      "description": "Order fill — AAPL x 1",
      "reference_id": 1234,
      "reference_type": "order",
      "occurred_at": 1745832000
    }
  ],
  "total_count": 42
}
```

**Error Responses:**
- `400` — invalid id
- `401` — missing or invalid JWT
- `403` — account does not belong to the caller
- `404` — account not found

---

### GET /api/v3/me/cards

List all cards belonging to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ]
}
```

---

### GET /api/v3/me/cards/:id

Get a single card by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object
**Response 404:** `{"error": {"code": "not_found", "message": "card not found"}}`

---

### POST /api/v3/me/payments

Initiate a new payment. The authenticated principal must be the owner of the source account.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as [Section 7: Payments - POST /api/v3/me/payments](#post-apiv1mepayments).

**Response 201:** Payment object
**Response 400:** Validation error

---

### GET /api/v3/me/payments

List payments for the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "payments": [ /* array of payment objects */ ],
  "total": 12
}
```

---

### GET /api/v3/me/payments/:id

Get a single payment by ID, scoped to the authenticated principal. Ownership is derived from the JWT — if the payment does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Response 200:** Payment object
**Response 404:** `{"error": {"code": "not_found", "message": "payment not found"}}` (also returned when the payment exists but belongs to another user)

---

### POST /api/v3/me/payments/:id/execute

Execute a pending payment after verification.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | Verification challenge ID (must have status `verified`) |

**Response 200:** Executed payment object

---

### POST /api/v3/me/transfers

Initiate a currency transfer between accounts.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as [Section 8: Transfers - POST /api/v3/me/transfers](#post-apiv1metransfers).

**Response 201:** Transfer object

---

### GET /api/v3/me/transfers

List transfers for the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "transfers": [ /* array of transfer objects */ ],
  "total": 5
}
```

---

### GET /api/v3/me/transfers/:id

Get a single transfer by ID, scoped to the authenticated principal. Ownership is derived from the JWT — if the transfer does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Response 200:** Transfer object
**Response 404:** `{"error": {"code": "not_found", "message": "transfer not found"}}` (also returned when the transfer exists but belongs to another user)

---

### POST /api/v3/me/transfers/:id/execute

Execute a pending transfer after verification.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | Verification challenge ID (must have status `verified`) |

**Response 200:** Executed transfer object

---

### POST /api/v3/me/payment-recipients

Save a new payment recipient.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as [Section 9: Payment Recipients - POST /api/v3/me/payment-recipients](#post-apiv1mepayment-recipients).

**Response 201:** Recipient object

---

### GET /api/v3/me/payment-recipients

List all saved recipients for the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**
```json
{
  "recipients": [ /* array of recipient objects */ ]
}
```

---

### POST /api/v3/me/loan-requests

Submit a new loan application.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as [Section 11: Loans - POST /api/v3/me/loan-requests](#post-apiv1meloan-requests).

**Response 201:** Loan request object

---

### GET /api/v3/me/loan-requests

List all loan requests submitted by the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "requests": [ /* array of loan request objects */ ],
  "total": 2
}
```

---

### GET /api/v3/me/loan-requests/:id

Get a single loan request the authenticated client submitted. The `/me` self-version of the employee route `GET /api/v3/loan-requests/:id`, so a client can track one of their own requests without an employee permission. Ownership is enforced from the JWT — a request belonging to another client returns `404`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:** A single loan request object (same shape as the items in `GET /api/v3/me/loan-requests`).

**Response 404:** Not found, or the request is not owned by the caller.

---

### GET /api/v3/me/loans

List all loans belonging to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "loans": [ /* array of loan objects */ ],
  "total": 2
}
```

---

### GET /api/v3/me/loans/:id

Get a single loan by ID, scoped to the authenticated principal. Ownership is derived from the JWT — if the loan does not belong to the caller, responds with `404 not_found`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Loan object
**Response 404:** `{"error": {"code": "not_found", "message": "loan not found"}}` (also returned when the loan exists but belongs to another user)

---

### GET /api/v3/me/loans/:id/installments

Get all installment records for a loan belonging to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:**
```json
{
  "installments": [ /* array of installment objects */ ]
}
```

---

### POST /api/v3/me/orders

Create a new stock order. See [Section 26: Orders](#26-orders) for full request/response details.

---

### GET /api/v3/me/orders

List authenticated user's orders. See [Section 26: Orders](#26-orders) for full request/response details.

---

### GET /api/v3/me/orders/:id

Get a specific order. See [Section 26: Orders](#26-orders) for full request/response details.

---

### POST /api/v3/me/orders/:id/cancel

Cancel a pending order. See [Section 26: Orders](#26-orders) for full request/response details.

---

### GET /api/v3/me/portfolio

List authenticated user's holdings. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

Each securities position (`asset_type` `stock`/`option`/`future`) additionally carries `reserved_quantity` (shares locked by formed OTC option contracts) and `available_quantity` (`quantity - reserved_quantity`, free to trade/list). Both are `0`/omitted for fund positions. The `Holding` shape on `GET /api/v3/me/holdings`-style reads exposes the same two fields. (VERSION 4.3.0; additive — existing clients unaffected.)

---

### GET /api/v3/me/portfolio/summary

Get portfolio summary. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

---

### POST /api/v3/me/portfolio/:id/exercise

Exercise an options contract. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

---

### GET /api/v3/me/tax

Returns paginated capital gains tax records for the authenticated user. See [Section 31: Tax](#31-tax) for full request/response details.

---

## 21. Mobile Auth

Mobile device authentication for the EXBanka mobile app. These endpoints are public (no auth required).

---

### POST /api/v3/mobile/auth/request-activation

Request a 6-digit activation code. The code is delivered through **two** channels:
the activation **email**, and a persistent in-app **general notification**
(`type: "mobile_activation_requested"`) for the account's principal. Both the web
and mobile apps poll `GET /api/v3/me/notifications`, so an already-authenticated
session sees the code without opening the email. The notification is best-effort —
a publish failure never fails the request (the email still goes out).

**Authentication:** None

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | User's registered email address |

**Example Request:**
```json
{
  "email": "user@example.com"
}
```

**Response 200:**
```json
{
  "success": true,
  "message": "activation code sent to email"
}
```

| Status | Description |
|---|---|
| 200 | Activation code sent |
| 400 | Invalid email format |
| 404 | Email not found |

---

### POST /api/v3/mobile/auth/activate

Activate a mobile device with the emailed code. Returns device credentials.

**Authentication:** None

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | User's registered email |
| `code` | string | Yes | 6-digit activation code |
| `device_name` | string | Yes | User-friendly device name (e.g., "Luka's iPhone") |

**Example Request:**
```json
{
  "email": "user@example.com",
  "code": "482916",
  "device_name": "Luka's iPhone 16"
}
```

**Response 200:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs...",
  "device_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "device_secret": "f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4"
}
```

> **CRITICAL:** `device_secret` is returned only at activation. Store in iOS Keychain / Android Keystore immediately.

| Status | Description |
|---|---|
| 200 | Device activated |
| 400 | Invalid code format or missing fields |
| 404 | Email not found |
| 409 | Code invalid, expired, or max attempts exceeded |

---

### POST /api/v3/mobile/auth/refresh

Refresh mobile access token.

**Authentication:** None (uses refresh token in body)

**Headers:**

| Header | Required | Description |
|---|---|---|
| `X-Device-ID` | Yes | Device UUID from activation |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `refresh_token` | string | Yes | Current refresh token |

**Example Request:**
```json
{
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

**Response 200:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

| Status | Description |
|---|---|
| 200 | Token refreshed |
| 400 | Missing X-Device-ID header |
| 401 | Refresh token invalid, expired, or device deactivated |

---

## 22. Mobile Device Management

Manage the authenticated mobile device. Requires `MobileAuthMiddleware`.

---

### GET /api/v3/mobile/device

Get info about the current device.

**Authentication:** Mobile JWT + `X-Device-ID` header

**Response 200:**
```json
{
  "device_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "device_name": "Luka's iPhone 16",
  "status": "active",
  "activated_at": "2026-04-01T10:00:00Z",
  "last_seen_at": "2026-04-01T12:00:00Z"
}
```

---

### POST /api/v3/mobile/device/deactivate

Deactivate the current mobile device. The device will need to go through activation again to reconnect.

**Authentication:** Mobile JWT + `X-Device-ID` header

**Response 200:**
```json
{
  "success": true
}
```

---

### POST /api/v3/mobile/device/transfer

Deactivate the current device and send a new activation code to the specified email.

**Authentication:** Mobile JWT + `X-Device-ID` header

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Email to send the new activation code to |

**Example Request:**
```json
{
  "email": "user@example.com"
}
```

**Response 200:**
```json
{
  "success": true,
  "message": "device deactivated, activation code sent to email"
}
```

---

## 23. Mobile Device Settings

Biometric authentication settings for the mobile device. Requires `MobileAuthMiddleware` + `RequireDeviceSignature`.

---

### POST /api/v3/mobile/device/biometrics

Enable or disable biometric authentication for the current device.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | Yes | Whether biometric authentication is enabled |

**Example Request:**
```json
{
  "enabled": true
}
```

**Response 200:**
```json
{
  "success": true
}
```

| Status | Description |
|---|---|
| 200 | Biometrics setting updated |
| 400 | Invalid input |
| 401 | Unauthorized |

---

### GET /api/v3/mobile/device/biometrics

Get current biometric authentication status for the device.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Response 200:**
```json
{
  "enabled": true
}
```

| Status | Description |
|---|---|
| 200 | Biometric status returned |
| 401 | Unauthorized |

---

## 24. Verification

The verification service provides two-factor authentication for payments and transfers. Challenges expire after 5 minutes and allow a maximum of 3 attempts. Employees with `verification.skip` permission bypass verification entirely.

**Verification methods:**

| Method | Status | Description |
|---|---|---|
| `code_pull` | **Active** (default) | 6-digit code delivered to the client's mobile app; client types it into the browser |
| `email` | **Removed** | Eliminated -- all verification is via code_pull |
| `qr_scan` | **Not available** | Planned -- selecting this returns 400 |
| `number_match` | **Not available** | Planned -- selecting this returns 400 |

**Recommended usage order:**

1. `POST /api/v3/me/payments` or `POST /api/v3/me/transfers` -- creates the transaction in `pending_verification` status
2. `POST /api/v3/verifications` -- creates a verification challenge for the pending transaction
3. `GET /api/v3/verifications/:id/status` -- browser polls until `status = "verified"`
4. On mobile: `GET /api/v3/mobile/verifications/pending` -- mobile app polls for pending challenges
5. Client submits code via `POST /api/v3/verifications/:id/code` (browser) or `POST /api/v3/mobile/verifications/:challenge_id/submit` (mobile) or `POST /api/v3/mobile/verifications/:id/biometric` (biometric)
6. `POST /api/v3/me/payments/:id/execute` or `POST /api/v3/me/transfers/:id/execute` -- executes the transaction with the verified `challenge_id`

**VerificationChallenge model:**

| Field | Type | Description |
|---|---|---|
| `id` | uint64 | Challenge ID |
| `user_id` | uint64 | Owner of the challenge |
| `source_service` | string | `transaction`, `payment`, or `transfer` |
| `source_id` | uint64 | The payment/transfer ID |
| `method` | string | `code_pull` (active); `qr_scan`, `number_match` (planned) |
| `status` | string | `pending`, `verified`, `expired`, `failed` |
| `attempts` | int | Current attempt count (max 3) |
| `expires_at` | timestamp | Challenge expiry (5 minutes from creation) |
| `verified_at` | timestamp | When verification succeeded (nullable) |

---

### POST /api/v3/verifications

Create a new verification challenge for a pending transaction.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `source_service` | string | Yes | `transaction`, `payment`, or `transfer` |
| `source_id` | uint64 | Yes | The payment/transfer ID |
| `method` | string | No | `code_pull` only (default). `email`, `qr_scan`, `number_match` return 400. |

**Example Request:**
```json
{
  "source_service": "payment",
  "source_id": 456,
  "method": "code_pull"
}
```

**Response 200:**
```json
{
  "challenge_id": 123,
  "challenge_data": {},
  "expires_at": "2026-04-01T12:05:00Z"
}
```

`challenge_data` depends on method:
- **code_pull:** `{}` -- code is delivered to mobile app

| Status | Description |
|---|---|
| 200 | Challenge created |
| 400 | Invalid method or missing fields |

---

### GET /api/v3/verifications/:id/status

Poll the status of a verification challenge until `verified`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Challenge ID |

**Response 200:**
```json
{
  "status": "pending",
  "method": "code_pull",
  "verified_at": null,
  "expires_at": "2026-04-01T12:05:00Z"
}
```

Possible `status` values: `pending`, `verified`, `expired`, `failed`.

---

### POST /api/v3/verifications/:id/code

Submit a verification code from the browser (for `code_pull` method).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Challenge ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `code` | string | Yes | 6-digit verification code |

**Example Request:**
```json
{
  "code": "482916"
}
```

**Response 200:**
```json
{
  "success": true,
  "remaining_attempts": 2
}
```

| Status | Description |
|---|---|
| 200 | Code submission processed (check `success` field) |
| 400 | Invalid challenge ID or missing code |
| 404 | Challenge not found |
| 409 | Challenge expired or max attempts exceeded |

---

### GET /api/v3/mobile/verifications/pending

Poll for pending verification challenges delivered to this device.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Response 200:**
```json
{
  "items": [
    {
      "id": 1,
      "challenge_id": 123,
      "method": "code_pull",
      "display_data": { "code": "482916" },
      "expires_at": "2026-04-01T12:05:00Z"
    }
  ]
}
```

`display_data` depends on method:
- **code_pull:** `{ "code": "482916" }` -- display the code so the user can type it into the browser

---

### POST /api/v3/mobile/verifications/:id/ack

Acknowledge a mobile inbox item, marking it as delivered. Acknowledged items no longer appear in `GET /api/v3/mobile/verifications/pending`.

**Authentication:** MobileAuthMiddleware + RequireDeviceSignature

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Inbox item ID (from the `id` field in the pending items list) |

**Response 200:**
```json
{ "success": true }
```

| Status | Description |
|---|---|
| 200 | Item marked as delivered |
| 400 | Invalid item id |
| 403 | Non-mobile token |
| 404 | Item not found or already delivered |

---

### POST /api/v3/mobile/verifications/:id/submit

Submit a verification response from the mobile app.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Verification challenge ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `response` | string | Yes | The verification response (6-digit code for `code_pull`) |

**Example Request:**
```json
{
  "response": "482916"
}
```

**Response 200:**
```json
{
  "success": true,
  "remaining_attempts": 2
}
```

| Status | Description |
|---|---|
| 200 | Submission accepted (check `success` field) |
| 400 | Invalid challenge ID or missing response |
| 404 | Challenge not found |
| 409 | Challenge expired or max attempts exceeded |

---

### POST /api/v3/mobile/verifications/:id/biometric

**v1-only endpoint.** Verify a pending challenge using the device's biometric authentication. No request body is needed -- the device signature (passed via the `RequireDeviceSignature` middleware) IS the biometric proof of authentication.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature (MobileAuthMiddleware + RequireDeviceSignature)

**Prerequisites:** Biometrics must be enabled on the device via `POST /api/v3/mobile/device/biometrics`. If biometrics are not enabled, the endpoint returns 403.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Challenge ID |

**Request Body:** None required.

**Response 200:**
```json
{
  "success": true
}
```

If the biometric verification fails (e.g., challenge already verified or expired):
```json
{
  "success": false
}
```

| Status | Description |
|---|---|
| 200 | Biometric verification processed (check `success` field) |
| 400 | Invalid challenge ID |
| 403 | Biometrics not enabled on this device |
| 409 | Challenge expired or already verified |

---

### POST /api/v3/verify/:challenge_id

> **Not available.** This endpoint is for the `qr_scan` method which is not yet implemented.

QR code verification. The mobile app scans a QR code displayed in the browser, extracts the URL and token, and POSTs here.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `challenge_id` | int | Verification challenge ID |

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `token` | string | Yes | QR verification token (64-char hex) |

**Response 200:**
```json
{
  "success": true
}
```

| Status | Description |
|---|---|
| 200 | QR verification successful |
| 400 | Missing token or invalid challenge ID |
| 404 | Challenge not found |
| 409 | Token mismatch, challenge expired, or already verified |

---

## 25. Stock Exchanges

### GET /api/v3/stock-exchanges

List all stock exchanges with pagination.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `search` | string | Search term for filtering |

**Response 200:**
```json
{
  "exchanges": [
    {
      "id": 1,
      "name": "New York Stock Exchange",
      "acronym": "NYSE",
      "mic_code": "XNYS",
      "polity": "USA",
      "currency": "USD",
      "time_zone": "America/New_York",
      "open_time": "09:30",
      "close_time": "16:00",
      "pre_market_open": "07:00",
      "post_market_close": "20:00",
      "is_open": true
    }
  ],
  "total_count": 5
}
```

Each exchange object carries an `is_open` boolean indicating whether the
exchange is currently open for trading. It is `true` when global testing mode is
enabled, otherwise computed from the exchange's own trading hours (`open_time` /
`close_time` interpreted in `time_zone`) at request time. The field is always
present — a closed exchange reports `"is_open": false`. Purely additive; older
clients that ignore unknown fields are unaffected.

---

### GET /api/v3/stock-exchanges/:id

Get a specific stock exchange by ID.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Exchange ID |

**Response 200:** Stock exchange object, including the `is_open` boolean
described above (true under testing mode, otherwise derived from the exchange's
trading hours; always present, even when false).

---

### POST /api/v3/stock-exchanges/testing-mode

Enable or disable testing mode for stock exchanges.

**Authentication:** Employee JWT + `exchanges.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | Yes | Whether testing mode is enabled |

**Response 200:**
```json
{
  "testing_mode": true
}
```

---

### GET /api/v3/stock-exchanges/testing-mode

Get current testing mode status.

**Authentication:** Employee JWT + `exchanges.manage` permission

**Response 200:**
```json
{
  "testing_mode": false
}
```

---

## 26. Securities

All securities endpoints require any valid JWT (AnyAuthMiddleware).

### GET /api/v3/securities/stocks

List stocks with filtering and sorting.

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `search` | string | Name/symbol search |
| `exchange_acronym` | string | Filter by exchange |
| `min_price` | string | Minimum price filter |
| `max_price` | string | Maximum price filter |
| `min_volume` | int | Minimum volume filter |
| `max_volume` | int | Maximum volume filter |
| `sort_by` | string | `price`, `volume`, `change`, or `margin` |
| `sort_order` | string | `asc` (default) or `desc` |

**Response 200:**
```json
{
  "stocks": [ ],
  "total_count": 100
}
```

---

### GET /api/v3/securities/stocks/:id

Get details of a specific stock.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Stock ID |

---

### GET /api/v3/securities/stocks/:id/history

Get stock price history.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Stock ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `period` | string | `day`, `week`, `month` (default), `year`, `5y`, `all` |
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 30) |

**Response 200:**
```json
{
  "history": [ ],
  "total_count": 30
}
```

---

### GET /api/v3/securities/futures

List futures contracts.

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `search` | string | Search term |
| `exchange_acronym` | string | Filter by exchange |
| `min_price` | string | Minimum price |
| `max_price` | string | Maximum price |
| `settlement_date_from` | string | ISO date |
| `settlement_date_to` | string | ISO date |
| `sort_by` | string | Sort field |
| `sort_order` | string | `asc` (default) or `desc` |

**Response 200:**
```json
{
  "futures": [ ],
  "total_count": 50
}
```

---

### GET /api/v3/securities/futures/:id

Get a specific futures contract.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Futures contract ID |

---

### GET /api/v3/securities/futures/:id/history

Get futures price history. Same query parameters as stocks history.

---

### GET /api/v3/securities/forex

List forex currency pairs.

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `search` | string | Search term |
| `base_currency` | string | ISO currency code |
| `quote_currency` | string | ISO currency code |
| `liquidity` | string | `high`, `medium`, or `low` |
| `sort_by` | string | Sort field |
| `sort_order` | string | `asc` (default) or `desc` |

**Response 200:**
```json
{
  "forex_pairs": [ ],
  "total_count": 20
}
```

---

### GET /api/v3/securities/forex/:id

Get a specific forex pair.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Forex pair ID |

---

### GET /api/v3/securities/forex/:id/history

Get forex pair price history. Same query parameters as stocks history.

---

### GET /api/v3/securities/options

List options contracts for a stock.

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `stock_id` | int | **Required.** Parent stock ID |
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `option_type` | string | `call` or `put` |
| `settlement_date` | string | Filter by settlement date |
| `min_strike` | string | Minimum strike price |
| `max_strike` | string | Maximum strike price |

**Response 200:**
```json
{
  "options": [ ],
  "total_count": 15
}
```

---

### GET /api/v3/securities/options/:id

Get a specific options contract.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Option ID |

---

### GET /api/v3/securities/candles

Get OHLCV candle chart data for a security from InfluxDB time-series storage.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `listing_id` | uint64 | Yes | Security listing ID |
| `interval` | string | No | Candle interval: `1m`, `5m`, `15m`, `1h` (default), `4h`, `1d` |
| `from` | string | Yes | Start time (RFC3339 format) |
| `to` | string | Yes | End time (RFC3339 format) |

**Example Request:**
```
GET /api/v3/securities/candles?listing_id=42&interval=1h&from=2026-04-01T00:00:00Z&to=2026-04-02T00:00:00Z
```

**Response 200:**
```json
{
  "candles": [
    {
      "time": "2026-04-01T10:00:00Z",
      "open": "150.00",
      "high": "152.50",
      "low": "149.00",
      "close": "151.75",
      "volume": 12500
    }
  ],
  "count": 24
}
```

| Status | Description |
|---|---|
| 200 | Candle data returned |
| 400 | Missing or invalid query parameters |
| 401 | Unauthorized |

---

## 27. Orders

### POST /api/v3/me/orders

Create a new stock / futures / forex / option order. Ownership is derived from the JWT — the `account_id` (and `base_account_id`, when provided) must belong to the JWT caller. Mismatches return `403 forbidden`.

An **employee** may also pass `on_behalf_of_fund_id` here to place the order against an investment fund they manage. For fund orders the caller-account ownership check is skipped and `account_id` is **optional** (auto-resolves to the fund's RSD account); a **buy** credits `fund_holdings`, a **sell** draws down `fund_holdings`. A non-employee passing `on_behalf_of_fund_id` is rejected with `403 forbidden`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `security_type` | string | Optional | `stock`, `futures`, `forex`, or `option`. Required on the client for forex-specific gateway validation; stock-service otherwise derives it from the listing. |
| `listing_id` | uint64 | Yes (buy) | Listing ID (required for buy orders) |
| `holding_id` | uint64 | Yes (sell) | Holding ID (required for sell orders) |
| `direction` | string | Yes | `buy` or `sell` (forex orders MUST be `buy`) |
| `order_type` | string | Yes | `market`, `limit`, `stop`, or `stop_limit` |
| `quantity` | int64 | Yes | Must be positive |
| `limit_value` | string | Conditional | Required for `limit` or `stop_limit` orders |
| `stop_value` | string | Conditional | Required for `stop` or `stop_limit` orders |
| `all_or_none` | boolean | No | Default: false |
| `margin` | boolean | No | Default: false |
| `account_id` | uint64 | Conditional | Account to debit; must belong to the JWT caller. Required for buy. **Optional for fund orders** (`on_behalf_of_fund_id` set) — auto-resolves to the fund's RSD account. |
| `on_behalf_of_fund_id` | uint64 | No | Employee-only. Place the order against an investment fund the caller manages. `account_id` auto-resolves; fills target `fund_holdings`. |
| `base_account_id` | uint64 | Yes (forex) | Required when `security_type=forex`. Account that will be credited with the base currency on fill. MUST differ from `account_id`; MUST be owned by the JWT caller. Ignored for non-forex orders. |

**Example Request (buy market order):**
```json
{
  "listing_id": 42,
  "direction": "buy",
  "order_type": "market",
  "quantity": 10,
  "account_id": 1
}
```

**Example Request (forex buy):**
```json
{
  "security_type": "forex",
  "listing_id": 501,
  "direction": "buy",
  "order_type": "market",
  "quantity": 1000,
  "account_id": 3,
  "base_account_id": 7
}
```

**Response 201:** Order object.

| Status | Description |
|---|---|
| 201 | Order created |
| 400 | Validation error — including: `forex orders must be direction=buy`, `forex orders require base_account_id`, `base_account_id must differ from account_id` |
| 403 | Account (or base account) does not belong to JWT caller |
| 409 | Business rule violation (e.g., insufficient available balance) |

---

### GET /api/v3/me/orders

List authenticated user's orders.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `status` | string | Filter by order status |
| `direction` | string | Filter by direction (`buy`/`sell`) |
| `order_type` | string | Filter by order type |

**Response 200:**
```json
{
  "orders": [ ],
  "total_count": 25
}
```

---

### GET /api/v3/me/orders/:id

Get a specific order.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Order object. Phase 2 reservation/audit fields:

| Field | Type | Description |
|---|---|---|
| `reservation_amount` | string (decimal, nullable) | Amount reserved on `reservation_account_id` at placement (buy orders only) |
| `reservation_currency` | string (3, nullable) | Currency of the reservation |
| `reservation_account_id` | uint64 (nullable) | Account whose funds are reserved |
| `base_account_id` | uint64 (nullable) | Forex only: base-currency account credited on fill |
| `placement_rate` | string (decimal, nullable) | FX rate snapshot at placement for cross-currency orders |
| `saga_id` | string (UUID) | Links the order to its saga_logs rows |

Nested `order_transactions` (when returned) include cross-currency audit fields: `native_amount`, `native_currency`, `converted_amount`, `account_currency`, `fx_rate` (all nullable; populated for cross-currency fills).

**Response 404:** `{"error": {"code": "not_found", "message": "order not found"}}`

---

### POST /api/v3/me/orders/:id/cancel

Cancel a pending order.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

---

### GET /api/v3/orders

List all orders (admin/supervisor view for approval).

**Authentication:** Employee JWT + `orders.approve` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `status` | string | Filter by status |
| `agent_email` | string | Filter by agent |
| `direction` | string | Filter by direction |
| `order_type` | string | Filter by order type |

**Response 200:**
```json
{
  "orders": [ ],
  "total_count": 50
}
```

---

### POST /api/v3/orders/:id/approve

Approve a pending order.

**Authentication:** Employee JWT + `orders.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

---

### POST /api/v3/orders/:id/reject

Reject a pending order that requires supervisor approval. Renamed from `/decline` in the v3 route standardization (2026-04-28) for verb consistency.

**Authentication:** Employee JWT + `orders.cancel.all` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing `orders.cancel.all`
- `404` — order not found
- `409` — order already in a terminal state

---

### POST /api/v3/orders

Place a stock/futures/forex/option order on behalf of **either** a named client **or** an investment fund. Supply exactly one of `client_id` or `on_behalf_of_fund_id`:

- **On behalf of a client** (`client_id`): the gateway verifies that `account_id` (and `base_account_id`, when present) belongs to `client_id` before forwarding to stock-service.
- **On behalf of a fund** (`on_behalf_of_fund_id`): the order targets the fund's RSD account, not a client account — the client-ownership check is skipped at the gateway. `account_id` is **OPTIONAL**: omit it and it auto-resolves to the fund's RSD account; if supplied it must equal that account. stock-service re-validates that the acting employee is the fund's manager and binds the account to the fund. A **buy** fill lands in `fund_holdings`; a **sell** draws down `fund_holdings` (the fund's portfolio) and credits proceeds to the fund's RSD account — mirroring `POST /api/v3/me/orders` with `on_behalf_of_fund_id`.

The order is recorded with `acting_employee_id` set to the caller's employee ID.

**Authentication:** Employee JWT + `orders.place-on-behalf` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Conditional | Client for whom the order is placed. Required unless `on_behalf_of_fund_id` is set. Mutually exclusive with `on_behalf_of_fund_id`. |
| `on_behalf_of_fund_id` | uint64 | Conditional | Investment fund for which the order is placed. Required unless `client_id` is set. Mutually exclusive with `client_id`. Acting employee must be the fund's manager. |
| `account_id` | uint64 | Conditional | Account to debit/credit. For client orders: **required**, must belong to `client_id`. For fund orders: **optional** — auto-resolves to the fund's RSD account when omitted; if supplied must equal it. |
| `security_type` | string | Optional | `stock`, `futures`, `forex`, or `option`. Required for forex-specific gateway validation. |
| `listing_id` | uint64 | Yes | Listing ID (the execution venue) — required for both buy and sell |
| `holding_id` | uint64 | No | Optional. Holdings aggregate per (owner, security); the sell venue is named by `listing_id`. |
| `direction` | string | Yes | `buy` or `sell` (forex orders MUST be `buy`) |
| `order_type` | string | Yes | `market`, `limit`, `stop`, or `stop_limit` |
| `quantity` | int64 | Yes | Must be positive |
| `limit_value` | string | Conditional | Required for `limit` or `stop_limit` orders |
| `stop_value` | string | Conditional | Required for `stop` or `stop_limit` orders |
| `all_or_none` | boolean | No | Default: false |
| `margin` | boolean | No | Default: false |
| `base_account_id` | uint64 | Yes (forex) | Required when `security_type=forex`. For client orders must belong to `client_id` and differ from `account_id`. |

**Example Request (on behalf of a client):**
```json
{
  "client_id": 5,
  "account_id": 12,
  "listing_id": 42,
  "direction": "buy",
  "order_type": "market",
  "quantity": 10
}
```

**Example Request (on behalf of a fund — account_id auto-resolves, sell from the fund's portfolio):**
```json
{
  "on_behalf_of_fund_id": 9,
  "listing_id": 42,
  "direction": "sell",
  "order_type": "market",
  "quantity": 10
}
```
`account_id` is omitted and auto-resolves to fund #9's RSD account. The 10 shares are drawn from the fund's `fund_holdings` position and the proceeds credit the fund's RSD account.

**Response 201:** Order object.

| Status | Description |
|---|---|
| 201 | Order created |
| 400 | Validation error — including forex direction/`base_account_id` mismatches, or supplying neither/both of `client_id` and `on_behalf_of_fund_id` |
| 403 | Account (or base account) does not belong to the specified client; or acting employee is not the fund's manager (fund orders, enforced by stock-service) |
| 403 | Missing `orders.place-on-behalf` permission |

---

### POST /api/v3/options/:option_id/orders

Place an order on a market option contract by its `option_id` (the gateway resolves the underlying `listing_id` and forwards to the standard order pipeline).

**Authentication:** Any JWT + one of `otc.trade.accept`, `otc.trade.exercise`, or `securities.trade`. Identity middleware: `OwnerIsBankIfEmployee` (employee → bank ownership; client → self).

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `option_id` | uint64 | Option contract ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `direction` | string | Yes | `buy` or `sell` |
| `order_type` | string | Yes | `market`, `limit`, `stop`, or `stop_limit` |
| `quantity` | int64 | Yes | Positive units |
| `limit_value` | string (decimal) | Conditional | Required when `order_type` is `limit` or `stop_limit` |
| `stop_value` | string (decimal) | Conditional | Required when `order_type` is `stop` or `stop_limit` |
| `all_or_none` | bool | No | All-or-none flag |
| `margin` | bool | No | Margin flag |
| `account_id` | uint64 | Yes | Account funding the order |
| `holding_id` | uint64 | No | Existing holding to consume on a sell |

**Response 201:** Order object (same shape as `POST /api/v3/me/orders`).

**Error Responses:**
- `400` — invalid `option_id`, missing required field, or order-type/value mismatch
- `409` — option not tradeable (no resolvable `listing_id`)

---

### POST /api/v3/options/:option_id/exercise

Exercise an option by `option_id`. If `holding_id` is omitted, the backend auto-resolves the caller's oldest long-option holding for that option.

**Authentication:** Any JWT + one of `otc.trade.accept`, `otc.trade.exercise`, or `securities.trade`. Identity middleware: `OwnerIsBankIfEmployee`.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `option_id` | uint64 | Option contract ID |

**Request Body (optional):**

| Field | Type | Required | Description |
|---|---|---|---|
| `holding_id` | uint64 | No | Holding to consume; auto-resolved when omitted |
| `on_behalf_of_fund_id` | uint64 | No | *E2, Plan E.* When non-zero, exercises the contract on behalf of an investment fund. The acting employee must be the fund's manager. Acquired shares land in `fund_holdings` instead of personal holdings. Only valid when the underlying `OptionContract` was itself accepted on behalf of a fund (i.e. its `on_behalf_of_fund_id` is set). |

**Response 200:** Exercise result (holding update + ledger entries).

**Error Responses:**
- `400` — invalid `option_id`
- `403` — `on_behalf_of_fund_id` set but acting employee is not the fund's manager
- `404` — option / holding not found

---

## 28. Portfolio

### GET /api/v3/me/portfolio

Returns the unified grouped portfolio: separate `securities` and `funds` groups, full P/L totals, and per-position `dividends_received_rsd` / `fund_status` fields. See [Section 48.1](#481-my-portfolio-client-or-bank) for the canonical response shape.

**Authentication:** Any JWT (AnyAuthMiddleware)
- Client principal → returns caller's own portfolio.
- Employee principal → returns the bank's portfolio (identity rule: `OwnerIsBankIfEmployee`).

**Response 200:** See [§48.1](#481-my-portfolio-client-or-bank) for the full response shape (grouped `securities` + `funds` with P/L totals and dividend fields).

### GET /api/v3/me/holdings/{id}/transactions

Returns the OrderTransactions that contributed to a holding — per-purchase price, native vs account currency, FX rate, commission, and which account was used. Use this after fetching `/me/portfolio` when the UI needs per-trade breakdown.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Holding ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `direction` | string | `buy` or `sell`; empty returns both |
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |

**Response 200:**

```json
{
  "transactions": [
    {
      "id": 42,
      "order_id": 7,
      "executed_at": "2026-04-20T09:15:00Z",
      "direction": "buy",
      "quantity": 5,
      "price_per_unit": "155.0000",
      "native_amount": "775.0000",
      "native_currency": "USD",
      "converted_amount": "85050.0000",
      "account_currency": "RSD",
      "fx_rate": "109.7419",
      "commission": "1.9400",
      "account_id": 42,
      "ticker": "AAPL"
    }
  ],
  "total_count": 3
}
```

**Responses:**
- `200 OK` — transactions list
- `400 validation_error` — `direction` not one of `buy`/`sell`
- `401 unauthorized`
- `404 not_found` — holding does not exist or does not belong to caller

---

### GET /api/v3/me/portfolio/summary

Get portfolio summary (total value, gains/losses, allocation).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:** Portfolio summary object.

---

### POST /api/v3/me/portfolio/:id/exercise

Exercise an options contract.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Holding ID (must be an option) |

**Response 200:** Exercise result object.

---

## 29. OTC Stocks Marketplace (REMOVED 2026-06-11)

The in-bank OTC stocks surface (`/api/v3/otc/stocks/...`, `/api/v3/me/otc/stocks/...`, `make-public`, `Holding.public_quantity`) was **removed** — the frontend's "market tab" was retired. Option offers (`/api/v3/otc/options/...`, [Section 47](#47-otc-marketplace)) now serve as the cross-bank "stock" inventory on `/public-stock`.

---

## 30. OTC Option Contracts

The OTC options surface — listings, per-bidder parallel negotiation chains, accept/contract-formation, and unified local + cross-bank discovery — lives under `/api/v3/otc/options/...`, `/api/v3/me/otc/options/...`, and `/api/v3/otc/contracts/...`. See [Section 47.2](#472-options-marketplace--parallel-negotiation-chains) for the full route documentation. Ratings live in [Section 42](#42-otc-trader-ratings-celina-3) and negotiation history in [Section 41](#41-otc-negotiation-history-celina-3).

The option-contract **list** (`GET /api/v3/me/otc/contracts`), **detail** (`GET /api/v3/otc/contracts/:id`), and **exercise** (`POST /api/v3/otc/contracts/:id/exercise`) routes serve LOCAL (intra-bank) and REMOTE (cross-bank) contracts uniformly — documented below.

#### GET /api/v3/me/otc/contracts (unified local + remote)

Returns the caller's formed option contracts — both LOCAL (intra-bank
`OptionContract` rows) and REMOTE (cross-bank `peer_option_contracts` mirror
rows where the caller is a party) — merged into one `contracts` array. Each
item carries provenance (`kind` / `routing_number` / `bank_code`) plus
`me_owner`.

**Authentication:** Any JWT + `ResolveIdentity` (AnyAuth — clients and employees accepted)

**Query Parameters:** `role` (`buyer`|`seller`|`either`, default `either`), `page` (default 1), `page_size` (default 20).

**`OptionContractResponse` shape:** in addition to the existing fields, every item now carries:

| Field | Type | Description |
|---|---|---|
| `kind` | string | `local` (intra-bank contract) or `remote` (cross-bank peer contract) |
| `routing_number` | int64 | Owning/hosting bank's routing. For `local`: our own routing. For `remote`: the COUNTERPARTY/peer bank's routing (the side we do NOT host). |
| `bank_code` | string | Owning/peer bank's code, matching `routing_number`. For `remote` this is the counterparty routing formatted as a string (the peer-contract mirror stores no separate bank-code field). |
| `me_owner` | bool | `true` ONLY when the caller is the contract's **buyer/holder** — a formed option is the buyer's owned asset, so the seller/writer is `false`. (DIFFERENT from offers/negotiations, where the poster/seller is the owner.) For `remote`: `true` iff the row's `direction == "CREDIT"` (this bank holds the buyer side). |

For `remote` items, `id` is the **local surrogate primary key** of this bank's
peer-contract mirror row (so callers correlate within this bank's id
namespace), and the terms (`stock_ticker`, `quantity`, `strike_price`,
`strike_currency`, `settlement_date`, `status`) are projected from the mirrored
cross-bank option. The `buyer` / `seller` `PartyRef`s carry the SI-TX
participant id as `display_name` plus the side's routing number as `bank_code`.

**Paging note:** `page`/`page_size` paginate the LOCAL set; REMOTE contracts
are appended in full after the local page (never silently truncated). `total`
reflects the local total only.

Remote contracts appear exclusively in the unified `contracts[]` with `kind="remote"` (there is no separate `peer_contracts[]` / `peer_total` — see [ROUTE-CHANGES.md](./ROUTE-CHANGES.md)).

**Note:** the remote merge is gated to **client** principals (cross-bank
participant ids are `client-<N>`); an employee acting as the bank skips the
remote merge and gets local contracts only.

#### GET /api/v3/otc/contracts/:id (unified local + remote)

Resolves a single option contract by id. A LOCAL `OptionContract` is returned
with `kind="local"`, own routing/bank-code provenance, and `me_owner` = (caller
is the buyer/holder). When the id is not a local contract, it falls back to the
cross-bank `peer_option_contracts` mirror (resolved by surrogate id) and returns
a `kind="remote"` projection with `me_owner` = (`direction == "CREDIT"`).

**Authentication:** Any JWT + `ResolveIdentity` (AnyAuth).

**Response 403:** Caller is neither the buyer nor the seller of a LOCAL contract.

**Response 404:** Neither a local nor a remote contract with that id exists. (A non-NotFound error from the remote mirror lookup surfaces as 500, never masked as 404.)

---

#### POST /api/v3/otc/contracts/:id/exercise (unified local + cross-bank)

Exercise an option contract by id. The dispatch (LOCAL exercise saga vs cross-bank
SI-TX exercise) is decided **inside stock-service** from the contract's routing,
so the frontend uses this **one** route regardless of kind.

- **LOCAL contract** (routing == own): runs the local exercise saga.
  Strike money moves buyer→seller, the reserved seller shares are consumed and
  credited to the buyer's holding. Accounts come from the persisted contract;
  `buyer_account_number` is ignored.
- **REMOTE contract** (a peer-hosted contract this bank holds the BUYER side of):
  runs the cross-bank SI-TX option-exercise flow. **Only the buyer/holder** may
  exercise (the writer/seller side and non-parties get `404` — existence must not
  leak). Supply `buyer_account_number` — the buyer's currency account that pays
  the strike; this is the only client-supplied resource on the money path, so the
  gateway gates it before forwarding (`403` on mismatch), authoritatively for ALL
  principals:
  - **client caller** → the account must be owned by that client;
  - **employee acting AS the bank** (no `on_behalf_of_client_id`) → the account
    must be a **BANK** account — a bank exercise pays its strike only from a bank
    account, never from a client's account of the matching currency;
  - **employee on behalf of a client** → the account must be owned by that client.

  stock-service additionally re-asserts the same predicate (bank buyer → bank
  account; client buyer → that client's account; active + strike-currency match)
  before dispatch, as defense-in-depth. The contract terms + counterparty come
  from the persisted remote row.

**Authentication:** Any JWT + one of `otc.trade.accept`, `securities.trade`. Identity middleware: `OwnerIsBankIfEmployee`.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Contract id (local surrogate id; serves both local and remote rows) |

**Request Body (optional):**

| Field | Type | Required | Description |
|---|---|---|---|
| `buyer_account_number` | string | Cross-bank only | The buyer's currency account that pays the strike. Required for a cross-bank (remote) contract; ignored for a local contract. The caller must be entitled to it per the gate above — a client must own it, a bank-acting employee must bind a BANK account, an on-behalf employee must bind that client's account (else `403`). |
| `on_behalf_of_client_id` | uint64 | No | Employee acting on behalf of a client. |
| `on_behalf_of_fund_id` | uint64 | No | *E2, Plan E.* When non-zero, exercises on behalf of an investment fund (local path). The acting employee must be the fund's manager; acquired shares land in `fund_holdings`. |

**Response 201:** Exercise result. For a cross-bank exercise the cross-bank
transaction id rides in `saga_id` (the correlation handle for polling
`GET /api/v3/me/otc/transactions/:txid/status`) and `status` reflects the SI-TX
dispatch state (e.g. `pending`).

**Error Responses:**
- `400` — invalid `id`, or a remote contract exercised without `buyer_account_number`
- `403` — `on_behalf_of_fund_id` set but acting employee is not the fund's manager; or the supplied strike account is not one the caller is entitled to (client not the owner / bank-acting employee binding a non-bank account / on-behalf employee binding a non-matching account)
- `404` — contract not found, or the caller is not the buyer/holder of a remote contract
- `409` — business rule (e.g. contract not active/expired, insufficient funds on the cross-bank strike)

---

### GET /api/v3/me/otc/transactions/:txid/status

Status of a **cross-bank** OTC trade's underlying SI-TX transaction, resolved via `PeerTxService.GetTxStatus`. The `:txid` accepts either id a client may hold:
- the bare idem returned in a dispatch's `poll_url`; or
- a composite id of the form `"<peerCode>:<idem>"` (e.g. obtained from the `crossbank_tx_id` column stored internally on `peer_option_contracts`).

Note: the unified `OptionContractResponse` items in `contracts[]` (including `kind=remote` entries) do **not** expose a `crossbank_tx_id` field — that field existed only on the now-removed `peer_contracts[]` / `PeerOptionContractResponse`. The composite tx id can be obtained from the dispatch flow's `poll_url` or from a bank-internal lookup; it is not surfaced in the `GET /api/v3/me/otc/contracts` response.

The composite form is split into `(caller_peer_bank_code, transaction_id)` so the status resolves on **both** banks — the dispatching (sender) bank via its outbound row, the receiving bank via its inbound idempotence record. The id is only known to the trade's parties, so holding it authorizes reading its status.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**
```json
{ "transaction_id": "222:aaaa-...-7777", "status": "committed", "role": "sender", "last_action_at": "2026-05-30T00:00:00Z", "last_error": "" }
```

> Protocol note: this is a local, client-facing read endpoint. It does **not** touch the (frozen, multi-team) cross-bank SI-TX protocol — the transaction id is already persisted locally on `peer_option_contracts.crossbank_tx_id` and the status is read via the existing `GetTxStatus` RPC.

---

## 31. Investment Funds (Celina 4)

Supervisor-managed investment funds (Specification §24). Clients and the bank take positions in funds via invest/redeem; supervisors manage the catalog and place on-behalf-of-fund orders. Each fund has one bank-owned RSD account that holds its cash; positions and contributions are tracked in `client_fund_positions` and `fund_contributions`.

**Permissions used:**
- `funds.manage` — create / update funds (supervisor / admin)
- `funds.bank-position-read` — read bank's own positions and actuary performance

**Identity middleware:** invest/redeem use `OwnerIsBankIfEmployee` — employees act as the bank, clients act as themselves.

---

### POST /api/v3/investment-funds

Create a new investment fund. Provisions a bank-owned RSD account dedicated to the fund.

**Authentication:** Employee JWT + `funds.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Unique fund name |
| `description` | string | No | Short investment-strategy description |
| `minimum_contribution_rsd` | string (decimal) | No | Smallest allowed invest amount in RSD (default `0`) |
| `dividend_mode` | string | No | `payout` (default) credits received stock dividends to the fund's cash; `reinvest` auto-buys more of the dividend-paying stock (DRIP). Also settable via `PUT /api/v3/investment-funds/{id}`. Surfaced as `dividend_mode` on every fund response. (SP4) |

**Example Request:**
```json
{
  "name": "Alpha Growth Fund",
  "description": "IT-sector focus",
  "minimum_contribution_rsd": "1000.00",
  "dividend_mode": "reinvest"
}
```

**Response 201:**
```json
{
  "fund": {
    "id": 101,
    "name": "Alpha Growth Fund",
    "description": "IT-sector focus",
    "minimum_contribution_rsd": "1000.00",
    "manager_employee_id": 25,
    "rsd_account_id": 9001,
    "active": true,
    "created_at": "2026-04-28T15:00:00Z"
  }
}
```

**Error Responses:**
- `400` — missing `name`
- `403` — missing `funds.manage`
- `409` — name already taken

---

### GET /api/v3/investment-funds

List investment funds (Discovery page).

**Authentication:** Any JWT

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `page` | int | 1 | Page number |
| `page_size` | int | 20 | Items per page |
| `search` | string | — | Case-insensitive substring on `name` |
| `active_only` | bool | false | When `true`, hide inactive funds |
| `sort_by` | string | — | `name` \| `value` \| `profit` \| `annualized_return` \| `volatility` \| `reward_to_variability` \| `max_drawdown` (SP3) |
| `sort_order` | string | desc | `asc` \| `desc` |

Each fund also carries the SP3 statistics (`annualized_return_pct`, `volatility_pct`, `reward_to_variability`, `max_drawdown_pct`, `metrics_available`). When a fund lacks enough snapshot history the numeric metrics are `"0"` and `metrics_available` is `false`. `GET /api/v3/investment-funds/{id}` additionally returns `history` (this fund's daily NAV series: `[{date, total_value_rsd}]`) and `average_history` (the system-average series, each fund indexed to 100 at its first snapshot) for the detail charts.

**Response 200:**
```json
{
  "funds": [
    {
      "id": 101,
      "name": "Alpha Growth Fund",
      "description": "IT-sector focus",
      "minimum_contribution_rsd": "1000.00",
      "manager_employee_id": 25,
      "fund_value_rsd": "2600000.00",
      "liquid_cash_rsd": "1500000.00",
      "profit_rsd": "5000.00",
      "active": true
    }
  ],
  "total": 1
}
```

---

### GET /api/v3/investment-funds/:id

Get one fund detail with enriched statistics (E1, Plan E 2026-05-28).

**Authentication:** Any JWT

> `holdings` is always a JSON array. A fund with no positions returns `"holdings": []` (never `null`).

**Response 200:**
```json
{
  "fund": {
    "id": 7,
    "name": "Alpha Growth",
    "description": "...",
    "manager_employee_id": 3,
    "minimum_contribution_rsd": "1000.00",
    "rsd_account_id": 12345,
    "active": true,
    "created_at": "...",
    "updated_at": "..."
  },
  "holdings": [
    {
      "security_type": "stock",
      "security_id": 42,
      "ticker": "AAPL",
      "quantity": 100,
      "average_price_rsd": "20000.00",
      "current_price_rsd": "22000.00",
      "current_value_rsd": "2200000.00",
      "acquired_at": "2026-05-01T00:00:00Z"
    }
  ],
  "investor_count": 42,
  "total_contributed_rsd": "5000000.00",
  "liquid_rsd_balance": "1500000.00",
  "total_holdings_value_rsd": "3500000.00",
  "total_value_rsd": "5000000.00",
  "total_dividends_paid_rsd": "0.00",
  "profit_rsd": "0.00",
  "profit_pct": "0.0000"
}
```

**Notes:**
- `total_dividends_paid_rsd` = sum of all `fund_dividend_payments.amount_rsd` for this fund (E4). Returns `"0.00"` when no dividends have been paid out yet.
- `profit_rsd` = `total_value_rsd` − `total_contributed_rsd`.
- `profit_pct` = `profit_rsd / total_contributed_rsd × 100` (4 decimal places). Zero when `total_contributed_rsd` is zero.
- `current_value_rsd` per holding = `quantity × current_price_rsd` (computed server-side).

---

### PUT /api/v3/investment-funds/:id

Update a fund's mutable fields.

**Authentication:** Employee JWT + `funds.manage` permission

**Request Body:** Any subset; omitted fields are unchanged.

| Field | Type | Description |
|---|---|---|
| `name` | string | New name |
| `description` | string | New description |
| `minimum_contribution_rsd` | string (decimal) | New minimum |
| `active` | bool | Toggle visibility / acceptance of new investments |

**Response 200:** `{ "fund": <updated fund> }`.

---

### POST /api/v3/investment-funds/:id/invest

Invest money into a fund. Runs the invest saga: `debit_source` → `credit_fund_rsd_account` → `upsert_position`. Cross-currency invests convert via exchange-service before debit.

**Authentication:** Any JWT (`OwnerIsBankIfEmployee`)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Fund ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `source_account_id` | uint64 | Yes | Account to debit |
| `amount` | string (decimal) | Yes | Amount in `currency` |
| `currency` | string | Yes | ISO code of `amount` (e.g. `RSD`, `EUR`) |
| `on_behalf_of_type` | string | No | `self` (default — caller's position) or `bank` (employee invests for the bank) |

**Response 201:**
```json
{
  "contribution": {
    "id": 7001,
    "fund_id": 101,
    "amount_rsd": "10000.00",
    "is_inflow": true,
    "status": "completed",
    "created_at": "2026-04-28T15:30:00Z"
  }
}
```

**Error Responses:**
- `400` — missing `source_account_id` / `amount` / `currency`
- `409` — amount below `minimum_contribution_rsd`, insufficient balance, or fund inactive

---

### POST /api/v3/investment-funds/:id/redeem

Redeem money out of a fund. Runs the redeem saga: `debit_fund` (amount + fee) → `credit_target` → optional `credit_bank_fee` → `decrement_position`. The fee is `fund_redemption_fee_pct` (0.5% by default; bank redeems pay 0). When the fund's RSD cash is insufficient, the call returns `409 insufficient_fund_cash` (liquidation sub-saga is a follow-up).

**Authentication:** Any JWT (`OwnerIsBankIfEmployee`)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Fund ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `amount_rsd` | string (decimal) | Yes | Amount to redeem in RSD |
| `target_account_id` | uint64 | Yes | Account that receives the proceeds |
| `on_behalf_of_type` | string | No | `self` (default) or `bank` |

**Response 201:**
```json
{
  "contribution": {
    "id": 7002,
    "fund_id": 101,
    "amount_rsd": "5000.00",
    "is_inflow": false,
    "status": "completed",
    "fee_rsd": "25.00",
    "created_at": "2026-04-28T15:45:00Z"
  }
}
```

**Error Responses:**
- `400` — missing `amount_rsd` / `target_account_id`
- `409` — `insufficient_fund_cash`, position too small, or fund inactive

---

### GET /api/v3/me/investment-funds

List the caller's fund positions (Moji fondovi tab). Employees get the bank's positions; clients get their own.

**Authentication:** Any JWT (`OwnerIsBankIfEmployee`)

**Response 200:**
```json
{
  "positions": [
    {
      "fund_id": 101,
      "fund_name": "Alpha Growth Fund",
      "total_contributed_rsd": "25000.00",
      "current_value_rsd": "27000.00",
      "percentage_fund": "0.005",
      "profit_rsd": "2000.00",
      "last_change_at": "2026-04-15T10:00:00Z"
    }
  ]
}
```

---

### GET /api/v3/investment-funds/positions

List the bank's own fund positions (Portal: Profit Banke → Investment Funds Positions).

**Authentication:** Employee JWT + `funds.bank-position-read` permission

**Response 200:**
```json
{
  "positions": [
    {
      "fund_id": 101,
      "fund_name": "Alpha Growth Fund",
      "manager_employee_id": 25,
      "total_contributed_rsd": "500000.00",
      "current_value_rsd": "540000.00",
      "percentage_fund": "20.0",
      "profit_rsd": "40000.00"
    }
  ]
}
```

**Error Responses:**
- `403` — missing `funds.bank-position-read`

---

## 32. Actuaries

### GET /api/v3/actuaries

List all actuaries (trading agents).

**Authentication:** Employee JWT + `agents.manage` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `search` | string | Search by name |
| `position` | string | Filter by position |

**Response 200:**
```json
{
  "actuaries": [ ],
  "total_count": 12
}
```

---

### PUT /api/v3/actuaries/:id/limit

Set trading limit for an actuary.

**Authentication:** Employee JWT + `agents.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `limit` | string | Yes | Trading limit amount |

**Response 200:** Updated actuary object.

---

### POST /api/v3/actuaries/:id/reset-limit

Reset used trading limit for an actuary back to zero.

**Authentication:** Employee JWT + `agents.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Response 200:** Updated actuary object.

---

### GET /api/v3/actuaries/performance

Realised-profit feed for the Profit Banke → Actuary Performances page. Returns one entry per acting employee, aggregated across all on-behalf-of-bank trades they have placed. Profit is realised P&L only — open positions are not marked-to-market here.

**Authentication:** Employee JWT + `actuaries.read.all` permission

**Response 200:**
```json
{
  "actuaries": [
    {
      "employee_id": 25,
      "first_name": "Marija",
      "last_name": "Marković",
      "position": "supervisor",
      "realised_profit_rsd": "125000.00",
      "trade_count": 42
    }
  ]
}
```

**Error Responses:**
- `401` — missing or invalid JWT
- `403` — missing `actuaries.read.all`

---

### POST /api/v3/actuaries/:id/require-approval

Require supervisor approval for all orders placed by this actuary. No request body required. Replaces `PUT /api/v3/actuaries/:id/approval` with `{"need_approval": true}`.

**Authentication:** Employee JWT + `employees.update.any` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Response 200:** Updated actuary object.

**Error Responses:**
- `400` — invalid actuary ID
- `401` — missing or invalid JWT
- `403` — missing `employees.update.any`
- `404` — actuary not found

---

### POST /api/v3/actuaries/:id/skip-approval

Remove the supervisor approval requirement for this actuary (orders go straight to the market). No request body required. Replaces `PUT /api/v3/actuaries/:id/approval` with `{"need_approval": false}`.

**Authentication:** Employee JWT + `employees.update.any` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Response 200:** Updated actuary object.

**Error Responses:**
- `400` — invalid actuary ID
- `401` — missing or invalid JWT
- `403` — missing `employees.update.any`
- `404` — actuary not found

---

## 33. Tax

### GET /api/v3/tax

List all tax records.

**Authentication:** Employee JWT + `tax.manage` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `user_type` | string | `client` or `actuary` |
| `search` | string | Search term |

**Response 200:**
```json
{
  "tax_records": [ ],
  "total_count": 30
}
```

---

### POST /api/v3/tax/collect

Collect/process taxes for all users.

**Authentication:** Employee JWT + `tax.manage` permission

**Request Body:** None (empty POST)

**Response 200:**
```json
{
  "collected_count": 15,
  "total_collected_rsd": "125000.00",
  "failed_count": 0
}
```

---

### GET /api/v3/me/tax

Returns paginated capital gains tax records for the authenticated user (client or employee/actuary). Ownership is derived from the JWT `user_id`.

**Authentication:** `AnyAuthMiddleware` (any valid token -- no specific permission required)

**Query Parameters:**

| Parameter   | Type | Default | Description                       |
|-------------|------|---------|-----------------------------------|
| `page`      | int  | 1       | Page number                       |
| `page_size` | int  | 10      | Items per page                    |

**Example Request:**

```
GET /api/v3/me/tax?page=1&page_size=10
Authorization: Bearer <token>
```

**Success Response (200):**

```json
{
  "records": [
    {
      "id": 1,
      "security_type": "stock",
      "ticker": "AAPL",
      "quantity": 10,
      "buy_price_per_unit": "150.0000",
      "sell_price_per_unit": "175.0000",
      "total_gain": "250.0000",
      "currency": "USD",
      "tax_year": 2026,
      "tax_month": 3,
      "created_at": "2026-03-15T10:30:00Z"
    }
  ],
  "total_count": 1,
  "tax_paid_this_year": "12500.00",
  "tax_unpaid_this_month": "37.50"
}
```

**Error Responses:**

| Code | Description              |
|------|--------------------------|
| 401  | Unauthorized -- invalid or missing token |
| 500  | Internal server error    |

---

## 34. Blueprints

**v1-only section.** Limit blueprints are reusable named templates that define a set of limit values. They can be created for employees, actuaries, or clients. Applying a blueprint copies its values to the target entity's limits.

**Authentication:** Employee token with `limits.manage` permission

---

### GET /api/v3/blueprints

List all limit blueprints, optionally filtered by type.

**Authentication:** Employee JWT + `limits.manage` permission

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `type` | string | No | Filter by type: `employee`, `actuary`, or `client` |

**Response 200:**
```json
{
  "blueprints": [
    {
      "id": 1,
      "name": "BasicTeller",
      "description": "Default teller blueprint",
      "type": "employee",
      "values_json": "{\"max_loan_approval_amount\":\"50000.0000\",\"max_single_transaction\":\"100000.0000\"}"
    }
  ]
}
```

| Status | Description |
|---|---|
| 200 | Blueprints returned |
| 400 | Invalid type filter |
| 401 | Unauthorized |
| 500 | Internal server error |

---

### POST /api/v3/blueprints

Create a new limit blueprint.

**Authentication:** Employee JWT + `limits.manage` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Blueprint name (e.g., `"BasicTeller"`) |
| `description` | string | No | Human-readable description |
| `type` | string | Yes | Target entity type: `employee`, `actuary`, or `client` |
| `values` | object | Yes | JSON object containing the limit values for the blueprint |

**Example Request (employee blueprint):**
```json
{
  "name": "BasicTeller",
  "description": "Default teller blueprint",
  "type": "employee",
  "values": {
    "max_loan_approval_amount": "50000.0000",
    "max_single_transaction": "100000.0000",
    "max_daily_transaction": "500000.0000",
    "max_client_daily_limit": "250000.0000",
    "max_client_monthly_limit": "2500000.0000"
  }
}
```

**Example Request (client blueprint):**
```json
{
  "name": "PremiumClient",
  "description": "Premium client limits",
  "type": "client",
  "values": {
    "daily_limit": "500000.0000",
    "monthly_limit": "5000000.0000",
    "transfer_limit": "250000.0000"
  }
}
```

**Response 201:** Created blueprint object

| Status | Description |
|---|---|
| 201 | Blueprint created |
| 400 | Invalid input (missing name, bad type) |
| 401 | Unauthorized |
| 409 | Duplicate name+type combination |
| 500 | Internal server error |

---

### GET /api/v3/blueprints/:id

Get a single blueprint by ID.

**Authentication:** Employee JWT + `limits.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Blueprint ID |

**Response 200:** Blueprint object

| Status | Description |
|---|---|
| 200 | Blueprint returned |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 404 | Blueprint not found |
| 500 | Internal server error |

---

### PUT /api/v3/blueprints/:id

Update an existing blueprint's name, description, or values. The `type` field cannot be changed after creation.

**Authentication:** Employee JWT + `limits.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Blueprint ID |

**Request Body** (all fields optional):

| Field | Type | Description |
|---|---|---|
| `name` | string | New blueprint name |
| `description` | string | New description |
| `values` | object | New limit values (replaces existing) |

**Example Request:**
```json
{
  "name": "UpdatedTeller",
  "description": "Updated teller blueprint",
  "values": {
    "max_loan_approval_amount": "75000.0000",
    "max_single_transaction": "150000.0000",
    "max_daily_transaction": "750000.0000",
    "max_client_daily_limit": "350000.0000",
    "max_client_monthly_limit": "3500000.0000"
  }
}
```

**Response 200:** Updated blueprint object

| Status | Description |
|---|---|
| 200 | Blueprint updated |
| 400 | Invalid input |
| 401 | Unauthorized |
| 404 | Blueprint not found |
| 500 | Internal server error |

---

### DELETE /api/v3/blueprints/:id

Delete a blueprint by ID. Does not affect limits that have already been applied from this blueprint.

**Authentication:** Employee JWT + `limits.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Blueprint ID |

**Response 204:** No content (deleted)

| Status | Description |
|---|---|
| 204 | Blueprint deleted |
| 400 | Invalid ID |
| 401 | Unauthorized |
| 404 | Blueprint not found |
| 500 | Internal server error |

---

### POST /api/v3/blueprints/:id/apply

Apply a blueprint's limit values to a target entity. The target type is determined by the blueprint's `type` field (employee, actuary, or client).

**Routing note (SP-4):** The gateway dispatches apply calls based on blueprint type. For **client-type** blueprints the gateway calls `ClientLimitService.SetClientLimits` on client-service directly (synchronously), bypassing user-service entirely. For **employee** and **actuary** type blueprints the gateway calls `BlueprintService.ApplyBlueprint` on user-service as before. The request body (`target_id`), response shape, and status codes are the same regardless of type.

**Authentication:** Employee JWT + `limits.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Blueprint ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `target_id` | int64 | Yes | ID of the target entity (employee ID, actuary ID, or client ID based on blueprint type) |

**Example Request:**
```json
{
  "target_id": 42
}
```

**Response 200:**
```json
{
  "message": "blueprint applied successfully"
}
```

| Status | Description |
|---|---|
| 200 | Blueprint applied to target |
| 400 | Invalid input or target_id not positive |
| 401 | Unauthorized |
| 404 | Blueprint not found |
| 500 | Internal server error |

---

## 35. Changelog (Audit Trail)

Field-level change history for core entities. All five changelog endpoints are **fully implemented** — they return paginated audit log entries from each service's own changelog table, recording every field mutation with old value, new value, and the employee who made the change.

Each endpoint requires the same permission as the parent resource's read-all permission.

**Common Query Parameters:**

| Parameter | Type | Default | Max | Description |
|---|---|---|---|---|
| `page` | int | 1 | — | Page number |
| `page_size` | int | 20 | 200 | Items per page |

**Common Response Shape (200):**

```json
{
  "entries": [
    {
      "id": 123,
      "entity_type": "account",
      "entity_id": 42,
      "action": "update",
      "field_name": "status",
      "old_value": "\"active\"",
      "new_value": "\"inactive\"",
      "changed_by": 7,
      "changed_at": "2026-04-28T14:32:11Z",
      "reason": "Manual deactivation by supervisor"
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 20
}
```

Fields:
- `id` — changelog entry ID
- `entity_type` — resource type (`"account"`, `"card"`, `"client"`, `"loan"`, `"employee"`)
- `entity_id` — numeric ID of the changed entity
- `action` — `"create"`, `"update"`, or `"delete"`
- `field_name` — name of the changed field
- `old_value` / `new_value` — JSON-encoded previous and current values (strings are wrapped in quotes)
- `changed_by` — employee ID who performed the change (0 if system-initiated)
- `changed_at` — RFC3339 timestamp
- `reason` — free-text reason if the change came via a supervisor action (may be empty)

**Common Error Responses:**
- `400` — `id` is not a positive integer, or `page_size` > 200
- `401` — missing or invalid JWT
- `403` — missing required permission

---

### GET /api/v3/accounts/:id/changelog

Get the field-level change history for an account.

**Authentication:** Employee JWT + `accounts.read.all`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:** Common changelog shape (see above), `entity_type: "account"`.

---

### GET /api/v3/employees/:id/changelog

Get the field-level change history for an employee.

**Authentication:** Employee JWT + `employees.read.all`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Response 200:** Common changelog shape (see above), `entity_type: "employee"`.

---

### GET /api/v3/clients/:id/changelog

Get the field-level change history for a client.

**Authentication:** Employee JWT + `clients.read.all`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Response 200:** Common changelog shape (see above), `entity_type: "client"`.

---

### GET /api/v3/cards/:id/changelog

Get the field-level change history for a payment card.

**Authentication:** Employee JWT + `cards.read.all`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Common changelog shape (see above), `entity_type: "card"`.

---

### GET /api/v3/loans/:id/changelog

Get the field-level change history for a loan.

**Authentication:** Employee JWT + `credits.read.all`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Common changelog shape (see above), `entity_type: "loan"`.

---

## 36. Sessions & Login History

Manage active sessions and view login history for the authenticated user.

---

### GET /api/v3/me/sessions

List all active sessions for the authenticated user.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**
```json
{
  "sessions": [
    {
      "id": 1,
      "user_role": "client",
      "ip_address": "192.168.1.100",
      "user_agent": "Mozilla/5.0 ...",
      "device_id": "",
      "system_type": "client",
      "last_active_at": "2026-04-01T12:00:00Z",
      "created_at": "2026-04-01T10:00:00Z",
      "is_current": true
    }
  ]
}
```

| Status | Description |
|---|---|
| 200 | Sessions returned |
| 401 | Unauthorized |

---

### DELETE /api/v3/me/sessions/:id

Revoke a specific session by ID, logging out the device associated with it. Renamed from `POST /api/v3/me/sessions/revoke` (which read the session ID from the request body) in the v3 route standardization (2026-04-28). No request body required.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int64 | Session ID to revoke |

**Example Request:**
```
DELETE /api/v3/me/sessions/42
Authorization: Bearer <token>
```

**Response 200:**
```json
{
  "message": "session revoked successfully"
}
```

| Status | Description |
|---|---|
| 200 | Session revoked |
| 400 | Invalid or missing session ID |
| 401 | Unauthorized |
| 404 | Session not found |

---

### POST /api/v3/me/sessions/revoke-others

Revoke all sessions except the current one. The current session is identified by the provided refresh token.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `current_refresh_token` | string | Yes | The refresh token of the current session (to keep it alive) |

**Example Request:**
```json
{
  "current_refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

**Response 200:**
```json
{
  "message": "all other sessions revoked successfully"
}
```

| Status | Description |
|---|---|
| 200 | All other sessions revoked |
| 400 | Invalid input |
| 401 | Unauthorized |

---

### GET /api/v3/me/login-history

Get recent login attempts for the authenticated user.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `limit` | int | No | Max entries to return (default: 50, max: 100) |

**Response 200:**
```json
{
  "entries": [
    {
      "id": 1,
      "ip_address": "192.168.1.100",
      "user_agent": "Mozilla/5.0 ...",
      "device_type": "web",
      "success": true,
      "created_at": "2026-04-01T10:00:00Z"
    }
  ]
}
```

| Status | Description |
|---|---|
| 200 | Login history returned |
| 401 | Unauthorized |

---

## 37. Notifications

**v1-only section.** General persistent notifications for the authenticated user. Unlike verification notifications (mobile-only, time-limited), these persist indefinitely and track read/unread status. Accessible by both browser and mobile clients.

**Authentication:** Any JWT (AnyAuthMiddleware — both employee and client tokens)

Notification types generated by the system:

| Type | Source | Description |
|---|---|---|
| `account_created` | account-service | New bank account created for the user |
| `card_issued` | card-service | New card issued on user's account |
| `card_blocked` | card-service | User's card was blocked |
| `money_sent` | transaction-service | Payment or transfer sent from user's account |
| `money_received` | transaction-service | Payment or transfer received to user's account |
| `loan_approved` | credit-service | User's loan request was approved |
| `loan_rejected` | credit-service | User's loan request was rejected |
| `password_changed` | auth-service | User's password was changed |
| `mobile_activation_requested` | auth-service | A mobile activation code was requested; the code is in the message body (also emailed) |

---

### GET /api/v3/me/notifications

List notifications for the authenticated user, ordered by creation date (newest first).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default 1) |
| `page_size` | int | No | Items per page (default 20, max 100) |
| `read` | string | No | Filter: `"read"`, `"unread"`, or omit for all |

**Response 200:**

```json
{
  "notifications": [
    {
      "id": 42,
      "type": "money_received",
      "title": "Money Received",
      "message": "You received 5000.00 to account 1234567890.",
      "is_read": false,
      "ref_type": "transfer",
      "ref_id": 123,
      "created_at": "2026-04-09T14:30:00Z"
    }
  ],
  "total": 15
}
```

| Status | Description |
|---|---|
| 200 | Notifications list returned |
| 401 | Unauthorized |

---

### GET /api/v3/me/notifications/unread-count

Get the number of unread notifications for the authenticated user.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**

```json
{
  "unread_count": 3
}
```

| Status | Description |
|---|---|
| 200 | Count returned |
| 401 | Unauthorized |

---

### POST /api/v3/me/notifications/:id/read

Mark a single notification as read.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Notification ID |

**Response 200:**

```json
{
  "success": true
}
```

| Status | Description |
|---|---|
| 200 | Marked as read |
| 400 | Invalid notification ID |
| 404 | Notification not found (or belongs to another user) |

---

### POST /api/v3/me/notifications/read-all

Mark all unread notifications as read for the authenticated user.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**

```json
{
  "success": true,
  "count": 5
}
```

`count` is the number of notifications that were marked as read.

| Status | Description |
|---|---|
| 200 | All marked as read |
| 401 | Unauthorized |

---

## 38. Stock Data Source

Admin-only endpoints for managing the stock-service data source. Switching sources is **destructive** — it wipes every securities row, listing, option, order, holding, capital gain, tax collection, and order transaction, then reseeds from the new source. Use with care.

Routes were moved from `/api/v3/admin/stock-source` to `/api/v3/stock-sources` in the v3 route standardization (2026-04-28).

**Authentication:** `AuthMiddleware` + permission `securities.manage.catalog` (seeded on `EmployeeAdmin` only)

---

### POST /api/v3/stock-sources

Switch the active stock data source and reseed the database. For `generated`, the reseed runs synchronously (response returns after the DB is ready). For `external` and `simulator`, the reseed runs in a background goroutine and the response returns immediately with `status: "reseeding"`; poll `GET /api/v3/stock-sources/active` to watch for `status: "idle"`.

**Request Body:**

```json
{ "source": "external" | "generated" | "simulator" }
```

**Response 202 Accepted:**

```json
{
  "source": "generated",
  "status": "idle",
  "started_at": "2026-04-13T12:34:56Z",
  "last_error": ""
}
```

**Possible statuses:** `idle` | `reseeding` | `failed`.

**Error Responses:**

- `400 validation_error` — unknown `source` value
- `403 forbidden` — missing `securities.manage.catalog` permission
- `409 conflict` — another switch is already in progress
- `500 internal_error` — wipe or reseed failed; check `last_error` via GET
- `503 unavailable` — simulator unreachable (when `source=simulator`)

---

### GET /api/v3/stock-sources/active

Return the current active source and the most recent switch status.

**Authentication:** `AuthMiddleware` + permission `securities.manage.catalog`

**Response 200:**

```json
{
  "source": "simulator",
  "status": "reseeding",
  "started_at": "2026-04-13T12:34:56Z",
  "last_error": ""
}
```

When `status=failed`, `last_error` contains the failure reason. The admin can retry by issuing a new POST.

---

## 39. Peer Banks (Admin) — SI-TX cross-bank registry (Celina 5)

Runtime registry of cross-bank peer banks. Backs the SI-TX `POST /api/v3/cross-bank-protocol/interbank` middleware, which looks up peer authentication credentials in this table. EmployeeAdmin only (`peer_banks.manage.any` permission).

> **Status:** fully wired. The admin CRUD, the `POST /api/v3/cross-bank-protocol/interbank` envelope handler (`NEW_TX` / `COMMIT_TX` / `ROLLBACK_TX`), and both auth paths (`X-Api-Key` via `ResolvePeerByAPIToken` and the HMAC bundle via `ResolvePeerByBankCode`) are all implemented. `POST /api/v3/cross-bank-protocol/interbank` only returns `501 Not Implemented` if the gRPC backend itself returns `Unimplemented`, which it does not in the current build.

### GET /api/v3/peer-banks

List all registered peer banks.

**Authentication:** Employee JWT + `peer_banks.manage.any` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `active_only` | bool | When `true`, hide inactive peers |

**Response 200:**
```json
{
  "peer_banks": [
    {
      "id": 1,
      "bank_code": "222",
      "routing_number": 222,
      "base_url": "http://peer-222/api/v3",
      "api_token_preview": "…-222",
      "hmac_enabled": false,
      "active": true,
      "created_at": 1714345200,
      "updated_at": 1714345200
    }
  ]
}
```

`api_token_preview` returns only the last 4 characters of the token. The full token is never exposed via this endpoint.

---

### GET /api/v3/peer-banks/:id

Read one peer bank by ID.

**Authentication:** Employee JWT + `peer_banks.manage.any` permission

**Response 200:** Peer bank object (same shape as List).
**Response 404:** When the peer bank doesn't exist.

---

### POST /api/v3/peer-banks

Register a new peer bank.

**Authentication:** Employee JWT + `peer_banks.manage.any` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `bank_code` | string | Yes | 3-digit prefix (e.g. "222") |
| `routing_number` | int64 | Yes | The peer's routing number. MUST equal the numeric value of `bank_code` (e.g. `bank_code` `"222"` ⇒ `routing_number` `222`); a non-numeric `bank_code` or a mismatch is rejected (see the invariant note below). |
| `base_url` | string | Yes | Peer's `/api/v3` base URL |
| `api_token` | string | Yes | Plaintext API token issued by peer; bcrypt-hashed before persist |
| `hmac_inbound_key` | string | No | HMAC key to verify inbound HMAC-mode requests from this peer |
| `hmac_outbound_key` | string | No | HMAC key to sign outbound requests to this peer |
| `active` | bool | Yes | Whether this peer accepts traffic |

**Example Request:**
```json
{
  "bank_code": "222",
  "routing_number": 222,
  "base_url": "http://peer-222/api/v3",
  "api_token": "secret-token-from-peer-222",
  "active": true
}
```

**Response 201:** Peer bank object (`api_token_preview` returned, never the full token).
**Response 400:** Validation error — a missing required field; OR `bank_code`/`routing_number` equals this bank's own (peer-collision rejected, SP-2a); OR `bank_code` is not the numeric `routing_number` (non-numeric, or numerically different — routing-invariant, 4.4.7).

> **Peer-collision guard (SP-2a):** `POST /api/v3/peer-banks` returns `400 validation_error` when `bank_code` or `routing_number` matches this bank's own configuration. This is enforced at the gRPC service layer (interbank-service `CreatePeerBank` returns `InvalidArgument` → gateway maps to 400). The invariant ensures `routing_number == OwnRouting()` reliably distinguishes local rows from remote (folded-in) rows in the unified OTC tables.

> **bank_code = routing_number invariant (4.4.7):** `CreatePeerBank` also returns `400 validation_error` when `bank_code` is not numeric or does not numerically equal `routing_number`. Inbound peer authentication resolves a caller to its `bank_code`, and the cross-bank OTC paths derive the caller's routing from it (`peerRoutingForCode`); the two MUST agree, or the peer could authenticate yet its derived routing would never match the routing stored on a negotiation/contract mirror — every inbound `GET`/`PUT`/`accept` for it would `404`. Enforcing the invariant at registration makes the derivation provably exact for every registered peer.

---

### PUT /api/v3/peer-banks/:id

Update mutable fields. Only fields present in the body are updated.

**Authentication:** Employee JWT + `peer_banks.manage.any` permission

**Request Body (all fields optional):**

| Field | Type | Description |
|---|---|---|
| `base_url` | string | New base URL |
| `api_token` | string | New plaintext token (bcrypt re-hashed on persist) |
| `hmac_inbound_key` | string | New inbound HMAC key |
| `hmac_outbound_key` | string | New outbound HMAC key |
| `active` | bool | Toggle peer on/off |

**Response 200:** Updated peer bank object.
**Response 404:** When the peer bank doesn't exist.

---

### DELETE /api/v3/peer-banks/:id

Remove a peer bank.

**Authentication:** Employee JWT + `peer_banks.manage.any` permission

**Response 204:** Success, no body.

---

---

## Cross-Bank Protocol (`/cross-bank-protocol`)

> **Cross-bank protocol routes live ONLY at `/api/v3/cross-bank-protocol/...`. There is no legacy alias.**
>
> As of 2026-05-29, the legacy paths (`/api/v3/interbank`, `/api/v3/public-stock`, `/api/v3/negotiations/*`, `/api/v3/user/*`) have been removed. Any cohort bank still using the old prefix will receive 404 and MUST update its peer-banks registration immediately.
>
> Authentication for all routes in this section: PeerAuth (hybrid `X-Api-Key` or HMAC bundle — see §39 for the trust setup).
>
> **Registering this bank:** Set `base_url` to `http://<this-bank-host>/api/v3/cross-bank-protocol` in your `peer_banks` table. The outbound HTTP client appends only the leaf names (`/interbank`, `/public-stock`, `/negotiations`, `/user`) to `base_url`.

### Route Summary

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v3/cross-bank-protocol/interbank` | SI-TX `Message<Type>` envelope (NEW_TX / COMMIT_TX / ROLLBACK_TX) |
| `GET` | `/api/v3/cross-bank-protocol/interbank/:transaction_id/status` | CHECK_STATUS: query cross-bank TX state |
| `GET` | `/api/v3/cross-bank-protocol/public-stock` | List this bank's OTC option offers — the sole cross-bank option-discovery surface |
| `POST` | `/api/v3/cross-bank-protocol/negotiations` | Create cross-bank OTC negotiation |
| `PUT` | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | Counter-offer on existing negotiation |
| `GET` | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | Read negotiation state |
| `DELETE` | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | Cancel negotiation |
| `GET` | `/api/v3/cross-bank-protocol/negotiations/:rid/:id/accept` | Accept negotiation (triggers 4-posting SI-TX) |
| `GET` | `/api/v3/cross-bank-protocol/user/:rid/:id` | Counterparty user identity lookup |

---

### POST /api/v3/cross-bank-protocol/interbank

Receives the SI-TX `Message<Type>` envelope from peer banks. Fully wired: `NEW_TX` validates postings (UNBALANCED_TX check + per-posting account/asset/active checks), reserves credit-postings via `account-service.ReserveIncoming`, and emits a `TransactionVote`. `COMMIT_TX` finalises reservations; `ROLLBACK_TX` releases them. Idempotence-key replay returns the cached vote.

**Authentication:** Hybrid `PeerAuth` middleware. Either:
- `X-Api-Key: <token>` — looked up against `peer_banks.api_token_plaintext` via the internal `ResolvePeerByAPIToken` RPC.
- `X-Bank-Code: <code>` + `X-Bank-Signature: <hex SHA-256>` + `X-Timestamp: <RFC3339, ±5min>` + `X-Nonce: <single-use>` — verified against `peer_banks.hmac_inbound_key` via `ResolvePeerByBankCode`.

#### Envelope: `Message<Type>`

Every inbound message is wrapped in the same envelope. Shape verbatim from the cohort SI-TX-Proto spec.

| Field | Type | Notes |
|---|---|---|
| `idempotenceKey` | `{ "routingNumber": int, "locallyGeneratedKey": string }` | Each message (NEW_TX, COMMIT_TX, ROLLBACK_TX) carries its OWN unique idempotence key. Replaying the same key returns the cached result. |
| `messageType` | string | `"NEW_TX"` \| `"COMMIT_TX"` \| `"ROLLBACK_TX"` |
| `message` | object | The message body — its shape depends on `messageType` (see below). |

> COMMIT_TX / ROLLBACK_TX correlate back to the original NEW_TX via the `transactionId` field in their body, **not** via the envelope's `idempotenceKey` (which is unique per message).

#### `messageType: "NEW_TX"` — message is a `Transaction`

| Field | Type | Notes |
|---|---|---|
| `postings` | `Posting[]` | The legs of the transaction. Must net to zero per asset (else `UNBALANCED_TX`). |
| `transactionId` | `{ "routingNumber": int, "id": string }` | Identifies this transaction; COMMIT/ROLLBACK reference it. |
| `message` | string | Free-text description. |
| `callNumber` | string (optional) | Serbian payment reference / model-and-call-number. |
| `paymentCode` | string | Serbian payment code (e.g. `"289"`). |
| `paymentPurpose` | string | Payment purpose text. |

**`Posting`** = `{ "account": TxAccount, "amount": number, "asset": Asset }`

- `amount` is a **SIGNED** JSON number: **negative** = credit / the asset *leaves* this account; **positive** = debit / the asset *arrives* at this account.

**`TxAccount`** — tagged union on `type`:

| `type` | Shape |
|---|---|
| `PERSON` | `{ "type": "PERSON", "id": { "routingNumber": int, "id": string } }` |
| `ACCOUNT` | `{ "type": "ACCOUNT", "num": string }` (18-digit account number) |
| `OPTION` | `{ "type": "OPTION", "id": { "routingNumber": int, "id": string } }` |

**`Asset`** — tagged union on `type`:

| `type` | Shape |
|---|---|
| `MONAS` | `{ "type": "MONAS", "asset": { "currency": string } }` |
| `STOCK` | `{ "type": "STOCK", "asset": { "ticker": string } }` |
| `OPTION` | `{ "type": "OPTION", "asset": <OptionDescription> }` |

**Example NEW_TX body** (the canonical "coffee" transfer — 260 RSD from a peer's `ACCOUNT` to one of ours):

```json
{
  "idempotenceKey": {
    "routingNumber": 111,
    "locallyGeneratedKey": "k-coffee-1"
  },
  "messageType": "NEW_TX",
  "message": {
    "postings": [
      {
        "account": { "type": "ACCOUNT", "num": "444000100182503611" },
        "amount": -260,
        "asset": { "type": "MONAS", "asset": { "currency": "RSD" } }
      },
      {
        "account": { "type": "ACCOUNT", "num": "111000141215476411" },
        "amount": 260,
        "asset": { "type": "MONAS", "asset": { "currency": "RSD" } }
      }
    ],
    "transactionId": { "routingNumber": 111, "id": "k-coffee-1" },
    "message": "coffee",
    "paymentCode": "289",
    "paymentPurpose": "debt"
  }
}
```

**NEW_TX response — `200 OK` with a `TransactionVote`:**

- YES vote: `{ "vote": "YES" }`
- NO vote: `{ "vote": "NO", "reasons": [ { "reason": <code>, "posting": <the full offending Posting> } ] }`

Each NO reason echoes back the **entire offending `Posting`** (not an index). Reason codes:

`UNBALANCED_TX`, `NO_SUCH_ACCOUNT`, `NO_SUCH_ASSET`, `UNACCEPTABLE_ASSET`, `INSUFFICIENT_ASSET`, `OPTION_AMOUNT_INCORRECT`, `OPTION_USED_OR_EXPIRED`, `OPTION_NEGOTIATION_NOT_FOUND`.

**Example NO vote:**

```json
{
  "vote": "NO",
  "reasons": [
    {
      "reason": "INSUFFICIENT_ASSET",
      "posting": {
        "account": { "type": "ACCOUNT", "num": "111000141215476411" },
        "amount": 260,
        "asset": { "type": "MONAS", "asset": { "currency": "RSD" } }
      }
    }
  ]
}
```

#### `messageType: "COMMIT_TX"` / `"ROLLBACK_TX"` — message is `{ transactionId }`

```json
{
  "idempotenceKey": { "routingNumber": 111, "locallyGeneratedKey": "k-coffee-commit-1" },
  "messageType": "COMMIT_TX",
  "message": { "transactionId": { "routingNumber": 111, "id": "k-coffee-1" } }
}
```

`COMMIT_TX` finalises the reservation made by the matching `NEW_TX`; `ROLLBACK_TX` releases it. Both are idempotent and both respond **204 No Content** with an empty body.

#### Responses

- **200 OK** — `NEW_TX` final vote (`TransactionVote` body).
- **204 No Content** — `COMMIT_TX` / `ROLLBACK_TX` final, empty body.
- **202 Accepted** — peer accepted the message but the result is not yet final; the sender should retry later.
- **401 Unauthorized** — empty body when auth fails (constant-time compare; no info leak).

> **Sender semantics:** `202` = peer accepted, retry later; `200` = final (vote); `204` = final, empty.

> **Receiver-side 202 (async):** For a `NEW_TX` whose local reserve exceeds `SITX_RECEIVE_SYNC_DEADLINE` (transaction-service, default `5s`), the receiver returns **202 Accepted** with an empty body and finishes the reserve in the background. The sender retransmits the same idempotence key; once the reserve completes, the retransmit returns **200** with the vote. `COMMIT_TX` / `ROLLBACK_TX` are always processed synchronously (**204**).

---

### GET /api/v3/cross-bank-protocol/interbank/:transaction_id/status

Allows a peer bank to query the status of a cross-bank SI-TX transaction by its `transactionId` (the UUID / idempotence key used in the original `NEW_TX` envelope). Used by the Celina-5 CHECK_STATUS mechanism so stuck sagas can be resolved by either side when communication breaks mid-flight.

**Authentication:** PeerAuth (X-Api-Key or HMAC bundle).

**Path Parameters:**
- `transaction_id` — the SI-TX transaction UUID (= the `locallyGeneratedKey` sent in the original `NEW_TX` envelope).

**Response 200:**
```json
{
  "transaction_id": "abc-123-uuid",
  "state":          "committed",
  "our_role":       "sender",
  "last_action_at": "2026-05-28T12:00:00Z",
  "last_error":     ""
}
```

- `state`: one of `"prepared"` (pending/in-progress), `"committed"`, `"rolled_back"`, `"dead_letter"` (terminal failure, max retries exceeded), `"unknown"` (no record found).
- `our_role`: `"sender"` (we initiated this TX via `InitiateOutboundTx`), `"receiver"` (we received a `NEW_TX` from the caller), or `""` when unknown.
- `last_action_at`: RFC3339 timestamp of the last status update, empty when unknown.
- `last_error`: last recorded error string, empty on success.

**Responses:**
- **200 OK** — always returned for any known or unknown transaction (unknown → `state: "unknown"`).
- **400 Bad Request** — `transaction_id` missing (path param required).
- **401 Unauthorized** — peer auth failed.
- **500 Internal Server Error** — unexpected backend error.

---

### GET /api/v3/cross-bank-protocol/public-stock

Peer-facing OTC discovery — returns stock holdings on this bank flagged for OTC public trading. Used by peer banks to populate their OTC discovery pages.

**Authentication:** PeerAuth (X-Api-Key or HMAC bundle).

**Response 200:** A **BARE JSON array** (no `{ "stocks": ... }` wrapper). Each element groups all public sellers of one ticker. There is **no** price or currency on this endpoint — it is a discovery surface only; pricing is negotiated per-offer.

```json
[
  {
    "stock": { "ticker": "AAPL" },
    "sellers": [
      { "seller": { "routingNumber": 111, "id": "client-3" }, "amount": 50 },
      { "seller": { "routingNumber": 111, "id": "client-9" }, "amount": 20 }
    ]
  }
]
```

| Field | Type | Notes |
|---|---|---|
| `stock.ticker` | string | The security symbol. |
| `sellers[].seller` | `{ "routingNumber": int, "id": string }` | The seller's `ForeignBankId`. `routingNumber` is this bank; `id` is the **standard opaque participant id** — `client-<N>` for a client-held holding, `bank` for a bank-held one. This is the SAME value a discovering bank echoes back verbatim as `sellerId` in `POST /negotiations` and it resolves to a local seller. The bank never advertises a bare numeric owner id here (a peer could not address it back). |
| `sellers[].amount` | int | Quantity this seller has flagged public for that ticker. |

---

### POST /api/v3/cross-bank-protocol/negotiations

Peer initiates a cross-bank OTC negotiation against a publicly-listed holding on this bank. The peer's offer is persisted in `peer_otc_negotiations` and gets a fresh negotiation ID owned by this bank.

**Authentication:** PeerAuth.

**Request Body:** SI-TX `OtcOffer` payload — verbatim from the cohort spec at <https://arsen.srht.site/si-tx-proto/>. The body IS the `OtcOffer`; there is no wrapping object. Per SI-TX §2.5 the monetary `amount` fields are JSON **numbers** (the gateway also tolerates a quoted string for peers that still quote).

```json
{
  "stock":          { "ticker": "AAPL" },
  "settlementDate": "2026-12-31T00:00:00Z",
  "pricePerUnit":   { "amount": 180.50, "currency": "USD" },
  "premium":        { "amount": 700,    "currency": "USD" },
  "buyerId":        { "routingNumber": 222, "id": "550e8400-e29b-41d4-a716-446655440000" },
  "sellerId":       { "routingNumber": 111, "id": "client-1" },
  "amount":         50,
  "lastModifiedBy": { "routingNumber": 222, "id": "550e8400-e29b-41d4-a716-446655440000" }
}
```

**Participant-id validation (SI-TX §2.3):** `buyerId.id`, `sellerId.id`, and `lastModifiedBy.id` are **opaque `ForeignBankId.id` strings**. The gateway enforces ONLY the §2.3 bound — non-empty and ≤ 64 bytes — and does **NOT** format-check the BUYER's id:

- `buyerId.id` belongs to the PEER (`routingNumber` = the authenticated peer). Per §2.3 a bank MUST NOT interpret another bank's opaque id, so **any** scheme is accepted (a UUID like `550e8400-…`, `acc-42`, etc.) and stored verbatim. *(Prior to 2.7.0 the gateway wrongly required `client-<N>`/`employee-<N>` here, which rejected spec-conformant peers — fixed.)*
- `sellerId.id` is OURS (`sellerId.routingNumber` MUST equal this bank). Because the seller is a local participant — not another bank's opaque id — it MUST be **well-formed**: `bank`, `employee-<digits>`, or `client-<digits>`. *(As of 2.8.0 a malformed seller such as `employee-abc` or `employee-` is rejected `400 validation_error` and **no row is persisted**; previously such an id created an inert junk row — a row-spam vector.)* A `client-<N>` is additionally existence-checked against client-service: a non-existent client → `404 not_found`, no row.
- `lastModifiedBy.routingNumber` is **DERIVED**, not trusted: an inbound bid was by definition last-modified by the peer that sent it, so the receiving bank persists `lastModifiedBy.routingNumber = the authenticated peer's routing`, **overriding** whatever the payload claimed. *(As of 2.8.1 a forged `lastModifiedBy` claiming this bank's routing is no longer rejected — it is simply overridden to the sender's routing; the inbound bid still succeeds `201`.)* The opaque `lastModifiedBy.id` is kept **verbatim** (§2.3 — a bank MUST NOT interpret another bank's opaque id). Deriving the routing from the authenticated sender (rather than trusting or rejecting the payload) keeps the stored `lastModifiedBy` trustworthy by construction for the accept guard.

**Response 201:** `ForeignBankId` directly (the new negotiation's id, owned by this bank).

```json
{ "routingNumber": 111, "id": "neg-uuid" }
```

---

### PUT /api/v3/cross-bank-protocol/negotiations/:rid/:id

Counter-offer on an existing negotiation.

**Authentication:** PeerAuth.

**Path Parameters:**
- `rid` — peer's routing number (int64)
- `id` — peer's negotiation id (string)

**Request Body:** SI-TX `OtcOffer` (same shape as POST).

**`lastModifiedBy` handling (as of 2.8.1):** `lastModifiedBy.routingNumber` is **DERIVED** from the authenticated sender, not trusted from the payload — an inbound counter was by definition last-modified by the peer that PUT it, so the receiving bank persists `lastModifiedBy.routingNumber = the authenticated peer's routing`, **overriding** any claimed value (the counter still succeeds `200`). The opaque `lastModifiedBy.id` is kept verbatim (§2.3). This keeps the stored `lastModifiedBy` trustworthy for the accept guard by construction: a forged `{thisBank}` counter has its routing overridden to the sender's, so the sender can never self-accept it.

**Turn / closed guards (SI-TX §3.3, as of 2.9.2):** Before persisting the counter the receiving bank checks the stored negotiation row:
- **Closed:** if the negotiation is no longer ongoing (cancelled / accepted / rejected / expired) → **409 Conflict** (`business_rule_violation`), no mutation.
- **Out of turn:** a party may counter only when the OTHER side made the last modification. Because the stored `lastModifiedBy.routingNumber` is derived from whoever last acted, the calling peer may PUT a counter only when the stored routing is *this* bank's own routing (we last proposed → it is the peer's turn). If the stored routing is the calling peer's own (it already made the last modification) → **409 Conflict** (`business_rule_violation`), no mutation. Note: immediately after a peer's own bid (`POST /negotiations`) the stored routing is the peer's, so a peer counter right after its own bid is correctly out of turn — the receiving side must counter or accept first.

**Response 200:** Empty body on success (in-turn counter on an ongoing negotiation).

**Response 409:** Out of turn, or the negotiation is closed (per SI-TX §3.3). The stored negotiation is left un-mutated.

---

### GET /api/v3/cross-bank-protocol/negotiations/:rid/:id

Read a negotiation's current state.

**Authentication:** PeerAuth.

**Response 200:** SI-TX `OtcNegotiation` = `OtcOffer & { isOngoing: boolean }`. Monetary `amount` fields are JSON **numbers** per SI-TX §2.5.

```json
{
  "stock":          { "ticker": "AAPL" },
  "settlementDate": "2026-12-31T00:00:00Z",
  "pricePerUnit":   { "amount": 180.50, "currency": "USD" },
  "premium":        { "amount": 700,    "currency": "USD" },
  "buyerId":        { "routingNumber": 222, "id": "client-1" },
  "sellerId":       { "routingNumber": 111, "id": "client-1" },
  "amount":         50,
  "lastModifiedBy": { "routingNumber": 222, "id": "client-1" },
  "isOngoing":      true
}
```

**Response 404:** Negotiation not found.

---

### DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id

Cancel a negotiation. Either side may delete; status flips to `cancelled`.

**Authentication:** PeerAuth.
**Response 204:** Success, no body.

---

### GET /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept

Accept a negotiation. Composes a 4-posting `Transaction` (premium money debit-buyer/credit-seller + 1× `OptionDescription` debit-seller/credit-buyer) and dispatches via `PeerTxService.InitiateOutboundTxWithPostings`. The resulting SI-TX TX runs through the normal `NEW_TX` → `COMMIT_TX` flow.

**Authentication:** PeerAuth.

**Authoritative accept guards (as of 2.8.0; refined 2.8.1):** the accept must come from the **counterparty** — per SI-TX §3.6 the accepting party is "the person whose negotiation term it is" (the side that did NOT last propose), and their bank sends this GET. So on the inbound `/accept` the **stored** `lastModifiedBy.routingNumber` (read from the persisted negotiation row) MUST equal **this bank's** routing (the local side last proposed); otherwise → `403 forbidden`, **no settlement SI-TX dispatched, no contract**. Because the stored routing is **derived from the authenticated sender** at create/counter time (2.8.1) rather than trusted from the payload, it is trustworthy by construction: a peer can never accept its own (or a forged) proposal. Additionally, when this bank hosts the **parent listing**, an accept against a child of a **cancelled/consumed** listing is rejected `409 business_rule_violation` (orphan-accept guard) — authoritatively, regardless of the best-effort sibling cascade-cancel timing.

**Response 200:**
```json
{ "transactionId": "tx-uuid", "status": "pending" }
```

The `transactionId` is the same idempotence-key the OutboundReplayCron uses; clients can poll `/api/v3/me/transfers/{transactionId}` for terminal status (transfer endpoints recognise OTC tx ids by id format).

---

### GET /api/v3/cross-bank-protocol/user/:rid/:id

Returns identity info for a counterparty user. Peers call this when displaying user names alongside OTC negotiations or transfer history.

**Authentication:** PeerAuth.

**Path Parameters:**
- `rid` — must match `OWN_BANK_CODE`'s routing number; otherwise 404 (we don't proxy lookups across banks).
- `id` — `client-<n>` or `employee-<n>` format; routes to client-service or user-service accordingly.

**Response 200:** (SI-TX §3.7 shape)
```json
{
  "bankDisplayName": "EXBanka",
  "displayName":     "Marko Marković"
}
```

- `bankDisplayName` — this bank's human-readable name, from `OWN_BANK_NAME` (falls back to `OWN_BANK_CODE` when unset).
- `displayName` — the user's first + last name, space-joined.

**Response 404:** Foreign rid or unknown user id.

---

## 40. Watchlist (Celina 3)

Personal list of tracked listings (stocks, options, futures, forex pairs) per `(owner_type, owner_id)`. Read enriches each item with the current price and daily change pulled from `listings` + the latest price-refresh tick. No notifications, no schedulers — UX feature only.

Owner is resolved from the caller's JWT via `ResolveIdentity(OwnerIsBankIfEmployee)`:
- Client principal → tracks under their own (`client`, principal_id).
- Employee principal → tracks under the bank's sentinel (`bank`, NULL).

### GET /api/v3/me/watchlist

List tracked listings with current prices + daily change.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `listing_type` | string | One of `stock`, `option`, `futures`, `forex`. Filters by listing kind. |

**Response 200:**
```json
{
  "items": [
    {
      "id": 42,
      "listing_id": 7,
      "security_type": "stock",
      "ticker": "AAPL",
      "current_price": "187.4500",
      "daily_change": "1.2500",
      "daily_change_percent": "0.6720",
      "added_at_unix": 1731699200
    }
  ]
}
```

**Response 400:** Invalid `listing_type`.

**Use cases:**
- Watchlist tab in the securities portal: render rows with live price + daily-change badges.
- Header widget that previews tracked symbols (use `listing_type=stock`).
- Decide whether a tracked instrument is in range for a planned order before navigating into the order ticket.

### POST /api/v3/me/watchlist

Add a listing to the caller's watchlist. Idempotent — re-adding an already-tracked listing is a no-op and still returns 201 with the existing row.

**Authentication:** Any JWT

**Request body:**
```json
{ "listing_id": 7 }
```

**Response 201:**
```json
{ "item": { /* same shape as the List items */ } }
```

**Response 404:** `listing_id` does not exist.

**Use cases:**
- "Add to watchlist" button on a stock / option / futures detail page.
- Quick-add from the OTC discovery page after viewing a counterparty's offer.
- "Save for later" button on a price alert that's about to fire on a non-tracked instrument.

### DELETE /api/v3/me/watchlist/:listing_id

Remove a listing from the caller's watchlist.

**Authentication:** Any JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `listing_id` | int | The listing id to remove. |

**Response 204:** No content on success.

### Named watchlists (SP6)

The legacy `/me/watchlist` routes above operate on the caller's default **"My Watchlist"** (created lazily). Callers can also keep **multiple named lists** (e.g. "tech", "forex pairs"). All routes are `AnyAuthMiddleware`; a list is owned by the caller and can only be touched by its owner. The same listing may live in more than one list.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v3/me/watchlists` | List the caller's named watchlists with `{id, name, item_count, created_at}` (always includes the default). |
| `POST` | `/api/v3/me/watchlists` | Create a named list. Body `{ "name": "tech" }` (1–64 chars). Idempotent on name. Returns `201 {watchlist}`. |
| `DELETE` | `/api/v3/me/watchlists/:watchlist_id` | Delete a named list and its items. `204` / `404`. |
| `GET` | `/api/v3/me/watchlists/:watchlist_id/items?listing_type=` | List a named list's items (same enriched shape as `/me/watchlist`). |
| `POST` | `/api/v3/me/watchlists/:watchlist_id/items` | Add a listing to a named list. Body `{ "listing_id": 1 }`. `201`. |
| `DELETE` | `/api/v3/me/watchlists/:watchlist_id/items/:listing_id` | Remove a listing from a named list. `204` / `404`. |

**Response 404:** The listing is not on the caller's watchlist.

**Use cases:**
- "Remove from watchlist" / star-toggle button in the watchlist UI.
- Bulk-cleanup flow after a user closes a position they no longer want to track.

---

## 41. OTC Negotiation History (Celina 3)

Read-only view of *terminal* OTC negotiations for the caller, **LOCAL (intra-bank) and REMOTE (cross-bank) merged into one list**. The active `/me/otc/options` list excludes terminal offers; this endpoint surfaces them with optional status, date-range, and counterparty filters.

### GET /api/v3/me/otc/history

List the caller's terminal OTC negotiations (local + remote).

**Authentication:** Any JWT

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `status` | string (repeatable) | `ACCEPTED` / `REJECTED` / `EXPIRED` / `FAILED`. Default: all four. |
| `since` | string (YYYY-MM-DD) | Lower bound on `updated_at`. |
| `until` | string (YYYY-MM-DD) | Upper bound on `updated_at`. Must be ≥ `since`. |
| `counterparty_id` | int | Restrict to offers where the OTHER party has this owner id (LOCAL items only). |
| `page` | int | 1-based; default 1. |
| `page_size` | int | 1..100; default 20. |

**Response 200:**
```json
{
  "offers": [
    { "id": 42, "status": "ACCEPTED", "direction": "sell_initiated", "kind": "local",  "routing_number": 111, "bank_code": "111", "me_owner": true,  "...": "..." },
    { "id": 55, "status": "accepted", "stock_ticker": "ACME",        "kind": "remote", "routing_number": 222, "bank_code": "222", "me_owner": false, "...": "..." }
  ],
  "total": 1
}
```

Each item carries provenance + ownership fields:

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `local` (this bank hosts the negotiation) or `remote` (resolved from a cross-bank peer chain). |
| `routing_number` / `bank_code` | int / string | The hosting bank for `local`; the **counterparty/peer** bank for `remote`. |
| `me_owner` | bool | `true` only when the caller **posted/originated** the offer (initiator side) — a history row where the caller was the bidder/counterparty is `false`. For `remote`: `true` iff WE host the **seller/poster** side (`SellerRoutingNumber == ownRouting`). |

**Local vs remote behavior:**
- **LOCAL** items are the caller's terminal `OTCOffer` rows (statuses `ACCEPTED` / `REJECTED` / `EXPIRED` / `FAILED`), sorted by `updated_at` descending; item shape mirrors `/api/v3/me/otc/options`.
- **REMOTE** items are the caller's cross-bank peer negotiation chains in a *terminal* peer status. Only client principals receive remote items (a bank/employee caller has no cross-bank identity and gets local-only). The request `status` filter is mapped onto the peer status vocabulary: `ACCEPTED→accepted`, `REJECTED→{rejected,declined,cancelled}`, `EXPIRED→expired`, `FAILED→(none)`. Remote chain statuses are surfaced in the peer's lowercase vocabulary.
- **Paging:** `page` / `page_size` apply to the LOCAL set only; remote terminal rows are **appended in full** after the local page (never silently truncated). `total` reflects the local total only, so the `offers` array length may exceed `total` by the remote count. Unified cross-source paging is out of scope for SP-1.

**Response 400:** Invalid `status` value, bad date format, `since > until`.

**Use cases:**
- "Past trades" / "Negotiation history" tab on the OTC portal.
- Audit a counterparty before accepting their next offer — filter by `counterparty_id` to pull every past negotiation against the same trader.
- Year-end / month-end review report — filter by `since` / `until` to scope a reporting window.
- Resurface a `FAILED` saga so the user can see WHY a previously-attempted OTC accept aborted (the `failure_reason` field on the offer carries the saga's terminal error).

---

## 42. OTC Trader Ratings (Celina 3)

After a terminally-accepted OTC offer, either party may rate the other on a 1..5 scale with an optional comment. Each `(offer, rater)` pair allows at most one rating. Aggregates surface via a public profile endpoint usable for OTC discovery.

### POST /api/v3/me/otc/ratings

Submit a 1..5 rating + optional comment for the counterparty of an ACCEPTED offer.

**Authentication:** Any JWT

**Request body:**
```json
{ "offer_id": 42, "score": 5, "comment": "smooth transaction" }
```

`score` must be `1..5`. `comment` is optional and ≤ 1000 characters. The rated party is derived from the offer — caller submits a rating, the OTHER side gets the score.

**Response 201:**
```json
{
  "rating": {
    "id": 1,
    "offer_id": 42,
    "rater_owner_type": "client",
    "rater_owner_id": 7,
    "rated_owner_type": "client",
    "rated_owner_id": 20,
    "score": 5,
    "comment": "smooth transaction",
    "created_at_unix": 1731699200
  }
}
```

**Response 400:** `score` out of range, `comment` too long, missing `offer_id`.
**Response 403:** Caller is not a party to the referenced offer.
**Response 409:** The caller has already rated this offer.
**Response 412:** Offer is not in `ACCEPTED` status.

**Use cases:**
- Show a "Rate this counterparty" prompt to the user right after their OTC accept settles.
- Background prompt 24 h after the option exercise window opens — nudges users who never rated the original contract.
- Email-link callback that lets users rate from outside the app (link contains `offer_id`).

### GET /api/v3/otc/traders/:owner_type/:owner_id/rating

Public aggregate rating + recent comments for a trader. Visible to all authenticated callers — used by OTC discovery to surface "reputable" counterparties.

**Authentication:** Any JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `owner_type` | string | `client` or `bank`. |
| `owner_id` | int | Owner id (use `0` for bank). |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `recent_limit` | int | 1..100, default 20 (number of recent comments to surface). |

**Response 200:**
```json
{
  "owner_type": "client",
  "owner_id": 20,
  "average": 4.5,
  "count": 12,
  "recent": [ { "score": 5, "comment": "...", "created_at_unix": 1731699200 } ]
}
```

**Use cases:**
- Trader-card hover / popover in the OTC discovery list — show stars + count next to each listed offer.
- Counterparty risk gate on the accept-offer flow: warn if rating is < 3.0 or `count == 0`.
- "Top traders" leaderboard on the OTC portal landing page.

### GET /api/v3/me/otc/ratings/received

List ratings the caller has received from past OTC counterparties.

**Authentication:** Any JWT

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `limit` | int | 1..100, default 20. |

**Response 200:**
```json
{ "ratings": [ { "id": 1, "score": 5, "comment": "...", "rater_owner_id": 7, "...": "..." } ] }
```

**Use cases:**
- "My reputation" widget on the OTC portal — shows the caller their own aggregate score + recent feedback.
- Profile / settings page section that lets users see who rated them poorly so they can reach out / appeal.

---

## 43. Price Alerts (Celina 3)

Per-owner reactive alerts on a listing's price or daily change %. Conditions: `gte`, `lte`, `daily_change_pct_gte`, `daily_change_pct_lte`. Single-shot alerts (`is_recurring=false`) deactivate themselves on first match; recurring alerts honour `cooldown_seconds`. Evaluation runs on a 30 s cron over active alerts; matches publish a `PRICE_ALERT_TRIGGERED` general notification.

### GET /api/v3/me/price-alerts

List the caller's price alerts.

**Authentication:** Any JWT

**Response 200:**
```json
{
  "alerts": [
    {
      "id": 1,
      "listing_id": 7,
      "condition": "gte",
      "threshold": "200.00",
      "is_recurring": false,
      "cooldown_seconds": 3600,
      "email_too": false,
      "active": true,
      "last_triggered_unix": 0,
      "created_at_unix": 1731699200
    }
  ]
}
```

**Use cases:**
- "Alerts" tab in the user's account — list active + inactive alerts grouped by listing.
- Inline indicator on watchlist rows: badge each tracked listing that has ≥ 1 active alert.

### POST /api/v3/me/price-alerts

Create a new alert.

**Authentication:** Any JWT

**Request body:**
```json
{
  "listing_id": 7,
  "condition": "gte",
  "threshold": "200.00",
  "is_recurring": false,
  "cooldown_seconds": 3600,
  "email_too": false
}
```

`cooldown_seconds` must be 60..86400 when `is_recurring=true`. `email_too` flags the matching notification for an email render in addition to the in-app push.

**Response 201:** `{ "alert": { /* PriceAlertResponse */ } }`

**Response 400:** Invalid `condition`, missing `listing_id` / `threshold`, out-of-range cooldown.
**Response 404:** `listing_id` does not exist.

**Use cases:**
- "Alert me when X reaches Y" modal on a stock detail page.
- Quick-set "+5%" / "-5%" daily-change alerts on the watchlist row context menu.
- Recurring alert (`is_recurring=true`) for users tracking volatile instruments — re-fires every cooldown window when the condition remains true.

### GET /api/v3/me/price-alerts/:id

Read one alert. 404 if the alert isn't owned by the caller.

**Authentication:** Any JWT

**Use cases:**
- Edit-alert form prefill.
- Deep-link from the alert notification email/push back into the app.

### PUT /api/v3/me/price-alerts/:id

Update an alert. Accepts the same fields as create plus an `active` boolean.

**Authentication:** Any JWT

**Response 200:** `{ "alert": { /* PriceAlertResponse */ } }`
**Response 404:** The alert isn't owned by the caller.

**Use cases:**
- Edit the threshold/condition from the alert list.
- Toggle `active=false` to pause an alert without deleting it (useful around earnings announcements).

### DELETE /api/v3/me/price-alerts/:id

Delete an alert permanently.

**Authentication:** Any JWT

**Response 204:** Success.
**Response 404:** The alert isn't owned by the caller.

**Use cases:**
- "Remove" / trash icon on the alert list.
- Cleanup after closing a position — the alert is no longer useful.

---

## 44. Transfer Status (Celina 4 / SI-TX)

Surface the four-state client-facing lifecycle (`INITIATED`, `PENDING`, `COMPLETED`, `FAILED`) so the frontend can poll a single field without tracking the internal `pending`/`pending_verification`/`processing`/`completed`/`failed` enum or the SI-TX cross-bank split.

Per-transition push notifications (`TRANSFER_SENT`, `TRANSFER_RECEIVED`, `TRANSFER_FAILED`) are already emitted by the notification-coverage work; this endpoint is the read counterpart.

### GET /api/v3/me/transfers/:id/status

Get the client-facing status of one of the caller's transfers.

**Authentication:** Any JWT (must own the transfer)

**Response 200:**
```json
{
  "transfer_id": 42,
  "status": "COMPLETED",
  "internal_status": "completed",
  "failure_reason": "",
  "last_changed_unix": 1731699200
}
```

| Internal status | Public `status` |
|---|---|
| `pending`, `pending_verification` | `INITIATED` |
| `processing` | `PENDING` |
| `completed` | `COMPLETED` |
| `failed` | `FAILED` |

**Response 403:** Caller does not own the transfer.
**Response 404:** Transfer not found.

**Use cases:**
- Polling loop on the transfer-detail screen while the user waits for cross-bank settlement.
- Inline status pill on the transfer-list row.
- Decision input for "show retry button" — when `status=FAILED` and `failure_reason` indicates a transient cause.
- Drive the user-facing toast / banner when WebSocket push isn't connected.

---

## 45. Recurring Securities Orders (Celina 3)

User configures a weekly or monthly Market-order template; a scheduler materialises a real order on each due tick. Insufficient funds skip the tick and notify the owner; pause / resume / cancel are explicit lifecycle controls.

> **Note:** The hourly cron loop currently no-ops on placement until the order-placer integration lands. CRUD + state transitions still operate. Once wired, insufficient-funds skips emit `RECURRING_ORDER_SKIPPED`; successful placements emit `RECURRING_ORDER_EXECUTED`.

### GET /api/v3/me/recurring-orders

List the caller's recurring-order templates.

**Authentication:** Any JWT

**Response 200:** `{ "recurring_orders": [ /* RecurringOrderResponse[] */ ] }`

**Use cases:**
- "Recurring orders" tab in the trading portal — show next-run timer per template.
- Dashboard widget: "You have 3 active recurring buys this month".

### POST /api/v3/me/recurring-orders

Create a recurring (weekly or monthly) Market-order template.

**Authentication:** Any JWT

**Request body:**
```json
{
  "listing_id": 7,
  "side": "buy",
  "quantity": 10,
  "account_id": 42,
  "interval": "monthly",
  "day_of_month": 15,
  "start_date_unix": 1731699200,
  "end_date_unix": 0
}
```

Validation:
- `side` ∈ `buy` / `sell`.
- `interval` ∈ `weekly` / `monthly`. `day_of_week` (0..6) is required for weekly; `day_of_month` (1..28) is required for monthly.
- `end_date_unix=0` means "no end".

**Response 201:** `{ "recurring_order": { /* RecurringOrderResponse */ } }`

**Use cases:**
- "Set up auto-invest" wizard on a stock detail page — preset to monthly + day-of-month-1 for paycheck timing.
- Treasury / corporate user defining weekly accumulation orders.

### GET /api/v3/me/recurring-orders/:id

Read one recurring order. Caller-scoped.

**Authentication:** Any JWT

**Use cases:** Edit-form prefill; deep-link from the activity feed.

### POST /api/v3/me/recurring-orders/:id/pause

Transition active → paused.

**Authentication:** Any JWT

**Response 200:** `{ "recurring_order": { /* with status=paused */ } }`

**Use cases:**
- "Hold for now" toggle when the user expects a short-term cash crunch (avoids the SKIPPED notification on the next tick).

### POST /api/v3/me/recurring-orders/:id/resume

Transition paused → active.

**Authentication:** Any JWT

**Use cases:** Resume a previously-paused template; common pattern is pause-while-vacation, resume-after.

### POST /api/v3/me/recurring-orders/:id/cancel

Permanently cancel (terminal state). No further ticks will execute.

**Authentication:** Any JWT

**Use cases:** "I'm done with this strategy" — terminate the template; differs from `delete` in that the audit trail is retained.

---

## 46. Recurring Fund Investments (Celina 4)

Monthly Dollar-Cost-Averaging template — every month on `day_of_month` the cron auto-invests `amount_rsd` from `source_account_id` into `fund_id`. Insufficient funds (or fund-no-longer-eligible) skip the tick with a `FUND_RECURRING_SKIPPED` push; successful contributions emit `FUND_RECURRING_EXECUTED`. The recurrence stays active across skips.

Personal to clients — employee tokens get an empty result list.

### GET /api/v3/me/recurring-funds

List my recurring fund-investment templates.

**Authentication:** Any JWT (client principal returns data; employee returns empty)

**Response 200:** `{ "recurring_funds": [ /* RecurringFundResponse[] */ ] }`

**Use cases:**
- "My auto-invest plans" page in the fund portal.
- Pre-investment screen: show existing DCA into the fund the user is about to invest into manually (avoid double-invest confusion).

### POST /api/v3/me/recurring-funds

Create a monthly DCA fund-investment template.

**Authentication:** Any JWT

**Request body:**
```json
{
  "fund_id": 7,
  "amount_rsd": "1000.0000",
  "source_account_id": 42,
  "day_of_month": 15
}
```

Validation:
- `day_of_month` must be 1..28.
- `fund_id` must point to an open fund OR a closed fund currently in the fundraising window (otherwise no tick can ever fire).
- `amount_rsd` must be ≥ the fund's `minimum_contribution_rsd`.

**Response 201:** `{ "recurring_fund": { /* RecurringFundResponse */ } }`

**Use cases:**
- "Auto-invest into this fund every month" CTA on the fund detail page.
- Closed-end fund onboarding: lock in a DCA during the fundraising window.

### GET /api/v3/me/recurring-funds/:id

Read one recurring fund investment. Caller-scoped.

**Authentication:** Any JWT

**Use cases:** Edit form / detail view.

### POST /api/v3/me/recurring-funds/:id/pause

Toggle `active=false`.

**Authentication:** Any JWT

**Use cases:** Vacation hold; cash-crunch pause without losing the template.

### POST /api/v3/me/recurring-funds/:id/resume

Toggle `active=true`.

**Authentication:** Any JWT

**Use cases:** Resume after a temporary pause.

### DELETE /api/v3/me/recurring-funds/:id

Permanently cancel.

**Authentication:** Any JWT

**Response 204:** Success.

**Use cases:** Wind down a DCA strategy.

---

## 47. OTC Marketplace

The OTC marketplace is the **option-contract** marketplace under `/api/v3/otc/options/...` — **with negotiation**. Any user can post an option listing; many other users can each open their own bid chain on the same listing; first-to-accept wins atomically and sibling chains cascade-cancel inside the same DB transaction. (The in-bank stock marketplace `/api/v3/otc/stocks/...` + `/api/v3/me/otc/stocks/...` — publish/fill standing share offers — was **removed 2026-06-11** along with `make-public` and `Holding.public_quantity`; option offers now serve as the cross-bank "stock" inventory on `/public-stock`.)

It supports local + cross-bank discovery (peer banks publish their listings via `/api/v3/cross-bank-protocol/public-stock` — the sole cross-bank discovery surface; each bank's stock-service polls every ~5 s and merges into an in-memory cache).

> **The bank is a first-class cross-bank OTC principal.** An employee acting **as the bank** (via the `bankIfEmp` group, which resolves `owner_type="bank"`) participates in the cross-bank option marketplace exactly like a client, settling against **BANK** accounts/holdings (owner sentinel `1000000000`):
> - **Bank-owned offers are biddable cross-bank.** When a bank-owned `OTCOffer` is published to peers, its `sellerId` is the stable wire identity `employee-<ActingEmployeeID>` (never the legacy literal `"bank"`); legacy/seed bank offers with no acting employee are not exposed cross-bank. A peer bank may bid on it.
> - **The bank can bid / counter / accept / reject / cancel / exercise cross-bank.** A bank-driven bid publishes `buyerId=employee-<ActingEmployeeID>`; later wire actions on that chain reuse the **row's** stored `employee-<N>` (a different acting employee keeps the same wire id). Settlement debits/credits BANK accounts and BANK holdings.
> - **Stable per-resource wire id.** The acting employee is persisted in the `acting_employee_id` column on the offer / mirror row; it is SI-TX **wire identity only** and is never used to look up an employee for ownership — local ownership/settlement always binds the BANK.
> - **The bank sees its own remote chains in every read view** — `ListMyNegotiations`, the per-listing `negotiations` view, history, timeline, and the `my_negotiation_id` stamp — matched by the `employee-<N>` prefix. Client and bank principal scopes never cross.
> - **Exercise strike-account gate.** The cross-bank exercise's caller-supplied `buyer_account_number` is gated by the gateway's `ResolveAndCheckAccountByNumber` (a bank-acting employee must bind a BANK account, else `403`), and stock-service re-asserts the bank settlement.
> - **Inbound back-compat.** A peer that still sends the legacy literal `"bank"` party id is parsed to bank ownership; the wire-conformant `employee-<N>` form is parsed identically (the numeric id is audit-only).

### 47.2 Options marketplace — parallel negotiation chains

Each OTC option listing (an `OTCOffer` posted by a seller or buyer) can accept many parallel **negotiation chains** in the new model. One bidder per chain; the chain has its own counter history and current terms. First chain to accept wins atomically; the parent listing flips to `consumed` and sibling chains cascade-cancel in the same transaction.

An option offer is **termless "optionable inventory"** keyed by `(owner, ticker, quantity)` — it carries **no** strike/premium/settlement_date of its own. Those terms are negotiated per chain (each bidder proposes their own on `POST /api/v3/otc/options/:id/bid`). On read surfaces the `strike_price` / `premium` / `settlement_date` fields are **viewer-contextual**, projected from the negotiation chain: a bidder sees their own chain's current terms; the owner sees their most recent counter; otherwise the fields are empty. A freshly-created offer with no negotiation thus shows empty terms.

#### POST /api/v3/me/otc/options

Create a **termless** OTC option listing. At most **one open offer per `(owner, ticker, direction)`** may exist — a duplicate open offer returns **409 conflict** (resize the existing one with `PUT /api/v3/me/otc/options/:id` instead of posting a second).

**Authentication:** `AnyAuthMiddleware` (client token, or employee acting as the bank / on behalf of a client) + `ResolveIdentity`.

**Request Body:**

| Field | Type | Description |
|---|---|---|
| `ticker` | string | Stock ticker symbol. Either `ticker` or `stock_id` is required; an unknown ticker ⇒ 400. |
| `stock_id` | int | Alternative to `ticker`. |
| `quantity` | string (decimal) | Total optionable quantity. Must be **> 0** and not exceed the owner's holding for the ticker. |
| `account_id` | int | The owner's account bound to the listing (ownership-verified gateway-side: a client must own it; an employee acting as the bank must supply a bank account, or an on-behalf client's account). |
| `direction` | string | `sell_initiated` (writer publishes shares to option out) or `buy_initiated` (publishes a standing demand). |

Term fields (`strike_price`, `premium`, `settlement_date`) are **no longer accepted** — the create contract dropped them; the created offer is always termless (terms are negotiated per chain).

**Response 201:** `{ "offer": OTCOfferResponse }` — the created listing (term fields empty/viewer-contextual).

**Response 400:** Validation (missing/zero quantity, unknown ticker, quantity above the owner's holding).

**Response 403:** `account_id` is not owned by the caller (or is not a bank account for an employee acting as the bank).

**Response 409:** An open offer for the same `(owner, ticker, direction)` already exists — resize it via `PUT /api/v3/me/otc/options/:id`.

---

#### POST /api/v3/otc/options/:id/bid

Open a new negotiation chain by placing the initial bid on an open listing. `:id` may resolve to a **LOCAL** listing (an `OTCOffer` this bank hosts) or a folded-in **REMOTE** listing (a peer-bank listing surfaced via the cross-bank discovery feed). The same route handles both — stock-service dispatches by the parent listing's routing (SP-2b):

- **LOCAL listing** — runs the intra-bank first-accept-wins negotiation path (unchanged). Returns a `kind=local` negotiation.
- **REMOTE listing** — composes the SI-TX `OtcOffer` (with the caller's `buyerId`, the listing's `sellerId`, the resolved `buyerAccountNumber`, and the listing's cascade-cancel `parentOfferId` lot key) and POSTs it to the seller's bank. Records a local **remote** negotiation mirror row and returns a `kind=remote` negotiation carrying the peer-assigned id. **Both a client AND the bank may bid cross-bank (SP-3):** a client bidder publishes `buyerId=client-<ownerID>`; an **employee acting as the bank** (the `bankIfEmp` group resolves the caller to bank ownership) publishes the stable `buyerId=employee-<actingEmployeeID>` and the bid settles against a **BANK** account — the gateway's `ResolveAndCheckAccount` ownership gate requires a bank-owned `bidder_account_id` for the bank principal (a non-bank account ⇒ 403), and stock-service re-asserts the bank settlement on the wire-identity path. Cross-bank SI-TX has **no FX**, so the bidder account's currency must equal the listing's premium currency.

**Authentication:** Any JWT + `securities.trade` OR `otc.trade.accept` + `ResolveIdentity`

**Path:** `:id` — the parent OTCOffer listing id (local or remote surrogate id).

**Request Body:**

| Field | Type | Description |
|---|---|---|
| `bidder_account_id` | int | Caller's account that will pay/receive premium on accept. For a remote listing it is re-validated (ownership, active, currency==listing premium currency) and its account number is threaded to the seller's bank. |
| `quantity` | string (decimal) | Initial bid quantity. Must be **> 0**. |
| `strike_price` | string (decimal) | Initial bid strike. Must be **> 0**. |
| `premium` | string (decimal) | Initial bid premium. Must be **>= 0** (zero allowed; negative rejected with 400). |
| `settlement_date` | string | RFC3339 or YYYY-MM-DD. **Must not be before today (UTC)** — a past settlement date is rejected with 400. |

The gateway validates `quantity`/`strike_price` as strictly positive, `premium` as non-negative decimals, and `settlement_date` as a parseable date **not before today** before forwarding (a malformed/non-positive amount or a past settlement date ⇒ 400). The same checks apply to the `counter` route below.

**Response 201:** `{ "negotiation": OTCNegotiationResponse }`. Status `open` (local) / `ongoing` (remote, peer status vocabulary). `kind` is `local` or `remote`.

**Response 400/403/409:** Validation, account-ownership, chain-already-exists (one chain per bidder per listing), or — for a remote listing — a premium-currency mismatch (SI-TX has no FX). A bank/employee-acting-as-bank bidder is **accepted** cross-bank (SP-3 lifted the earlier deferral); the bid publishes `employee-<N>` and settles against a bank account.

**Response 412:** Parent listing is no longer open (consumed, cancelled, or expired).

---

> **Unified local + cross-bank dispatch.** The four per-chain mutation
> routes below — `counter`, `accept`, `reject`, and the `DELETE …/:nid` cancel —
> each handle a **LOCAL** chain (this bank hosts the listing) and a **REMOTE**
> chain (a peer bank hosts the listing) on the **same** route. stock-service
> dispatches on the chain's parent listing routing: a local chain runs the
> intra-bank path, a remote chain is forwarded to the peer over SI-TX and the
> local mirror row is updated to match.

#### POST /api/v3/me/otc/options/:id/negotiations/:nid/counter

Counter the current terms on one of the caller's chains. Either party (the chain's bidder OR the listing's poster) may counter. Handles local and remote chains uniformly (see the dispatch note above).

**Request Body:** new `{ quantity, strike_price, premium, settlement_date }`. `settlement_date` must not be before today (UTC) — a past date is rejected with 400.

**Response 200:** updated `OTCNegotiationResponse`. Status flips to `countered`. Snapshot terms updated; a new COUNTER revision is appended to the chain's history.

**Response 403:** Caller is neither the bidder nor the listing poster.

---

#### POST /api/v3/me/otc/options/:id/negotiations/:nid/accept

Accept the current terms on a chain. Caller must be the party **opposite** to whoever proposed the current terms (i.e. you cannot "accept your own offer" — you have to wait for the other side's response).

**Phase 9: now two-stage.** The accept performs negotiation state transitions in a DB transaction AND immediately runs the 4-step contract-formation saga (mints the `OptionContract` row, reserves the seller's underlying shares, reserves+settles the buyer's premium, credits the seller). The safety invariants — **cannot sell what you don't have / cannot buy if you don't have money** — are enforced by the saga; if the seller's holding has dropped below `quantity` OR the buyer's balance has dropped below `premium`, the saga aborts and the negotiation is flipped to a terminal `failed` status so the front-end sees coherent state.

**Request Body:**
```json
{
  "acceptor_account_id": 42,
  "on_behalf_of_fund_id": 0
}
```

| Field | Type | Description |
|---|---|---|
| `acceptor_account_id` | int | Caller's account — pays the premium if accepter is the buyer (parent direction = `sell_initiated`); receives the premium if accepter is the seller (parent direction = `buy_initiated`). Currency must match the seller's account currency or a cross-currency FX conversion is performed via exchange-service. |
| `on_behalf_of_fund_id` | int | *Optional.* When non-zero, places this accept on behalf of an investment fund (E2, Plan E). `acceptor_account_id` **must** equal the fund's RSD account. Caller must be the fund's manager (`acting_employee_id` = `fund.manager_employee_id`). The resulting `OptionContract` records `on_behalf_of_fund_id` so that exercise credits `fund_holdings`. |

**Stage 1 — negotiation state TX (first-accept-wins):**
1. `SELECT FOR UPDATE` on the winning negotiation row.
2. `SELECT FOR UPDATE` on the parent OTCOffer listing.
3. Reject with 412 if parent is no longer open (a parallel sibling already won — your call serialised behind theirs and lost).
4. Verify the caller is the opposite party to `last_action_by_owner_*`.
5. Flip the winning negotiation to `accepted` + append ACCEPT revision.
6. Flip parent listing to `consumed`.
7. `SELECT FOR UPDATE` every sibling chain in `open`/`countered` status and flip them to `cancelled` (cascade).

**Stage 2 — contract-formation saga (runs after the TX commits):**
1. `reserve_and_contract` — create `OptionContract` row + `ReserveForOTCContract` on seller's holding. **Seller-can-deliver check**: aborts if seller no longer has free shares; contract row is deleted in the Backward path.
2. `reserve_premium` — `ReserveFunds` on buyer's account. **Buyer-has-cash check**: aborts if buyer's balance dropped below premium; seller's share reservation released.
3. `settle_premium_buyer` — `PartialSettleReservation` (debits the premium from the buyer's reservation).
4. `credit_premium_seller` — `CreditAccount` on seller (in seller's currency).

**Response 200:**
```json
{
  "winning":             OTCNegotiationResponse,
  "parent_offer_id":     123,
  "parent_status":       "consumed",
  "cancelled_siblings":  [OTCNegotiationResponse...],
  "contract": {
    "id":                17,
    "offer_id":          123,
    "buyer_owner_type":  "client",
    "buyer_owner_id":    7,
    "seller_owner_type": "client",
    "seller_owner_id":   42,
    "ticker":            "AAPL",
    "quantity":          "10",
    "strike_price":      "175.50",
    "premium_paid":      "700.00",
    "premium_currency":  "USD",
    "strike_currency":   "USD",
    "settlement_date":   "2027-08-01T00:00:00Z",
    "buyer_account_id":  42,
    "seller_account_id": 99,
    "status":            "ACTIVE",
    "premium_paid_at":   "2026-05-16T03:20:00Z"
  },
  "cross_bank_transaction_id": ""
}
```

`contract` is `null` when the formation saga failed; in that case the negotiation status is `failed`, the parent stays `consumed`, and the front-end can surface a "contract not formed" warning + suggest re-listing.

`cross_bank_transaction_id` (optional) is populated **only when the accepted `:nid` resolves to a folded-in cross-bank (REMOTE) negotiation chain**: it carries the peer bank's SI-TX `transactionId` returned by the peer's `GET .../accept`, so the FE can poll cross-bank settlement via `GET /me/otc/transactions/:txid/status` during the accept→contract-mirror window. It is an empty string for a LOCAL accept (no cross-bank transaction) and for a remote accept whose peer body did not include a decodable `transactionId`.

**Exercise:** the minted `OptionContract` row is consumable by `POST /api/v3/otc/contracts/:id/exercise` (see existing exercise route) — strike money moves buyer→seller, the reserved seller shares are consumed and credited to the buyer's holding. When the contract has `on_behalf_of_fund_id` set, the `exercise` endpoint also accepts `on_behalf_of_fund_id` in the request body (same fund-manager validation applies) and shares land in `fund_holdings`.

**Fund-on-behalf accept (`on_behalf_of_fund_id`):**
When this field is non-zero:
- `acceptor_account_id` **must** equal the fund's `rsd_account_id`.
- The acting employee must be the fund's manager.
- The minted contract records `on_behalf_of_fund_id`; on exercise, shares land in `fund_holdings` instead of personal holdings.
- Returns 403 `fund_not_managed_by_actor` if manager check fails.
- Returns 400 `acceptor_account_id must equal fund RSD account for fund orders` if account mismatch.

**Response 400:**
- `acceptor_account_id` missing or zero.
- `ErrOTCAcceptorAccountRequired` from the service if the gRPC layer is reached without it.

**Response 403:**
- Caller proposed the current terms (`ErrOTCAcceptUnauthorized`) or is not a party to the chain.
- `acceptor_account_id` does not belong to caller (gateway-side ownership check).
- `on_behalf_of_fund_id` set but acting employee is not the fund manager.

**Response 412:**
- Parent listing no longer open OR negotiation is in a terminal state.
- Settlement date is no longer in the future.
- Seller short on shares at saga step 1 / buyer short on cash at saga step 2 — negotiation flipped to `failed`.
- Cross-currency premium without exchange-service wired.

---

#### POST /api/v3/me/otc/options/:id/negotiations/:nid/reject

Reject a chain. Either party may reject; ends that chain only — the parent listing stays open, other chains continue.

**Response 200:** updated `OTCNegotiationResponse` with status `rejected`.

**Response 403:** Caller is neither a party nor the listing's poster.

---

#### DELETE /api/v3/me/otc/options/:id/negotiations/:nid

Cancel (withdraw) the caller's own chain. **Bidder-only** — the listing's poster cannot cancel a bidder's chain (use reject for that).

**Response 204:** Status flipped to `cancelled`.

**Response 403:** Caller is not the chain's bidder (`ErrOTCCounterUnauthorized` — naming holdover from the counter route; same sentinel).

---

#### PUT /api/v3/me/otc/options/:id

Edit the **total quantity** of the caller's own open option offer. An option offer is termless inventory `(owner, ticker, quantity)`; since only one open offer per `(owner, ticker, direction)` is allowed, the owner edits the total rather than posting a second offer. The supplied quantity **SETS** the new total (up or down).

**Auth:** `AnyAuthMiddleware` (client token, or employee acting as the bank / on behalf of a client).

**Path parameters:**
- `id` — the offer's surrogate id.

**Request body:**

```json
{ "quantity": "80" }
```

- `quantity` (string decimal, required) — the new TOTAL quantity. Must be `> 0`.

**Validation / business rules (enforced authoritatively in stock-service under the offer's row lock):**
- `quantity > 0` (gateway pre-checks and returns 400; service re-validates).
- Not **below** the shares already committed to formed/forming contracts on this offer (Σ quantity of `accepted` negotiation chains whose `parent_offer_id = id`). For an open offer this floor is normally 0 (an accept consumes the listing).
- Not **above** the owner's holding for the ticker, net of the owner's other active commitments (the offer being resized does not count against itself).
- **Owner-only:** only the offer's initiator may edit.
- The offer must be **local** and **open** (`open`/`PENDING`/`COUNTERED`).
- Optimistic-lock safe (`SELECT FOR UPDATE` + version check).

**Example request:**

```
PUT /api/v3/me/otc/options/6
Content-Type: application/json

{ "quantity": "80" }
```

**Response 200:** `{ "offer": OTCOfferResponse }` with the updated `quantity`.

**Response 400:** `quantity` missing, unparseable, non-positive, below the committed floor, or above the owner's holding.

**Response 403:** Caller is authenticated but is not the offer's owner.

**Response 404:** Offer does not exist (or is a remote mirror row, which is not editable here).

**Response 409:** Offer is not open for edit (already accepted/consumed/cancelled/expired), or an optimistic-lock conflict.

---

#### DELETE /api/v3/me/otc/options/:id

Cancel (withdraw) the caller's own OPEN parent listing. **Initiator-only** — only the poster can cancel; bidders use the per-chain DELETE above. The listing's status flips to `cancelled` and every still-open child negotiation chain cascade-cancels in the same DB transaction. Each cascaded chain's bidder receives an `OTC_OFFER_CASCADE_CANCELLED` notification with `reason="listing_cancelled"`.

No funds or shares are unwound — none are reserved at listing-creation time; reservations only exist inside the accept saga, which can no longer run on a cancelled listing.

**Response 204:** Listing cancelled. No body.

**Response 403:** Caller is authenticated but is not the listing's initiator (e.g. they are the bound counterparty or a stranger). As of 2026-06-05 this also covers a non-participant acting on an existing offer they don't own — `GetOffer` is now public, so the gateway pre-check fetches the offer and the initiator-only check returns 403 (OTC offers are already publicly discoverable, so this confirms nothing a stranger could not learn from `GET /api/v3/otc/options`).

**Response 404:** Offer does not exist (neither a local listing nor a remote mirror row for `id`).

**Response 409:** Listing is no longer open (already accepted/consumed, already cancelled, expired, etc.).

---

#### GET /api/v3/otc/options/:id/negotiations

List the negotiation chains against a listing. `:id` is the stable surrogate id from the discovery feed and may resolve to a **LOCAL** listing (an `OTCOffer` this bank hosts, `routing_number=own`) or a **REMOTE** listing (a folded-in remote `OTCOffer` row mirroring a peer-bank listing, `routing_number=<peer>`; SP-2a). (SP-1 Task 8b extends this to remote ids.)

**Authentication:** Any JWT + `ResolveIdentity`.

**LOCAL `:id` — unchanged audience + behavior.** Returns **every** chain on the listing (any status). **Restricted audience:** only the listing's poster (a client whose `principal_id` matches the offer's initiator) or an employee holding the `otc.read.all` permission may call this. A competing bidder — or any other client — receives **403**; bidders see only their own chain via `GET /api/v3/me/otc/options/negotiations`. Each item is now stamped `kind="local"` + own `routing_number` / `bank_code`; `me_owner` is `false` (the field reflects the *chain's* bidder ownership, not the listing's — a bidder is never the owner).

> **LOCAL listing — peer bids included for BOTH bank- and client-owned listings (2026-06-06 local/remote parity).** When a **peer** bank bids on a LOCAL listing we host, that bid lives as a REMOTE chain where we host the seller (the listing's poster). The response merges those peer bids for the owner-side view, correlated to this listing by the remote chain's `(remote_parent_routing, remote_parent_native_id)` lot key == the offer's `(routing_number, native_id)`, so only bids on *this* listing appear (each `kind="remote"`, `me_owner=true` because the owner hosts the seller). The seller principal is derived from the listing's **initiator**: a bank-owned listing matches `employee-<N>` seller chains; a **client-owned** listing matches the poster's `client-<initiatorID>` seller chains — so both the poster and a permission-gated employee (`otc.read.all`) see the cross-bank bids. *(Previously only bank-owned listings merged peer bids; client-owned listings showed local chains only.)*

**REMOTE `:id` — caller's own chain(s) only.** We do not host the listing, so we can only surface the **caller's own** chain(s) against it — never other parties' chains. Returns the caller's remote negotiation rows whose `(remote_parent_routing, remote_parent_native_id)` lot key matches the mirror's `(routing_number, native_id)`, each stamped `kind="remote"` with counterparty provenance and `me_owner` per the seller-side rule. If the caller has no chain on it → **empty list** (not 403/404). **Both a client AND the bank have a cross-bank bidder identity:** a client matches its exact `client-<N>` chains; an employee acting as the bank matches its `employee-<N>` bid chains (prefix-matched). Any other caller yields an empty list. The two principal scopes never cross. *(2026-06-06: this path now fires reliably — the per-listing read no longer pre-empts the remote fallback when the folded-in mirror offer shares the local `otc_offers` table.)*

**Response 200:** `{ "negotiations": [OTCNegotiationResponse...], "total": int }`.

**Response 403:** (LOCAL only) caller is neither the listing's poster nor a permission-gated employee.

**Response 404:** `:id` is neither a local offer nor a remote mirror.

---

#### GET /api/v3/otc/options/:id/timeline

Cross-chain interaction timeline for an offer. `:id` may resolve to a **LOCAL** listing or a **REMOTE** mirror (SP-1 Task 8b extends this to remote ids).

**LOCAL `:id` — local chains PLUS remote peer bids (2026-06-06 local/remote parity):** the offer plus **every** negotiation chain merged into a single stream sorted ascending by `created_at`. This includes both the LOCAL chains' full per-revision history AND the REMOTE chains a peer placed on this listing — each now also expanded into its **full recorded history** (one entry per BID/COUNTER/ACCEPT/REJECT, with `action_by_wire_id` carrying the mover's opaque id; legacy remote chains with no recorded revisions fall back to a single current-terms entry) — correlated to this listing by the same `(remote_parent_routing, remote_parent_native_id)` lot key as the per-listing view. This is the offer-owner "front page" view — one call returns the whole offer's history across all bidders, local and cross-bank, so the frontend never needs to fan out per chain. **Restricted audience:** identical to `GET /api/v3/otc/options/:id/negotiations` — listing poster or employee with `otc.read.all` only. Competing bidders receive **403**. *(Previously the timeline showed LOCAL chains only; remote peer bids on the listing were omitted.)*

**REMOTE `:id` — caller's own chain(s) only:** we do not host the listing, so the timeline surfaces only the **caller's own** chain(s) against it (never other parties'). The folded-in remote `OTCOffer` row provides the `offer` header (`kind="remote"`); each of the caller's matching peer chains (lot key `(remote_parent_routing, remote_parent_native_id)` == remote row `(routing_number, native_id)`) expands into its **full recorded history** — one entry per move (BID/COUNTER/ACCEPT/REJECT) — merged chronologically. Each remote entry carries `action_by_principal_type` = the mover's role (`buyer`/`seller`) and `action_by_wire_id` = the mover's opaque SI-TX id (`client-N`/`employee-N`/`bank`). A chain created before history logging (2026-06-06) with no recorded revisions falls back to a single current-terms entry. No matching chain → offer header + **empty timeline** (not 403/404). **Both a client and the bank have a cross-bank bidder identity** (client matches exact `client-<N>`; the bank matches `employee-<N>` prefix).

**Path:** `:id` — the surrogate listing id (local OTCOffer or folded-in remote OTCOffer row).

**Response 200:**

```json
{
  "offer": { /* OTCOfferResponse */ },
  "timeline": [
    {
      "negotiation_id":           100,
      "bidder_owner_type":        "client",
      "bidder_owner_id":          7,
      "revision_number":          1,
      "action":                   "BID",
      "quantity":                 "10",
      "strike_price":             "150.00",
      "premium":                  "5.00",
      "settlement_date":          "2026-07-01T00:00:00Z",
      "action_by_principal_type": "client",
      "action_by_principal_id":   7,
      "created_at":               "2026-06-01T12:00:00Z"
    },
    {
      "negotiation_id":           100,
      "bidder_owner_type":        "client",
      "bidder_owner_id":          7,
      "revision_number":          2,
      "action":                   "COUNTER",
      "quantity":                 "10",
      "strike_price":             "155.00",
      "premium":                  "7.00",
      "settlement_date":          "2026-07-01T00:00:00Z",
      "action_by_principal_type": "client",
      "action_by_principal_id":   1,
      "created_at":               "2026-06-01T12:05:00Z"
    }
  ]
}
```

Entries are ordered by `created_at ASC`; ties break by `(negotiation_id, revision_number)` for deterministic ordering (LOCAL only).

**Response 403:** (LOCAL only) caller is neither the listing's poster nor a permission-gated employee.

**Response 404:** `:id` is neither a local offer nor a remote mirror.

---

#### GET /api/v3/me/otc/options/negotiations

Returns a **unified list** of the caller's negotiation chains — both LOCAL
(intra-bank chains where the caller is the bidder) and REMOTE (cross-bank
peer chains where the caller is a party) — merged into one `negotiations`
array. Each item carries provenance (`kind` / `routing_number` /
`bank_code`) plus `me_owner`. (SP-1 Task 7)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `statuses` | string | Comma-separated filter: `open,countered,accepted,rejected,cancelled,expired` (applied to both local and remote items) |
| `page` | int | Default 1 |
| `page_size` | int | Default 20, max 200 |

**`OTCNegotiationResponse` shape:** in addition to the existing fields and
`minted_contract_id` (uint64, 0 when absent; populated on `status=accepted`
rows that successfully minted a contract), every item now carries:

| Field | Type | Description |
|---|---|---|
| `kind` | string | `local` (intra-bank chain) or `remote` (cross-bank peer chain) |
| `routing_number` | int64 | Owning bank's routing. For `local`: our own routing. For `remote`: the COUNTERPARTY/peer bank's routing (the side we do not host). |
| `bank_code` | string | Owning/peer bank's code, matching `routing_number`. |
| `me_owner` | bool | `true` ONLY when the caller is the parent listing's poster/seller (someone is bidding on MY listing). A chain the caller opened **as the bidder** is `false`. For `remote`: `true` iff WE host the seller/poster side (`seller_routing == own_routing`). |

**Viewer-relative action hints** (computed per caller, like `me_owner`) — the FE renders buttons directly from these without re-deriving turn rules. Omitted-when-false (treat an absent flag as `false`):

| Field | Type | Description |
|---|---|---|
| `viewer_role` | string | The caller's side on this chain: `"bidder"` or `"poster"` (omitted/`""` when the caller is neither — e.g. an employee browsing a client's listing read-only). |
| `last_action_mine` | bool | The caller authored the chain's latest revision (it is currently the counterparty's turn). |
| `awaiting_viewer` | bool | It is the caller's turn — the chain is live (`open`/`countered`) AND the OTHER side made the last move. |
| `can_accept` | bool | The caller may accept the current terms. **Turn-based** (`== awaiting_viewer`): only the party who did NOT make the latest offer may accept it, and only the latest offer. You can never accept your own standing offer. |
| `can_counter` | bool | The caller may post a counter. **NOT turn-based** (`== chain is live`): either party may counter at any time while the chain is `open`/`countered`; a new counter supersedes prior offers (they become non-acceptable). So the side that just bid/countered can still place a new counter while awaiting a reply. |
| `can_reject` | bool | The caller may reject (decline) the latest offer. **Turn-based** (`== awaiting_viewer`): surfaced to the receiver of the latest offer (either role). The maker instead supersedes with a counter, or — if the bidder — withdraws. |
| `can_withdraw` | bool | The caller (the **bidder**) may withdraw their own chain while it is live. The poster cancels the whole listing instead, so this is never set for the poster. |

The same `viewer_role` / `last_action_mine` / `awaiting_viewer` / `can_*` block is added to `GET /api/v3/otc/options/:id/negotiations` (poster's view of bids on a listing).

For `remote` items, `id` is the **local surrogate primary key** of this
bank's peer-negotiation mirror row (so callers correlate within this
bank's id namespace), and the terms (`quantity`, `strike_price`, `premium`,
`settlement_date`, `status`) are projected from the mirrored cross-bank
offer.

**Paging note:** `page`/`page_size` paginate the LOCAL set; REMOTE chains
are appended in full after the local page (never silently truncated).
`total` reflects the local total only.

**Remote-merge principal scope (SP-3 Task 5b):** the REMOTE merge is keyed on
the caller's cross-bank bidder identity. A **client** caller matches its exact
wire principal `client-<N>`. A caller acting **as the bank** (`owner_type="bank"`,
an employee) now also gets the bank's own cross-bank **bid** chains — matched by
the `employee-<N>` prefix (the bank has no single wire principal across chains).
The two scopes never cross: a client never receives bank chains, and a bank
never receives client chains.

---

#### GET /api/v3/me/otc/options/negotiations/:nid/revisions

Retrieve the full revision chain (bid, counter, counter, accept/reject) for a single negotiation. `:nid` may be a **LOCAL** chain or a folded-in **REMOTE** (cross-bank) chain — both return their full recorded history (2026-06-06 parity). For a LOCAL chain either the bidder or the parent listing's poster may call this; a third-party gets 403. For a REMOTE chain the caller must be the party WE host on it (the hosted `client-<N>`, or an employee acting as the bank for an `employee-<N>` side); a non-party gets **404** (existence is not leaked cross-party).

**Authentication:** Any JWT + `ResolveIdentity` (AnyAuth — clients and employees accepted)

**Path:** `:nid` — the negotiation chain id (local surrogate id, including the local id of a remote mirror chain).

**Response 200:**

```json
{
  "revisions": [
    {
      "id":                       1,
      "negotiation_id":           5,
      "revision_number":          1,
      "action":                   "BID",
      "quantity":                 "10",
      "strike_price":             "150.00",
      "premium":                  "7.50",
      "settlement_date":          "2026-07-01T00:00:00Z",
      "action_by_principal_type": "client",
      "action_by_principal_id":   42,
      "action_by_wire_id":        "",
      "created_at":               "2026-06-01T12:00:00Z",
      "mine":                     true,
      "is_latest":                false
    }
  ]
}
```

Revisions are ordered by `revision_number ASC`. For **remote** chains, `action_by_principal_type` is the mover's role (`buyer`/`seller`), `action_by_principal_id` is `0`, and `action_by_wire_id` carries the mover's opaque SI-TX id (`client-N`/`employee-N`/`bank`). For **local** chains `action_by_wire_id` is an empty string.

Each revision also carries two **viewer-relative** flags (computed per caller): `mine` (`bool` — the caller authored this revision) and `is_latest` (`bool` — this is the chain's most recent revision). The FE shows Accept/Counter when the latest revision is NOT `mine` and the chain is live. The same `mine`/`is_latest` flags are added to each entry of `GET /api/v3/otc/options/:id/timeline` (for the timeline, `is_latest` is per-chain — the most recent revision of each `negotiation_id` in the merged stream).

**Response 403:** (LOCAL chain) caller is neither the bidder nor the listing's poster.

**Response 404:** Negotiation not found, or (REMOTE chain) the caller is not a party to it.

---

#### GET /api/v3/me/otc/options

Marketplace view of the caller's OWN open OTC option listings. Returns the **same response shape as `GET /api/v3/otc/options`** (`kind` / `bank_code` / `routing_number` / `offer_id` / `seller_id` / `direction` / `ticker` / `amount` / viewer-contextual `strike_price` / `strike_currency` / `premium` / `premium_currency` / `settlement_date` / `created_at` / optional `best_bid` / optional `best_ask` / optional `active_chains_count`), filtered to listings whose seller id matches the caller's SI-TX identity (`client-<principal_id>` for clients, `bank` for bank-on-behalf calls). Only listings in an OPEN status appear here — the unified cache is open-only. For full history (cancelled / accepted / expired listings the caller posted) use `GET /api/v3/me/otc/options/posted`.

**Query Parameters:** same `ticker` / `direction` / `page` / `page_size` as `GET /api/v3/otc/options`. `kind` and `bank_code` are accepted but redundant (results are always `kind=local` by definition).

**Response 200:** `{ "offers": [...], "total_count": int, "peers_total": 0, "peers_reached": 0, "partial": false, "last_refresh": "..." }`. (Peer fields are always zero/false on this endpoint since cross-bank listings can never be the caller's.)

---

#### GET /api/v3/me/otc/options/posted

History view: every OTC option listing the caller has ever posted, **any status** (open, consumed, accepted, rejected, expired, cancelled). Unlike `GET /api/v3/me/otc/options` this returns the raw `OTCOfferResponse` rows (with `revisions`, `last_modified_by`, etc.), not the marketplace shape.

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `statuses` | string | Comma-separated filter: `open,countered,consumed,accepted,rejected,cancelled,expired,failed` |
| `page` | int | Default 1 |
| `page_size` | int | Default 20 |

**Response 200:** `{ "offers": [OTCOfferResponse...], "total": int }`.

---

#### GET /api/v3/otc/options/:id

Resolve a single OTC option offer by its **stable surrogate id** — the `local_id` surfaced on every row of `GET /api/v3/otc/options`. Works for both **local** offers (this bank's listings) and **remote** offers (a peer-bank listing mirrored locally). The handler first tries the local lookup; if that 404s it falls back to the persistent remote mirror, so a frontend can address any discovered offer — local or remote — by the same id.

**Authentication:** Any JWT + `ResolveIdentity`.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | The surrogate id from the discovery feed (`local_id`) |

**Response 200 — local offer** (backward-compatible: the existing `offer` / `revisions` body is preserved, with decoration fields added):
```json
{
  "offer":    { "...": "OTCOfferResponse", "kind": "local", "me_owner": true,
                "seller_id": "bank",
                "my_negotiation_id": 88, "my_negotiation_status": "open" },
  "revisions": [ "..." ]
}
```
- `kind` is always `"local"` here.
- **Public read (2026-06-05):** the offer body is readable by **any** authenticated caller, mirroring the unified discovery list (`GET /api/v3/otc/options`) — a caller that can see an offer in the list can read its detail. A **non-participant** receives the offer with `me_owner=false` and an **empty `revisions[]`** (the negotiation/counter history stays gated to participants — the listing's poster/seller or the chain's bidder). Reading a non-owned offer triggers no read-receipt. (Previously a non-participant got a `not_found` masked as 500; that reject path is removed.)
- `me_owner` is `true` when the acting identity owns the listing (client whose `seller_id` is `client-<their owner id>`, or an employee acting as the bank on a `bank`-owned listing), else `false`.
- **`seller_id`** — the LOCAL read view's SI-TX seller identity of the offer's initiator: `"bank"` for a bank-owned listing, `"client-<N>"` for a client-owned one. The same value the unified marketplace listing surfaces, now stamped uniformly on every single-offer response (create / detail / counter / cancel). Distinct from the cross-bank wire id `"employee-<N>"`, composed only on the SI-TX publish path.
- **`my_negotiation_id` / `my_negotiation_status` (SP-2b, 2026-06-05):** when the authenticated caller has an own (bidder) negotiation chain against this offer, these carry that chain's surrogate id + status so the FE can jump straight to its chain. **Omitted/0 / "" when the caller has no bidder chain** — note a poster who never bid on their own listing is `me_owner=true` but has NO `my_negotiation_id` (the two are independent). When several chains exist on one offer the **active** one wins: an accepted chain beats a live (`open`/`countered`/`ongoing`) one beats a terminal one; ties break to the most recently created. Works for local and remote offers.
- **Viewer-relative action hints (4.7.0):** this detail response also carries `viewer_role` / `last_action_mine` / `awaiting_viewer` / `can_bid` / `can_accept` / `can_counter` / `can_reject` / `can_withdraw`, computed per caller from their own bidder chain — identical to the marketplace-row flags documented under `GET /api/v3/otc/options`. The poster of the listing (`me_owner`) gets only `viewer_role="poster"`.

**Response 200 — remote (cross-bank) offer** (resolved from the mirror; flat shape):
```json
{
  "id":               7,
  "kind":             "remote",
  "me_owner":         false,
  "offer_id":         "off-9",
  "bank_code":        "222",
  "routing_number":   222,
  "seller_id":        "client-3",
  "direction":        "sell_initiated",
  "ticker":           "AAPL",
  "amount":           50,
  "strike_price":     "180.50",
  "strike_currency":  "USD",
  "premium":          "700.00",
  "premium_currency": "USD",
  "settlement_date":  "2026-12-31T00:00:00Z",
  "status":           "open",
  "created_at":       "2026-05-10T14:00:00Z"
}
```
- `kind` is always `"remote"` and `me_owner` is always `false` (a peer hosts the listing — it is never ours).
- `status` is `open` or `cancelled` (cancelled mirror rows are still returned so the FE can render a terminal state rather than a 404).
- `my_negotiation_id` / `my_negotiation_status` (SP-2b) are present here too when the caller has bid on this remote offer cross-bank (matched on the chain's remote parent routing+native id); omitted/0/"" otherwise. Both a **client** caller (its `client-<N>` chain) and a caller acting **as the bank** (its `employee-<N>` bid chain, SP-3 Task 5b) are stamped; the two principal scopes never cross.

**Response 400:** `{ "error": { "code": "validation_error", "message": "invalid id" } }`
**Response 404:** `{ "error": { "code": "not_found", "message": "OTC offer not found" } }` — neither a local offer nor a remote mirror row exists for `id`.

---

#### GET /api/v3/otc/options

Unified cross-bank discovery view: every open OTC option listing on this bank + every peer bank's open listings (refreshed every ~5 s by the OptionRefresher). Filterable, paginated, partial-failure tolerant (the cache exposes `peers_total` / `peers_reached` / `partial=true` if some peers were unreachable in the last refresh).

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `ticker` | string | Filter to one ticker (case-insensitive) |
| `kind` | string | `local` or `remote` |
| `bank_code` | string | Filter to one bank (e.g. `222`) |
| `direction` | string | `sell_initiated` or `buy_initiated` |
| `page` | int | Default 1 |
| `page_size` | int | Default 10 |

**Response 200:**
```json
{
  "offers": [
    {
      "kind":                "remote",
      "bank_code":           "222",
      "routing_number":      222,
      "local_id":            17,
      "offer_id":            "42",
      "seller_id":           "client-7",
      "seller_name":         "",
      "direction":           "sell_initiated",
      "ticker":              "AAPL",
      "amount":              50,
      "strike_price":        "180.50",
      "strike_currency":     "USD",
      "premium":             "700.00",
      "premium_currency":    "USD",
      "settlement_date":     "2026-12-31T00:00:00Z",
      "created_at":          "2026-05-10T14:00:00Z",
      "best_bid":            "850",
      "active_chains_count": 3,
      "my_negotiation_id":     88,
      "my_negotiation_status": "open"
    }
  ],
  "total_count":   1,
  "peers_total":   2,
  "peers_reached": 2,
  "partial":       false,
  "last_refresh":  "2026-05-16T02:50:00Z"
}
```

**SP-1 provenance + ownership fields (2026-06-04):** Every item in `offers[]` carries:

| Field | Type | Notes |
|---|---|---|
| `kind` | string | `"local"` (this bank's listing) or `"remote"` (peer-bank mirror). **Derived from `routing_number`:** `kind="local"` iff `routing_number == OwnRouting()`; `kind="remote"` otherwise. There is no `kind` column in the DB — it is a computed, frontend-only label. |
| `routing_number` | int64 | Bank routing number identifying the hosting bank. |
| `bank_code` | string | 3-digit bank code. |
| `local_id` | uint64 | Stable local surrogate id — the folded-in remote `OTCOffer.id` for remote rows (SP-2a); the numeric offer id for local rows. Use this as `:id` in `GET /api/v3/otc/options/:id`. |
| `me_owner` | bool | `true` when the acting caller is the listing's poster/seller. Always `false` for remote rows. Omitted (falsy) when not owned. |
| `strike_price` / `premium` / `settlement_date` | string | **Viewer-contextual** — projected from the negotiation chain, NOT stored on the (termless) offer: a bidder sees their own chain's current terms; the owner sees their most recent counter; otherwise empty. A freshly-created offer with no negotiation shows empty terms. (The legacy `has_preset_terms` flag was removed — there is no preset/no-preset distinction anymore; the bidder always proposes the terms on their chain.) |

**SP-2b caller's-own-chain fields (2026-06-05):** Each item the authenticated caller has an own (bidder) negotiation chain against also carries:

| Field | Type | Notes |
|---|---|---|
| `my_negotiation_id` | uint64 | Surrogate id of the caller's own (as **bidder**) negotiation chain on this offer, so the FE can jump straight to its chain. **Omitted when the caller has no chain here** (0). A poster who never bid is `me_owner=true` but has no `my_negotiation_id` — the two are independent. Works for local and remote offers (remote chains match on the chain's remote parent routing+native id). When multiple chains exist, the **active** one wins (accepted > live `open`/`countered`/`ongoing` > terminal; ties → most recently created). |
| `my_negotiation_status` | string | That chain's status. Omitted when `my_negotiation_id` is absent. |

**Viewer-relative ACTION hints on each row (4.7.0).** So the FE can render Bid / Counter / Accept / Reject / Withdraw buttons **directly on the marketplace row** (and on `GET /api/v3/otc/options/:id`) without a separate negotiation fetch. Computed per caller from their own bidder chain on the listing; omitted-when-false (treat an absent flag as `false`). The semantics match the per-chain flags on the negotiations endpoints, applied to the caller's chain:

| Field | Type | Notes |
|---|---|---|
| `viewer_role` | string | `"bidder"` (caller has a chain on this listing), `"poster"` (`me_owner` — their own listing), or `""` (read-only/non-party). |
| `last_action_mine` | bool | The caller authored their chain's latest offer (it's the counterparty's turn). |
| `awaiting_viewer` | bool | The caller's turn — chain is live AND the other side moved last. |
| `can_bid` | bool | The caller has **no live chain** on this **open** listing → may open a bid. (Mirrors the existing "Bid vs Counter" rule: `can_bid` ⇔ `my_negotiation_id` absent.) |
| `can_accept` | bool | May accept the latest offer (`== awaiting_viewer`) — only the party who didn't make it. |
| `can_counter` | bool | May counter (chain is live; **not** turn-based — either party can counter any time). |
| `can_reject` | bool | May reject the latest offer (`== awaiting_viewer`). |
| `can_withdraw` | bool | The bidder may withdraw their own live chain. |

For the listing's own **poster** (`me_owner`) only `viewer_role="poster"` is set — the poster acts on individual bids via `GET /api/v3/otc/options/:id/negotiations` (the Activity panel), not on the row.

**Best-bid / best-ask surface (Part A 2026-05-16).** Three optional fields surface aggregated active-chain pricing so a prospective bidder sees that competition is live before placing an offer at the seller's static ask:

| Field | When present | Meaning |
|---|---|---|
| `best_bid` | parent `direction = sell_initiated` AND ≥1 active chain | MAX premium across `open`/`countered` chains — the going buy-side bid |
| `best_ask` | parent `direction = buy_initiated` AND ≥1 active chain | MIN premium across `open`/`countered` chains — the lowest seller is willing to accept |
| `active_chains_count` | ≥1 active chain | Count of `open` or `countered` chains on the listing |

All three are **omitted from the JSON when no active chains exist** (or when the row is remote and the peer bank doesn't publish them — graceful older-bank compat: their offers just don't show the surface and the FE renders "—"). Re-aggregated on every cache refresh (~5 s), so a freshly-placed counter shows up within one tick.

After picking a remote offer, bidders drive negotiation via the unified `POST /api/v3/otc/options/:id/bid` (using the discovered row's `local_id` as `:id`); stock-service dispatches the cross-bank negotiation to the seller's bank.

#### Cross-bank cascade-cancel on accept (Phase 10)

Mirrors the intra-bank Phase 2 first-accept-wins cascade for cross-bank negotiations. Two bidders on different banks can both negotiate the same seller's listing; when the seller accepts one chain, every sibling chain (under that seller, with matching `parent_offer_id`) auto-cancels on both the seller's bank and each bidder's bank.

**The atomic per-listing key.** The user's concern "seller should be able to have two option offers at the same time with same ticker" is honoured because cascade matches on the listing's actual `parent_offer_id` (the OTCOffer row id), NOT on ticker+settlement_date. Two legitimately distinct listings on the same ticker+date have different `parent_offer_id` values → they're in different cascade groups → accepting one never touches the other.

**Capturing the key.** Bidders get it from the discovery payload (`offer_id` + `routing_number` in the `/otc/options` row) and pass it when bidding on the remote listing via the unified `POST /api/v3/otc/options/:id/bid` (stock-service composes the SI-TX `OtcOffer` from this and dispatches it to the seller's bank). The cross-bank-specific fields the dispatch consumes:

```
POST /api/v3/otc/options/:id/bid   (remote listing → cross-bank dispatch)
{
  "seller_bank_code":  "111",
  "seller_id":         "client-7",
  "stock":             { "ticker": "AAPL" },
  "settlement_date":   "2027-08-01T00:00:00Z",
  "price_per_unit":    { "amount": 175, "currency": "USD" },
  "premium":           { "amount": 40,  "currency": "USD" },
  "amount":            2,
  "bidder_account_id": 13,                                   ← REQUIRED (Fix #1, 2026-05-16)
  "parent_offer_id":   { "routingNumber": 111, "id": "42" }  ← optional
}
```

The `price_per_unit.amount` and `premium.amount` fields are JSON **numbers** (SI-TX §2.5); the gateway also tolerates a quoted decimal string for backward compatibility. They are re-serialized to the seller's bank as JSON numbers in the outbound `OtcOffer`.

**`bidder_account_id` (Fix #1, 2026-05-16).** REQUIRED. The buyer's account that pays the premium on accept. Gateway validates: (a) account exists and belongs to caller; (b) account is `active`; (c) **account currency matches `premium.currency`**. Cross-bank SI-TX has no FX (postings must balance per asset_id across banks, so converting on the buyer's side would break conservation) — open an account in the offer's currency or pick one. The resolved 18-digit account number is threaded into the SI-TX `OtcOffer` as `buyerAccountNumber` so the seller's bank uses this exact account for the buyer-debit posting on accept, instead of resolving `client-<id>` to "first active account in this currency" (which was non-deterministic and silently failed when the buyer had no matching account).

**Security note (Fix #7, 2026-05-16).** The seller's bank rejects inbound bids whose `buyerId.routingNumber` doesn't match the authenticated peer's routing — a peer cannot spoof a third bank as the buyer (which previously would have routed the premium debit to that third bank's account).

**Errors:**

| Status | Code | When |
|---|---|---|
| 400 | `validation_error` | `bidder_account_id` missing or zero |
| 400 | `validation_error` | currency mismatch (account.currency_code ≠ premium.currency) |
| 403 | `forbidden` | `bidder_account_id` does not belong to caller |
| 404 | `not_found` | `bidder_account_id` not found, or peer bank not registered |
| 424 | `internal_error` | `bidder_account_id` is not active |

The gateway forwards `parentOfferId` in the SI-TX `OtcOffer` body; the seller's bank stores it on `peer_otc_negotiations.parent_offer_routing` / `.parent_offer_id`. The buyer-side mirror also stores it. Free-form bidders (no discovery) omit the field — they're never part of any cascade group, so a seller's free-form listings stay safe.

**Cascade flow on accept.** When the seller accepts a remote chain via the unified `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` (stock-service dispatches the cross-bank accept):

1. Proxy GET `.../accept` → SI-TX dispatch → premium move → option contracts on both banks.
2. Local mirror flip to `accepted` (`MarkNegotiationAccepted` semantics, now run inside stock-service's outbound accept flow).
3. Stock-service runs the cross-bank cascade (`CascadeCancelSiblings` semantics): every other `ongoing` chain under the same seller with the same `parent_offer_id` is flipped to `cancelled` locally on the seller's bank.
4. For each cancelled sibling, stock-service fires `DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id` to that bidder's bank so the bidder's mirror flips to `cancelled` too.

**Out-of-cascade rows preserved.** Rows with NULL `parent_offer_id` (free-form bids) are excluded by the cascade query. The seller can hold two distinct listings on the same ticker+settlement_date without accidental cross-cancel.

**Response shape — parity with intra-bank accept.** The accept response carries `cancelled_siblings` so the FE can render local and cross-bank accepts with the same component. Each entry projects the same fields the FE consumes from the unified negotiations list (`GET /api/v3/me/otc/options/negotiations`): buyer_id, seller_id, offer, status, role, updated_at — different business logic backs the two flows, but the presentation shape is the same.

```json
POST /api/v3/me/otc/options/:id/negotiations/:nid/accept   ← remote chain → cross-bank dispatch

{
  "transactionId": "tx-...",        // proxied from peer's /accept
  "status":        "accepted",      // proxied from peer's /accept
  "cancelled_siblings": [
    {
      "peer_bank_code": "222",
      "foreign_id":     "neg-7",
      "buyer_id":       { "routingNumber": 222, "id": "client-3" },
      "seller_id":      { "routingNumber": 111, "id": "client-7" },
      "offer": {
        "ticker":           "AAPL",
        "amount":           2,
        "price_per_stock":  "175",
        "currency":         "USD",
        "premium":          "40",
        "premium_currency": "USD",
        "settlement_date":  "2027-08-01T00:00:00Z",
        "parent_offer_id":  { "routingNumber": 111, "id": "42" }
      },
      "status":     "cancelled",
      "role":       "seller",        // caller's side — cascade fires on accept
      "updated_at": "2026-05-16T..."
    }
  ]
}
```

The intra-bank equivalent (`POST /me/otc/options/:id/negotiations/:nid/accept`) returns the same `cancelled_siblings` key, populated with `OTCNegotiationResponse` rows (`id`, `parent_offer_id`, `bidder_*`, `quantity`, `strike_price`, `premium`, `settlement_date`, `status`, `last_action_*`). FE keys off `cancelled_siblings[*]` without branching the cascade UI on flow type.

#### Listing the caller's cross-bank negotiations

The caller's cross-bank OTC negotiations (as buyer and as seller) are returned by the **unified** `GET /api/v3/me/otc/options/negotiations` list alongside intra-bank chains — remote rows carry `kind="remote"`.

#### Notification coverage (2026-05-16)

Every OTC option negotiation lifecycle event now produces an in-app notification visible at `GET /api/v3/me/notifications`. Each bank notifies **only its own local users**; cross-bank state changes propagate via the SI-TX protocol and both banks then independently publish their own notifications.

| Event | Recipient | Notification type |
|---|---|---|
| Someone bid on your listing | Listing poster (seller) | `OTC_OFFER_RECEIVED` |
| Counter on your chain | The OTHER party in the chain | `OTC_OFFER_COUNTERED` |
| Chain rejected | The OTHER party | `OTC_OFFER_REJECTED` |
| Bidder cancelled their chain | Listing poster | `OTC_OFFER_CANCELLED` |
| Your bid was cancelled because seller accepted a competitor | Each losing bidder | `OTC_OFFER_CASCADE_CANCELLED` (carries `accepted_premium` so you see the winning price) |
| Negotiation accepted → contract minted | Both parties | `OTC_CONTRACT_CREATED` |

Templates are admin-editable via the 3a notification template management endpoints (`PUT /api/v3/notification-templates/:type/:channel`). All notifications carry `ref_type=otc_negotiation` (lifecycle events) or `ref_type=otc_contract` (final mint) with `ref_id` set, so the FE can deep-link from a feed entry to the chain or contract view.

### 47.3 Peer protocol (SI-TX cross-bank)

Cross-bank OTC option discovery happens **only** via `GET /api/v3/cross-bank-protocol/public-stock` (see §47.x / the cross-bank-protocol route table). The proprietary `GET /api/v3/cross-bank-protocol/public-option-offers` endpoint was **removed** on 2026-06-11 — peers now discover this bank's open, sell-initiated, public option offers through the `/public-stock` catalog (one seller entry per ticker, conformant SI-TX seller id). `buy_initiated` listings remain intra-bank only and are never exposed cross-bank.

---

## 53. System Version

Public endpoint — no authentication required.

### GET /api/v3/version

Returns the semantic version of the running backend, so front-end developers can confirm which backend build they are talking to. The version is the contents of the repo-root `VERSION` file, baked into the gateway binary at build time.

**Authentication:** None (public)

**Response 200:**
```json
{
  "version": "1.0.0"
}
```

---

## Error Response Format

All error responses follow this format:

```json
{
  "error": {
    "code": "snake_case_error_code",
    "message": "Human-readable error message"
  }
}
```

The `code` field is a stable machine-readable string. The `message` field is human-readable and suitable for display.

**Common error codes:**

| `code` | HTTP Status | Meaning |
|---|---|---|
| `validation_error` | 400 | Request body or query param validation failed |
| `invalid_input` | 400 | Malformed or out-of-range value |
| `not_authenticated` | 401 | Missing or invalid bearer token |
| `forbidden` | 403 | Authenticated but insufficient permissions |
| `not_found` | 404 | Requested resource does not exist |
| `business_rule_violation` | 409 | Operation violates a business rule (e.g., card already blocked) |
| `not_implemented` | 501 | Endpoint planned but not yet available |
| `internal_error` | 500 | Unexpected server-side failure |

**Common HTTP Status Codes:**

| Code | Meaning |
|---|---|
| 200 | Success |
| 201 | Created |
| 204 | No content (deleted) |
| 400 | Bad request / validation error |
| 401 | Unauthenticated (missing or invalid token) |
| 403 | Forbidden (insufficient permissions or wrong role) |
| 404 | Resource not found |
| 409 | Business rule violation (gRPC FailedPrecondition) |
| 429 | Rate limited |
| 500 | Internal server error |
| 501 | Not implemented |

---

## 48. Unified Portfolio Routes (2026-05-28)

A single consistent route shape for viewing any portfolio — client, bank, or investment fund — with fund positions shown alongside stocks, options, and futures, and full P/L totals computed on read.

### Portfolio identity

Portfolios are identified by a URL-safe `portfolio_id` string:

| `portfolio_id` | Owner type | Notes |
|---|---|---|
| `client-<n>` | client with id n | |
| `bank` | bank | singleton |
| `fund-<n>` | investment fund with id n | |

### 48.1 My Portfolio (client or bank)

**GET /api/v3/me/portfolio**

- Authentication: `AnyAuthMiddleware` (client or employee JWT)
- Identity rule: `OwnerIsBankIfEmployee` — clients see their own portfolio, employees see the bank's portfolio
- No query parameters

**Response 200:**
```json
{
  "portfolio_id": "client-42",
  "owner_type": "client",
  "owner_id": 42,
  "owner_name": "",
  "total_value_rsd": "11000.0000",
  "total_profit_rsd": "1000.0000",
  "total_profit_pct": "9.0909",
  "securities": {
    "total_value_rsd": "11000.0000",
    "total_profit_rsd": "1000.0000",
    "total_profit_pct": "9.0909",
    "positions": [
      {
        "asset_type": "stock",
        "symbol": "AAPL",
        "holding_id": 153,
        "quantity": 50,
        "avg_cost_rsd": "200.0000",
        "current_price_rsd": "220.0000",
        "current_value_rsd": "11000.0000",
        "p_l_rsd": "1000.0000",
        "p_l_pct": "10.0000",
        "last_updated": "2026-05-28T10:00:00Z"
      }
    ]
  },
  "funds": {
    "total_value_rsd": "27000.0000",
    "total_profit_rsd": "2000.0000",
    "total_profit_pct": "8.0000",
    "positions": [
      {
        "asset_type": "investment_fund",
        "fund_id": 7,
        "fund_name": "Alpha Growth",
        "amount_invested_rsd": "25000.0000",
        "current_value_rsd": "27000.0000",
        "pct_of_fund": "100.0000",
        "p_l_rsd": "2000.0000",
        "p_l_pct": "8.0000",
        "last_updated": "2026-05-28T10:00:00Z"
      }
    ]
  }
}
```

> **`holding_id` on security positions.** Each `securities` position carries `holding_id` — the numeric id of the underlying holdings row (`asset_type` `stock`/`option`/`future`). This is the id required by **exercise** (`POST /api/v3/me/portfolio/:id/exercise`), so a client can act on a position straight from this response without a separate lookup. Fund positions omit `holding_id` (they are keyed by `fund_id`, not a holding).

### 48.2 Portfolio by ID (generic form)

**GET /api/v3/portfolio/:portfolio_id**

- Authentication: `AuthMiddleware` (employee JWT only)
- Permissions: employees always access bank; `portfolio.view_client` for client portfolios; `portfolio.view_fund` for fund portfolios
- Path: `portfolio_id` — one of `client-<n>`, `bank`, or `fund-<n>`

| Status | Meaning |
|---|---|
| 200 | Success — same shape as 48.1 |
| 400 | invalid portfolio_id format |
| 401 | missing or invalid token |
| 403 | caller lacks permission |

### 48.3 Portfolio by owner type — convenience aliases

**GET /api/v3/portfolio/bank**
- Returns bank's portfolio. Any authenticated employee.

**GET /api/v3/portfolio/client/:client_id**
- Returns client's portfolio. Requires `portfolio.view_client`.

**GET /api/v3/portfolio/investment-fund/:fund_id**
- Returns fund's portfolio. Requires `portfolio.view_fund`.

All aliases use the same response shape as 48.1 and the same access enforcement as 48.2.

### 48.4 Watchlist by portfolio_id

**GET /api/v3/watchlist/:portfolio_id**

- Authentication: `AuthMiddleware` (employee JWT)
- Permissions: same as 48.2 — `portfolio.view_client` for client watchlists, `portfolio.view_fund` for fund watchlists
- Returns the same shape as `GET /api/v3/me/watchlist` but for the specified portfolio owner

| Status | Meaning |
|---|---|
| 200 | Success — `{ "items": [...] }` |
| 400 | invalid portfolio_id |
| 403 | forbidden |

---

## 49. Admin / Cron Management (C10 — 2026-05-28)

All five routes require an **employee JWT** (`AuthMiddleware`). Each sub-route carries its own permission; all three permissions are held by `EmployeeAdmin` via the wildcard grant.

### 49.1 List all crons

**GET /api/v3/admin/crons**

- Authentication: `AuthMiddleware` (employee JWT)
- Permission: `admin.crons.view`
- Fans out in parallel to all configured services (stock, credit, account, card, transaction, notification, user).
- Each service appears in the response regardless of reachability.

**Response 200:**
```json
{
  "services": [
    {
      "service": "stock-service",
      "status": "ok",
      "crons": [
        {
          "name": "tax-collection",
          "service": "stock-service",
          "description": "...",
          "interval": "24h",
          "cron_expression": "",
          "last_started_at": "2026-05-28T00:00:00Z",
          "last_finished_at": "2026-05-28T00:01:05Z",
          "last_error": "",
          "next_scheduled_at": "2026-05-29T00:00:00Z",
          "is_paused": false,
          "paused_by_employee": 0,
          "paused_at": "",
          "run_count": 42,
          "error_count": 1
        }
      ]
    },
    {
      "service": "notification-service",
      "status": "unreachable",
      "crons": null,
      "error": "connection refused"
    }
  ]
}
```

| Status | Meaning |
|---|---|
| 200 | Always returned, even if all services are unreachable |
| 401 | missing or invalid JWT |
| 403 | missing permission |

### 49.2 Get one cron

**GET /api/v3/admin/crons/:service/:name**

- Authentication: `AuthMiddleware` (employee JWT)
- Permission: `admin.crons.view`
- `:service` — exact service label (e.g. `stock-service`)
- `:name` — exact cron name as registered in the cron registry

**Response 200:**
```json
{
  "cron": { ...CronInfoMsg fields... }
}
```

| Status | Meaning |
|---|---|
| 200 | success |
| 401 | missing or invalid JWT |
| 403 | missing permission |
| 404 | service label unknown or cron name not found |
| 500 | service returned an unexpected error |

### 49.3 Trigger a cron

**POST /api/v3/admin/crons/:service/:name/trigger**

- Authentication: `AuthMiddleware` (employee JWT)
- Permission: `admin.crons.trigger`
- Body (optional): `{ "force": bool, "reason": string }`
  - `force`: if true, execute even if the cron is paused
  - `reason`: free-text for audit log
- Publishes `AdminCronActionMessage{action:"trigger"}` to `admin.cron-action` Kafka topic after success.

**Response 200:**
```json
{ "status": "triggered" }
```

| Status | Meaning |
|---|---|
| 200 | cron triggered |
| 401 | missing or invalid JWT |
| 403 | missing permission |
| 404 | service or cron not found |
| 409 | cron is paused and force=false |
| 500 | service error |

### 49.4 Pause a cron

**POST /api/v3/admin/crons/:service/:name/pause**

- Authentication: `AuthMiddleware` (employee JWT)
- Permission: `admin.crons.manage`
- Body (optional): `{ "reason": string }`
- Publishes `AdminCronActionMessage{action:"pause"}` to `admin.cron-action` after success.

**Response 200:**
```json
{ "status": "paused" }
```

| Status | Meaning |
|---|---|
| 200 | cron paused |
| 401 | missing or invalid JWT |
| 403 | missing permission |
| 404 | service or cron not found |
| 409 | already paused |
| 500 | service error |

### 49.5 Resume a cron

**POST /api/v3/admin/crons/:service/:name/resume**

- Authentication: `AuthMiddleware` (employee JWT)
- Permission: `admin.crons.manage`
- Body (optional): `{ "reason": string }`
- Publishes `AdminCronActionMessage{action:"resume"}` to `admin.cron-action` after success.

**Response 200:**
```json
{ "status": "resumed" }
```

| Status | Meaning |
|---|---|
| 200 | cron resumed |
| 401 | missing or invalid JWT |
| 403 | missing permission |
| 404 | service or cron not found |
| 409 | not paused |
| 500 | service error |

---

## 50. Admin / Audit Logs (D4 — 2026-05-28)

Six global audit-log read endpoints. Each returns the full changelog table for one service (or the cron-action audit table in notification-service), paginated and optionally filtered. All six routes require **EmployeeAdmin** role (permission `admin.audit.view`).

### Common query parameters (all six routes)

| Parameter  | Type   | Default | Description |
|------------|--------|---------|-------------|
| `page`     | int    | 1       | 1-based page number |
| `page_size`| int    | 50      | Entries per page (max 200) |
| `since`    | string | —       | Filter entries from this date inclusive (`YYYY-MM-DD`) |
| `until`    | string | —       | Filter entries up to this date inclusive (`YYYY-MM-DD`) |
| `actor_id` | int    | —       | Filter by employee (actor) ID |
| `action`   | string | —       | Filter by action string (exact match) |

### Common response shape (changelog routes)

```json
{
  "entries": [
    {
      "id": 123,
      "entity_type": "client",
      "entity_id": 42,
      "action": "updated",
      "field_name": "first_name",
      "old_value": "Marko",
      "new_value": "Marija",
      "actor_id": 7,
      "timestamp": "2026-05-28T10:00:00Z",
      "reason": ""
    }
  ],
  "total": 1234,
  "page": 1,
  "page_size": 50
}
```

**GET /api/v3/admin/audit/clients-changelog**

Returns all changelog entries from client-service across all clients.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

| Status | Description |
|--------|-------------|
| 200 | Paginated changelog entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

**GET /api/v3/admin/audit/accounts-changelog**

Returns all changelog entries from account-service across all accounts.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

| Status | Description |
|--------|-------------|
| 200 | Paginated changelog entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

**GET /api/v3/admin/audit/cards-changelog**

Returns all changelog entries from card-service across all cards.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

| Status | Description |
|--------|-------------|
| 200 | Paginated changelog entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

**GET /api/v3/admin/audit/loans-changelog**

Returns all changelog entries from credit-service across all loans and loan requests.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

| Status | Description |
|--------|-------------|
| 200 | Paginated changelog entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

**GET /api/v3/admin/audit/employees-changelog**

Returns all changelog entries from user-service across all employees.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

| Status | Description |
|--------|-------------|
| 200 | Paginated changelog entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

**GET /api/v3/admin/audit/cron-actions**

Returns admin cron-action audit log entries (trigger/pause/resume) persisted by notification-service.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

Response shape:

```json
{
  "entries": [
    {
      "id": 1,
      "action": "trigger",
      "service": "credit-service",
      "cron_name": "overdue-marking",
      "employee_id": 5,
      "reason": "Manual trigger for testing",
      "timestamp": "2026-05-28T10:00:00Z"
    }
  ],
  "total": 42,
  "page": 1,
  "page_size": 50
}
```

**GET /api/v3/admin/audit/business-actions**

Returns the business-action audit log — who changed an employee/actuary limit, reset a usedLimit, approved/rejected an order, changed role/employee permissions, or triggered manual tax collection — persisted by notification-service. The actor is the JWT principal who performed the action.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`

Query parameters (all optional):

| Param | Type | Notes |
|---|---|---|
| `page` | int | default 1 |
| `page_size` | int | default 50, max 200 |
| `since` | string | `YYYY-MM-DD` inclusive lower bound |
| `until` | string | `YYYY-MM-DD` inclusive upper bound |
| `actor_id` | int | filter by actor employee id |
| `action` | string | `limit.set` \| `limit.used_reset` \| `order.approve` \| `order.decline` \| `permissions.set` \| `tax.collect` |
| `target_type` | string | `employee` \| `order` \| `role` \| `tax` |

Response shape:

```json
{
  "entries": [
    {
      "id": 1,
      "action": "limit.set",
      "actor_id": 5,
      "target_type": "employee",
      "target_id": "9",
      "detail": "max_single=5000 max_daily=20000 ...",
      "timestamp": "2026-06-04T10:00:00Z"
    }
  ],
  "total": 7,
  "page": 1,
  "page_size": 50
}
```

**GET /api/v3/admin/audit/saga-logs**

Returns transaction-service saga execution logs (transfer/payment forward + compensation steps), so an admin can review saga history and stuck/compensating flows.

- Authentication: Bearer token (employee only)
- Permission: `admin.audit.view`
- Query params: `page` (default 1), `page_size` (default 50, max 200), `saga_id` (filter to one saga), `status` (`pending|completed|failed|compensating|dead_letter`), `transaction_type` (`transfer|payment`), `since`/`until` (`YYYY-MM-DD`)

Response shape:

```json
{
  "logs": [
    {
      "id": 12,
      "saga_id": "6f3e…",
      "transaction_id": 84,
      "transaction_type": "transfer",
      "step_number": 2,
      "step_name": "credit_destination",
      "status": "completed",
      "is_compensation": false,
      "account_number": "111000…",
      "amount": "100.0000",
      "error_message": "",
      "compensation_of": 0,
      "retry_count": 0,
      "created_at": 1748520000,
      "completed_at": 1748520001
    }
  ],
  "total": 7,
  "page": 1,
  "page_size": 50
}
```

> `logs` is always a JSON array (`[]` when empty). Currently sourced from transaction-service (transfer/payment sagas); credit-service and stock-service saga logs can be added behind the same route as follow-ups.

| Status | Description |
|--------|-------------|
| 200 | Paginated cron-action audit log entries |
| 400 | Invalid query parameter |
| 401 | Missing or invalid token |
| 403 | Insufficient permissions |
| 500 | Downstream gRPC error |

---

## 51. Dividends (E4 — 2026-05-28)

Dividend infrastructure for securities held directly by clients/bank and indirectly via investment funds.

### POST /api/v3/admin/dividends

**Authentication:** Employee JWT — requires `securities.manage.catalog` permission.

Declares a dividend for a security. Idempotent on `(security_id, payment_date)` — a second call with the same key returns the existing record.

**Request body:**
```json
{
  "security_id": 12,
  "ticker": "AAPL",
  "amount_per_share_rsd": "50.00",
  "payment_date": "2026-06-15"
}
```

**Success 201:**
```json
{
  "dividend_payment": {
    "id": 1,
    "security_id": 12,
    "ticker": "AAPL",
    "amount_per_share_rsd": "50.0000",
    "payment_date": "2026-06-15",
    "status": "declared",
    "declared_by_employee_id": 3,
    "paid_out_at": "",
    "created_at": "2026-06-01T10:00:00Z"
  }
}
```

**Error responses:**
- `400 validation_error` — missing or invalid fields.
- `403 forbidden` — insufficient permission.

---

### POST /api/v3/admin/dividends/:id/payout

**Authentication:** Employee JWT — requires `securities.manage.catalog` permission.

Triggers the fan-out payout for a declared dividend. Walks every holding of the security:
- Client direct holdings: 15% tax withheld; net credited to holder's account.
- Bank direct holdings: no tax; full gross credited to bank's RSD account.
- Investment fund holdings: no tax at this stage (deferred); full gross credited to fund's RSD account; a per-investor snapshot is recorded.

**Path param:** `:id` — `dividend_payment_id`.

**Request body:** empty.

**Success 200:**
```json
{
  "payouts_created": 12,
  "fund_payouts": 2,
  "total_amount_rsd": "150000.00"
}
```

**Error responses:**
- `400 validation_error` — invalid id.
- `403 forbidden` — insufficient permission.
- `500 internal_error` — payment already paid_out or cancelled.

---

### GET /api/v3/me/dividends

**Authentication:** Any valid JWT (client or employee).

Returns the caller's paginated dividend payout history, sorted most-recent first.

**Query params:** `page` (default 1), `page_size` (default 20).

**Success 200:**
```json
{
  "payouts": [
    {
      "id": 5,
      "dividend_payment_id": 1,
      "holding_owner_type": "client",
      "holding_owner_id": 42,
      "holding_id": 17,
      "shares": 100,
      "gross_amount_rsd": "5000.00",
      "tax_amount_rsd": "750.00",
      "net_amount_rsd": "4250.00",
      "credited_account_id": 1003,
      "created_at": "2026-06-15T12:00:00Z"
    }
  ],
  "total": 1
}
```

---

### GET /api/v3/investment-funds/:id/dividends

**Authentication:** Any valid JWT (client or employee).

Returns paginated `fund_dividend_payments` for the fund, most-recent first. Each record includes a `per_investor_snapshot` JSON string with per-investor share breakdowns at payout time.

**Path param:** `:id` — fund ID.
**Query params:** `page` (default 1), `page_size` (default 20).

**Success 200:**
```json
{
  "payments": [
    {
      "id": 3,
      "dividend_payment_id": 1,
      "fund_id": 7,
      "amount_rsd": "30000.00",
      "per_investor_snapshot": "[{\"investor_owner_type\":\"client\",\"investor_owner_id\":42,\"pct_at_payment\":\"60.0000\",\"gross_share_rsd\":\"18000.0000\"}]",
      "created_at": "2026-06-15T12:00:00Z"
    }
  ],
  "total": 1
}
```

**Error responses:**
- `400 validation_error` — invalid fund id.

---

### Portfolio dividend fields (E3)

The `GET /api/v3/me/portfolio` and `GET /api/v3/portfolio/*` responses include two new fields on each `PortfolioPosition`:

| Field | Description |
|---|---|
| `dividends_received_rsd` | For fund positions: sum of the caller's pro-rata share from `fund_dividend_payments` at each payment's `pct_at_payment`. For direct security positions: sum of `dividend_payouts.net_amount_rsd` for this owner. `"0.00"` when no dividends have been paid. |
| `fund_status` | For `asset_type = "investment_fund"` positions: the fund's lifecycle status (`open`, `fundraising`, `active`, `matured`, `liquidated`). Empty for non-fund positions. |

---

## Password Requirements

Passwords for both employees and clients must satisfy:
- 8 to 32 characters
- At least 2 digits
- At least 1 uppercase letter
- At least 1 lowercase letter

---

## Notes for Frontend Developers

1. **Token expiry:** Access tokens expire after 15 minutes. Implement automatic refresh using the refresh token before expiry.

2. **Client vs Employee routes:** Employee routes require an employee JWT with specific permissions. Client self-service routes are under `/api/v3/me/*` and accept any valid JWT (employee or client). Do not use a client token to call employee-only endpoints.

3. **Error format:** All error responses are structured objects: `{"error": {"code": "...", "message": "..."}}`. Parse `error.code` for programmatic error handling and `error.message` for display.

4. **Pagination:** All list endpoints support `page` (1-based) and `page_size` query parameters. Default page size is 20.

5. **Date fields:** `date_of_birth` is a Unix timestamp in seconds. Convert to/from a date object in your application.

6. **Account numbers:** Account numbers follow the format `265-XXXXXXXXXXX-YY` (Serbian bank account format with control digits).

7. **Card numbers:** The full card number and CVV are only returned in the create card response. Subsequent reads return a masked card number (e.g., `**** **** **** 4242`).

8. **JMBG:** The 13-digit Serbian national ID. Validated server-side for exact length and uniqueness.

9. **CORS:** The API Gateway allows all origins with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` methods and `Authorization`, `Content-Type` headers.

10. **Version alias:** `/api/latest/` rewrites to `/api/v3/`. You may use either prefix; `/api/latest/` will always point to the newest stable version.

11. **Mobile auth flow:** Mobile devices use a separate auth flow (`POST /api/v3/mobile/auth/request-activation` -> `POST /api/v3/mobile/auth/activate`). Mobile JWT tokens include `system_type: "mobile"` and require `X-Device-ID` header for all authenticated requests.

12. **Verification flow:** Payments and transfers require two-factor verification. Create the transaction, then create a verification challenge, wait for mobile approval, then execute. Users with `verification.skip` permission bypass this flow.

13. **Biometric verification:** If biometrics are enabled on a mobile device, challenges can be verified via `POST /api/v3/mobile/verifications/:id/biometric` without entering a code. The signed device request itself serves as the biometric proof.

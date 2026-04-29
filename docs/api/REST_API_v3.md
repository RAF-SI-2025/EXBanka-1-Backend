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

Access tokens expire after 15 minutes. Use the refresh token to obtain a new pair.

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
15. [Transfer Fees](#15-transfer-fees)
16. [Interest Rate Tiers](#16-interest-rate-tiers)
17. [Bank Margins](#17-bank-margins)
18. [Card Requests](#18-card-requests)
19. [Me (Self-Service)](#19-me-self-service)
20. [Mobile Auth](#20-mobile-auth)
21. [Mobile Device Management](#21-mobile-device-management)
22. [Mobile Device Settings](#22-mobile-device-settings)
23. [Verification](#23-verification)
24. [Stock Exchanges](#24-stock-exchanges)
25. [Securities](#25-securities)
26. [Orders](#26-orders)
27. [Portfolio](#27-portfolio)
28. [OTC Offers (Public Stock Listings)](#28-otc-offers-public-stock-listings)
29. [OTC Option Contracts (Celina 4)](#29-otc-option-contracts-celina-4)
30. [Investment Funds (Celina 4)](#30-investment-funds-celina-4)
31. [Actuaries](#31-actuaries)
32. [Tax](#32-tax)
33. [Blueprints](#33-blueprints)
34. [Changelog (Audit Trail)](#34-changelog-audit-trail)
35. [Sessions & Login History](#35-sessions--login-history)
36. [Notifications](#36-notifications)
37. [Stock Data Source](#37-stock-data-source)
38. [Peer Banks (Admin) — SI-TX cross-bank registry (Celina 5)](#38-peer-banks-admin--si-tx-cross-bank-registry-celina-5)
39. [Error Response Format](#error-response-format)
40. [Password Requirements](#password-requirements)
41. [Notes for Frontend Developers](#notes-for-frontend-developers)

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

Payments are domestic/foreign transfers from one account to another with optional payment metadata.

---

### POST /api/v3/me/payments

Initiate a new payment from a client account.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | float64 | Yes | Payment amount (in source currency) |
| `recipient_name` | string | No | Recipient display name |
| `payment_code` | string | No | Payment code (e.g., `"289"`) |
| `reference_number` | string | No | Reference/model number |
| `payment_purpose` | string | No | Description or purpose of payment |

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

> **Inter-bank dispatch (Phase 3):** When `to_account_number`'s 3-digit prefix differs from this bank's `OWN_BANK_CODE`, the request is dispatched to `PeerTxService.InitiateOutboundTx` via gRPC and returns `202 Accepted` with `{transaction_id, poll_url, status}`. Poll the returned URL for SI-TX completion status. Intra-bank receivers (own prefix) keep the legacy `201 Created` shape above.

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

## 15. Transfer Fees

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

## 16. Interest Rate Tiers

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

## 17. Bank Margins

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

## 18. Card Requests

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

## 19. Me (Self-Service)

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

---

### GET /api/v3/me/portfolio/summary

Get portfolio summary. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

---

### POST /api/v3/me/portfolio/:id/make-public

Make a holding available on the OTC market. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

---

### POST /api/v3/me/portfolio/:id/exercise

Exercise an options contract. See [Section 27: Portfolio](#27-portfolio) for full request/response details.

---

### GET /api/v3/me/tax

Returns paginated capital gains tax records for the authenticated user. See [Section 31: Tax](#31-tax) for full request/response details.

---

## 20. Mobile Auth

Mobile device authentication for the EXBanka mobile app. These endpoints are public (no auth required).

---

### POST /api/v3/mobile/auth/request-activation

Request a 6-digit activation code sent to the user's email.

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

## 21. Mobile Device Management

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

## 22. Mobile Device Settings

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

## 23. Verification

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

## 24. Stock Exchanges

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
  "exchanges": [ ],
  "total_count": 5
}
```

---

### GET /api/v3/stock-exchanges/:id

Get a specific stock exchange by ID.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Exchange ID |

**Response 200:** Stock exchange object.

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

## 25. Securities

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

## 26. Orders

### POST /api/v3/me/orders

Create a new stock / futures / forex / option order. Ownership is derived from the JWT — the `account_id` (and `base_account_id`, when provided) must belong to the JWT caller. Mismatches return `403 forbidden`.

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
| `account_id` | uint64 | Yes (buy) | Account to debit; must belong to the JWT caller |
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

Place a stock/futures/forex/option order on behalf of a named client. The gateway verifies that the specified `account_id` belongs to the specified `client_id` before forwarding to stock-service. The order is recorded with `acting_employee_id` set to the caller's employee ID.

**Authentication:** Employee JWT + `orders.place-on-behalf` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | Client for whom the order is placed |
| `account_id` | uint64 | Yes | Account to debit; must belong to `client_id` |
| `security_type` | string | Optional | `stock`, `futures`, `forex`, or `option`. Required for forex-specific gateway validation. |
| `listing_id` | uint64 | Yes (buy) | Listing ID (required for buy orders) |
| `holding_id` | uint64 | Yes (sell) | Holding ID (required for sell orders) |
| `direction` | string | Yes | `buy` or `sell` (forex orders MUST be `buy`) |
| `order_type` | string | Yes | `market`, `limit`, `stop`, or `stop_limit` |
| `quantity` | int64 | Yes | Must be positive |
| `limit_value` | string | Conditional | Required for `limit` or `stop_limit` orders |
| `stop_value` | string | Conditional | Required for `stop` or `stop_limit` orders |
| `all_or_none` | boolean | No | Default: false |
| `margin` | boolean | No | Default: false |
| `base_account_id` | uint64 | Yes (forex) | Required when `security_type=forex`. Must belong to `client_id` and differ from `account_id`. |

**Example Request:**
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

**Response 201:** Order object.

| Status | Description |
|---|---|
| 201 | Order created |
| 400 | Validation error — including forex direction/`base_account_id` mismatches |
| 403 | Account (or base account) does not belong to the specified client |
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

**Response 200:** Exercise result (holding update + ledger entries).

**Error Responses:**
- `400` — invalid `option_id`
- `404` — option / holding not found

---

## 27. Portfolio

### GET /api/v3/me/portfolio

List authenticated user's holdings.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `security_type` | string | `stock`, `futures`, or `option` |

**Response 200:**

Holdings aggregate per `(user_id, system_type, security_type, security_id)` — buying the same stock from two different accounts returns a single row with the combined quantity. Per-purchase price, profit, and FX details are available at [GET /me/holdings/{id}/transactions](#get-apiv1meholdingsidtransactions).

```json
{
  "holdings": [
    {
      "id": 1,
      "security_type": "stock",
      "ticker": "AAPL",
      "name": "Apple Inc.",
      "quantity": 10,
      "public_quantity": 3,
      "account_id": 42,
      "last_modified": "2026-04-01T12:00:00Z"
    }
  ],
  "total_count": 10
}
```

**Holding object fields:**

| Field | Type | Description |
|---|---|---|
| `id` | uint64 | Holding ID |
| `security_type` | string | `stock`, `futures`, or `option` |
| `ticker` | string | Security ticker symbol |
| `name` | string | Security name |
| `quantity` | int64 | Total units owned (aggregated across every account used) |
| `public_quantity` | int64 | Units listed on OTC market |
| `account_id` | uint64 | Last-used account (audit pointer, not authoritative) |
| `last_modified` | string | ISO 8601 timestamp of last update |

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

### POST /api/v3/me/portfolio/:id/make-public

Make a holding available on the OTC market.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Holding ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `quantity` | int64 | Yes | Number of units to make public (must be positive) |

**Response 200:** Public holding object.

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

## 28. OTC Offers (Public Stock Listings)

These three endpoints cover the legacy stock-public-OTC flow — a holding made publicly tradeable via `POST /api/v3/me/portfolio/:id/make-public` and bought outright by another user. For the Celina 4 option-contract negotiations (offer / counter / accept / reject / exercise), see [Section 29](#29-otc-option-contracts-celina-4).

### GET /api/v3/otc/offers

List public stock OTC offers (publicly listed holdings).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `security_type` | string | `stock` or `futures` |
| `ticker` | string | Filter by ticker symbol |

**Response 200:**
```json
{
  "offers": [ ],
  "total_count": 8
}
```

---

### POST /api/v3/otc/offers/:id/buy

Purchase an OTC offer. Ownership is derived from the JWT — the account must belong to the JWT caller.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Offer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `quantity` | int64 | Yes | Number of units to buy (must be positive) |
| `account_id` | uint64 | Yes | Account to debit; must belong to the JWT caller |

**Response 200:** Transaction object.

---

### POST /api/v3/otc/offers/:id/buy-on-behalf

Purchase an OTC offer on behalf of a named client. The gateway verifies that the specified `account_id` belongs to the specified `client_id` before forwarding to stock-service. The resulting order is recorded with `acting_employee_id` set to the caller's employee ID.

Renamed from `POST /api/v3/otc/admin/offers/:id/buy` in the v3 route standardization (2026-04-28): the `/admin/` namespace was dropped and the action verb-suffix `buy-on-behalf` was added to distinguish it from the self-service `POST /otc/offers/:id/buy`.

**Authentication:** Employee JWT + `otc.trade.accept` or `orders.place-on-behalf` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Offer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | Client for whom the offer is purchased |
| `account_id` | uint64 | Yes | Account to debit; must belong to `client_id` |
| `quantity` | int64 | Yes | Number of units to buy (must be positive) |

**Example Request:**
```json
{
  "client_id": 5,
  "account_id": 12,
  "quantity": 3
}
```

**Response 200:** Transaction object.

| Status | Description |
|---|---|
| 200 | OTC offer purchased |
| 400 | Validation error |
| 403 | Account does not belong to the specified client |
| 403 | Missing required permission |
| 404 | Offer not found |

---

## 29. OTC Option Contracts (Celina 4)

OTC option-contract negotiation flow (Specification §26). Two parties — both clients, both supervisors, or a client and an employee acting for the bank — exchange revisions on a stock-option contract until one side accepts or rejects. Acceptance triggers a premium-payment SAGA that creates an `OptionContract`. The contract can later be exercised before its `settlement_date`.

The URL namespace is shared with [Section 28](#28-otc-offers-public-stock-listings) (`/api/v3/otc/offers`) — the routes here are distinguished by their HTTP verb and action segment (`/counter`, `/accept`, `/reject`, `/exercise`).

**Permissions:** all trading actions (create, counter, accept, reject, exercise) require **both** `securities.trade` **and** `otc.trade` (`RequireAllPermissions`). Read endpoints accept any authenticated principal.

**Identity middleware:** these routes use `OwnerIsBankIfEmployee` — when the caller is an employee, the offer/contract is owned by `bank` (no client `OwnerID`); when the caller is a client, the offer/contract is owned by that client.

---

### POST /api/v3/otc/offers

Create a new OTC option offer (open a negotiation thread).

**Authentication:** Any JWT + `securities.trade` AND `otc.trade`

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `direction` | string | Yes | `sell_initiated` (caller is the seller of the option) or `buy_initiated` (caller is the buyer) |
| `stock_id` | uint64 | Yes | Stock listing ID the option is on |
| `quantity` | string (decimal) | Yes | Number of shares the option covers |
| `strike_price` | string (decimal) | Yes | Strike price per share, in the seller's account currency |
| `premium` | string (decimal) | No | Option premium (paid by buyer to seller on accept) |
| `settlement_date` | string (RFC3339 date) | Yes | Last day the option can be exercised |
| `counterparty_user_id` | int64 | No | Pin the offer to a specific counterparty (1:1 negotiation). Omit for a broadcast offer. |
| `counterparty_system_type` | string | No | `client` (default) or `employee` — paired with `counterparty_user_id` |

**Example Request:**
```json
{
  "direction": "sell_initiated",
  "stock_id": 42,
  "quantity": "100",
  "strike_price": "5000.00",
  "premium": "50000.00",
  "settlement_date": "2026-06-05",
  "counterparty_user_id": 8,
  "counterparty_system_type": "client"
}
```

**Response 201:**
```json
{
  "offer": {
    "id": 1001,
    "direction": "sell_initiated",
    "status": "PENDING",
    "stock_id": 42,
    "quantity": "100",
    "strike_price": "5000.00",
    "premium": "50000.00",
    "settlement_date": "2026-06-05",
    "initiator": { "owner_type": "client", "owner_id": 7 },
    "counterparty": { "owner_type": "client", "owner_id": 8 },
    "last_modified_at": "2026-04-28T14:20:00Z"
  }
}
```

**Error Responses:**
- `400` — invalid direction / missing required field
- `403` — missing `securities.trade` or `otc.trade`

---

### POST /api/v3/otc/offers/:id/counter

Send a counter-offer on an existing negotiation thread. The counterparty becomes the new `last_modified_by`. Each counter appends an `OTCOfferRevision` row.

**Authentication:** Any JWT + `securities.trade` AND `otc.trade`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Offer ID |

**Request Body:** Any subset of the negotiable terms — fields left blank are unchanged.

| Field | Type | Description |
|---|---|---|
| `quantity` | string (decimal) | New share count |
| `strike_price` | string (decimal) | New strike per share |
| `premium` | string (decimal) | New premium |
| `settlement_date` | string (RFC3339 date) | New settlement date |

**Response 200:** `{ "offer": <updated offer> }`.

**Error Responses:**
- `400` — invalid id / invalid body
- `403` — missing perms
- `409` — offer is already accepted, rejected, or expired

---

### POST /api/v3/otc/offers/:id/accept

Accept the current revision of an offer. Triggers the premium-payment SAGA: reserve seller's shares → debit buyer's account by `premium` → credit seller → create `OptionContract` row in `ACTIVE` status. Cross-currency cases convert through exchange-service.

**Authentication:** Any JWT + `securities.trade` AND `otc.trade`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Offer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `buyer_account_id` | uint64 | Yes | Account the premium is debited from |
| `seller_account_id` | uint64 | Yes | Account the premium is credited to |

**Response 201:**
```json
{
  "offer": { "id": 1001, "status": "ACCEPTED", ... },
  "contract": {
    "id": 5001,
    "status": "ACTIVE",
    "stock_id": 42,
    "quantity": "100",
    "strike_price": "5000.00",
    "premium": "50000.00",
    "settlement_date": "2026-06-05",
    "buyer": { "owner_type": "client", "owner_id": 7 },
    "seller": { "owner_type": "client", "owner_id": 8 }
  }
}
```

**Error Responses:**
- `400` — missing `buyer_account_id` / `seller_account_id`
- `403` — missing perms
- `409` — insufficient buyer funds, insufficient seller shares, or offer no longer pending

---

### POST /api/v3/otc/offers/:id/reject

Reject an offer. Marks status `REJECTED`; no further revisions are accepted.

**Authentication:** Any JWT + `securities.trade` AND `otc.trade`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Offer ID |

**Request Body:** None.

**Response 200:** `{ "offer": <updated offer> }`.

---

### GET /api/v3/otc/offers/:id

Get full detail for one offer including its revision history.

**Authentication:** Any JWT (read-only)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Offer ID |

**Response 200:**
```json
{
  "offer": { "id": 1001, ... },
  "revisions": [
    {
      "revision_number": 1,
      "modified_by": { "principal_type": "client", "principal_id": 7 },
      "quantity": "100",
      "strike_price": "5000.00",
      "premium": "45000.00",
      "settlement_date": "2026-06-05",
      "created_at": "2026-04-28T14:20:00Z"
    },
    {
      "revision_number": 2,
      "modified_by": { "principal_type": "client", "principal_id": 8 },
      "quantity": "100",
      "strike_price": "5000.00",
      "premium": "50000.00",
      "settlement_date": "2026-06-05",
      "created_at": "2026-04-28T14:35:00Z"
    }
  ]
}
```

---

### GET /api/v3/me/otc/offers

List the caller's OTC offers. Employees see all bank-owned offers; clients see their own.

**Authentication:** Any JWT (`OwnerIsBankIfEmployee`)

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `role` | string | `either` | `initiator`, `counterparty`, or `either` |
| `page` | int | 1 | Page number |
| `page_size` | int | 20 | Items per page |

**Response 200:**
```json
{
  "offers": [ { "id": 1001, "status": "PENDING", "unread": true, ... } ],
  "total": 1
}
```

---

### POST /api/v3/otc/contracts/:id/exercise

Exercise an active option contract — the buyer pays `quantity * strike_price` to the seller and receives the underlying shares. Runs the 5-phase exercise SAGA.

**Authentication:** Any JWT + `securities.trade` AND `otc.trade`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Contract ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `buyer_account_id` | uint64 | Yes | Account that pays the strike (and where the holding lands) |
| `seller_account_id` | uint64 | Yes | Account that receives the strike payment |

**Response 201:**
```json
{
  "contract": { "id": 5001, "status": "EXERCISED", ... },
  "holding": { "id": 9001, "stock_id": 42, "quantity": "100", "owner": { ... } }
}
```

**Error Responses:**
- `400` — missing buyer/seller account IDs
- `403` — missing perms
- `409` — contract already exercised / expired, or insufficient buyer funds

---

### GET /api/v3/otc/contracts/:id

Get one option contract.

**Authentication:** Any JWT (read-only)

**Response 200:** `{ "contract": { ... } }`.

---

### GET /api/v3/me/otc/contracts

List the caller's option contracts.

**Authentication:** Any JWT (`OwnerIsBankIfEmployee`)

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `role` | string | `either` | `buyer`, `seller`, or `either` |
| `page` | int | 1 | Page number |
| `page_size` | int | 20 | Items per page |

**Response 200:**
```json
{
  "contracts": [ { "id": 5001, "status": "ACTIVE", ... } ],
  "total": 1
}
```

---

## 30. Investment Funds (Celina 4)

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

**Example Request:**
```json
{
  "name": "Alpha Growth Fund",
  "description": "IT-sector focus",
  "minimum_contribution_rsd": "1000.00"
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

Get one fund detail (used by the Detaljan prikaz page).

**Authentication:** Any JWT

**Response 200:**
```json
{
  "fund": { "id": 101, "name": "Alpha Growth Fund", ... },
  "holdings": [ { "stock_id": 42, "quantity": "100", "acquired_at": "..." } ],
  "performance": [ { "as_of": "2026-04-01", "fund_value_rsd": "2600000.00" } ]
}
```

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

## 31. Actuaries

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

## 32. Tax

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

## 33. Blueprints

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

## 34. Changelog (Audit Trail)

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

## 35. Sessions & Login History

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

## 36. Notifications

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

## 37. Stock Data Source

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

## 38. Peer Banks (Admin) — SI-TX cross-bank registry (Celina 5)

Runtime registry of cross-bank peer banks. Backs the SI-TX `POST /api/v3/interbank` middleware, which looks up peer authentication credentials in this table. EmployeeAdmin only (`peer_banks.manage.any` permission).

> **Phase 2 status:** the table + admin CRUD is fully wired. The `POST /api/v3/interbank` endpoint exists but returns `501 Not Implemented` because the underlying TX execution is implemented in Phase 3. The `X-Api-Key` auth path on `/api/v3/interbank` is also stubbed in Phase 2 — Phase 3 introduces a dedicated internal RPC for plaintext-token lookup.

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
| `routing_number` | int64 | Yes | Numeric form of bank_code |
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
**Response 400:** Validation error (missing required field).

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

### POST /api/v3/interbank

Receives the SI-TX `Message<Type>` envelope from peer banks. Phase 3 implementation is fully wired: `NEW_TX` validates postings (UNBALANCED_TX check + per-posting account/asset/active checks), reserves credit-postings via `account-service.ReserveIncoming`, and emits `TransactionVote`. `COMMIT_TX` finalises reservations; `ROLLBACK_TX` releases them. Idempotence-key replay returns the cached vote.

**Authentication:** Hybrid `PeerAuth` middleware. Either:
- `X-Api-Key: <token>` — looked up against `peer_banks.api_token_plaintext` via the internal `ResolvePeerByAPIToken` RPC.
- `X-Bank-Code: <code>` + `X-Bank-Signature: <hex SHA-256>` + `X-Timestamp: <RFC3339, ±5min>` + `X-Nonce: <single-use>` — verified against `peer_banks.hmac_inbound_key` via `ResolvePeerByBankCode`.

**Request Body:** SI-TX `Message<Type>` envelope. Shape verbatim from the cohort spec at https://arsen.srht.site/si-tx-proto/.

```json
{
  "idempotenceKey": {
    "routingNumber": 222,
    "locallyGeneratedKey": "abc-123"
  },
  "messageType": "NEW_TX",
  "message": {
    "postings": [
      {"routingNumber": 222, "accountId": "222000001", "assetId": "RSD", "amount": "100.00", "direction": "DEBIT"},
      {"routingNumber": 111, "accountId": "111000001", "assetId": "RSD", "amount": "100.00", "direction": "CREDIT"}
    ]
  }
}
```

**Responses:**
- **200 OK** for `NEW_TX` — body is a `TransactionVote` (`{type: "YES", transactionId: "..."}` or `{type: "NO", noVotes: [...]}` with one or more of the 8 SI-TX reasons: `UNBALANCED_TX`, `NO_SUCH_ACCOUNT`, `NO_SUCH_ASSET`, `UNACCEPTABLE_ASSET`, `INSUFFICIENT_ASSET`, `OPTION_AMOUNT_INCORRECT`, `OPTION_USED_OR_EXPIRED`, `OPTION_NEGOTIATION_NOT_FOUND`).
- **204 No Content** for `COMMIT_TX` / `ROLLBACK_TX` (both idempotent).
- **401 Unauthorized** with empty body when auth fails (constant-time compare; no info leak).

---

### GET /api/v3/public-stock

Peer-facing OTC discovery — returns stock holdings on this bank flagged for OTC public trading. Used by peer banks to populate their OTC discovery pages.

**Authentication:** PeerAuth (X-Api-Key or HMAC bundle).

**Response 200:**
```json
{
  "stocks": [
    {
      "ownerId": {"routingNumber": 111, "id": "client-7"},
      "ticker": "AAPL",
      "amount": 50,
      "pricePerStock": "180.50",
      "currency": "USD"
    }
  ]
}
```

---

### POST /api/v3/negotiations

Peer initiates a cross-bank OTC negotiation against a publicly-listed holding on this bank. The peer's offer is persisted in `peer_otc_negotiations` and gets a fresh negotiation ID owned by this bank.

**Authentication:** PeerAuth.

**Request Body:**
```json
{
  "offer": {
    "ticker": "AAPL",
    "amount": 50,
    "pricePerStock": "180.50",
    "currency": "USD",
    "premium": "700",
    "premiumCurrency": "USD",
    "settlementDate": "2026-12-31",
    "lastModifiedBy": {"routingNumber": 222, "id": "user-1"}
  },
  "buyerId":  {"routingNumber": 222, "id": "buyer-1"},
  "sellerId": {"routingNumber": 111, "id": "seller-1"}
}
```

**Response 201:**
```json
{ "negotiationId": {"routingNumber": 111, "id": "neg-uuid"} }
```

---

### PUT /api/v3/negotiations/:rid/:id

Counter-offer on an existing negotiation. The negotiation must have been created via `POST /api/v3/negotiations` first.

**Authentication:** PeerAuth.

**Path Parameters:**
- `rid` — peer's routing number (int64)
- `id` — peer's negotiation id (string)

**Request Body:** Same `offer` shape as POST.

**Response 200:** Empty body on success.

---

### GET /api/v3/negotiations/:rid/:id

Read a negotiation's current state.

**Authentication:** PeerAuth.

**Response 200:**
```json
{
  "id":       {"routingNumber": 111, "id": "neg-uuid"},
  "buyerId":  {"routingNumber": 222, "id": "buyer-1"},
  "sellerId": {"routingNumber": 111, "id": "seller-1"},
  "offer":    { /* same shape as POST.offer */ },
  "status":   "ongoing",
  "updatedAt": "2026-04-29T12:00:00Z"
}
```

**Response 404:** Negotiation not found.

---

### DELETE /api/v3/negotiations/:rid/:id

Cancel a negotiation. Either side may delete; status flips to `cancelled`.

**Authentication:** PeerAuth.
**Response 204:** Success, no body.

---

### GET /api/v3/negotiations/:rid/:id/accept

Accept a negotiation. Composes a 4-posting `Transaction` (premium money debit-buyer/credit-seller + 1× `OptionDescription` debit-seller/credit-buyer) and dispatches via `PeerTxService.InitiateOutboundTxWithPostings`. The resulting SI-TX TX runs through the normal `NEW_TX` → `COMMIT_TX` flow.

**Authentication:** PeerAuth.

**Response 200:**
```json
{ "transactionId": "tx-uuid", "status": "pending" }
```

The `transactionId` is the same idempotence-key the OutboundReplayCron uses; clients can poll `/api/v3/me/transfers/{transactionId}` for terminal status (transfer endpoints recognise OTC tx ids by id format).

---

### GET /api/v3/user/:rid/:id

Returns identity info for a counterparty user. Peers call this when displaying user names alongside OTC negotiations or transfer history.

**Authentication:** PeerAuth.

**Path Parameters:**
- `rid` — must match `OWN_BANK_CODE`'s routing number; otherwise 404 (we don't proxy lookups across banks).
- `id` — `client-<n>` or `employee-<n>` format; routes to client-service or user-service accordingly.

**Response 200:**
```json
{
  "id":        {"routingNumber": 111, "id": "client-7"},
  "firstName": "Marko",
  "lastName":  "Marković"
}
```

**Response 404:** Foreign rid or unknown user id.

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

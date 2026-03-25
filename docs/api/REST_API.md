# EXBanka REST API Documentation

**Base URL:** `http://localhost:8080`
**Content-Type:** `application/json`
**Swagger UI:** `http://localhost:8080/swagger/index.html`

---

## Authentication

Most endpoints require a JWT bearer token in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

There are two token types:
- **Employee token** — issued via `POST /api/auth/login` when the principal is an employee, required for employee-protected routes
- **Client token** — issued via `POST /api/auth/login` when the principal is a client, required for client-protected routes

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
10. [Verification Codes](#10-verification-codes)
11. [Exchange Rates](#11-exchange-rates)
12. [Loans](#12-loans)
13. [Limits](#13-limits)
14. [Bank Accounts](#14-bank-accounts)
15. [Transfer Fees](#15-transfer-fees)
16. [Interest Rate Tiers](#16-interest-rate-tiers)
17. [Bank Margins](#17-bank-margins)
18. [Card Requests](#18-card-requests)

---

## Bootstrap

### POST /api/bootstrap

One-time admin account setup. Creates the system admin employee and provisions their auth account, triggering an activation email. Idempotent — safe to call multiple times (subsequent calls re-send the activation token if the account is not yet active).

Returns `404 Not Found` when `BOOTSTRAP_SECRET` is not set in the api-gateway environment (disabled in production).

**Authentication:** None (public). Protected by shared secret instead.

**Request Body:**

```json
{
  "secret": "dev-bootstrap-secret",
  "email": "admin@example.com"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `secret` | string | yes | Must match the `BOOTSTRAP_SECRET` env var configured in api-gateway |
| `email` | string | yes | Email address to use for the admin account |

**Responses:**

| Status | Description |
|---|---|
| `200 OK` | Admin employee created (or already existed) and activation token published to Kafka |
| `400 Bad Request` | Missing or invalid request body |
| `401 Unauthorized` | Wrong bootstrap secret |
| `404 Not Found` | Bootstrap endpoint disabled (`BOOTSTRAP_SECRET` not set) |
| `500 Internal Server Error` | Failed to create employee or provision auth account |

---

## 1. Auth

### POST /api/auth/login

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

### POST /api/auth/refresh

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

### POST /api/auth/logout

Revoke the current refresh token to end the session.

**Authentication:** None (public — the refresh token itself is the credential)

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

### POST /api/auth/password/reset-request

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

### POST /api/auth/password/reset

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

### POST /api/auth/activate

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

## 2. Employees

All employee endpoints require an employee JWT. Read endpoints require `employees.read` permission; create requires `employees.create`; update requires `employees.update`.

**Roles and permissions:**
- `EmployeeAdmin` — can manage employees + all other permissions
- `EmployeeSupervisor` — agents/OTC/funds management
- `EmployeeAgent` — securities trading
- `EmployeeBasic` — clients/accounts/cards/credits

---

### GET /api/employees

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

### POST /api/employees

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
  "last_name": "Petrović",
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

### GET /api/employees/:id

Get a single employee by ID.

**Authentication:** Employee JWT + `employees.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Response 200:** Employee object
**Response 404:** `{"error": "employee not found"}`

---

### PUT /api/employees/:id

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

### GET /api/roles

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

### GET /api/roles/:id

Get a single role by ID.

**Authentication:** Employee JWT + `employees.permissions` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Role ID |

**Response 200:** Role object with permissions array
**Response 404:** `{"error": "role not found"}`

---

### POST /api/roles

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

### PUT /api/roles/:id/permissions

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

### GET /api/permissions

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

### PUT /api/employees/:id/roles

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

### PUT /api/employees/:id/permissions

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

### POST /api/clients

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
  "last_name": "Jovanović",
  "date_of_birth": 820454400,
  "gender": "M",
  "email": "marko.jovanovic@email.com",
  "phone": "+381611234567",
  "address": "Jovana Subotića 12, Beograd",
  "jmbg": "0506960710123"
}
```

**Response 201:**
```json
{
  "id": 1,
  "first_name": "Marko",
  "last_name": "Jovanović",
  "date_of_birth": 820454400,
  "gender": "M",
  "email": "marko.jovanovic@email.com",
  "phone": "+381611234567",
  "address": "Jovana Subotića 12, Beograd",
  "jmbg": "0506960710123",
  "active": false,
  "created_at": "2026-03-13T10:00:00Z"
}
```

---

### GET /api/clients

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

### GET /api/clients/:id

Get a single client by ID.

**Authentication:** Employee JWT + `clients.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Response 200:** Client object
**Response 404:** `{"error": "client not found"}`

---

### GET /api/clients/me

Get the currently authenticated client's profile.

**Authentication:** Client JWT

**Response 200:** Client object
**Response 401:** `{"error": "not authenticated"}`

---

### PUT /api/clients/:id

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

### POST /api/accounts

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

### GET /api/accounts

List all accounts with optional filters.

**Authentication:** Employee JWT + `accounts.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `name_filter` | string | Filter by account name |
| `account_number_filter` | string | Filter by account number |
| `type_filter` | string | Filter by account type |

**Response 200:**
```json
{
  "accounts": [
    {
      "id": 1,
      "account_number": "265-1234567890123-56",
      "account_name": "My Current Account",
      "owner_id": 1,
      "owner_name": "Marko Jovanović",
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

### GET /api/accounts/:id

Get a single account by ID.

**Authentication:** Employee JWT + `accounts.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:** Account object
**Response 404:** `{"error": "account not found"}`

---

### GET /api/accounts/by-number/:account_number

Get an account by its account number.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `account_number` | string | Full account number |

**Response 200:** Account object
**Response 404:** `{"error": "account not found"}`

---

### GET /api/accounts/client/:client_id

List all accounts belonging to a specific client. Clients can only access their own accounts (`:client_id` must match the JWT `user_id`).

**Authentication:** Any JWT (Employee or Client)

**Response 403:** `{"error": "clients can only access their own resources"}` (client accessing another client's data)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

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

### PUT /api/accounts/:id/name

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

### PUT /api/accounts/:id/limits

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
| `verification_code` | string | Yes | Verification code for authorization (obtained via `POST /api/verification`) |

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

### PUT /api/accounts/:id/status

Update the status of an account (activate, block, close, etc.).

**Authentication:** Employee JWT + `accounts.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | string | Yes | `"active"` or `"inactive"` |

**Response 200:** Updated account object

---

### GET /api/currencies

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
      "symbol": "€"
    }
  ]
}
```

---

### POST /api/companies

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
  "address": "Bulevar Oslobođenja 1, Novi Sad",
  "owner_id": 1
}
```

---

## 6. Cards

Card endpoints require specific employee permissions (see per-endpoint notes). Creating cards requires `cards.create`; blocking, unblocking, and deactivating require `cards.update`; approving/rejecting card requests requires `cards.approve`. Clients can read their own cards.

---

### POST /api/cards

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

### GET /api/cards/:id

Get a card by ID.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object (with masked card number)
**Response 404:** `{"error": "card not found"}`

---

### GET /api/cards/account/:account_number

List all cards linked to a specific account.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `account_number` | string | Account number |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ]
}
```

---

### GET /api/cards/client/:client_id

List all cards belonging to a specific client. Clients can only access their own cards (`:client_id` must match the JWT `user_id`).

**Authentication:** Any JWT (Employee or Client)

**Response 403:** `{"error": "clients can only access their own resources"}` (client accessing another client's data)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ]
}
```

---

### PUT /api/cards/:id/block

Block a card (e.g., reported as lost or stolen).

**Authentication:** Employee JWT + `cards.update` permission **OR** Client JWT (own cards only)

Employees with the `cards.update` permission can block any card. Clients can block their own cards only — the card's `owner_id` must match the authenticated client's `user_id`. If a client attempts to block a card that does not belong to them, a `403 Forbidden` error is returned.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "BLOCKED"`

**Response 403:** `{"error": "clients can only block their own cards"}` (client attempting to block another client's card)

**Response 404:** `{"error": "card not found"}` (card does not exist)

---

### PUT /api/cards/:id/unblock

Unblock a previously blocked card. Only employees can unblock cards.

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "ACTIVE"`

---

### PUT /api/cards/:id/deactivate

Permanently deactivate a card.

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "DEACTIVATED"`

---

### POST /api/cards/authorized-person

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
  "last_name": "Jovanović",
  "date_of_birth": 946684800,
  "gender": "F",
  "email": "ana.j@email.com",
  "phone": "+381651234567",
  "address": "Trg Slobode 5, Novi Sad",
  "account_id": 1
}
```

---

### POST /api/cards/virtual

Create a virtual card for a client account. Virtual cards can be single-use or multi-use and expire after 1-3 months.

**Authentication:** Client JWT

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

### POST /api/cards/:id/pin

Set the 4-digit PIN for a card.

**Authentication:** Client JWT

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

### POST /api/cards/:id/verify-pin

Verify the 4-digit PIN for a card. The card is permanently blocked after 3 consecutive failed attempts.

**Authentication:** Client JWT

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

### POST /api/cards/:id/temporary-block

Temporarily block a card for a specified duration in hours. The card is automatically unblocked by a background job when the duration expires.

**Authentication:** Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `duration_hours` | int32 | Yes | Block duration in hours (1–720) |
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

### POST /api/payments

Initiate a new payment from a client account.

**Authentication:** Client JWT

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
  "status": "COMPLETED",
  "timestamp": "2026-03-13T10:00:00Z"
}
```

---

### GET /api/payments/:id

Get a payment by ID.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Response 200:** Payment object
**Response 404:** `{"error": "payment not found"}`

---

### GET /api/payments/account/:account_number

List payments for a specific account with filters.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `account_number` | string | Account number |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `date_from` | string | Start date filter (RFC3339 or YYYY-MM-DD) |
| `date_to` | string | End date filter (RFC3339 or YYYY-MM-DD) |
| `status_filter` | string | Filter by status (e.g., `"COMPLETED"`, `"PENDING"`) |
| `amount_min` | float64 | Minimum amount filter |
| `amount_max` | float64 | Maximum amount filter |

**Response 200:**
```json
{
  "payments": [ /* array of payment objects */ ],
  "total": 87
}
```

---

### POST /api/payments/:id/execute

Execute a pending payment after verification. The payment must have been created previously via `POST /api/payments` and a valid verification code must be obtained via `POST /api/verification`.

**Authentication:** Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `verification_code` | string | Yes | Verification code obtained from `POST /api/verification` |

**Example Request:**
```json
{
  "verification_code": "847291"
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
  "status": "COMPLETED",
  "timestamp": "2026-03-13T10:00:00Z"
}
```

| Status | Description |
|---|---|
| 200 | Payment executed |
| 400 | Invalid input or invalid payment ID |
| 401 | Unauthorized |
| 422 | Verification code invalid or expired |
| 500 | Internal server error |

---

### GET /api/payments/client/:client_id

Returns all payments where any of the client's accounts appears as sender or recipient.
Equivalent to `GET /api/transfers/client/:client_id` but for payments.

**Authentication:** Required (employee or client; clients may only query their own `client_id`)

**Path parameters:**

| Parameter   | Type    | Description |
|-------------|---------|-------------|
| `client_id` | integer | Client ID   |

**Query parameters:**

| Parameter   | Type    | Default | Description             |
|-------------|---------|---------|-------------------------|
| `page`      | integer | 1       | Page number             |
| `page_size` | integer | 20      | Items per page          |

**Example request:**
```
GET /api/payments/client/42?page=1&page_size=20
Authorization: Bearer <token>
```

**Response 200:**
```json
{
  "payments": [
    {
      "id": 7,
      "from_account_number": "115-0001234567-10",
      "to_account_number": "115-0009876543-10",
      "initial_amount": "500.0000",
      "final_amount": "500.0000",
      "commission": "0.0000",
      "recipient_name": "John Doe",
      "payment_code": "289",
      "reference_number": "",
      "payment_purpose": "Test",
      "status": "completed",
      "timestamp": "2026-03-24 10:30:00 +0000 UTC"
    }
  ],
  "total": 1
}
```

**Response 400:** `{ "error": "invalid client_id" }`
**Response 401:** `{ "error": "not authenticated" }`
**Response 403:** `{ "error": "forbidden" }` (client accessing another client's data)
**Response 500:** `{ "error": "..." }`

---

## 8. Transfers

Transfers are inter-account currency exchanges (can be same currency or cross-currency).

---

### POST /api/transfers

Initiate a currency transfer between accounts.

**Authentication:** Client JWT

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

---

### GET /api/transfers/:id

Get a transfer by ID.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Response 200:** Transfer object
**Response 404:** `{"error": "transfer not found"}`

---

### GET /api/transfers/client/:client_id

List all transfers for a specific client. Clients can only access their own transfers (`:client_id` must match the JWT `user_id`).

**Authentication:** Any JWT (Employee or Client)

**Response 403:** `{"error": "clients can only access their own resources"}` (client accessing another client's data)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

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

---

### POST /api/transfers/:id/execute

Execute a pending transfer after verification. The transfer must have been created previously via `POST /api/transfers` and a valid verification code must be obtained via `POST /api/verification`.

**Authentication:** Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `verification_code` | string | Yes | Verification code obtained from `POST /api/verification` |

**Example Request:**
```json
{
  "verification_code": "847291"
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
| 422 | Verification code invalid or expired |
| 500 | Internal server error |

---

## 9. Payment Recipients

Saved payment recipients (favorites) for a client.

---

### POST /api/payment-recipients

Save a new payment recipient.

**Authentication:** Client JWT

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

### GET /api/payment-recipients/:client_id

List all saved recipients for a client.

**Authentication:** Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

**Response 200:**
```json
{
  "recipients": [ /* array of recipient objects */ ]
}
```

---

### PUT /api/payment-recipients/:id

Update a saved recipient.

**Authentication:** Client JWT

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

### DELETE /api/payment-recipients/:id

Delete a saved recipient.

**Authentication:** Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Recipient ID |

**Response 200:** `{"success": true}`

---

## 10. Verification Codes

One-time verification codes for authorizing sensitive transactions (2FA).

---

### POST /api/verification

Generate a one-time verification code for a transaction. The code is also sent to the client's email.

**Authentication:** Client JWT

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | Client ID |
| `transaction_id` | uint64 | Yes | ID of the transaction to authorize |
| `transaction_type` | string | Yes | `"PAYMENT"` or `"TRANSFER"` |

**Example Request:**
```json
{
  "client_id": 1,
  "transaction_id": 42,
  "transaction_type": "PAYMENT"
}
```

**Response 201:**
```json
{
  "code": "847291",
  "expires_at": "2026-03-13T10:10:00Z"
}
```

---

### POST /api/verification/validate

Validate a verification code.

**Authentication:** Client JWT

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | Client ID |
| `transaction_id` | uint64 | Yes | Transaction ID being verified |
| `code` | string | Yes | 6-digit code received by email |

**Example Request:**
```json
{
  "client_id": 1,
  "transaction_id": 42,
  "code": "847291"
}
```

**Response 200:**
```json
{
  "valid": true
}
```

---

## 11. Exchange (Menjačnica)

Exchange rate lookup and currency conversion endpoints. All public — no authentication required.

Rates are fetched from `open.er-api.com` every 6 hours and stored in the exchange-service database. The bank applies a configurable spread (±0.3% by default) to derive buy/sell rates from mid-market, and a commission (0.5% by default) on informational `Calculate` calls.

Supported currencies: `RSD`, `EUR`, `USD`, `CHF`, `GBP`, `JPY`, `CAD`, `AUD`

**Backward-compatible aliases** (delegate to the same handlers):
- `GET /api/exchange-rates` → same as `GET /api/exchange/rates`
- `GET /api/exchange-rates/:from/:to` → same as `GET /api/exchange/rates/:from/:to`

---

### GET /api/exchange/rates

List all current buy/sell rates for supported currencies against RSD.

**Authentication:** None (public)

**Response 200:**
```json
{
  "rates": [
    {
      "from_currency": "EUR",
      "to_currency": "RSD",
      "buy_rate": "116.500000",
      "sell_rate": "117.800000",
      "updated_at": "2026-03-13T08:00:00Z"
    }
  ]
}
```

**Response 500:** `{"error": "..."}`

---

### GET /api/exchange/rates/:from/:to

Get the buy/sell rates for a specific currency pair.

**Authentication:** None (public)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `from` | string | Source currency code (e.g., `EUR`) — case-insensitive |
| `to` | string | Target currency code (e.g., `RSD`) — case-insensitive |

**Response 200:**
```json
{
  "from_currency": "EUR",
  "to_currency": "RSD",
  "buy_rate": "116.500000",
  "sell_rate": "117.800000",
  "updated_at": "2026-03-13T08:00:00Z"
}
```

**Response 404:** `{"error": "exchange rate not found"}`

---

### POST /api/exchange/calculate

Calculate the converted amount for a currency exchange, including the bank's selling rate and commission. **Informational only — no transaction is created.**

**Authentication:** None (public)

**Request Body:**
```json
{
  "fromCurrency": "EUR",
  "toCurrency": "USD",
  "amount": "100.00"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `fromCurrency` | string | Yes | Source currency code (case-insensitive) |
| `toCurrency` | string | Yes | Target currency code (case-insensitive) |
| `amount` | string | Yes | Positive decimal amount in source currency |

**Response 200:**
```json
{
  "from_currency": "EUR",
  "to_currency": "USD",
  "input_amount": "100.0000",
  "converted_amount": "106.3215",
  "commission_rate": "0.005",
  "effective_rate": "0.107370"
}
```

**Response 400:** `{"error": "amount must be a positive number"}` or `{"error": "unsupported fromCurrency: XYZ"}`

**Response 404:** `{"error": "exchange rate not found for ..."}` (rate pair not available)

**Response 500:** `{"error": "..."}`

---

## 12. Loans

Loan request and loan management endpoints. Clients can apply and view their loans; employees can approve/reject and view all.

---

### POST /api/loans/requests

Submit a new loan application.

**Authentication:** Client JWT

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `client_id` | uint64 | Yes | Client ID making the request |
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

### GET /api/loans/requests

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

**Response 200:**
```json
{
  "requests": [ /* array of loan request objects */ ],
  "total": 23
}
```

---

### GET /api/loans/requests/:id

Get a single loan request by ID.

**Authentication:** Employee JWT + `credits.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Loan request object
**Response 404:** `{"error": "loan request not found"}`

---

### PUT /api/loans/requests/:id/approve

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

**Response 500 (limit exceeded):**
```json
{
  "error": "loan amount 500000.00 exceeds your approval limit of 100000.00"
}
```

> **Note:** The approving employee's `MaxLoanApprovalAmount` limit is enforced. If the loan request amount exceeds the employee's configured limit, the approval is rejected.

---

### PUT /api/loans/requests/:id/reject

Reject a loan request. Sends a rejection email to the client.

**Authentication:** Employee JWT + `credits.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Updated loan request object with `"status": "REJECTED"`

---

### GET /api/loans

List all active loans (employee view).

**Authentication:** Employee JWT + `credits.read` permission

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

### GET /api/loans/:id

Get a single loan by ID.

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Loan object
**Response 404:** `{"error": "loan not found"}`

---

### GET /api/loans/client/:client_id

List all loans belonging to a specific client. Clients can only access their own loans (`:client_id` must match the JWT `user_id`).

**Authentication:** Any JWT (Employee or Client)

**Response 403:** `{"error": "clients can only access their own resources"}` (client accessing another client's data)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

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

### GET /api/loans/requests/client/:client_id

List all loan requests submitted by a specific client. Clients can only access their own loan requests (`:client_id` must match the JWT `user_id`).

**Authentication:** Any JWT (Employee or Client)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Client ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "requests": [ { "id": 1, "loan_type": "cash", "amount": "500000.0000", "status": "pending" } ],
  "total": 1
}
```

**Response 403:** `{"error": "clients can only access their own resources"}` (client accessing another client's data)

---

### GET /api/loans/:id/installments

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

## 13. Limits

Manage transaction and approval limits for employees, and transaction limits for bank clients.

All monetary values are decimal strings (e.g., `"50000.0000"`).

**Authentication:** All endpoints require a valid employee Bearer token.

**Required permission:** `limits.manage`

---

### GET /api/employees/:id/limits

Retrieve the current transaction and approval limits for an employee.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Employee ID |

**Example request:**
```
GET /api/employees/42/limits
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

### PUT /api/employees/:id/limits

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

### POST /api/employees/:id/limits/template

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

### GET /api/limits/templates

List all predefined and custom limit templates.

**Example request:**
```
GET /api/limits/templates
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

### POST /api/limits/templates

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

### GET /api/clients/:id/limits

Retrieve the current transaction limits for a client.

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| id | integer | Client ID |

**Example request:**
```
GET /api/clients/7/limits
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

### PUT /api/clients/:id/limits

Set or update transaction limits for a client. The employee's own limits constrain the maximum values they may assign (daily and monthly). Requires the authenticated employee's `max_client_daily_limit` ≥ requested `daily_limit` and `max_client_monthly_limit` ≥ requested `monthly_limit`.

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

## Error Response Format

All error responses follow this format:

```json
{
  "error": "human-readable error message"
}
```

**Common HTTP Status Codes:**

| Code | Meaning |
|---|---|
| 200 | Success |
| 201 | Created |
| 400 | Bad request / validation error |
| 401 | Unauthenticated (missing or invalid token) |
| 403 | Forbidden (insufficient permissions or wrong role) |
| 404 | Resource not found |
| 500 | Internal server error |

---

## 14. Bank Accounts

Bank account management endpoints allow administrators to manage internal bank-owned accounts used for fee collection and loan repayments. The bank must always maintain at least one RSD account and at least one foreign currency account.

**Authentication:** Employee token with `bank-accounts.manage` permission

---

### GET /api/bank-accounts

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

### POST /api/bank-accounts

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

### DELETE /api/bank-accounts/:id

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

Configurable fee rules applied to payments and transfers. Multiple active fee rules can apply to the same transaction — they stack additively. For example, a percentage fee AND a fixed fee can both apply to the same transaction.

Fee calculation is DB-backed: if the fee service is unavailable, the transaction is rejected. If no rules match (e.g., amount below threshold), zero fee is charged (not an error).

**Authentication:** Employee token with `fees.manage` permission

**Fee types:**
- `percentage` — charged as a percentage of the transaction amount (e.g., `0.1` = 0.1%)
- `fixed` — a flat fee regardless of amount

---

### GET /api/fees

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

### POST /api/fees

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

### PUT /api/fees/:id

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

### DELETE /api/fees/:id

Deactivate a fee rule. The rule is not deleted from the database — it is soft-deactivated and will no longer apply to new transactions. It can be reactivated via `PUT /api/fees/{id}` with `"active": true`.

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

### GET /api/interest-rate-tiers

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

### POST /api/interest-rate-tiers

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

### PUT /api/interest-rate-tiers/:id

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

### DELETE /api/interest-rate-tiers/:id

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

### POST /api/interest-rate-tiers/:id/apply

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

### GET /api/bank-margins

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

### PUT /api/bank-margins/:id

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

### POST /api/cards/requests

Client submits a request to obtain a card for one of their accounts.

**Authentication:** Client JWT (ClientBearerAuth)

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

### GET /api/cards/requests/me

Returns all card requests submitted by the authenticated client.

**Authentication:** Client JWT (ClientBearerAuth)

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

### GET /api/cards/requests

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

### GET /api/cards/requests/:id

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

### PUT /api/cards/requests/:id/approve

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
| 422 | Request already processed (not pending) |
| 500 | Internal server error |

---

### PUT /api/cards/requests/:id/reject

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
| 422 | Request already processed (not pending) |
| 500 | Internal server error |

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

2. **Client vs Employee routes:** The API gateway uses separate middleware for employee and client JWTs. Do not use a client token to call employee endpoints or vice versa.

3. **Pagination:** All list endpoints support `page` (1-based) and `page_size` query parameters. Default page size is 20.

4. **Date fields:** `date_of_birth` is a Unix timestamp in seconds. Convert to/from a date object in your application.

5. **Account numbers:** Account numbers follow the format `265-XXXXXXXXXXX-YY` (Serbian bank account format with control digits).

6. **Card numbers:** The full card number and CVV are only returned in the create card response. Subsequent reads return a masked card number (e.g., `**** **** **** 4242`).

7. **JMBG:** The 13-digit Serbian national ID. Validated server-side for exact length and uniqueness.

8. **CORS:** The API Gateway allows all origins with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` methods and `Authorization`, `Content-Type` headers.

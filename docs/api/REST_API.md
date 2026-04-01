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
22. [Mobile Verification](#22-mobile-verification)
23. [Browser Verification](#23-browser-verification)
24. [WebSocket](#24-websocket)
25. [Stock Exchanges](#25-stock-exchanges)
26. [Securities](#26-securities)
27. [Orders](#27-orders)
28. [Portfolio](#28-portfolio)
29. [OTC Offers](#29-otc-offers)
30. [Actuaries](#30-actuaries)
31. [Tax](#31-tax)

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

### ~~GET /api/clients/me~~ (removed — use GET /api/me)

> **Removed.** Use `GET /api/me` instead. The new endpoint accepts both employee and client JWTs and returns the current principal's profile.

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

List all accounts with optional filters. Pass `client_id` to filter by owner — this replaces the old `GET /api/accounts/client/:client_id` path. Clients looking for their own accounts should use `GET /api/me/accounts` instead.

**Authentication:** Employee JWT + `accounts.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `name_filter` | string | Filter by account name |
| `account_number_filter` | string | Filter by account number |
| `type_filter` | string | Filter by account type |
| `client_id` | int | Filter accounts belonging to a specific client (replaces `GET /api/accounts/client/:client_id`) |

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

### ~~GET /api/accounts/client/:client_id~~ (removed)

> **Removed.** Employees should use `GET /api/accounts?client_id=X`. Clients should use `GET /api/me/accounts`.

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

**Authentication:** Employee JWT only (`AuthMiddleware` + `cards.read` permission). Clients must use `GET /api/me/cards/:id` instead.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object (with masked card number)
**Response 404:** `{"error": "card not found"}`

---

### ~~GET /api/cards/account/:account_number~~ (removed)

> **Removed.** Use `GET /api/cards?account_number=X` instead.

---

### ~~GET /api/cards/client/:client_id~~ (removed)

> **Removed.** Employees should use `GET /api/cards?client_id=X`. Clients should use `GET /api/me/cards`.

---

### GET /api/cards

List cards with optional filters. Employees can filter by `client_id` or `account_number`. Exactly one filter should be provided; if neither is provided, all cards visible to the employee are returned.

**Authentication:** Employee JWT + `cards.manage` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Filter cards belonging to a specific client |
| `account_number` | string | Filter cards linked to a specific account number |
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |

**Response 200:**
```json
{
  "cards": [ /* array of card objects */ ],
  "total": 5
}
```

---

### POST /api/cards/:id/block

Block a card (e.g., reported as lost or stolen). Method changed from `PUT` to `POST`.

**Authentication:** Employee JWT + `cards.update` permission

Employees with the `cards.update` permission can block any card. Clients who want to block their own card should use `POST /api/me/cards/:id/block` (see [Me — Self-Service](#20-me-self-service) below, though the temporary block for a duration is at `POST /api/me/cards/:id/temporary-block`).

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "BLOCKED"`

**Response 404:** `{"error": {"code": "not_found", "message": "card not found"}}`

---

### POST /api/cards/:id/unblock

Unblock a previously blocked card. Only employees can unblock cards. Method changed from `PUT` to `POST`.

**Authentication:** Employee JWT + `cards.update` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "ACTIVE"`

---

### POST /api/cards/:id/deactivate

Permanently deactivate a card. Method changed from `PUT` to `POST`.

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

### ~~POST /api/cards/virtual~~ (moved — use POST /api/me/cards/virtual)

> **Moved to `/api/me/*`.** See `POST /api/me/cards/virtual` in the [Me — Self-Service](#20-me-self-service) section.

The documentation below is preserved for reference; the request/response shape is unchanged.

### POST /api/me/cards/virtual

Create a virtual card for a client account. Virtual cards can be single-use or multi-use and expire after 1-3 months.

**Authentication:** Any JWT (AnyAuthMiddleware — identity scoped to the token principal)

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

### ~~POST /api/cards/:id/pin~~ (moved — use POST /api/me/cards/:id/pin)

> **Moved to `/api/me/*`.** See `POST /api/me/cards/:id/pin` in the [Me — Self-Service](#20-me-self-service) section. Request/response shape is unchanged.

### POST /api/me/cards/:id/pin

Set the 4-digit PIN for a card.

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

### ~~POST /api/cards/:id/verify-pin~~ (moved — use POST /api/me/cards/:id/verify-pin)

> **Moved to `/api/me/*`.** See `POST /api/me/cards/:id/verify-pin` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/cards/:id/verify-pin

Verify the 4-digit PIN for a card. The card is permanently blocked after 3 consecutive failed attempts.

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

### ~~POST /api/cards/:id/temporary-block~~ (moved — use POST /api/me/cards/:id/temporary-block)

> **Moved to `/api/me/*`.** See `POST /api/me/cards/:id/temporary-block` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/cards/:id/temporary-block

Temporarily block a card for a specified duration in hours. The card is automatically unblocked by a background job when the duration expires.

**Authentication:** Any JWT (AnyAuthMiddleware)

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

### ~~POST /api/payments~~ (moved — use POST /api/me/payments)

> **Moved to `/api/me/*`.** See `POST /api/me/payments` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/payments

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
| `method` | string | No | Verification method: `code_pull` (default), `qr_scan`, `number_match`. Ignored if user has no mobile device (falls back to `email`). |

**Example Request:**
```json
{
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-9876543210987-12",
  "amount": 5000.00,
  "recipient_name": "EX Tech d.o.o.",
  "payment_code": "289",
  "reference_number": "97 123456789",
  "payment_purpose": "Invoice #INV-2026-001",
  "method": "code_pull"
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

> **Note:** Payment is created in `pending_verification` status. The browser must create a verification challenge via `POST /api/verifications` and then poll `GET /api/verifications/:id/status` until verified. Once verified, call `POST /api/me/payments/:id/execute` with the `challenge_id`. Users with `verification.skip` permission skip verification entirely.

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

### ~~POST /api/payments/:id/execute~~ (moved — use POST /api/me/payments/:id/execute)

> **Moved to `/api/me/*`.** See `POST /api/me/payments/:id/execute` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/payments/:id/execute

Execute a pending payment after verification. The payment must have been created previously via `POST /api/me/payments`. Verification is handled by the verification-service — pass the `challenge_id` from the completed verification challenge.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | The verification challenge ID (must have status `verified`) |
| `verification_code` | string | No | Deprecated — kept for backwards compatibility |

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

### ~~GET /api/payments/client/:client_id~~ (removed)

> **Removed.** Employees should use `GET /api/payments?client_id=X`. Clients should use `GET /api/me/payments`.

---

### GET /api/payments

List payments with optional filters. For employees, pass `client_id` or `account_number` to filter. This endpoint replaces the old path-based `GET /api/payments/client/:client_id` and `GET /api/payments/account/:account_number`.

**Authentication:** Employee JWT + `transactions.read` permission

**Query Parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `client_id` | integer | — | Filter payments where the client's accounts appear as sender or recipient |
| `account_number` | string | — | Filter payments for a specific account number |
| `page` | integer | 1 | Page number |
| `page_size` | integer | 20 | Items per page |
| `date_from` | string | — | Start date filter (RFC3339 or YYYY-MM-DD) |
| `date_to` | string | — | End date filter (RFC3339 or YYYY-MM-DD) |
| `status_filter` | string | — | Filter by status (e.g., `"COMPLETED"`, `"PENDING"`) |
| `amount_min` | float64 | — | Minimum amount filter |
| `amount_max` | float64 | — | Maximum amount filter |

**Response 200:**
```json
{
  "payments": [ /* array of payment objects */ ],
  "total": 87
}
```

---

## 8. Transfers

Transfers are inter-account currency exchanges (can be same currency or cross-currency).

---

### ~~POST /api/transfers~~ (moved — use POST /api/me/transfers)

> **Moved to `/api/me/*`.** See `POST /api/me/transfers` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/transfers

Initiate a currency transfer between accounts.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `from_account_number` | string | Yes | Source account number |
| `to_account_number` | string | Yes | Destination account number |
| `amount` | float64 | Yes | Amount to transfer (in source currency) |
| `method` | string | No | Verification method: `code_pull` (default), `qr_scan`, `number_match`. Ignored if user has no mobile device (falls back to `email`). |

**Example Request:**
```json
{
  "from_account_number": "265-1234567890123-56",
  "to_account_number": "265-1234500000EUR-78",
  "amount": 1000.00,
  "method": "code_pull"
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

> **Note:** Transfer is created in `pending_verification` status. The browser must create a verification challenge via `POST /api/verifications` and then poll `GET /api/verifications/:id/status` until verified. Once verified, call `POST /api/me/transfers/:id/execute` with the `challenge_id`. Users with `verification.skip` permission skip verification entirely.

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

### ~~GET /api/transfers/client/:client_id~~ (removed)

> **Removed.** Employees should use `GET /api/transfers?client_id=X`. Clients should use `GET /api/me/transfers`.

---

### GET /api/transfers

List transfers with optional filters. Pass `client_id` to filter by owner.

**Authentication:** Employee JWT + `transactions.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `client_id` | int | Filter transfers belonging to a specific client |
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

### ~~POST /api/transfers/:id/execute~~ (moved — use POST /api/me/transfers/:id/execute)

> **Moved to `/api/me/*`.** See `POST /api/me/transfers/:id/execute` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/transfers/:id/execute

Execute a pending transfer after verification. The transfer must have been created previously via `POST /api/me/transfers`. Verification is handled by the verification-service — pass the `challenge_id` from the completed verification challenge.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `challenge_id` | uint64 | Yes | The verification challenge ID (must have status `verified`) |
| `verification_code` | string | No | Deprecated — kept for backwards compatibility |

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

### ~~POST /api/payment-recipients~~ (moved — use POST /api/me/payment-recipients)

> **Moved to `/api/me/*`.** See `POST /api/me/payment-recipients` in the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/payment-recipients

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

### ~~GET /api/payment-recipients/:client_id~~ (removed — use GET /api/me/payment-recipients)

> **Removed.** Use `GET /api/me/payment-recipients` instead. Identity is inferred from the JWT — no `client_id` path segment needed.

---

### PUT /api/me/payment-recipients/:id

Update a saved recipient. (Previously `PUT /api/payment-recipients/:id` — now under `/api/me/*`.)

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

### DELETE /api/me/payment-recipients/:id

Delete a saved recipient. (Previously `DELETE /api/payment-recipients/:id` — now under `/api/me/*`.)

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Recipient ID |

**Response 200:** `{"success": true}`

---

## 10. Exchange Rates

Public endpoints — no authentication required.

Supported currencies: `RSD`, `EUR`, `USD`, `CHF`, `GBP`, `JPY`, `CAD`, `AUD`.

---

### GET /api/exchange/rates

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

### GET /api/exchange/rates/:from/:to

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

### POST /api/exchange/calculate

Calculate a currency conversion including the bank's commission. Informational only — no transaction is created.

**Authentication:** None (public)

**Request body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `fromCurrency` | string | yes | Source currency code (e.g. `EUR`) |
| `toCurrency` | string | yes | Target currency code (e.g. `RSD`) |
| `amount` | string | yes | Amount to convert (must be positive decimal) |

**Example request:**
```json
POST /api/exchange/calculate
Content-Type: application/json

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

A verification code has been sent to the client's registered email. Use it when calling the execute endpoint.

| Code | Description |
|---|---|
| 200 | Conversion result |
| 400 | Validation error (missing fields, invalid amount, unsupported currency) — `{"error": {"code": "validation_error", "message": "..."}}` |
| 404 | Exchange rate not found for the requested pair |
| 500 | Internal error |

---

## 11. Loans

Loan management endpoints. Employees can view all loans and approve/reject loan requests. Clients should use `GET /api/me/loans` and related `/api/me/*` routes to view and manage their own loans.

Loan request management has been promoted to its own top-level section — see [Section 13: Loan Requests](#13-loan-requests).

---

### ~~POST /api/loans/requests~~ (moved — use POST /api/me/loan-requests)

> **Moved.** Clients should use `POST /api/me/loan-requests`. See the [Me — Self-Service](#20-me-self-service) section.

### POST /api/me/loan-requests

Submit a new loan application.

**Authentication:** Any JWT (AnyAuthMiddleware)

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

### ~~GET /api/loans/requests~~ (moved — use GET /api/loan-requests)

> **Moved to top-level.** See `GET /api/loan-requests` in [Section 13: Loan Requests](#13-loan-requests).

### ~~GET /api/loans/requests/:id~~ (moved — use GET /api/loan-requests/:id)

> **Moved to top-level.** See `GET /api/loan-requests/:id` in [Section 13: Loan Requests](#13-loan-requests).

### ~~PUT /api/loans/requests/:id/approve~~ (moved and method changed — use POST /api/loan-requests/:id/approve)

> **Moved and method changed to POST.** See `POST /api/loan-requests/:id/approve` in [Section 13: Loan Requests](#13-loan-requests).

### ~~PUT /api/loans/requests/:id/reject~~ (moved and method changed — use POST /api/loan-requests/:id/reject)

> **Moved and method changed to POST.** See `POST /api/loan-requests/:id/reject` in [Section 13: Loan Requests](#13-loan-requests).

---

### ~~GET /api/loans/requests/client/:client_id~~ (removed — use GET /api/me/loan-requests)

> **Removed.** Clients should use `GET /api/me/loan-requests`. Employees should use `GET /api/loan-requests?client_id=X`.

---

### GET /api/loans

List loans (employee view). Pass `client_id` to filter loans for a specific client — this replaces the old `GET /api/loans/client/:client_id`. Clients should use `GET /api/me/loans`.

**Authentication:** Employee JWT + `credits.read` permission

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 20) |
| `loan_type_filter` | string | Filter by loan type |
| `account_number_filter` | string | Filter by account number |
| `status_filter` | string | Filter by status |
| `client_id` | int | Filter loans belonging to a specific client (replaces `GET /api/loans/client/:client_id`) |

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

### ~~GET /api/loans/client/:client_id~~ (removed)

> **Removed.** Employees should use `GET /api/loans?client_id=X`. Clients should use `GET /api/me/loans`.

---

### ~~GET /api/loans/requests/client/:client_id~~ (removed)

> **Removed.** Clients should use `GET /api/me/loan-requests`. Employees should use `GET /api/loan-requests?client_id=X`.

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

## 12. Loan Requests

Loan request management endpoints (employee-facing). These routes have been promoted from the `/api/loans/requests/` sub-path to the top-level `/api/loan-requests/`. Clients should use the `/api/me/loan-requests` routes instead.

---

### GET /api/loan-requests

List all loan requests (employee view). Previously `GET /api/loans/requests`.

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

### GET /api/loan-requests/:id

Get a single loan request by ID. Previously `GET /api/loans/requests/:id`.

**Authentication:** Employee JWT + `credits.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Loan request object
**Response 404:** `{"error": {"code": "not_found", "message": "loan request not found"}}`

---

### POST /api/loan-requests/:id/approve

Approve a loan request. Creates a loan and sends an approval email to the client. Previously `PUT /api/loans/requests/:id/approve` — method changed to POST.

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

### POST /api/loan-requests/:id/reject

Reject a loan request. Sends a rejection email to the client. Previously `PUT /api/loans/requests/:id/reject` — method changed to POST.

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
| `internal_error` | 500 | Unexpected server-side failure |

**Common HTTP Status Codes:**

| Code | Meaning |
|---|---|
| 200 | Success |
| 201 | Created |
| 400 | Bad request / validation error |
| 401 | Unauthenticated (missing or invalid token) |
| 403 | Forbidden (insufficient permissions or wrong role) |
| 404 | Resource not found |
| 409 | Business rule violation (gRPC FailedPrecondition) |
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

### ~~POST /api/cards/requests~~ (moved — use POST /api/me/cards/requests)

> **Moved to `/api/me/*`.** See `POST /api/me/cards/requests` in [Section 20: Me — Self-Service](#20-me-self-service).

### POST /api/me/cards/requests

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

### ~~GET /api/cards/requests/me~~ (moved — use GET /api/me/cards/requests)

> **Moved to `/api/me/*`.** See `GET /api/me/cards/requests` in [Section 20: Me — Self-Service](#20-me-self-service).

### GET /api/me/cards/requests

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

### POST /api/cards/requests/:id/approve

Employee approves a pending card request. This creates the actual card. Method changed from `PUT` to `POST`.

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
| 409 | Request already processed (not pending) — `business_rule_violation` |
| 500 | Internal server error |

---

### POST /api/cards/requests/:id/reject

Employee rejects a pending card request with a reason. Method changed from `PUT` to `POST`.

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
| 409 | Request already processed (not pending) — `business_rule_violation` |
| 500 | Internal server error |

---

## 19. Me (Self-Service)

The `/api/me/*` route group provides self-service access for both employees and bank clients. All routes in this group are protected by `AnyAuthMiddleware`, which accepts any valid JWT (employee or client). Results are automatically scoped to the authenticated principal — no `client_id` path segment is needed.

This group replaces all the old `clientProtected` routes that were previously scattered across other sections.

---

### GET /api/me

Get the currently authenticated principal's profile. Replaces `GET /api/clients/me`.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200 (client):** Client profile object
**Response 200 (employee):** Employee profile object
**Response 401:** `{"error": {"code": "not_authenticated", "message": "..."}}`

---

### GET /api/me/accounts

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

### GET /api/me/accounts/:id

Get a single account by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Response 200:** Account object
**Response 404:** `{"error": {"code": "not_found", "message": "account not found"}}`

---

### GET /api/me/cards

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

### GET /api/me/cards/:id

Get a single card by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object
**Response 404:** `{"error": {"code": "not_found", "message": "card not found"}}`

---

### POST /api/me/payments

Initiate a new payment. The authenticated principal must be the owner of the source account.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as the old `POST /api/payments` — see [Section 7: Payments](#7-payments) for field documentation.

**Response 201:** Payment object (includes `verification_code_expires_at` Unix timestamp). A verification code is automatically sent to the client's registered email — use it when calling `POST /api/me/payments/:id/execute`.
**Response 400:** Validation error

---

### GET /api/me/payments

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

### GET /api/me/payments/:id

Get a single payment by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Response 200:** Payment object

---

### POST /api/me/payments/:id/execute

Execute a pending payment after verification.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `verification_code` | string | Yes | Verification code sent automatically to the client's registered email when the payment was created |

**Response 200:** Executed payment object

---

### POST /api/me/transfers

Initiate a currency transfer between accounts.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as the old `POST /api/transfers` — see [Section 8: Transfers](#8-transfers).

**Response 201:** Transfer object (includes `verification_code_expires_at` Unix timestamp). A verification code is automatically sent to the client's registered email — use it when calling `POST /api/me/transfers/:id/execute`.

---

### GET /api/me/transfers

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

### GET /api/me/transfers/:id

Get a single transfer by ID.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Response 200:** Transfer object

---

### POST /api/me/transfers/:id/execute

Execute a pending transfer after verification.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `verification_code` | string | Yes | Verification code sent automatically to the client's registered email when the transfer was created |

**Response 200:** Executed transfer object

---

### POST /api/me/payment-recipients

Save a new payment recipient.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as the old `POST /api/payment-recipients` — see [Section 9: Payment Recipients](#9-payment-recipients).

**Response 201:** Recipient object

---

### GET /api/me/payment-recipients

List all saved recipients for the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:**
```json
{
  "recipients": [ /* array of recipient objects */ ]
}
```

---

### POST /api/me/loan-requests

Submit a new loan application.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:** Same as the old `POST /api/loans/requests` — see [Section 12: Loans](#12-loans) for field documentation.

**Response 201:** Loan request object

---

### GET /api/me/loan-requests

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

### GET /api/me/loans

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

### GET /api/me/loans/:id

Get a single loan by ID, scoped to the authenticated principal.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Loan object
**Response 404:** `{"error": {"code": "not_found", "message": "loan not found"}}`

---

### GET /api/me/loans/:id/installments

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

## Password Requirements

Passwords for both employees and clients must satisfy:
- 8 to 32 characters
- At least 2 digits
- At least 1 uppercase letter
- At least 1 lowercase letter

---

## Notes for Frontend Developers

1. **Token expiry:** Access tokens expire after 15 minutes. Implement automatic refresh using the refresh token before expiry.

2. **Client vs Employee routes:** Employee routes require an employee JWT with specific permissions. Client self-service routes are under `/api/me/*` and accept any valid JWT (employee or client). Do not use a client token to call employee-only endpoints.

3. **Error format:** All error responses are structured objects: `{"error": {"code": "...", "message": "..."}}`. Parse `error.code` for programmatic error handling and `error.message` for display.

4. **Pagination:** All list endpoints support `page` (1-based) and `page_size` query parameters. Default page size is 20.

5. **Date fields:** `date_of_birth` is a Unix timestamp in seconds. Convert to/from a date object in your application.

6. **Account numbers:** Account numbers follow the format `265-XXXXXXXXXXX-YY` (Serbian bank account format with control digits).

7. **Card numbers:** The full card number and CVV are only returned in the create card response. Subsequent reads return a masked card number (e.g., `**** **** **** 4242`).

8. **JMBG:** The 13-digit Serbian national ID. Validated server-side for exact length and uniqueness.

9. **CORS:** The API Gateway allows all origins with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` methods and `Authorization`, `Content-Type` headers.

10. **Migration:** If upgrading from the previous API, see `docs/api/MIGRATION.md` for a full old→new route mapping and breaking change notes.

## 20. Mobile Auth

Mobile device authentication for the EXBanka mobile app. These endpoints are public (no auth required).

---

### POST /api/mobile/auth/request-activation

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

### POST /api/mobile/auth/activate

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

### POST /api/mobile/auth/refresh

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

### GET /api/mobile/device

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

### POST /api/mobile/device/deactivate

Deactivate the current mobile device. The device will need to go through activation again to reconnect.

**Authentication:** Mobile JWT + `X-Device-ID` header

**Response 200:**
```json
{
  "success": true
}
```

---

### POST /api/mobile/device/transfer

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

## 22. Mobile Verification

Mobile-side verification endpoints. Requires `MobileAuthMiddleware` + `RequireDeviceSignature` (HMAC-SHA256).

See the [Mobile App Integration Guide](../mobile/MOBILE_APP_INTEGRATION.md) for request signing details.

---

### GET /api/mobile/verifications/pending

Poll for pending verification items delivered to this device.

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

The `display_data` contents depend on the verification method:
- **code_pull:** `{ "code": "482916" }` — display the code to the user so they can type it into the browser

> **Note:** `qr_scan` and `number_match` methods are planned but not yet available.

---

### POST /api/mobile/verifications/:challenge_id/submit

Submit a verification response from the mobile app.

**Authentication:** Mobile JWT + `X-Device-ID` + Device Signature

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `challenge_id` | int | Verification challenge ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `response` | string | Yes | The verification response (6-digit code for code_pull) |

**Example Request (code_pull):**
```json
{
  "response": "482916"
}
```

> **Note:** Currently only `code_pull` responses are supported on mobile. `number_match` is planned for a future release.

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

### POST /api/verify/:challenge_id

> **Note:** This endpoint is for the `qr_scan` method which is not yet available. Documented for future reference.

QR code verification endpoint. The mobile app scans a QR code displayed on the browser, extracts the URL and token, and POSTs here.

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

## 23. Browser Verification

Browser-facing verification endpoints for creating and checking verification challenges. Requires `AnyAuthMiddleware`.

---

### POST /api/verifications

Create a new verification challenge for a pending transaction.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `source_service` | string | Yes | Source service: `transaction`, `payment`, `transfer` |
| `source_id` | uint64 | Yes | The payment/transfer ID that triggered verification |
| `method` | string | No | `code_pull` (default) or `email`. Other methods (`qr_scan`, `number_match`) are not yet available. |

**Example Request:**
```json
{
  "source_service": "transaction",
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

The `challenge_data` contents depend on the method:
- **code_pull:** `{}` (code is delivered to mobile app; if no mobile app registered, use `email` method instead)
- **email:** `{}` (code is sent via email to the user's registered address)

> **Note:** `qr_scan` and `number_match` methods are planned but not yet available.

| Status | Description |
|---|---|
| 200 | Challenge created |
| 400 | Invalid method or missing fields |

---

### GET /api/verifications/:id/status

Poll the status of a verification challenge. The browser polls this endpoint until status becomes `verified`.

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

### POST /api/verifications/:id/code

Submit a verification code from the browser (for `code_pull` and `email` methods where the user types the code into the browser).

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

## 24. WebSocket

Real-time push notifications for mobile devices.

---

### GET /ws/mobile

Establish a WebSocket connection for real-time verification challenge delivery.

**Authentication:** Mobile JWT via `Authorization` header + `X-Device-ID` header

**Connection:**
```
GET /ws/mobile
Authorization: Bearer <mobile_jwt>
X-Device-ID: <device_id>
```

**Server → Client Messages:**
```json
{
  "type": "verification_challenge",
  "challenge_id": 123,
  "method": "code_pull",
  "display_data": { "code": "482916" },
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**Keepalive:** Server sends ping every 30s. Client must respond with pong. Connections with no pong for 60s are closed.

**Reconnection:** If the WebSocket disconnects, fall back to polling `GET /api/mobile/verifications/pending` every 2s (foreground) or 30s (background).

---

## 25. Stock Exchanges

### GET /api/stock-exchanges

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

### GET /api/stock-exchanges/:id

Get a specific stock exchange by ID.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Exchange ID |

**Response 200:** Stock exchange object.

---

### POST /api/stock-exchanges/testing-mode

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

### GET /api/stock-exchanges/testing-mode

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

### GET /api/securities/stocks

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

### GET /api/securities/stocks/:id

Get details of a specific stock.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Stock ID |

---

### GET /api/securities/stocks/:id/history

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

### GET /api/securities/futures

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

### GET /api/securities/futures/:id

Get a specific futures contract.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Futures contract ID |

---

### GET /api/securities/futures/:id/history

Get futures price history. Same query parameters as stocks history.

---

### GET /api/securities/forex

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

### GET /api/securities/forex/:id

Get a specific forex pair.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Forex pair ID |

---

### GET /api/securities/forex/:id/history

Get forex pair price history. Same query parameters as stocks history.

---

### GET /api/securities/options

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

### GET /api/securities/options/:id

Get a specific options contract.

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Option ID |

---

## 27. Orders

### POST /api/me/orders

Create a new stock order.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `listing_id` | uint64 | Yes (buy) | Listing ID (required for buy orders) |
| `holding_id` | uint64 | Yes (sell) | Holding ID (required for sell orders) |
| `direction` | string | Yes | `buy` or `sell` |
| `order_type` | string | Yes | `market`, `limit`, `stop`, or `stop_limit` |
| `quantity` | int64 | Yes | Must be positive |
| `limit_value` | string | Conditional | Required for `limit` or `stop_limit` orders |
| `stop_value` | string | Conditional | Required for `stop` or `stop_limit` orders |
| `all_or_none` | boolean | No | Default: false |
| `margin` | boolean | No | Default: false |
| `account_id` | uint64 | Yes (buy) | Account to debit (required for buy orders) |

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

**Response 201:** Order object.

| Status | Description |
|---|---|
| 201 | Order created |
| 400 | Validation error |

---

### GET /api/me/orders

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

### GET /api/me/orders/:id

Get a specific order.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

---

### POST /api/me/orders/:id/cancel

Cancel a pending order.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

---

### GET /api/orders

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

### POST /api/orders/:id/approve

Approve a pending order.

**Authentication:** Employee JWT + `orders.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

---

### POST /api/orders/:id/decline

Decline a pending order.

**Authentication:** Employee JWT + `orders.approve` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Order ID |

**Response 200:** Updated order object.

---

## 28. Portfolio

### GET /api/me/portfolio

List authenticated user's holdings.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default: 1) |
| `page_size` | int | Items per page (default: 10) |
| `security_type` | string | `stock`, `futures`, or `option` |

**Response 200:**
```json
{
  "holdings": [ ],
  "total_count": 10
}
```

---

### GET /api/me/portfolio/summary

Get portfolio summary (total value, gains/losses, allocation).

**Authentication:** Any JWT (AnyAuthMiddleware)

**Response 200:** Portfolio summary object.

---

### POST /api/me/portfolio/:id/make-public

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

### POST /api/me/portfolio/:id/exercise

Exercise an options contract.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Holding ID (must be an option) |

**Response 200:** Exercise result object.

---

## 29. OTC Offers

### GET /api/otc/offers

List OTC trading offers.

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

### POST /api/otc/offers/:id/buy

Purchase an OTC offer.

**Authentication:** Any JWT (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Offer ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `quantity` | int64 | Yes | Number of units to buy (must be positive) |
| `account_id` | uint64 | Yes | Account to debit |

**Response 200:** Transaction object.

---

## 30. Actuaries

### GET /api/actuaries

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

### PUT /api/actuaries/:id/limit

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

### POST /api/actuaries/:id/reset-limit

Reset used trading limit for an actuary back to zero.

**Authentication:** Employee JWT + `agents.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Response 200:** Updated actuary object.

---

### PUT /api/actuaries/:id/approval

Set whether an actuary's orders require supervisor approval.

**Authentication:** Employee JWT + `agents.manage` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Actuary (employee) ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `need_approval` | boolean | Yes | Whether orders need approval |

**Response 200:** Updated actuary object.

---

## 31. Tax

### GET /api/tax

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

### POST /api/tax/collect

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

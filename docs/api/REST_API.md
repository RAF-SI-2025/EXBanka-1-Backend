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
- **Employee token** — issued via `POST /api/auth/login`, required for employee-protected routes
- **Client token** — issued via `POST /api/auth/client-login`, required for client-protected routes

Employee routes additionally require specific permissions (see per-endpoint notes). Client routes require `role="client"` in the JWT.

Access tokens expire after 15 minutes. Use the refresh token to obtain a new pair.

---

## Table of Contents

1. [Auth](#1-auth)
2. [Employees](#2-employees)
3. [Clients](#3-clients)
4. [Accounts](#4-accounts)
5. [Cards](#5-cards)
6. [Payments](#6-payments)
7. [Transfers](#7-transfers)
8. [Payment Recipients](#8-payment-recipients)
9. [Verification Codes](#9-verification-codes)
10. [Exchange Rates](#10-exchange-rates)
11. [Loans](#11-loans)
12. [Limits](#12-limits)

---

## 1. Auth

### POST /api/auth/login

Authenticate an employee with email and password.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Employee email address |
| `password` | string | Yes | Employee password |

**Example Request:**
```json
{
  "email": "john.doe@exbanka.com",
  "password": "Secur3Pass99"
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

### POST /api/auth/client-login

Authenticate a bank client with email and password. Returns a client-scoped JWT.

**Authentication:** None (public)

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Client email address |
| `password` | string | Yes | Client password |

**Example Request:**
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
  "refresh_token": "b7e1a9c3f2d8..."
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
      "permissions": ["clients.read", "accounts.read", "cards.read", "loans.read"]
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

## 3. Clients

Client management endpoints require an employee JWT with `clients.read` permission (EmployeeBasic+). Clients can view their own profile via a client JWT.

---

### POST /api/clients

Create a new bank client.

**Authentication:** Employee JWT + `clients.read` permission

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

**Authentication:** Employee JWT + `clients.read` permission

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

### POST /api/clients/set-password

Set a client's password hash (used internally during client activation flow).

**Authentication:** Employee JWT + `clients.read` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `user_id` | uint64 | Yes | Client ID |
| `password_hash` | string | Yes | Bcrypt password hash |

**Response 200:** `{"success": true}`

---

## 4. Accounts

Account endpoints require an employee JWT with `accounts.read` permission (EmployeeBasic+). Clients can look up accounts by number.

---

### POST /api/accounts

Create a new bank account.

**Authentication:** Employee JWT + `accounts.read` permission

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `owner_id` | uint64 | Yes | Client ID who owns the account |
| `account_kind` | string | Yes | `"CHECKING"`, `"SAVINGS"`, `"FOREIGN_CURRENCY"`, `"BUSINESS"` |
| `account_type` | string | Yes | `"CURRENT"` or `"TERM"` |
| `account_category` | string | No | `"PERSONAL"` or `"COMPANY"` |
| `currency_code` | string | Yes | ISO 4217 code (e.g., `"RSD"`, `"EUR"`, `"USD"`) |
| `employee_id` | uint64 | No | Employee who created the account |
| `initial_balance` | float64 | No | Starting balance (default: 0) |
| `create_card` | bool | No | Auto-create a debit card for this account |
| `company_id` | uint64 | No | Associated company ID (for business accounts) |

**Example Request:**
```json
{
  "owner_id": 1,
  "account_kind": "CHECKING",
  "account_type": "CURRENT",
  "account_category": "PERSONAL",
  "currency_code": "RSD",
  "employee_id": 5,
  "initial_balance": 10000.00,
  "create_card": true
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
      "account_name": "My Checking Account",
      "owner_id": 1,
      "owner_name": "Marko Jovanović",
      "balance": 15000.50,
      "available_balance": 14500.00,
      "employee_id": 5,
      "created_at": "2026-03-13T10:00:00Z",
      "expires_at": "2031-03-13T10:00:00Z",
      "currency_code": "RSD",
      "status": "ACTIVE",
      "account_kind": "CHECKING",
      "account_type": "CURRENT",
      "account_category": "PERSONAL",
      "maintenance_fee": 150.00,
      "daily_limit": 100000.00,
      "monthly_limit": 500000.00,
      "daily_spending": 0.00,
      "monthly_spending": 0.00,
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

**Authentication:** Employee JWT + `accounts.read` permission, OR Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `account_number` | string | Full account number |

**Response 200:** Account object
**Response 404:** `{"error": "account not found"}`

---

### GET /api/accounts/client/:client_id

List all accounts belonging to a specific client.

**Authentication:** Employee JWT + `accounts.read` permission

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

**Authentication:** Employee JWT + `accounts.read` permission

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

Update the daily/monthly spending limits of an account.

**Authentication:** Employee JWT + `accounts.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Request Body** (at least one required):

| Field | Type | Description |
|---|---|---|
| `daily_limit` | float64 | New daily spending limit |
| `monthly_limit` | float64 | New monthly spending limit |

**Response 200:** Updated account object

---

### PUT /api/accounts/:id/status

Update the status of an account (activate, block, close, etc.).

**Authentication:** Employee JWT + `accounts.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Request Body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | string | Yes | `"ACTIVE"`, `"BLOCKED"`, `"CLOSED"`, `"INACTIVE"` |

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

**Authentication:** Employee JWT + `accounts.read` permission

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

## 5. Cards

Card endpoints require an employee JWT with `cards.read` permission (EmployeeBasic+). Clients can read their own cards.

---

### POST /api/cards

Issue a new payment card linked to an account.

**Authentication:** Employee JWT + `cards.read` permission

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

**Authentication:** Employee JWT + `cards.read` permission, OR Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Card object (with masked card number)
**Response 404:** `{"error": "card not found"}`

---

### GET /api/cards/account/:account_number

List all cards linked to a specific account.

**Authentication:** Employee JWT + `cards.read` permission, OR Client JWT

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

List all cards belonging to a specific client.

**Authentication:** Employee JWT + `cards.read` permission, OR Client JWT

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

**Authentication:** Employee JWT + `cards.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "BLOCKED"`

---

### PUT /api/cards/:id/unblock

Unblock a previously blocked card.

**Authentication:** Employee JWT + `cards.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "ACTIVE"`

---

### PUT /api/cards/:id/deactivate

Permanently deactivate a card.

**Authentication:** Employee JWT + `cards.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response 200:** Updated card object with `"status": "DEACTIVATED"`

---

### POST /api/cards/authorized-person

Create an authorized person who can also hold a card linked to an existing account.

**Authentication:** Employee JWT + `cards.read` permission

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

## 6. Payments

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

**Authentication:** Employee JWT + `accounts.read` permission, OR Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Payment ID |

**Response 200:** Payment object
**Response 404:** `{"error": "payment not found"}`

---

### GET /api/payments/account/:account_number

List payments for a specific account with filters.

**Authentication:** Employee JWT + `accounts.read` permission, OR Client JWT

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

## 7. Transfers

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
  "timestamp": "2026-03-13T10:00:00Z"
}
```

---

### GET /api/transfers/:id

Get a transfer by ID.

**Authentication:** Employee JWT + `accounts.read` permission, OR Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Transfer ID |

**Response 200:** Transfer object
**Response 404:** `{"error": "transfer not found"}`

---

### GET /api/transfers/client/:client_id

List all transfers for a specific client.

**Authentication:** Employee JWT + `accounts.read` permission, OR Client JWT

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

## 8. Payment Recipients

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

## 9. Verification Codes

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

## 10. Exchange Rates

Public endpoints — no authentication required.

---

### GET /api/exchange-rates

List all current exchange rates.

**Authentication:** None (public)

**Response 200:**
```json
{
  "rates": [
    {
      "from_currency": "EUR",
      "to_currency": "RSD",
      "buy_rate": 116.50,
      "sell_rate": 117.80,
      "updated_at": "2026-03-13T08:00:00Z"
    }
  ]
}
```

---

### GET /api/exchange-rates/:from/:to

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
  "buy_rate": 116.50,
  "sell_rate": 117.80,
  "updated_at": "2026-03-13T08:00:00Z"
}
```

**Response 404:** `{"error": "exchange rate not found"}`

---

## 11. Loans

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

**Authentication:** Employee JWT + `loans.read` permission

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

**Authentication:** Employee JWT + `loans.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Loan request object
**Response 404:** `{"error": "loan request not found"}`

---

### PUT /api/loans/requests/:id/approve

Approve a loan request. Creates a loan and sends an approval email to the client.

**Authentication:** Employee JWT + `loans.read` permission

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

---

### PUT /api/loans/requests/:id/reject

Reject a loan request. Sends a rejection email to the client.

**Authentication:** Employee JWT + `loans.read` permission

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan request ID |

**Response 200:** Updated loan request object with `"status": "REJECTED"`

---

### GET /api/loans

List all active loans (employee view).

**Authentication:** Employee JWT + `loans.read` permission

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

**Authentication:** Employee JWT + `loans.read` permission, OR Client JWT

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response 200:** Loan object
**Response 404:** `{"error": "loan not found"}`

---

### GET /api/loans/client/:client_id

List all loans belonging to a specific client.

**Authentication:** Employee JWT + `loans.read` permission, OR Client JWT

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

### GET /api/loans/:id/installments

Get all installment records for a loan.

**Authentication:** Employee JWT + `loans.read` permission, OR Client JWT

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

## 12. Limits

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

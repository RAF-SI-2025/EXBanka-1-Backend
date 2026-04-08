# EXBanka REST API v1 Documentation

> **Version note:** `/api/v1/` mirrors all routes from the unversioned `/api/` surface.
> For the complete route catalog (auth, accounts, cards, payments, transfers, loans, etc.),
> see [REST_API.md](REST_API.md) and substitute `/api/` with `/api/v1/` in all paths.
>
> `/api/latest/` is an alias that rewrites to `/api/v1/`.

This document covers **only the new endpoints introduced in v1** that are not available
on the unversioned `/api/` surface.

---

## Table of Contents

1. [Changelog](#1-changelog-new-in-v1)
2. [Sessions and Login History](#2-sessions-and-login-history)
3. [Securities Candles](#3-securities-candles)
4. [Transfer Preview](#4-transfer-preview)

---

## 1. Changelog (new in v1)

Field-level change history for core entities. All changelog endpoints currently return
**501 Not Implemented** and will be backed by audit trail infrastructure in a future release.

### GET /api/v1/accounts/:id/changelog

Get the field-level change history for an account.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `accounts.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default 1) |
| `page_size` | int | No | Items per page (default 20) |

**Response:** `501 Not Implemented`

```json
{
  "error": {
    "code": "not_implemented",
    "message": "this endpoint is coming in a future release"
  }
}
```

---

### GET /api/v1/employees/:id/changelog

Get the field-level change history for an employee.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `employees.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Response:** `501 Not Implemented`

---

### GET /api/v1/clients/:id/changelog

Get the field-level change history for a client.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `clients.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Response:** `501 Not Implemented`

---

### GET /api/v1/cards/:id/changelog

Get the field-level change history for a card.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `cards.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Response:** `501 Not Implemented`

---

### GET /api/v1/loans/:id/changelog

Get the field-level change history for a loan.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `credits.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Response:** `501 Not Implemented`

---

## 2. Sessions and Login History

These endpoints are mirrored from the existing `/api/me/sessions` and `/api/me/login-history`
routes. They use the same handlers (implemented in Plan 3) and are available under `/api/v1/me/`.

### GET /api/v1/me/sessions

List all active sessions for the authenticated user.

**Authentication:** Bearer token (AnyAuthMiddleware)

See [REST_API.md - Sessions](REST_API.md) for full request/response documentation.

---

### POST /api/v1/me/sessions/revoke

Revoke a specific session.

**Authentication:** Bearer token (AnyAuthMiddleware)

See [REST_API.md - Sessions](REST_API.md) for full request/response documentation.

---

### POST /api/v1/me/sessions/revoke-others

Revoke all sessions except the current one.

**Authentication:** Bearer token (AnyAuthMiddleware)

See [REST_API.md - Sessions](REST_API.md) for full request/response documentation.

---

### GET /api/v1/me/login-history

Get the login history for the authenticated user.

**Authentication:** Bearer token (AnyAuthMiddleware)

See [REST_API.md - Sessions](REST_API.md) for full request/response documentation.

---

## 3. Securities Candles

The candle endpoint is mirrored from the existing `/api/securities/candles` route.
It uses the same handler (implemented in Plan 5) and is available under `/api/v1/securities/`.

### GET /api/v1/securities/candles

Get OHLCV candle chart data for a security.

**Authentication:** Bearer token (AnyAuthMiddleware)

See [REST_API.md - Securities](REST_API.md) for full request/response documentation.

---

## 4. Transfer Preview

### POST /api/v1/me/transfers/preview

Preview transfer costs (fees and exchange rate) without creating the transfer.

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

**Error Responses:**

| Status | Code | Description |
|---|---|---|
| 400 | `validation_error` | Missing or invalid fields |
| 401 | `unauthorized` | Missing or invalid token |
| 404 | `not_found` | Account not found |

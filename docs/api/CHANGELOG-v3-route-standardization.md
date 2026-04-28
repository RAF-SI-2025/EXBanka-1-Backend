# v3 Route Standardization — Frontend Migration Guide

**Date:** 2026-04-28
**Affects:** All frontend/test code calling `/api/v3` endpoints
**Status:** Breaking changes — old routes removed. v3 is the only live API version (no v4 yet), so migration is mandatory.

---

## TL;DR

- **11 routes renamed** for naming consistency (path params, verb usage, plural/singular, action naming)
- **5 list endpoints split** into purpose-specific sub-routes (no more query-param dispatch)
- **5 NEW changelog endpoints** added (fully implemented — no longer 501)

---

## Phase A: Renames

### A1 — Role permission endpoints: `:role_name` → `:id`

The path parameter is now a **numeric role ID**, not a role name string.

| Old | New |
|---|---|
| `POST /api/v3/roles/:role_name/permissions` | `POST /api/v3/roles/:id/permissions` |
| `DELETE /api/v3/roles/:role_name/permissions/:permission` | `DELETE /api/v3/roles/:id/permissions/:permission` |

**Migration:** Look up the role ID from `GET /api/v3/roles` (returns `id` + `name`) and use the numeric ID in the path. The `:permission` segment stays as the permission code string.

**Example:**
```bash
# Old
curl -X POST /api/v3/roles/EmployeeBasic/permissions \
     -d '{"permission":"clients.read.all"}'

# New
curl -X POST /api/v3/roles/1/permissions \
     -d '{"permission":"clients.read.all"}'
```

---

### A2 — Account by number: path endpoint removed, use query param

| Old | New |
|---|---|
| `GET /api/v3/accounts/by-number/:account_number` | `GET /api/v3/accounts?account_number=<number>` |

**Migration:** Replace path-style lookup with a query parameter. The new endpoint returns an **array** (`accounts[]`) of 0 or 1 items — it never returns 404 for a missing account. Unwrap `accounts[0]` to get the single account object.

**Example:**
```bash
# Old — returns a single account object directly
curl /api/v3/accounts/by-number/265-1234567890123-56

# New — returns { "accounts": [...], "total": 0|1 }
curl /api/v3/accounts?account_number=265-1234567890123-56
```

**Response shape difference:** Old endpoint returned a bare account object. New endpoint returns the standard list envelope `{ "accounts": [ {...} ], "total": 1 }`. Update any response parsing code accordingly.

---

### A3 — Order decline → reject

| Old | New |
|---|---|
| `POST /api/v3/orders/:id/decline` | `POST /api/v3/orders/:id/reject` |

No request body change. Just rename the URL segment.

---

### A4 — Authorized person: singular → plural

| Old | New |
|---|---|
| `POST /api/v3/cards/authorized-person` | `POST /api/v3/cards/authorized-persons` |

No request/response body change.

---

### A5 — Stock-source: out of `/admin/` namespace

| Old | New |
|---|---|
| `POST /api/v3/admin/stock-source` | `POST /api/v3/stock-sources` |
| `GET /api/v3/admin/stock-source` | `GET /api/v3/stock-sources/active` |

No request/response body change. The GET endpoint moved to `/active` (a more explicit sub-resource path vs the ambiguous bare collection).

**Example:**
```bash
# Switch source (POST body unchanged)
curl -X POST /api/v3/stock-sources \
     -d '{"source":"generated"}'

# Poll status
curl /api/v3/stock-sources/active
```

---

### A6 — OTC on-behalf: out of `/admin/` namespace, explicit verb suffix

| Old | New |
|---|---|
| `POST /api/v3/otc/admin/offers/:id/buy` | `POST /api/v3/otc/offers/:id/buy-on-behalf` |

No request/response body change. The new URL makes it unambiguous that this is an employee action on behalf of a client (vs. `POST /otc/offers/:id/buy` which is client self-service).

---

### A7 — Account status: `PUT status` split into action pair

| Old | New |
|---|---|
| `PUT /api/v3/accounts/:id/status` with body `{"status":"active"}` | `POST /api/v3/accounts/:id/activate` (no body) |
| `PUT /api/v3/accounts/:id/status` with body `{"status":"inactive"}` | `POST /api/v3/accounts/:id/deactivate` (no body) |

**Migration:** Remove the request body. Pick the appropriate action endpoint based on the desired status.

**Example:**
```bash
# Old
curl -X PUT /api/v3/accounts/42/status \
     -d '{"status":"inactive"}'

# New
curl -X POST /api/v3/accounts/42/deactivate
```

---

### A8 — Actuary approval: `PUT approval` split into action pair

| Old | New |
|---|---|
| `PUT /api/v3/actuaries/:id/approval` with body `{"need_approval":true}` | `POST /api/v3/actuaries/:id/require-approval` (no body) |
| `PUT /api/v3/actuaries/:id/approval` with body `{"need_approval":false}` | `POST /api/v3/actuaries/:id/skip-approval` (no body) |

**Migration:** Remove the request body. Pick the appropriate action endpoint.

**Example:**
```bash
# Old
curl -X PUT /api/v3/actuaries/7/approval \
     -d '{"need_approval":true}'

# New
curl -X POST /api/v3/actuaries/7/require-approval
```

---

### A9 — Session revoke: `POST` with body → `DELETE` with path param

| Old | New |
|---|---|
| `POST /api/v3/me/sessions/revoke` with body `{"session_id": 42}` | `DELETE /api/v3/me/sessions/42` |

**Migration:** Change HTTP method from POST to DELETE, move the session ID from the request body to the URL path, and remove the request body entirely.

**Example:**
```bash
# Old
curl -X POST /api/v3/me/sessions/revoke \
     -H "Authorization: Bearer <token>" \
     -d '{"session_id": 42}'

# New
curl -X DELETE /api/v3/me/sessions/42 \
     -H "Authorization: Bearer <token>"
```

**Note:** `POST /api/v3/me/sessions/revoke-others` is unchanged — it remains a POST with a `current_refresh_token` body.

---

## Phase B: List Endpoint Splits

These endpoints previously dispatched to different gRPC backends based on which query param was provided. Each variant is now its own URL. This eliminates confusing "which filter mode am I in?" logic and makes permissions explicit per endpoint.

### B1 — Payments

| Old | New | Permission |
|---|---|---|
| `GET /api/v3/payments?client_id=X` | `GET /api/v3/clients/:id/payments` | `accounts.read.all` or `accounts.read.own` |
| `GET /api/v3/payments?account_number=X` | `GET /api/v3/accounts/:id/payments` | `accounts.read.all` or `accounts.read.own` |

Note: the account-scoped endpoint takes the **account numeric ID**, not the account number string. Use `GET /api/v3/accounts?account_number=X` first if you only have the number.

All rich filters (`date_from`, `date_to`, `status_filter`, `amount_min`, `amount_max`) are still available as query params on `GET /api/v3/accounts/:id/payments`.

**Example:**
```bash
# Old
curl /api/v3/payments?client_id=5
curl /api/v3/payments?account_number=265-1234567890123-56

# New
curl /api/v3/clients/5/payments
curl /api/v3/accounts/42/payments
```

---

### B2 — Transfers

| Old | New | Permission |
|---|---|---|
| `GET /api/v3/transfers?client_id=X` | `GET /api/v3/clients/:id/transfers` | `accounts.read.all` or `accounts.read.own` |

**Example:**
```bash
# Old
curl /api/v3/transfers?client_id=5

# New
curl /api/v3/clients/5/transfers
```

---

### B3 — Accounts

| Old | New | Permission |
|---|---|---|
| `GET /api/v3/accounts?client_id=X` | `GET /api/v3/clients/:id/accounts` | `accounts.read.all` or `accounts.read.own` |

`GET /api/v3/accounts` still exists but no longer accepts `?client_id`. It now only accepts `name_filter`, `account_number`, and `type_filter` query params (the cross-cutting "find any account" view).

**Example:**
```bash
# Old
curl /api/v3/accounts?client_id=5

# New
curl /api/v3/clients/5/accounts
```

---

### B4 — Loans

| Old | New | Permission |
|---|---|---|
| `GET /api/v3/loans?client_id=X` | `GET /api/v3/clients/:id/loans` | `credits.read.all` or `credits.read.own` |

`GET /api/v3/loans` still exists but no longer accepts `?client_id`. Supports `loan_type_filter`, `account_number_filter`, and `status_filter`.

**Example:**
```bash
# Old
curl /api/v3/loans?client_id=5

# New
curl /api/v3/clients/5/loans
```

---

### B5 — Cards

| Old | New | Permission |
|---|---|---|
| `GET /api/v3/cards?client_id=X` | `GET /api/v3/clients/:id/cards` | `cards.read.all` or `cards.read.own` |
| `GET /api/v3/cards?account_number=X` | `GET /api/v3/accounts/:id/cards` | `cards.read.all` or `cards.read.own` |

`GET /api/v3/cards` (bare collection) has been **removed entirely** — there is no top-level ListAllCards RPC. Use one of the two scoped variants.

Note: the account-scoped endpoint takes the **account numeric ID**, not the account number string.

**Example:**
```bash
# Old
curl /api/v3/cards?client_id=5
curl /api/v3/cards?account_number=265-1234567890123-56

# New
curl /api/v3/clients/5/cards
curl /api/v3/accounts/42/cards
```

---

## Phase C: NEW Changelog Endpoints

Five audit-trail endpoints are now fully implemented (they previously returned 501 Not Implemented). Each returns paginated field-level change history for the entity.

| Endpoint | Permission | Description |
|---|---|---|
| `GET /api/v3/accounts/:id/changelog` | `accounts.read.all` | Account field change history |
| `GET /api/v3/cards/:id/changelog` | `cards.read.all` | Card field change history |
| `GET /api/v3/clients/:id/changelog` | `clients.read.all` | Client field change history |
| `GET /api/v3/loans/:id/changelog` | `credits.read.all` | Loan field change history |
| `GET /api/v3/employees/:id/changelog` | `employees.read.all` | Employee field change history |

**Query Parameters:** `?page=1&page_size=20` (max `page_size=200`)

**Response shape:**
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

**Example:**
```bash
curl /api/v3/accounts/42/changelog?page=1&page_size=20 \
     -H "Authorization: Bearer <employee-token>"
```

---

## Permission Notes

Several scoped endpoints inherit the **parent resource's** read permission rather than the child resource's. This is intentional — viewing a client's payments is a client-read privilege, not a payments-read privilege:

| Endpoint | Required Permission |
|---|---|
| `GET /api/v3/clients/:id/payments` | `accounts.read.all` or `accounts.read.own` |
| `GET /api/v3/clients/:id/transfers` | `accounts.read.all` or `accounts.read.own` |
| `GET /api/v3/clients/:id/accounts` | `accounts.read.all` or `accounts.read.own` |
| `GET /api/v3/clients/:id/loans` | `credits.read.all` or `credits.read.own` |
| `GET /api/v3/clients/:id/cards` | `cards.read.all` or `cards.read.own` |
| `GET /api/v3/accounts/:id/payments` | `accounts.read.all` or `accounts.read.own` |
| `GET /api/v3/accounts/:id/cards` | `cards.read.all` or `cards.read.own` |

The role-permission endpoints now gate on the role's **numeric ID** instead of name. The permission code for the role's permission management is unchanged (`roles.permissions.assign` / `roles.permissions.revoke`).

The actuary action endpoints (`require-approval`, `skip-approval`) require `employees.update.any`.

The account activate/deactivate endpoints require `accounts.deactivate.any`.

---

## Summary of Removed Routes

These routes no longer exist. Requests to them will return 404:

| Removed Route | Replacement |
|---|---|
| `POST /api/v3/roles/:role_name/permissions` | `POST /api/v3/roles/:id/permissions` |
| `DELETE /api/v3/roles/:role_name/permissions/:permission` | `DELETE /api/v3/roles/:id/permissions/:permission` |
| `GET /api/v3/accounts/by-number/:account_number` | `GET /api/v3/accounts?account_number=X` |
| `POST /api/v3/orders/:id/decline` | `POST /api/v3/orders/:id/reject` |
| `POST /api/v3/cards/authorized-person` | `POST /api/v3/cards/authorized-persons` |
| `POST /api/v3/admin/stock-source` | `POST /api/v3/stock-sources` |
| `GET /api/v3/admin/stock-source` | `GET /api/v3/stock-sources/active` |
| `POST /api/v3/otc/admin/offers/:id/buy` | `POST /api/v3/otc/offers/:id/buy-on-behalf` |
| `PUT /api/v3/accounts/:id/status` | `POST /api/v3/accounts/:id/activate` or `/deactivate` |
| `PUT /api/v3/actuaries/:id/approval` | `POST /api/v3/actuaries/:id/require-approval` or `/skip-approval` |
| `POST /api/v3/me/sessions/revoke` | `DELETE /api/v3/me/sessions/:id` |
| `GET /api/v3/payments?client_id=X` | `GET /api/v3/clients/:id/payments` |
| `GET /api/v3/payments?account_number=X` | `GET /api/v3/accounts/:id/payments` |
| `GET /api/v3/transfers?client_id=X` | `GET /api/v3/clients/:id/transfers` |
| `GET /api/v3/accounts?client_id=X` | `GET /api/v3/clients/:id/accounts` |
| `GET /api/v3/loans?client_id=X` | `GET /api/v3/clients/:id/loans` |
| `GET /api/v3/cards?client_id=X` | `GET /api/v3/clients/:id/cards` |
| `GET /api/v3/cards?account_number=X` | `GET /api/v3/accounts/:id/cards` |
| `GET /api/v3/cards` (bare list) | Use `GET /api/v3/clients/:id/cards` or `GET /api/v3/accounts/:id/cards` |

# EXBanka REST API Migration Guide

This document describes the breaking changes introduced in the REST API overhaul and provides a route-by-route mapping from the old API shape to the new one.

**Date of change:** 2026-03-25

---

## Table of Contents

1. [Error Response Format Change](#1-error-response-format-change)
2. [New /api/me/* Self-Service Group](#2-new-apime-self-service-group)
3. [Route Renames and Promotions](#3-route-renames-and-promotions)
4. [Path Parameters Replaced by Query Parameters](#4-path-parameters-replaced-by-query-parameters)
5. [HTTP Method Changes (PUT → POST for Actions)](#5-http-method-changes-put--post-for-actions)
6. [HTTP Status Code Corrections](#6-http-status-code-corrections)
7. [Removed Routes](#7-removed-routes)
8. [Authentication Changes](#8-authentication-changes)
9. [Summary Table](#9-summary-table)

---

## 1. Error Response Format Change

**This is a breaking change for all endpoints.**

All error responses have changed from a flat string to a structured object.

### Old format

```json
{"error": "human-readable message string"}
```

### New format

```json
{
  "error": {
    "code": "snake_case_error_code",
    "message": "Human-readable message"
  }
}
```

The `code` field is a stable machine-readable string that clients can use for programmatic error handling. The `message` field is a human-readable description suitable for display.

### Common error codes

| `code` | HTTP Status | Meaning |
|---|---|---|
| `validation_error` | 400 | Request body or query param validation failed |
| `invalid_input` | 400 | Malformed or out-of-range value |
| `not_authenticated` | 401 | Missing or invalid bearer token |
| `forbidden` | 403 | Authenticated but insufficient permissions |
| `not_found` | 404 | Requested resource does not exist |
| `business_rule_violation` | 409 | Operation violates a business rule (e.g., card already blocked) |
| `internal_error` | 500 | Unexpected server-side failure |

**Migration:** Update all error-handling logic that reads `response.error` as a string to read `response.error.code` and `response.error.message` instead.

---

## 2. New /api/me/* Self-Service Group

A new route group `/api/me/*` has been added. These routes are protected by `AnyAuthMiddleware`, which accepts both employee and client JWTs. Each route scopes its results to the authenticated principal — clients see only their own data, employees see only their own data within this group.

**This group replaces all `clientProtected` routes that were previously scattered across other sections.**

| New route | Description | Old equivalent |
|---|---|---|
| `GET /api/me` | Get current user profile | `GET /api/clients/me` (client only) |
| `GET /api/me/accounts` | List my accounts | `GET /api/accounts/client/:client_id` (client only) |
| `GET /api/me/accounts/:id` | Get my account by ID | (no direct equivalent) |
| `GET /api/me/cards` | List my cards | `GET /api/cards/client/:client_id` (client only) |
| `GET /api/me/cards/:id` | Get my card by ID | (no direct equivalent) |
| `POST /api/me/cards/virtual` | Create virtual card | `POST /api/cards/virtual` (client only) |
| `POST /api/me/cards/requests` | Create card request | `POST /api/cards/requests` (client only) |
| `GET /api/me/cards/requests` | List my card requests | `GET /api/cards/requests/me` (client only) |
| `POST /api/me/cards/:id/pin` | Set card PIN | `POST /api/cards/:id/pin` (client only) |
| `POST /api/me/cards/:id/verify-pin` | Verify card PIN | `POST /api/cards/:id/verify-pin` (client only) |
| `POST /api/me/cards/:id/temporary-block` | Temporarily block card | `POST /api/cards/:id/temporary-block` (client only) |
| `POST /api/me/payments` | Create payment | `POST /api/payments` (client only) |
| `GET /api/me/payments` | List my payments | `GET /api/payments/client/:client_id` (client only) |
| `GET /api/me/payments/:id` | Get my payment | `GET /api/payments/:id` |
| `POST /api/me/payments/:id/execute` | Execute payment | `POST /api/payments/:id/execute` (client only) |
| `POST /api/me/transfers` | Create transfer | `POST /api/transfers` (client only) |
| `GET /api/me/transfers` | List my transfers | `GET /api/transfers/client/:client_id` (client only) |
| `GET /api/me/transfers/:id` | Get my transfer | `GET /api/transfers/:id` |
| `POST /api/me/transfers/:id/execute` | Execute transfer | `POST /api/transfers/:id/execute` (client only) |
| `POST /api/me/payment-recipients` | Create payment recipient | `POST /api/payment-recipients` (client only) |
| `GET /api/me/payment-recipients` | List my payment recipients | `GET /api/payment-recipients/:client_id` (client only) |
| `PUT /api/me/payment-recipients/:id` | Update payment recipient | `PUT /api/payment-recipients/:id` (client only) |
| `DELETE /api/me/payment-recipients/:id` | Delete payment recipient | `DELETE /api/payment-recipients/:id` (client only) |
| `POST /api/me/verification` | Create verification code | `POST /api/verification` (client only) |
| `POST /api/me/verification/validate` | Validate verification code | `POST /api/verification/validate` (client only) |
| `POST /api/me/loan-requests` | Create loan request | `POST /api/loans/requests` (client only) |
| `GET /api/me/loan-requests` | List my loan requests | `GET /api/loans/requests/client/:client_id` (client only) |
| `GET /api/me/loans` | List my loans | `GET /api/loans/client/:client_id` (client only) |
| `GET /api/me/loans/:id` | Get my loan | (scoped from `GET /api/loans/:id`) |
| `GET /api/me/loans/:id/installments` | Get my loan installments | `GET /api/loans/:id/installments` |

**Migration for clients:** Replace all path-based client-scoped calls with the corresponding `/api/me/*` route. Remove the `client_id` path segment — identity is inferred from the JWT.

---

## 3. Route Renames and Promotions

### Loan requests promoted to top-level

Loan request routes have moved from under `/api/loans/requests/` to the top-level `/api/loan-requests/`.

| Old route | New route | Notes |
|---|---|---|
| `POST /api/loans/requests` | `POST /api/me/loan-requests` | Client use: moved to `/api/me/*` |
| `GET /api/loans/requests` | `GET /api/loan-requests` | Employee view: now top-level |
| `GET /api/loans/requests/:id` | `GET /api/loan-requests/:id` | Employee view: now top-level |
| `PUT /api/loans/requests/:id/approve` | `POST /api/loan-requests/:id/approve` | Method also changed to POST |
| `PUT /api/loans/requests/:id/reject` | `POST /api/loan-requests/:id/reject` | Method also changed to POST |
| `GET /api/loans/requests/client/:client_id` | `GET /api/me/loan-requests` | Client use: moved to `/api/me/*` |

---

## 4. Path Parameters Replaced by Query Parameters

Several employee list endpoints previously used path-based client/account filtering. These have been unified into single list endpoints with optional query parameters.

| Old route | New route | Notes |
|---|---|---|
| `GET /api/accounts/client/:client_id` | `GET /api/accounts?client_id=X` | Employee version only. Clients use `GET /api/me/accounts`. |
| `GET /api/cards/client/:client_id` | `GET /api/cards?client_id=X` | Employee version only. Clients use `GET /api/me/cards`. |
| `GET /api/cards/account/:account_number` | `GET /api/cards?account_number=X` | Unified into the same list endpoint. |
| `GET /api/payments/account/:account_number` | `GET /api/payments?account_number=X` | Employee view. |
| `GET /api/payments/client/:client_id` | `GET /api/payments?client_id=X` | Employee view. Clients use `GET /api/me/payments`. |
| `GET /api/transfers/client/:client_id` | `GET /api/transfers?client_id=X` | Employee view. Clients use `GET /api/me/transfers`. |
| `GET /api/loans/client/:client_id` | `GET /api/loans?client_id=X` | Employee view. Clients use `GET /api/me/loans`. |
| `GET /api/loans/requests/client/:client_id` | `GET /api/loan-requests?client_id=X` | Employee view. Clients use `GET /api/me/loan-requests`. |

**Migration:** Update all calls that embed the filter in the path segment to use a query parameter instead. Both `client_id` and `account_number` filters are now mutually exclusive query params on a single list endpoint.

---

## 5. HTTP Method Changes (PUT → POST for Actions)

State-change action endpoints have been standardised to use `POST` instead of `PUT`. These endpoints do not update a resource representation — they trigger a state transition — so `POST` is the semantically correct method.

| Old | New |
|---|---|
| `PUT /api/cards/:id/block` | `POST /api/cards/:id/block` |
| `PUT /api/cards/:id/unblock` | `POST /api/cards/:id/unblock` |
| `PUT /api/cards/:id/deactivate` | `POST /api/cards/:id/deactivate` |
| `PUT /api/cards/requests/:id/approve` | `POST /api/cards/requests/:id/approve` |
| `PUT /api/cards/requests/:id/reject` | `POST /api/cards/requests/:id/reject` |
| `PUT /api/loans/requests/:id/approve` | `POST /api/loan-requests/:id/approve` |
| `PUT /api/loans/requests/:id/reject` | `POST /api/loan-requests/:id/reject` |

**Migration:** Change the HTTP method in all client calls for the above routes from `PUT` to `POST`. Request and response shapes are unchanged.

---

## 6. HTTP Status Code Corrections

| Scenario | Old status | New status | New error code |
|---|---|---|---|
| gRPC `FailedPrecondition` (business rule violation, e.g. card already blocked, PIN already locked) | 422 Unprocessable Entity | 409 Conflict | `business_rule_violation` |

**Migration:** Update any client code that handles `422` responses for business-rule errors to also handle `409`. If you were interpreting `422` as "verification code invalid", note that verification failures remain `400` with code `validation_error`; only pure business-rule violations have moved to `409`.

---

## 7. Removed Routes

The following routes have been removed entirely. Use the indicated replacements.

| Removed route | Replacement |
|---|---|
| `GET /api/clients/me` | `GET /api/me` |
| `GET /api/accounts/client/:client_id` (client call) | `GET /api/me/accounts` |
| `GET /api/cards/client/:client_id` (client call) | `GET /api/me/cards` |
| `GET /api/payments/client/:client_id` (client call) | `GET /api/me/payments` |
| `GET /api/transfers/client/:client_id` (client call) | `GET /api/me/transfers` |
| `GET /api/loans/client/:client_id` (client call) | `GET /api/me/loans` |
| `GET /api/loans/requests/client/:client_id` (client call) | `GET /api/me/loan-requests` |
| `GET /api/cards/requests/me` | `GET /api/me/cards/requests` |
| `POST /api/cards/virtual` (old client route) | `POST /api/me/cards/virtual` |
| `POST /api/cards/requests` (old client route) | `POST /api/me/cards/requests` |
| `POST /api/cards/:id/pin` (old client route) | `POST /api/me/cards/:id/pin` |
| `POST /api/cards/:id/verify-pin` (old client route) | `POST /api/me/cards/:id/verify-pin` |
| `POST /api/cards/:id/temporary-block` (old client route) | `POST /api/me/cards/:id/temporary-block` |
| `POST /api/payments` (old client route) | `POST /api/me/payments` |
| `POST /api/payments/:id/execute` (old client route) | `POST /api/me/payments/:id/execute` |
| `POST /api/transfers` (old client route) | `POST /api/me/transfers` |
| `POST /api/transfers/:id/execute` (old client route) | `POST /api/me/transfers/:id/execute` |
| `POST /api/payment-recipients` (old client route) | `POST /api/me/payment-recipients` |
| `GET /api/payment-recipients/:client_id` | `GET /api/me/payment-recipients` |
| `PUT /api/payment-recipients/:id` (old client route) | `PUT /api/me/payment-recipients/:id` |
| `DELETE /api/payment-recipients/:id` (old client route) | `DELETE /api/me/payment-recipients/:id` |
| `POST /api/verification` (old client route) | `POST /api/me/verification` |
| `POST /api/verification/validate` (old client route) | `POST /api/me/verification/validate` |
| `POST /api/loans/requests` (old client route) | `POST /api/me/loan-requests` |

---

## 8. Authentication Changes

The `/api/me/*` group uses `AnyAuthMiddleware`, which accepts both employee and client JWTs. You no longer need a separate client-login flow to access self-service endpoints — any valid JWT works, and the server scopes results to the authenticated principal.

Employee-only list endpoints (e.g., `GET /api/accounts?client_id=X`) continue to require employee JWTs with appropriate permissions.

---

## 9. Summary Table

| Category | Old pattern | New pattern |
|---|---|---|
| Error body | `{"error": "string"}` | `{"error": {"code": "...", "message": "..."}}` |
| Client self-service | Scattered across sections, requires Client JWT | Unified under `/api/me/*`, any JWT |
| Employee list by client | `GET /api/X/client/:id` | `GET /api/X?client_id=Y` |
| Employee list by account | `GET /api/X/account/:num` | `GET /api/X?account_number=Y` |
| Action endpoints | `PUT /api/X/:id/action` | `POST /api/X/:id/action` |
| Loan requests | Under `/api/loans/requests/` | Top-level `/api/loan-requests/` |
| Business rule errors | `422` | `409` with `business_rule_violation` |

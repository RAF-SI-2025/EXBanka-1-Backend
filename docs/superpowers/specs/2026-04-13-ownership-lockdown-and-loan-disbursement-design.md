---
date: 2026-04-13
title: Ownership Lockdown, Loan Disbursement Saga, and Employee On-Behalf Trading
status: design
---

# Ownership Lockdown, Loan Disbursement Saga, and Employee On-Behalf Trading

## Context

Three problems discovered during a security audit of the API gateway and credit-service:

1. **Loan approval disburses money from thin air.** `credit-service.ApproveLoanRequest` credits the borrower's account but never debits the bank's RSD sentinel account. The disbursement call is also made outside the DB transaction that creates the loan record, so a crash between the two leaves inconsistent state.
2. **Ten ownership-check leaks across `/api/me/*`.** Several handlers accept a resource ID from the URL or body and act on it without verifying the resource belongs to the authenticated client. A client can read or mutate another client's loans, payments, transfers, cards, payment recipients, stock orders, OTC purchases, loan requests, and virtual cards.
3. **No employee path for placing trades on behalf of clients.** The Banka 2025 specification explicitly authorizes EmployeeAgent and EmployeeSupervisor to execute trading orders on behalf of clients ("Agenti – Izvršavaju trgovinske naloge u ime klijenata i banke"), but the gateway has no such endpoint.

## Goals

- Every loan approval either fully disburses atomically (bank debited + client credited + loan active) or fully fails with no partial state.
- Bank liquidity is enforced: if the bank RSD sentinel account cannot cover a loan, the approval is rejected with `409 business_rule_violation`.
- Every `/api/me/*` handler trusts **only** the JWT `user_id` for scoping. No resource ID received from the client is acted on until it has been verified against the caller's ownership.
- Employees with `securities.manage` can place trades (stock, futures, forex, options, OTC) on behalf of a named client, with the acting employee's limits enforced and the action audited.
- `GET /api/v1/loans/:id` provides an employee-facing single-loan lookup (currently only `/api/me/loans/:id` exists, and it is one of the leaks).

## Non-goals

- Investment funds. No fund handlers exist in the codebase today; when they are added, they should follow the same employee on-behalf pattern defined here.
- Rate limiting on card PIN verification. Brute-force protection exists already (card locks after 3 failed attempts); ownership enforcement is the immediate concern.
- Retroactive audit logging of past unauthorized access. The fixes are forward-only.

## Architecture

### Part 1 — Loan disbursement saga

The loan approval flow becomes a three-step saga logged to `saga_log` in credit-service, mirroring the existing installment-collection saga in `cron_service.go`.

**Step 1 — DB transaction in credit-service:**

```go
db.Transaction(func(tx *gorm.DB) error {
    // create loan record with status = "pending_disbursement"
    // create installment schedule
    // write saga_log entries: "debit_bank" (pending), "credit_borrower" (pending)
    return nil
})
```

**Step 2 — Saga execution (outside DB transaction):**

```
accountClient.DebitBankAccount(currency, amount, reference=loan.id)
  → if FailedPrecondition (insufficient liquidity): loan → "failed_disbursement", return 409
  → on success: mark saga "debit_bank" completed

accountClient.UpdateBalance(borrower_account, +amount)
  → on failure: compensate via CreditBankAccount(reference), mark saga "compensating"
    loan → "failed_disbursement"
  → on success: mark saga "credit_borrower" completed, loan → "active"
```

**Step 3 — Post-commit Kafka event:**

```
kafka.publish("credit.loan-disbursed", {loan_id, borrower_id, amount, currency})
```

Published **after** the saga commits, never inside the DB transaction.

**New account-service RPCs:**

```proto
service AccountGRPCService {
  rpc DebitBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse);
  rpc CreditBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse);
}

message BankAccountOpRequest {
  string currency = 1;       // "RSD", "EUR", etc.
  string amount = 2;         // decimal string
  string reference = 3;      // idempotency key — loan id, order id, etc.
  string reason = 4;         // audit string
}
```

Both RPCs:
- Open a DB transaction.
- Resolve the bank sentinel account for the given currency (`is_bank_account=true`, `currency=?`).
- Lock the row with `clause.Locking{Strength: "UPDATE"}`.
- Check the `ledger_entries` table for an existing entry with the same `reference` — if present, return the existing result (idempotent replay).
- `DebitBankAccount`: if `balance < amount`, return `FailedPrecondition` with `"bank has insufficient liquidity in <currency>"`.
- Write a ledger entry keyed on `reference` (unique index).
- Update the bank account balance and bump `Version`.
- Commit.

### Part 2 — Ownership lockdown in /api/me/*

**New helper in `api-gateway/internal/handler/validation.go`:**

```go
// enforceOwnership returns nil if ownerID matches the JWT user_id.
// On mismatch it writes a 404 not_found response (not 403 — we do not leak existence)
// and returns a non-nil error. Callers should `return` immediately on non-nil.
func enforceOwnership(c *gin.Context, ownerID uint64) error {
    uid := uint64(c.GetInt64("user_id"))
    if ownerID != uid {
        apiError(c, 404, ErrNotFound, "resource not found")
        return errors.New("ownership mismatch")
    }
    return nil
}
```

**Handler changes (all in `api-gateway/internal/handler/`):**

| Handler | Route | Fix |
|---|---|---|
| `credit_handler.go:800` `GetMyLoan` | `GET /api/me/loans/:id` | After gRPC call, `enforceOwnership(c, resp.OwnerId)`. |
| `transaction_handler.go:645` `GetMyPayment` | `GET /api/me/payments/:id` | After gRPC call, `enforceOwnership(c, resp.ClientId)`. |
| `transaction_handler.go:692` `GetMyTransfer` | `GET /api/me/transfers/:id` | After gRPC call, `enforceOwnership(c, resp.ClientId)`. |
| `card_handler.go:423` `SetCardPin` | `POST /api/me/cards/:id/pin` | Load card via `GetCard`, `enforceOwnership(c, card.OwnerId)`, then set PIN. |
| `card_handler.go:463` `VerifyCardPin` | `POST /api/me/cards/:id/verify-pin` | Load card, enforce ownership, then verify. |
| `card_handler.go:504` `TemporaryBlockCard` | `POST /api/me/cards/:id/temporary-block` | Load card, enforce ownership, then block. |
| `transaction_handler.go:564` `UpdatePaymentRecipient` | `PUT /api/me/payment-recipients/:id` | Load recipient, enforce ownership, then update. |
| `transaction_handler.go:598` `DeletePaymentRecipient` | `DELETE /api/me/payment-recipients/:id` | Load recipient, enforce ownership, then delete. |
| `credit_handler.go:48` `CreateLoanRequest` | `POST /api/me/loan-requests` | Drop `client_id` from body. Derive from JWT. Frontend may still send it; we silently ignore and log a debug line. Breaking in spirit but transparent to callers that send the correct value. |
| `card_handler.go:369` `CreateVirtualCard` | `POST /api/me/cards/virtual` (new route under `/api/me`) | Drop `owner_id` from body; derive from JWT. Resolve `account_number` via account-service and verify `account.owner_id == user_id`. The existing `POST /api/cards/virtual` route stays alive as an employee route under `AuthMiddleware + cards.manage` permission, where it keeps accepting `owner_id` from body (employee explicitly names the owner). This preserves v1 backwards compatibility while giving clients a safe path. |
| `stock_order_handler.go:19` `CreateOrder` | `POST /api/me/orders` | For `buy`: call `account-service.GetAccount(account_id)`; if `owner_id != user_id` return 404. For `sell`: stock-service enforces `holding.account.owner_id == user_id`. |
| `portfolio_handler.go:129` `BuyOTCOffer` | `POST /api/me/otc/:id/buy` | Same as stock order — verify account belongs to caller. |
| `portfolio_handler.go:87` `ExerciseOption` | `POST /api/me/portfolio/:id/exercise` | Gateway already passes `user_id`; **verify stock-service actually enforces `holding.owner_id == user_id`** and fix that service if it does not. |

All mismatches return `404 not_found` with message `"resource not found"`. We do not use `403 forbidden` because that would confirm the target ID exists.

### Part 3 — Employee on-behalf trading

**`POST /api/v1/orders`** — employee creates a stock/futures/forex/options order on behalf of a named client.

- Auth: `AuthMiddleware` + `RequirePermission("securities.manage")`.
- Request body:
  ```json
  {
    "client_id": 42,
    "account_id": 101,
    "listing_id": 7,
    "holding_id": 0,
    "direction": "buy",
    "order_type": "limit",
    "quantity": 10,
    "limit_value": "152.50",
    "stop_value": null,
    "all_or_none": false,
    "margin": false
  }
  ```
- Gateway pre-checks (before gRPC):
  1. Validate enums via `oneOf()`, quantity positive, prices positive when present.
  2. Call `client-service.GetClient(client_id)` — must exist.
  3. For `buy`: call `account-service.GetAccount(account_id)` and verify `account.owner_id == client_id`. On mismatch return `403 forbidden` with `"account does not belong to client"`.
  4. For `sell`: stock-service enforces `holding.account.owner_id == client_id`.
- Forward to `stock-service.CreateOrder` with a new gRPC field `on_behalf_of_client_id=client_id` and the employee's `user_id` as the caller.
- Stock-service records `acting_employee_id` on the order row and enforces the acting employee's `MaxSingleTransaction` limit (looked up via user-service). Supervisors with no limit bypass the check.
- Response `201 Created` — same `Order` object returned by `POST /api/me/orders`.
- Errors:
  - `400 validation_error` — bad enum, missing required price, non-positive quantity.
  - `403 forbidden` — employee lacks permission, or account does not belong to client.
  - `404 not_found` — client, account, listing, or holding missing.
  - `409 business_rule_violation` — insufficient funds, employee limit exceeded, market closed.

**`POST /api/v1/otc/:id/buy`** — employee buys an OTC offer on behalf of a named client.

- Auth: `AuthMiddleware` + `RequirePermission("securities.manage")`.
- Request body:
  ```json
  {
    "client_id": 42,
    "account_id": 101,
    "quantity": 100
  }
  ```
- Gateway pre-checks: verify client exists; verify `account.owner_id == client_id`; forward to stock-service with `on_behalf_of_client_id`.
- Same response shape and error codes as the existing `BuyOTCOffer` path.

**Audit column on `orders` table:**

```sql
ALTER TABLE orders ADD COLUMN acting_employee_id BIGINT NULL;
```

Set when the order is placed via the on-behalf route. Null when the client placed it directly. Surfaced in the `Order` response object for employee views.

**Kafka:** existing `stock.order-placed` event gains an optional `acting_employee_id` field in the payload.

### Part 4 — New single-loan read route

**`GET /api/v1/loans/:id`** — employee-facing single-loan lookup.

- Auth: `AuthMiddleware` + `RequirePermission("credits.read")`.
- Response body: identical to the `Loan` object returned by the existing `GET /api/v1/credits/loans` list endpoint.
- No ownership check — employees with `credits.read` are trusted to read any loan.
- `/api/me/loans/:id` continues to exist for clients, now with the ownership fix from Part 2.

## Data flow

**Loan approval (happy path):**

```
employee → POST /api/v1/credits/loan-requests/:id/approve
gateway → credit-service.ApproveLoanRequest
credit-service:
  tx: create loan (status="pending_disbursement") + installments + saga_log rows
  commit
  saga step A: account-service.DebitBankAccount(RSD, amount, ref=loan.id)
    account-service: FOR UPDATE on bank sentinel, check balance, write ledger, update balance
  saga step B: account-service.UpdateBalance(borrower, +amount)
  mark saga completed, loan.status = "active"
  kafka: publish("credit.loan-disbursed", ...)
→ 200 OK { loan: { ..., status: "active" } }
```

**Loan approval (insufficient liquidity):**

```
saga step A fails with FailedPrecondition
credit-service: loan.status = "failed_disbursement"
gateway: maps FailedPrecondition → 409 business_rule_violation
→ 409 { error: { code: "business_rule_violation", message: "bank has insufficient liquidity in RSD" } }
```

**Loan approval (step B fails after step A succeeded):**

```
saga step A completed, step B fails
credit-service: compensate via CreditBankAccount(ref=loan.id) — idempotent on reference
  saga_log: credit_borrower → failed, compensating
  loan.status = "failed_disbursement"
gateway → 500 internal_error (or 409 if step B failure was FailedPrecondition)
```

Compensation failure (rare) leaves the saga in `compensating` state for a background recovery job to retry.

## Error handling

- All gateway errors use `apiError()` — never raw `gin.H`.
- Ownership mismatches in `/api/me/*` return `404 not_found` to avoid confirming existence.
- Employee on-behalf routes return `403 forbidden` on account-does-not-belong-to-client because the employee already knows both IDs exist (they are acting on an explicit client + account pair).
- Bank insufficient liquidity returns `409 business_rule_violation`.
- Optimistic lock conflicts on bank sentinel account: retry up to 3 times inside `DebitBankAccount` / `CreditBankAccount` before returning `FailedPrecondition`.

## Testing

### Unit tests

**credit-service/internal/service/loan_request_service_test.go:**

- `TestApproveLoanRequest_HappyPath` — bank debited, borrower credited, saga completed, Kafka event published.
- `TestApproveLoanRequest_InsufficientBankLiquidity` — DebitBankAccount returns FailedPrecondition → loan `failed_disbursement`, no borrower credit, error propagates.
- `TestApproveLoanRequest_BorrowerCreditFails` — DebitBankAccount succeeds, UpdateBalance fails → compensation via CreditBankAccount, loan `failed_disbursement`.
- `TestApproveLoanRequest_CompensationIdempotent` — replay of `CreditBankAccount` with same reference does not double-credit.

**account-service/internal/repository/ledger_repository_test.go:**

- `TestDebitBankAccount_Success` — balance decreases, version bumps, ledger entry written.
- `TestDebitBankAccount_InsufficientBalance` — returns FailedPrecondition, no state change.
- `TestDebitBankAccount_IdempotentReplay` — second call with same reference returns cached result, no double-debit.
- `TestCreditBankAccount_Success` — symmetric.

**api-gateway/internal/handler/*_test.go:**

For each of the 13 lockdown fixes, two tests:
- Caller is owner → happy path returns 200/201.
- Caller is not owner → returns 404 `not_found`.

For `POST /api/v1/orders`:
- Happy path: account belongs to client, employee has permission → 201.
- Account does not belong to client → 403.
- Employee lacks `securities.manage` → 403.
- Employee limit exceeded → 409.
- Client does not exist → 404.

For `POST /api/v1/otc/:id/buy`:
- Symmetric set.

For `GET /api/v1/loans/:id`:
- Employee with `credits.read` → 200.
- Employee without permission → 403.
- Missing loan → 404.

### Integration tests

**test-app/workflows/loan_disbursement_test.go (new):**

- `TestLoanApproval_BankBalanceDecreases` — seed bank RSD balance, approve loan, assert bank balance decreased by loan amount AND client balance increased by loan amount AND Kafka `credit.loan-disbursed` observed.
- `TestLoanApproval_InsufficientBankLiquidity` — seed bank RSD balance below loan amount, approve → 409, assert no client balance change, assert loan status `failed_disbursement`.

**test-app/workflows/ownership_lockdown_test.go (new):**

- `TestClientCannotReadOtherClientLoan` — client A creates loan, client B calls `GET /api/me/loans/:A-loan-id` → 404.
- `TestClientCannotSetPinOnOtherClientCard` — client A has card, client B calls `POST /api/me/cards/:A-card-id/pin` → 404.
- `TestClientCannotBlockOtherClientCard` — symmetric.
- `TestClientCannotReadOtherClientPayment` — symmetric for payments.
- `TestClientCannotReadOtherClientTransfer` — symmetric for transfers.
- `TestClientCannotUpdateOtherClientRecipient` — symmetric for payment recipients.
- `TestClientCannotPlaceOrderOnOtherClientAccount` — client B sends `POST /api/me/orders` with `account_id` owned by A → 404.
- `TestClientCannotBuyOTCOnOtherClientAccount` — symmetric for OTC.
- `TestCreateLoanRequestIgnoresBodyClientId` — client B sends `POST /api/me/loan-requests` with `client_id=A` → loan request is created for B, not A.
- `TestCreateVirtualCardIgnoresBodyOwnerId` — symmetric for virtual cards.

**test-app/workflows/employee_onbehalf_test.go (new):**

- `TestEmployeeCreatesOrderOnBehalf_HappyPath` — employee with `securities.manage` creates order for client; assert order has `acting_employee_id` set and client's account is debited.
- `TestEmployeeCreatesOrderOnBehalf_AccountNotOwnedByClient` — 403.
- `TestEmployeeWithoutPermissionCannotCreateOnBehalf` — 403.
- `TestEmployeeBuysOTCOnBehalf_HappyPath` — symmetric for OTC.
- `TestAgentLimitEnforcedOnOnBehalfOrder` — agent with low `MaxSingleTransaction` → 409 when amount exceeds limit.

### Lint and build gates

- `make lint` on credit-service, account-service, stock-service, api-gateway — zero new warnings.
- `make test` must pass.
- `make swagger` must regenerate `api-gateway/docs/*` with the new routes.

## Spec and docs updates

**`Specification.md`:**

- Section 11 (gRPC): add `DebitBankAccount` and `CreditBankAccount` RPCs on `AccountGRPCService`.
- Section 17 (REST routes): add `POST /api/v1/orders`, `POST /api/v1/otc/:id/buy`, `GET /api/v1/loans/:id`. Update `POST /api/me/orders`, `POST /api/me/otc/:id/buy`, `POST /api/me/loan-requests`, `POST /api/me/cards/virtual`, and all the GET/POST/PUT/DELETE lockdown routes with the new ownership semantics.
- Section 18 (entities): add `acting_employee_id` to Order; add `Loan.status = "pending_disbursement" | "failed_disbursement"`.
- Section 19 (Kafka): add `acting_employee_id` to `stock.order-placed`; add `credit.loan-disbursed` and `credit.loan-disbursement-failed` topics.
- Section 20 (enums): add the two new loan statuses.
- Section 21 (business rules): add "bank must have sufficient liquidity to approve a loan"; add "all /api/me/* routes derive ownership from JWT, never from request body or URL"; add "employee on-behalf trading routes verify account belongs to the named client".

**`docs/api/REST_API_v1.md`:**

- Add `POST /api/v1/orders`, `POST /api/v1/otc/:id/buy`, `GET /api/v1/loans/:id` sections.
- Update all 13 lockdown routes with the new ownership behavior and 404 semantics.
- Note that `POST /api/me/loan-requests` and `POST /api/me/cards/virtual` derive `client_id` / `owner_id` from the JWT and ignore any value sent in the body.

**Swagger:** regenerate via `make swagger` after all handler changes.

**`docker-compose.yml` / `docker-compose-remote.yml`:** no new env vars or services needed.

## Rollout

Single PR. All changes are either additive (new routes) or strictly tighter ownership checks. The only soft-breaking change is:
- `POST /api/me/loan-requests` ignores `client_id` from body.

`POST /api/cards/virtual` is kept unchanged as an employee route. A new `POST /api/me/cards/virtual` route is added for clients, which derives `owner_id` from the JWT. User has authorized the loan-request change. Frontend will continue sending `client_id` during the transition and update at their convenience; the gateway silently ignores it.

## Open questions

None. Design approved by user on 2026-04-13.

## Convention note for future instrument handlers

All trading on behalf of clients flows through exactly two gateway endpoints:
- `POST /api/v1/orders` — any listing-based instrument (stock, futures, forex pair, option).
- `POST /api/v1/otc/:id/buy` — OTC offers.

When investment funds are added, a third endpoint (`POST /api/v1/funds/:id/buy` or similar) should follow the same pattern: explicit `client_id` + `account_id` in body, `securities.manage` permission, gateway verifies account belongs to client, service records `acting_employee_id`, employee limit enforced for agents.

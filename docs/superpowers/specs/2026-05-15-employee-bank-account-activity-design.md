# Employee Bank-Account Activity — Design

**Date:** 2026-05-15
**Status:** Approved (brainstorming)
**Scope:** Spec 2 of 3 from the 2026-05-14 "OTC + account activity + notifications" request. Spec 1 (OTC client access) is done; Spec 3 (admin-defined notification templates + coverage) is pending.

## Problem

Clients can view the transaction activity of their own monetary accounts (`GET /api/v3/me/accounts/:id/activity`). Employees have no equivalent: they can *list* the bank's own accounts (`GET /api/v3/bank-accounts`) and see balances, but cannot see the ledger entries (debits/credits) that produced those balances.

## Goal

Give employees a read endpoint for a bank account's transaction activity, mirroring what clients have for their own accounts.

## Non-goals

- Viewing *client* account activity as an employee — out of scope for this spec (decided during brainstorming: employees see bank accounts, clients see their own).
- Any new account-service RPC, proto change, or schema change — `GetLedgerEntries` already serves any account.

## Key facts

- `account-service`'s `GetLedgerEntries(account_number, page, page_size)` RPC works on **any** account, bank or client.
- The client-facing handler `AccountHandler.GetMyAccountActivity` (`api-gateway/internal/handler/account_handler.go`) already wires `GetAccount` → ownership check → `GetLedgerEntries` → `ledgerEntryToJSON`.
- Bank accounts are flagged `account_kind == "bank"` and carry the sentinel `owner_id == 1_000_000_000`.
- The bank-accounts route group (`bankAccountsRead` in `router_v3.go`) is gated by `RequirePermission(perms.BankAccounts.Manage.Any)` — held by `EmployeeSupervisor` and `EmployeeAdmin`.

## Design

### Route

`GET /api/v3/bank-accounts/:id/activity` — added to the existing `bankAccountsRead` group in `api-gateway/internal/router/router_v3.go`, gated by `RequirePermission(perms.BankAccounts.Manage.Any)`. No identity middleware needed (employee-only, permission-gated).

### Handler — `AccountHandler.GetBankAccountActivity`

Mirrors `GetMyAccountActivity`, with the client ownership check replaced by a bank-account *kind* check:

1. Parse `:id` (→ 400 `validation_error` on bad id).
2. Parse `page` / `page_size` query params — default 1 / 20, cap `page_size` at 200 (identical to `GetMyAccountActivity`).
3. `GetAccount(id)` via the account-service client; gRPC errors mapped through `handleGRPCError`.
4. **Bank-account guard:** if the account is not a bank account (`account_kind != "bank"` and `owner_id != 1_000_000_000`), return `404 not_found`. This route lives in the bank-accounts namespace — passing a client account id must not leak that client's ledger.
5. `GetLedgerEntries(account_number, page, page_size)`.
6. Respond `200` with `{ "entries": [...], "total_count": N }`, each entry rendered via the existing `ledgerEntryToJSON(entry, currency)`.

### Why a kind guard rather than trusting the route prefix

The route is `/bank-accounts/:id/activity`, but `:id` is still a caller-supplied account ID. Without the guard an employee could pass any client account ID and read that client's ledger. The guard keeps the endpoint's blast radius equal to its name.

## Testing

**Unit (`account_handler_test.go`):**
- Bank account → `200`, response contains an `entries` array and `total_count`.
- Client / non-bank account id → `404 not_found` (no ledger fetched).
- Unknown account id (gRPC `NotFound`) → `404`.
- `GetLedgerEntries` gRPC error → mapped to the right status.

**Integration (`test-app/workflows/`):**
- Employee lists bank accounts, picks one, `GET`s `/bank-accounts/:id/activity` → `200` with an `entries` array.
- Employee `GET`s `/activity` on a client account id → `404`.

## Docs to update

- `docs/api/REST_API_v3.md` — new endpoint in the bank-accounts section (auth, path param, query params, response shape, error codes).
- `docs/Specification.md` — §17 routes table.
- Swagger annotations on `GetBankAccountActivity` + `make swagger`.

## Open questions

None.

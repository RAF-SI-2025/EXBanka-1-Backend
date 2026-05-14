# OTC Client Access + Ticker-Based Offers — Design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** Spec 1 of 3 from the "OTC + account activity + notifications" request. Specs 2 (employee account-activity visibility) and 3 (admin-defined email/push templates + notification coverage) are tracked separately.

## Problem

OTC trading is currently employee-only. The OTC routes accept client JWTs (`AnyAuthMiddleware`) but gate on employee permissions (`otc.trade.accept`, `securities.trade.any`), and clients are issued JWTs with no permissions — there is no client-role/permission system. Clients are therefore blocked from OTC entirely, including local and peer-bank option offers.

Two further problems block opening these routes safely:

1. **No ownership validation on the OTC option routes.** `CreateOffer` binds no account at all; `AcceptOffer` and `ExerciseContract` accept *both* `buyer_account_id` and `seller_account_id` from the caller, so a caller can supply the counterparty's account, and nothing checks that any account belongs to the caller.
2. **Stock identification by raw `stock_id`.** Clients must currently pass an opaque `stock_id` to create an option offer. Tickers are a more natural, stable identifier.

## Goals

- Clients can use the full OTC surface (local option offers + public-market buys + peer-bank flows), gated by resource ownership rather than employee permissions.
- Employees retain their permission-gated access and can act either as the bank or, explicitly, on behalf of a client.
- Every OTC route verifies that caller-supplied accounts/holdings belong to the resolved owner, before the gRPC call.
- `POST /otc/offers` is keyed by `ticker` instead of `stock_id`.

## Non-goals

- A client-role/permission catalog (considered and rejected — see below).
- Per-client OTC opt-in flags.
- Changes to the peer-bank SI-TX wire protocol — the cross-bank routes are already client-reachable via the `/me` group and need no gating change.
- Notification emission for OTC events (Spec 3).

## Key facts established during exploration

- `stock.ticker` has a `uniqueIndex` (`stock-service/internal/model/stock.go:14`). One ticker maps to exactly one `stock.id` globally; there is one `Listing` per `(stock_id, exchange_id)`. The ticker lives on the `Stock`, not on a listing — it is not exchange-specific.
- `CreateOTCOfferRequest.stock_id` is the **`Stock` entity's primary key**, not a listing ID and not a holding ID. At offer creation, `assertSellerHasShares` looks up the seller's holding by `holding.security_id == stock_id`. Swapping `stock_id` → `ticker` is a pure API-surface rename; nothing downstream changes.
- The peer-bank routes (`POST /me/peer-otc/negotiations`, `POST /me/otc/contracts/peer/:id/exercise`) are already in the `/me` group under `AnyAuthMiddleware` — clients can already reach them.
- The codebase has two inconsistent on-behalf patterns: stock orders use a single route with an `on_behalf_of_client_id` body field; the OTC public-market buy uses a separate `/buy-on-behalf` route. This design standardizes the OTC option routes on the body-field pattern.

## Design

### Section 1 — Routes & gating

**New middleware** `RequirePermissionOrClient(mode, perms...)` in `api-gateway/internal/middleware/`:

- `principal_type == "client"` → pass through.
- `principal_type == "employee"` → apply the existing all/any permission check (`mode` selects `RequireAllPermissions` vs `RequireAnyPermission` semantics).

It replaces `RequireAllPermissions` / `RequireAnyPermission` only on the OTC routes below.

**Route changes in `router_v3.go`:**

| Route | Today | After |
|-------|-------|-------|
| `POST /otc/offers`, `/otc/offers/:id/counter`, `/accept`, `/reject`, `/otc/contracts/:id/exercise` | `AnyAuth` + `RequireAllPermissions(securities.trade.any, otc.trade.accept)` | `AnyAuth` + `RequirePermissionOrClient(all, securities.trade.any, otc.trade.accept)` |
| `POST /otc/offers/:id/buy` | `AuthMiddleware` (rejects clients) + `RequireAnyPermission(...)` | `AnyAuth` + `RequirePermissionOrClient(any, otc.trade.accept, securities.trade.any)` |
| `POST /otc/offers/:id/buy-on-behalf` | employee-only (`AuthMiddleware`) | unchanged |
| `POST /me/peer-otc/negotiations`, `POST /me/otc/contracts/peer/:id/exercise` | `AnyAuth` (`/me` group) | unchanged |

**Employee on-behalf** on the OTC option routes: an optional `on_behalf_of_client_id` body field on `CreateOffer`, `CounterOffer`, `AcceptOffer`, `ExerciseContract`, and `buy`.

- Omitted → employee acts as the bank.
- Present → employee acts for that client, gated by a new `otc.trade.on_behalf` permission.

A new `otc.trade.on_behalf` permission is added to `contract/permissions/catalog.yaml` (assigned to roles that currently hold `orders.place.on_behalf_client` — `EmployeeAgent` and above). The existing `/buy-on-behalf` route, which currently reuses `orders.place.on_behalf_client` across namespaces, is migrated to `otc.trade.on_behalf` for consistency.

### Section 2 — Account-ownership validation & the account-binding model

**Bind each account at the moment its party commits.** Each party supplies and binds only their own account; the counterparty's account is read from the persisted record.

| Action | Account in request | Validation |
|--------|-------------------|------------|
| `CreateOffer` | initiator's `account_id` (new field) | belongs to resolved owner |
| `CounterOffer` | countering party's `account_id` | belongs to the countering party |
| `AcceptOffer` | acceptor's `account_id` only — the second account ID is dropped | acceptor's account validated; counterparty's account read from the offer record |
| `ExerciseContract` | none — both accounts already bound on the contract | n/a |
| `buy` / `buy-on-behalf` | buyer's `account_id` (already per-caller) | add/confirm ownership check (`buy-on-behalf` already validates) |
| `make-public` | `holding_id` | holding's owner must match resolved owner |

**Schema changes** (clean-break, no compat shims — in scope per project history):

- `OTCOffer` gains `initiator_account_id` and `counterparty_account_id`.
- `OptionContract` gains `buyer_account_id` and `seller_account_id`, populated at accept time.
- The corresponding proto messages (`CreateOTCOfferRequest`, `CounterOTCOfferRequest`, `AcceptOTCOfferRequest`) change: add `account_id` and `on_behalf_of_client_id`; `AcceptOTCOfferRequest` drops the second account ID. `ExerciseContractRequest` drops both account IDs.

**Validation rule** — one shared gateway-side helper in `api-gateway/internal/handler/validation.go`, run before the gRPC call:

```
resolveAndCheckAccount(identity, accountID, onBehalfClientID):
  fetch account from account-service
  client principal        → account.OwnerId == principal_id AND not a bank account
  employee, no on-behalf  → account is a bank account (account_kind == "bank")
  employee + on-behalf    → account.OwnerId == on_behalf_of_client_id
  else                    → 403
```

`make-public` uses an analogous holding-ownership check (holding's `owner_type`/`owner_id` vs resolved identity).

This matches CLAUDE.md's "gateway validates all input before forwarding" rule and the existing `enforceOwnership` / `buy-on-behalf` pattern. stock-service is not given a duplicate check — consistent with the current codebase trust boundary.

**Rejected alternative:** keep both account IDs in `AcceptOffer`/`Exercise` and validate each against the party-on-record. It avoids schema changes but leaves the contract with no record of which account settled, forces every handler to re-derive buyer/seller-vs-caller from offer direction, and the API still *looks* like it accepts someone else's account. The per-party binding model is cleaner and auditable.

### Section 3 — Ticker swap

`POST /otc/offers`: the `stock_id` field is replaced by `ticker` (string). In the gateway handler, before the gRPC call:

- Validate `ticker` is present and non-empty.
- Resolve it via stock-service (lookup stock by ticker) → `stock_id`.
- Unknown ticker → `400 validation_error`.
- Pass the resolved `stock_id` over gRPC — the `stock_id` field on `CreateOTCOfferRequest` and stock-service's internal handling are unchanged.

OTC offer/contract responses already carry a denormalized `ticker`, so clients never handle raw `stock_id`s on the way back. No other OTC route is affected — counter/accept/reject/exercise/buy key off offer/contract IDs.

### Section 4 — Testing

**Unit tests:**

- `RequirePermissionOrClient` — client passes; employee without perm → 403; employee with perm → passes; both `all` and `any` modes.
- Gateway ownership helper — full matrix: client owns / doesn't own; employee + bank account / + non-bank account; employee on-behalf + correct client account / wrong client / missing permission.
- Ticker resolution — valid ticker resolves; unknown → 400; missing → 400.
- `AcceptOffer` handler — accepts only the acceptor's `account_id`; counterparty account read from the offer record.
- stock-service OTC service — offer/contract persist the new account-ID fields; `assertSellerHasShares` unchanged.

**Integration tests (`test-app/workflows/`):**

- Full OTC option lifecycle driven by a client (create → counter → accept → exercise).
- Client create/accept with an account that isn't theirs → 403.
- Employee as bank (no `on_behalf`) with a bank account → success; with a client account → 403.
- Employee on-behalf: correct client account → success; mismatched account → 403; missing `otc.trade.on_behalf` → 403.
- Ticker-based create: valid ticker works; unknown ticker → 400.
- Public-market `buy` as a client.
- Replace `TestOTCOptions_ClientCannotTrade` — it asserts the old behavior; clients can now trade, so it is superseded by the client lifecycle test.

## Concurrency & safety

- No new read-modify-write paths are introduced in the gateway; ownership checks are reads against account-service.
- The OTC offer/contract account-ID fields are written within the existing offer/accept transactions in stock-service; no new transaction boundaries.
- Versioned models (`OTCOffer`, `OptionContract`) keep their existing optimistic-locking hooks; adding columns does not change the locking behavior.

## Docs to update

- `Specification.md` — §17 routes, §18 entities (`OTCOffer`/`OptionContract` account fields), §21 business rules (ownership), §6 permissions (`otc.trade.on_behalf`).
- `docs/api/REST_API_v3.md` — OTC section: `ticker`, `account_id`, `on_behalf_of_client_id`, dropped buyer/seller account from accept.
- Swagger annotations on the OTC handlers + `make swagger`.
- `contract/permissions/catalog.yaml` — add `otc.trade.on_behalf`.
- `CLAUDE.md` — Resource Ownership Verification Requirement section (already added).

## Open questions

None outstanding. Account-binding schema change and dropping the second account ID from `AcceptOffer` were confirmed during brainstorming.

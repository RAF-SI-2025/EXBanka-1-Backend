# Plan: OWN-1 — move account ownership checks into account-service

Date: 2026-06-06
Status: SUBSTANTIALLY COMPLETE. D0 foundation + service-side enforcement
(account/card/credit/transaction) + D2 gateway de-dup all done & committed
(commits 43a2197b, fc233996, 1fbd71b7, 8a77cada, fe29933f, d469db2a, 226a7ca1;
VERSION → 2.16.5). All 6 touched modules build+test+lint green. Remaining
(documented, intentional): stock OTC/order + portfolio account-binding stays at
the gateway (caller-supplied-account/permission-entangled); transaction
list-by-client stays (gateway-resolved account numbers); payment-recipient
ownership not yet service-enforced (kept at gateway). RBAC/permissions never moved.

## Scope boundary (IMPORTANT)

This refactor moves ONLY resource-OWNERSHIP ("does this user own *this*
account/card/offer/loan", i.e. owner_id == principal_id) into the owning
services. RBAC PERMISSION checks STAY at the gateway and are never touched:
`RequirePermission`, `RequireClientToken`, `AuthMiddleware`/`AnyAuthMiddleware`,
rate-limiting. The service-side `Caller.OwnsResource` deliberately returns true
for employees precisely because the gateway already permission-gated them. D2
removes ONLY the gateway ownership helpers (enforceOwnership / checkAccountOwnership
/ enforceClientSelf / inline owner_id compares) — never any RequirePermission.

## Goal

Per the SERVICE_REVIEW cross-cutting decision (reverses the CLAUDE.md "Resource
Ownership Verification Requirement"): the gateway should NOT check resource
ownership; each owning service checks ownership of its own resources, using
caller identity propagated via gRPC metadata. This plan covers account-service.

## Findings (current state — verified 2026-06-06)

1. **Only `x-changed-by` is propagated.** The gateway sends a single user id
   over gRPC metadata (`contract/changelog/metadata.go` key `x-changed-by`,
   set by `api-gateway/internal/middleware/changed_by.go`, read by
   `changelog.ExtractChangedBy` for AUDIT ATTRIBUTION only). There is NO
   `principal_type` / `principal_id` / `on_behalf_of_client_id` in metadata.
2. **account-service does ZERO authorization today** — it trusts the gateway.
3. **The gateway's ownership model** is `ResolvedIdentity`
   (`api-gateway/internal/middleware/identity.go`) + `checkAccountOwnership`
   (`api-gateway/internal/handler/validation.go:213`): client → `owner_id ==
   principal_id` & not bank-owned; employee (no on-behalf) → resource must be
   bank-owned; employee on-behalf → `owner_id == on_behalf_client_id` + the
   `*.on_behalf_client` permission. 404-vs-403 semantics matter.
4. **Most admin account RPCs have NO per-resource ownership today** — they are
   employee-only routes gated by `RequirePermission` (an employee with the perm
   may act on any account, by design). Per-resource ownership exists mainly on
   the client `/me/*` reads (GetMyAccount/GetMyAccountActivity) and the OTC
   account-binding handlers (`ResolveAndCheckAccount[ByNumber]`).

## The critical split: Tier-A (user-facing) vs Tier-B (service-to-service)

**Tier-B — service-to-service money ops; carry NO caller identity; MUST stay
callable by trusted services with NO client-ownership check:**
`UpdateBalance`, `ReserveIncoming`, `CommitIncoming`, `ReleaseIncoming`,
`ReserveOutgoing`, `SettleOutgoing`, `ReleaseOutgoing`, `ReserveFunds`,
`ReleaseReservation`, `PartialSettleReservation`. Callers: transaction-,
stock-, credit-, interbank-service. **Getting this wrong breaks transfers/OTC/
loans or opens a hole in the money path.**

**Tier-A — user-facing; CAN enforce ownership:** `GetAccount`,
`GetAccountByNumber`, `ListAccountsByClient`, `UpdateAccountName`,
`UpdateAccountLimits`, `UpdateAccountStatus`, `GetLedgerEntries`,
`CreateAccount` (owner_id supplied by caller — validate against identity).

## Design

### Phase D0 — identity-propagation foundation (CROSS-SERVICE, do first)
- New shared package `contract/identity` (or extend `contract/changelog`):
  metadata keys `x-principal-type` (employee|client|**service**),
  `x-principal-id`, `x-on-behalf-client-id`, plus a typed `Caller` struct +
  `Inject(ctx, Caller)` / `FromContext(ctx) (Caller, ok)`.
- A **service principal**: internal service-to-service calls set
  `x-principal-type=service` (a shared client interceptor stamps it for callers
  that are services, not the gateway). Tier-B RPCs require `service` (or accept
  absent identity as service, for backward-compat — decide explicitly).
- Gateway: extend the outbound interceptor to inject the full `Caller` from
  `ResolvedIdentity` (not just changed_by).
- **Backward-compat:** legacy callers send no identity → Tier-B still works
  (treat absent as trusted service), Tier-A fails closed (no identity → 401/403).

### Phase D1 — enforce in account-service (additive; keep gateway checks)
- Read `Caller` in the account handlers; for Tier-A RPCs apply the same
  client/employee/on-behalf rules `checkAccountOwnership` uses today (load the
  account, compare owner_id, 404 vs 403). Tier-B RPCs: require service principal
  (or absent), no ownership check.
- Keep the gateway checks in place during D1 (defense-in-depth; never a window
  where NEITHER checks). Add account-service unit tests for every rule + tier.

### Phase D2 — remove gateway ownership checks for account resources
- Only after D1 is proven: delete `checkAccountOwnership` /
  `ResolveAndCheckAccount[ByNumber]` call sites for account resources from the
  gateway, and the helpers if unused elsewhere. Preserve cross-bank money-path
  guarantees ([[project_crossbank_adversarial_findings]]). Update CLAUDE.md.

## Test plan
- account-service: table tests for each Tier-A RPC × {client-owner-match,
  client-mismatch→404/403, employee-bank-owned, employee-on-behalf+perm,
  on-behalf-without-perm→403}; Tier-B: service principal allowed, client
  principal rejected.
- gateway: identity injection interceptor test; after D2, regression that
  /me/* still 403s on another user's account.
- integration: a client cannot read/modify another client's account end-to-end;
  transfers/OTC/loans (Tier-B) still succeed.

## PROGRESS (2026-06-06)

- **D0 DONE** (commit 43a2197b, 2.15.7): `contract/identity` package
  (Caller{PrincipalType,PrincipalID,OnBehalfClientID}, Inject/FromIncoming,
  `OwnsResource`); gateway stamps identity on every authed route via
  `setPrincipalContext` + on-behalf via `ResolveIdentity`. Additive, no behavior change.
- **D1 account-service DONE** (commit fc233996, 2.16.0): enforces ownership on the
  DIRECT user-facing reads/list — GetAccount, GetAccountByNumber, GetLedgerEntries
  (→ 404), ListAccountsByClient (→ 403). Service/employee allowed; on-behalf bound.
  Gateway checks kept (defense-in-depth; no gap). Full tests.

### Service-side enforcement DONE (all applicable services)
- account (fc233996), card (1fbd71b7), credit (8a77cada), transaction (fe29933f):
  each enforces owner-matching on its user-facing reads/lists (+ card pin/block,
  credit installments) via identity.FromIncoming + OwnsResource. Additive — the
  gateway checks still run too (defense-in-depth, NO gap). Every service has
  OWN-1 unit tests (client-foreign→404, own→OK, employee→OK, service→OK, list→403).
- **stock-service: EXEMPT (keep gateway checks).** Its OTC/order check validates a
  CALLER-SUPPLIED account before a multi-party trade; the counterparty/bank
  accounts are read from the persisted record, NOT caller-supplied. Forwarding
  identity so account-service enforced on every GetAccount would REJECT the
  legitimate counterparty/bank reads and break OTC/trades. That validation
  belongs at the gateway boundary (validating caller INPUT), and
  enforcePortfolioAccess is permission-entangled (RBAC stays at the gateway).
- **transaction list-by-client: keep gateway.** Keyed by gateway-RESOLVED account
  numbers (the account→client mapping lives in account-service), not a client_id,
  so transaction-service can't re-verify it without an account lookup.

### D2 — gateway-check removal (the only remaining step; pure DE-DUP, not a
security change — the services already enforce, so removing the now-redundant
gateway ownership checks does NOT open a gap; leaving them is strictly safer).
Remove ONLY ownership helpers (never RequirePermission): for account/card/credit
the direct /me/:id inline owner checks + enforceClientSelf on list-by-client; for
transaction the enforceOwnership on GetPayment/GetTransfer/status (NOT the
list-by-account-numbers). Update the gateway tests that assert the gateway's own
403/404 (now the service's job). KEEP all stock checks + transaction list-by-client.

### (historical) Turnkey remaining steps (same pattern per service)
Pattern: in each service's gRPC handler, `caller := identity.FromIncoming(ctx)`;
on user-facing read/list/mutation, `if !caller.OwnsResource(int64(ownerID)) {
return <svc>.ErrNotFound }` (404, no leak) — or a Forbidden sentinel for
list-by-client. Add a `Caller.OwnsResource`-based test set. Keep gateway checks
until the matching service enforces, then remove (see inventory in the agent
report / validation.go call-sites).

- **card-service**: Card has OwnerID/OwnerType. Gate GetCard, ListCardsByClient,
  card_request GetCardRequest + ListCardRequestsByClient (by ClientID), and the
  pin/block mutations (SetCardPin/VerifyCardPin/TemporaryBlockCard/BlockCard —
  fetch the card, gate on OwnerID). Gateway sites to later remove:
  card_handler.go:239,897 (inline) + loadCardAndEnforceOwnership (438/481/525) +
  GetCardRequest 750.
- **credit-service**: Loan/LoanRequest have ClientID. Gate GetLoan, GetLoanRequest,
  ListLoansByClient, ListLoanRequests(by client). Gateway: credit_handler.go
  enforceClientSelf (284/369/448) + enforceOwnership (820/875).
- **transaction-service**: Payment/Transfer have ClientID. Gate GetPayment,
  GetTransfer, ListPaymentsByClient, ListTransfersByClient. Gateway:
  transaction_handler.go enforceClientSelf (219/466/884/983) + enforceOwnership
  (681/708/761/788).
- **stock-service**: portfolio/watchlist ownership already gated at the gateway
  via enforcePortfolioAccess; the order/OTC ACCOUNT-binding checks
  (stock_order_handler.go 413/425, portfolio_handler 478/528, otc_* handlers) go
  gateway→stock-service→account-service. account-service can only enforce these if
  the caller identity is FORWARDED on the service→service hop — see below. Until
  then, KEEP those gateway checks.
- **D-forward (prerequisite for indirect cases)**: add a shared gRPC CLIENT
  interceptor (contract/shared/grpcmw or contract/identity) that copies the
  INCOMING caller identity onto OUTGOING calls, wired into stock-/transaction-/
  credit-service's account-service clients. Only then can the OTC/order
  account-binding gateway checks be removed.
- **D2 (per service, after enforcement proven)**: delete the now-redundant gateway
  ownership checks for that service's DIRECT resources; rewrite CLAUDE.md's
  "Resource Ownership Verification Requirement". Never remove a gateway check
  whose service-side equivalent isn't yet live.

## Why not all-at-once (2026-06-06)
This is a cross-service platform change (new metadata contract touched by ALL
services + the gateway), it is security-critical in the money service, and the
Tier-A/Tier-B split must be exactly right. Another agent is concurrently editing
the shared `contract/` (transaction proto/pb) and interbank-service, so changing
the shared identity/metadata contract now risks conflicts. It deserves its own
focused effort with the foundation (D0) landed and verified before any gateway
check is removed (D2). The other account-service review items (A/B/C/F) shipped
independently (VERSION 2.15.4–2.15.6).

# Phase 2/3 Resume Notes — updated 2026-04-27

This doc supersedes the prior RESUME doc. Snapshot: **B2 merged. C is in a worktree, partially committed, build clean but 2 saga tests fail.**

## Current state of `feature/securities-bank-safety`

HEAD: **`1f0a693`** — `merge: B2 — cross-service saga coordination`

Merged so far (since the original `225b695` checkpoint):
- `225b695` chore(permissions): purge legacy permission strings + drift check (Phase 1 cleanup)
- `1f0a693` merge: B2 — cross-service saga coordination (21 commits worth)

So Phase 1 (A + D) and Phase 2 B2 are merged. Phase 2 C is in a worktree.

---

## C — Owner Type Schema (NOT YET MERGED)

**Worktree:** `.worktrees/owner-type-schema`
**Branch:** `refactor/owner-type-schema`
**HEAD:** `b4be758`

### Committed so far (7 commits since `225b695`)

```
b4be758 refactor(stock-service): handlers consume new OwnerType/OwnerID model fields  ← KNOWN: 2 tests fail
49a24fe refactor(stock-service): service layer + protos use OwnerType/OwnerID (handlers next)
3e35275 refactor(stock-service): repositories filter by (owner_type, owner_id)
cb9982c refactor(stock-service): models use owner_type/owner_id (intermediate — repositories follow)
cee7a13 refactor(gateway): auth middleware sets principal_type/principal_id keys + drop legacy shim
2327c4a refactor(auth): JWT claims renamed system_type→principal_type, user_id→principal_id
a4498b5 feat(middleware): ResolvedIdentity + per-route ResolveIdentity rule
```

That covers C Tasks 1-6 (identity middleware, JWT rename, gateway middleware, model migration, repository updates, service layer + proto, handler migration).

### Build state

- **All 11 services build clean.** Verified `for svc in ... ; do cd $svc-service && go build ./...`.
- **2 unit tests fail** in `stock-service/internal/service`:
  - `TestInvestSaga_DebitSourceFails_NoStateChange`
  - `TestInvestSaga_CreditFundFails_RefundsSource`
  - Likely from handler-migration interface changes affecting saga test mocks. Diagnose via `cd stock-service && go test ./internal/service/... -run TestInvestSaga -v -count=1`. Likely a mock that needs new fields (e.g., the test mock implements an interface whose method signature changed when the handler facade was updated to take `model.OwnerType, *uint64` instead of `uint64, string`).
- All other test suites (account, transaction, etc.) pass.

### Uncommitted

None. Working tree clean post-`b4be758`.

### To resume C

**Step 1 — Fix the 2 failing tests.** Read each test, see what mock it calls, update mock to match new signature. Probably 5-10 lines per test.

**Step 2 — C Task 7: Delete bank sentinel + identity helpers in api-gateway**

The biggest remaining piece. Files to touch:
- `api-gateway/internal/handler/validation.go` — DELETE constants `BankSentinelUserID = 1_000_000_000` (line ~159) + `BankSystemType = "bank"` (line ~165), and DELETE functions `meIdentity()` (~line 140), `mePortfolioIdentity()` (~185), `actingEmployeeID()` (~203).
- ADD proto-converter helpers in same file: `toProtoOwnerType(s string) stockpb.OwnerType`, `ownerIDToProto(*uint64) *wrapperspb.UInt64Value`, `empToProto(*uint64) *wrapperspb.UInt64Value`.
- For each of the ~30 handler call sites that used `mePortfolioIdentity` / `actingEmployeeID`: rewrite to consume `c.MustGet("identity").(*middleware.ResolvedIdentity)` instead. Find via `grep -rn "mePortfolioIdentity\|meIdentity\|actingEmployeeID\|BankSentinelUserID\|BankSystemType" api-gateway/`.

Note: the proto request shapes still use `user_id` / `system_type` fields (the agent didn't change those because it would break the gateway). Handlers convert at the boundary: `req.UserId = id.OwnerID; req.SystemType = string(id.OwnerType)`. OR update the proto request shapes to use `OwnerType`/`OwnerId` and feed the typed values directly. The plan calls for the latter; it's cleaner but more proto changes.

**Step 3 — C Task 8: Wire `ResolveIdentity` middleware into routes**

For each route group in api-gateway/internal/router/router_v1.go (and v2/v3 if present):
- `/me/*` trading routes → `middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee)`
- `/me/*` non-trading (profile, cards) → `middleware.ResolveIdentity(middleware.OwnerIsPrincipal)`
- `/clients/:client_id/*` (admin acts on a client) → `middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id")`

**Step 4 — C Task 9: Rename Kafka payload fields**

In `contract/kafka/messages.go` find every struct with `SystemType`/`UserID` fields (likely `OTCParty.SystemType`, `StockFundInvestedMessage`, `StockFundRedeemedMessage`, `AuthSessionCreatedMessage`). Rename to `OwnerType`/`OwnerID` (or `PrincipalType`/`PrincipalID` for auth-related). Update producers in stock-service + consumers in notification-service.

**Step 5 — C Task 10: Integration tests**

- Replace `wf_systemtype_isolation_test.go` with `wf_owner_type_isolation_test.go`
- Add actuary-limit regression test (employee /me/order ⇒ bank ownership ⇒ acting_employee_id gate fires)
- Add on-behalf-of-client test

**Step 6 — C Task 11: Drop legacy columns**

Add a one-shot startup migration in `stock-service/cmd/main.go`:
```go
dropLegacyColumns(db, "orders", []string{"user_id", "system_type"})
// ... for each migrated table
```

**Step 7 — C Task 12: Update Specification.md** (sections 6, 18, 19) to reflect the new identity model.

**Step 8 — Merge C → feature branch.** Conflicts likely with B2 (both touched cmd/main.go files). Resolve carefully — B2's interceptor wiring must coexist with C's identity middleware updates.

---

## Phase 3 — Plan E (route consolidation) — NOT STARTED

After C merges, run plan E in a fresh worktree. 10 tasks, all mechanical:
- Inventory v1+v2 routes
- Build `Handlers` bundle
- Expand `router_v3.go` to contain every route
- Update `cmd/main.go` to call `SetupV3` only
- Delete `router_v1.go` + `router_v2.go`
- Migrate test-app to `/api/v3`
- Smoke test old paths return 404
- Update REST_API docs (delete v1 + v2; rename v3 → REST_API.md)
- Document v4 pattern
- Update Specification.md

---

## How to resume next session

```
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
cat docs/superpowers/RESUME-PHASE-2.md  # this file
cd .worktrees/owner-type-schema
cd stock-service && go test ./internal/service/... -run TestInvestSaga -v -count=1  # see the 2 failures
```

Tell Claude: *"Read docs/superpowers/RESUME-PHASE-2.md and continue. Start by fixing the 2 failing InvestSaga tests in C, then Tasks 7-12, then merge, then Phase 3 (E)."*

Estimated remaining work:
- **C: ~7-10 commits** (fix 2 tests + Tasks 7-12 + merge with B2 conflict resolution)
- **E: ~10 commits** (mechanical)

---

## Future-ideas backlog (still tracked)

`docs/superpowers/specs/2026-04-27-future-ideas-backlog.md` items still open:
- F15 (HIGH): `db.Save` optimistic-lock gap in 6 stock-service repositories
- F16 (MED): Forward-recovery driver for past-pivot crossbank failures
- F17 (LOW): `StepDeliverShares` dead code
- F18 (LOW): `RecoveryRunner` unused infrastructure (B2 didn't wire it; future-ideas)

# Phase 2 Resume Notes — 2026-04-27

Both Phase 2 implementer agents (B2 + C) hit the org's monthly API quota mid-task. Their work is partially committed in worktrees on `feature/securities-bank-safety` ancestor branch `225b695`. **Neither worktree currently builds.**

This doc tells the next session exactly what was committed, what's uncommitted-but-staged, what's broken, and what tasks remain.

## Current state of `feature/securities-bank-safety`

HEAD: **`225b695`** — `chore(permissions): purge legacy permission strings + drift check`

Phase 1 (A + D) merged. B1 (saga refactor) merged earlier. **Phase 2 (B2 + C) is in worktree branches, NOT yet merged.**

---

## B2 — Cross-Service Saga Coordination

**Worktree:** `.worktrees/cross-service-saga`
**Branch:** `refactor/cross-service-saga`
**HEAD:** `d5109ed`

### Committed (15 commits since `225b695`)

```
d5109ed refactor(saga): saga steps pass idempotency keys to gRPC callees
9471435 refactor(stock-service): wrap cross-bank Handle* RPCs in idempotency
64a98df refactor(transaction-service): wrap saga-callee RPCs in idempotency
46ef947 refactor(account-service): wrap remaining saga-callee RPCs in idempotency
150cf51 refactor(account-service): UpdateBalance uses idempotency repository
503db41 feat(proto): mark saga-callee RPCs idempotent and require idempotency_key
27285d2 feat(credit-service): idempotency repository for saga-driven RPCs
385d8f3 feat(card-service): idempotency repository for saga-driven RPCs
95be4f9 feat(stock-service): idempotency repository for saga-driven RPCs
eac2b49 feat(transaction-service): idempotency repository for saga-driven RPCs
9342f5d feat(account-service): idempotency repository for saga-driven RPCs
c65164f feat(outbox): atomic enqueue + drainer with retry-on-failure
a0c6bf8 feat(grpcmw): bidirectional saga context interceptors via gRPC metadata
e1c44de feat(saga): context.Context helpers for saga id/step/acting-employee
1deea3a feat(saga): deterministic IdempotencyKey helper
```

That covers plan B2 Tasks 1-10 (shared infra, idempotency repos x5, proto marking, all handler wiring, saga callers passing keys).

### Uncommitted (Task 11 — ctx interceptor wiring, partial)

```
M account-service/cmd/main.go
M auth-service/cmd/main.go
M auth-service/internal/grpc/client_client.go
M card-service/cmd/main.go
M client-service/cmd/main.go
M contract/shared/grpc_dial.go    ← BROKEN (syntax error line 53)
M credit-service/cmd/main.go
M exchange-service/cmd/main.go
M notification-service/cmd/main.go
M stock-service/cmd/main.go
M transaction-service/cmd/main.go
M user-service/cmd/main.go
M verification-service/cmd/main.go
```

The agent was wiring `grpcmw.UnarySagaContextInterceptor` (server-side) and `grpcmw.UnaryClientSagaContextInterceptor` (client-side) across all `cmd/main.go` files when it died. **The shared `contract/shared/grpc_dial.go` is broken** — incomplete edit on line 53 (likely an unclosed `grpc.WithChainUnaryInterceptor(...)` call). Whole worktree fails to build because every cmd/main.go imports `contract/shared`.

### To resume B2

1. Fix `contract/shared/grpc_dial.go` line 53. Look at the diff (`git diff contract/shared/grpc_dial.go`) — likely just needs the missing closing paren or a complete `grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor())` line. If unsalvageable: `git checkout contract/shared/grpc_dial.go` and start the wire-up cleanly.
2. Verify each `cmd/main.go` follows the pattern:
   ```go
   srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
       grpcmw.UnaryLoggingInterceptor("<service>"),
       grpcmw.UnarySagaContextInterceptor(),
   ))
   ```
3. Ensure every gRPC client construction (in `<svc>/internal/grpc/*.go`, `api-gateway/internal/grpc/`, and any `contract/shared/grpc_dial.go` helper) has `grpcmw.UnaryClientSagaContextInterceptor()` chained.
4. Build clean: `for svc in account auth card client credit exchange notification stock transaction user verification; do (cd $svc-service && go build ./... 2>&1 | head -3); done`
5. Commit: `wire(saga-context): client + server interceptors across all services`

### B2 tasks remaining (NOT YET STARTED)

- **T12:** Stamp `saga_id` + `saga_step` on side-effect rows (account ledger entries, stock holdings). Schema additions + repository reads from saga.SagaIDFromContext(ctx).
- **T13:** Outbox tables in services that publish from sagas (start with stock-service). AutoMigrate `&outbox.Event{}`, start `OutboxDrainer` goroutine, replace direct `producer.Publish` calls with `outbox.Enqueue(tx, ...)`.
- **T14:** Repeat T13 for any other services that publish from sagas (audit each).
- **T15:** Integration tests for cross-service durability (idempotency replay + outbox crash safety). At least the idempotency replay test should be live; outbox test can be `t.Skip` pending docker-compose mid-test restart helper.
- **T16:** Update `docs/Specification.md` section 21 (or 22) with the cross-service saga coordination story.

After all B2 tasks: merge `refactor/cross-service-saga` → `feature/securities-bank-safety`.

---

## C — Owner Type Schema

**Worktree:** `.worktrees/owner-type-schema`
**Branch:** `refactor/owner-type-schema`
**HEAD:** `3e35275`

### Committed (5 commits since `225b695`)

```
3e35275 refactor(stock-service): repositories filter by (owner_type, owner_id)
cb9982c refactor(stock-service): models use owner_type/owner_id (intermediate — repositories follow)
cee7a13 refactor(gateway): auth middleware sets principal_type/principal_id keys + drop legacy shim
2327c4a refactor(auth): JWT claims renamed system_type→principal_type, user_id→principal_id
a4498b5 feat(middleware): ResolvedIdentity + per-route ResolveIdentity rule
```

That covers plan C Tasks 1-5 (identity middleware, JWT rename, gateway middleware update, model migration, repository updates).

### Uncommitted (Task 6 — proto + service-layer migration, partial)

```
M  contract/proto/stock/stock.proto                            ← OwnerType enum + nullable owner_id added
M  stock-service/internal/model/owner.go                       ← extended (probably bank-id-for-PK helper)
M  stock-service/internal/repository/option_contract_repository.go
M  stock-service/internal/repository/order_transaction_repository.go
M  stock-service/internal/repository/otc_offer_repository.go
M  stock-service/internal/service/{crossbank_accept_saga,fund_invest_saga,fund_position_reads,fund_redeem_saga,
   holding_reservation_service,interfaces,order_execution,order_service,otc_accept_saga,otc_exercise_saga,
   otc_expiry_cron,otc_offer_service,otc_service,portfolio_service,tax_service}.go
?? contract/credit/        ← spurious — `make proto` may have written to wrong dir
?? contract/exchange/      ← spurious
?? contract/stock/         ← spurious
?? contract/verification/  ← spurious
?? stock-service/internal/grpcutil/   ← new package: proto<->model OwnerType converters
```

The C agent was migrating the stock-service service layer to use the new OwnerType/OwnerID fields and the new `grpcutil` package converters. It died in the middle. **Stock-service does NOT build.**

### To resume C

1. **Investigate the spurious untracked dirs** (`contract/credit/`, `contract/exchange/`, `contract/stock/`, `contract/verification/`). Likely from `make proto` writing to the wrong path. If they contain only `.pb.go` files that already exist in `contract/<svc>pb/`, delete them: `rm -rf contract/credit contract/exchange contract/stock contract/verification`. Otherwise, investigate first.

2. **Verify the stock-service proto regeneration**: `git diff contract/proto/stock/stock.proto` — confirm the `OwnerType` enum + `google.protobuf.UInt64Value owner_id` + `optional uint64 acting_employee_id` were added correctly. Confirm `contract/stockpb/stock.pb.go` (NOT `contract/stock/stock.pb.go`) was regenerated.

3. **Read `stock-service/internal/grpcutil/owner.go`** — confirm the converters exist: `FromProto(stockpb.OwnerType) → model.OwnerType`, `ToProto`, `OwnerIDFromProto`, `OwnerIDToProto`, `ActingEmployeeIDFromProto`.

4. **Build stock-service**: `cd stock-service && go build ./... 2>&1 | head -30`. Expected: many errors in `internal/service/*.go` files. For each, the agent needs to:
   - Replace `UserID:` / `SystemType:` with `OwnerType:`/`OwnerID:`/`ActingEmployeeID:` in struct literals
   - Replace `repo.X(uid, st)` calls with `repo.X(grpcutil.FromProto(in.OwnerType), grpcutil.OwnerIDFromProto(in.OwnerId))`
   - Update internal function signatures from `(uid uint64, st string)` to `(ownerType model.OwnerType, ownerID *uint64)`

5. **Once stock-service builds**, run tests: `cd stock-service && go test ./... -count=1`. Fix any test failures (likely tests that hardcoded `BankSentinelUserID` — replace with `OwnerType: model.OwnerBank, OwnerID: nil`).

6. **Then deal with api-gateway**: it currently passes `UserID`/`SystemType` to stock-service via the now-deleted-from-proto fields. Either:
   - Add a temporary shim in api-gateway handlers (cast via `model.OwnerType(systemType)`)
   - Or skip ahead to Task 7 (delete bank sentinel + identity helpers + handlers use ResolvedIdentity)
   
   Task 7 is the clean answer.

7. **Commit Task 6** (proto + service layer): `refactor(stock-service): service layer + protos use OwnerType/OwnerID`

### C tasks remaining (NOT YET STARTED beyond Task 6)

- **T7:** Delete `BankSentinelUserID`, `BankSystemType`, `meIdentity`, `mePortfolioIdentity`, `actingEmployeeID` from `api-gateway/internal/handler/validation.go`. Update ~30 handler call sites to use `c.MustGet("identity").(*middleware.ResolvedIdentity)`. Add proto-converter helpers in `validation.go` (`toProtoOwnerType`, `ownerIDToProto`, `empToProto`).
- **T8:** Wire `middleware.ResolveIdentity(rule, args...)` into the router groups (`/me/*` trading routes get `OwnerIsBankIfEmployee`, `/me/profile` gets `OwnerIsPrincipal`, `/clients/:client_id/*` gets `OwnerFromURLParam`).
- **T9:** Rename Kafka payload fields (`OTCParty.SystemType` → `.OwnerType` + `.OwnerID`, `StockFundInvestedMessage.SystemType` → `.OwnerType` + nullable `.OwnerID`, etc.). Update producers in stock-service + consumers in notification-service.
- **T10:** Integration tests — actuary-limit regression test (employee /me/order ⇒ bank ownership ⇒ acting_employee_id gate fires) + on-behalf-of-client test + owner-type isolation test. Replace `wf_systemtype_isolation_test.go`.
- **T11:** Drop legacy `user_id` + `system_type` columns. Add `dropLegacyColumns` one-shot migration in `stock-service/cmd/main.go`. After deploy + verify, remove the migration call.
- **T12:** Update `docs/Specification.md` sections 6, 18, 19 to reflect the new identity model.

After all C tasks: merge `refactor/owner-type-schema` → `feature/securities-bank-safety`. **Important:** the merge may conflict with B2's branch (both touch `cmd/main.go`). Merge order matters — I'd suggest merging B2 first since it's mostly mechanical wiring, then C which has the schema changes.

---

## Phase 3 (E — route consolidation) — NOT STARTED

Plan E (`docs/superpowers/plans/2026-04-27-route-consolidation-v3.md`) requires:
- C and D both merged (router uses ResolvedIdentity from C + typed perms from D — D is already merged)

After Phase 2 merges, Phase 3 can run in a single worktree:
- Delete `router_v1.go` and `router_v2.go`
- Move all unique routes to `router_v3.go`
- Wire identity middleware per route group
- Update test-app to use `/api/v3` paths
- Document v4 pattern for future versions
- Delete REST_API.md, REST_API_v1.md, REST_API_v2.md (keep REST_API_v3.md as the canonical doc)

10 tasks, mostly mechanical.

---

## Followups still tracked in `docs/superpowers/specs/2026-04-27-future-ideas-backlog.md`

These are not blocking Phase 2/3 but were surfaced during execution:

- **F15** (HIGH): `db.Save` optimistic-lock gap in 6 stock-service repositories
- **F16** (MED): Forward-recovery driver for past-pivot crossbank failures
- **F17** (LOW): `StepDeliverShares` dead code
- **F18** (LOW): `RecoveryRunner` / `Classifier` infrastructure unused (B2 Task 11+ may wire it)

---

## Resume command for next session

1. Read this file: `docs/superpowers/RESUME-PHASE-2.md`
2. Read both plan files: `docs/superpowers/plans/2026-04-27-cross-service-saga-coordination.md` (B2) and `docs/superpowers/plans/2026-04-27-owner-type-schema.md` (C).
3. Pick which to resume first (recommend B2 — smaller remaining scope).
4. `cd .worktrees/<branch>` and `git status` to see uncommitted state.
5. Fix the broken file, finish the in-flight task, commit. Then proceed with remaining tasks per the plan.
6. After both Phase 2 plans merge, start Phase 3 (E) in a fresh worktree.

Estimated remaining work:
- B2: ~5 commits (interceptor wire-up + Tasks 12, 13, 15, 16)
- C: ~7 commits (Task 6 finish + Tasks 7-12)
- E: ~10 commits (mechanical)

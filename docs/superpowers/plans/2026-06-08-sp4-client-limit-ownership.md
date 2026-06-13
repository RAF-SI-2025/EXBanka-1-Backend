# SP-4: Client-Limit Ownership → client-service (remove user-service middle-man)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** Client limits are written ONLY by client-service. When a CLIENT-type limit blueprint is applied, the api-gateway calls client-service directly (synchronously) instead of routing the write through user-service. Removes the only client-limit write middle-man (SP-0 audit finding).

**Architecture:** Blueprint DEFINITIONS stay in user-service (unified admin template management for employee/actuary/client types). But applying a *client* blueprint no longer calls `clientClient.SetClientLimits` inside user-service. Instead the gateway's `ApplyBlueprint` handler: (1) reads the blueprint to learn its `Type`; (2) for `client` type, parses `ValuesJson` `{daily_limit,monthly_limit,transfer_limit}` and calls `clientLimitClient.SetClientLimits(targetID, …)` directly; (3) for `employee`/`actuary`, calls user-service `ApplyBlueprint` exactly as today. user-service's `applyClientBlueprint` cross-call (and the `clientClient` dependency on `BlueprintService`) is removed; `ApplyBlueprint(client type)` now returns `FailedPrecondition` (defensive — nothing should call it). No events are used for the client-limit write (synchronous); client-service's own `client.limits-updated` notification is unchanged (needed by SP-5).

**Why this matches the user's directive:** "client service should be the only service that manages client limit … apigateway talks to client service for it, no events for client limit, it shouldn't go over user." ✓ write done by client-service, ✓ gateway→client-service synchronous, ✓ no event-driven application, ✓ user-service no longer writes client limits.

**Tech Stack:** Go, Gin (gateway), gRPC. Modules: `api-gateway`, `user-service`.

**Compat:** The `POST /api/v3/blueprints/:id/apply` route keeps its exact request (`{target_id}`) and response shape and status codes — only the internal orchestration changes. NOT a breaking change. The cap enforcement (client limit ≤ employee's MaxClient*Limit) already lives in client-service's `SetClientLimits` and is unchanged here (its `client→user` read is denormalized later in SP-2b).

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `api-gateway/internal/handler/blueprint_handler.go` | `ApplyBlueprint`: branch client vs employee/actuary; client → client-service | Modify |
| `api-gateway/internal/handler/blueprint_handler_test.go` | client-type routes to client-service; employee-type still to user-service | Modify |
| `api-gateway/internal/handler/handlers.go` (or wherever `BlueprintHandler` is constructed/wired) | inject `clientLimitClient` into `BlueprintHandler` | Modify |
| `api-gateway/cmd/main.go` | pass `clientLimitClient` to the blueprint handler constructor | Modify |
| `user-service/internal/service/blueprint_service.go` | remove `applyClientBlueprint` cross-call + `clientClient` field; `ApplyBlueprint(client)` → FailedPrecondition | Modify |
| `user-service/internal/service/blueprint_service_test.go` | client-type apply now errors; client gRPC no longer called | Modify |
| `user-service/cmd/main.go` | stop wiring `clientClient` into `BlueprintService` | Modify |
| `docs/api/REST_API_v3.md` | clarify apply-blueprint behavior for client type | Modify |
| `docs/Specification.md` | note ownership change (§21 business rules / §3 wiring) | Modify |
| `VERSION`, `api-gateway/internal/version/version.go` | bump | Modify |

---

## Task 1: Gateway applies client blueprints via client-service

**Files:** `api-gateway/internal/handler/blueprint_handler.go`, its constructor/wiring, `api-gateway/cmd/main.go`; Test `blueprint_handler_test.go`.

- [ ] **Step 1 — inspect** `blueprint_handler.go`: the `BlueprintHandler` struct + constructor, the `ApplyBlueprint` method (~:229) which calls `h.client.ApplyBlueprint(...)`. Confirm `h.client` (userpb blueprint client) has `GetBlueprint`. Confirm how other handlers receive `clientpb.ClientLimitServiceClient` (the `LimitHandler` has `clientLimitClient` — see `limit_handler.go:50`). Find where `BlueprintHandler` is constructed in `cmd/main.go` and the `Handlers` struct.

- [ ] **Step 2 — failing tests** in `blueprint_handler_test.go`: add a stub `clientLimitClient` (implements `clientpb.ClientLimitServiceClient`, records `SetClientLimits` calls) and have the blueprint client's `GetBlueprint` stub return a `client`-type blueprint with `ValuesJson: '{"daily_limit":"5000.00","monthly_limit":"100000.00","transfer_limit":"2000.00"}'`. Test `TestBlueprint_ApplyClientType_CallsClientService`: applying a client blueprint calls `clientLimitClient.SetClientLimits` with `client_id == target_id` and the parsed values, and does NOT call user-service `ApplyBlueprint`. Test `TestBlueprint_ApplyEmployeeType_CallsUserService`: an employee-type blueprint still calls `h.client.ApplyBlueprint` and NOT client-service. Run → FAIL.

- [ ] **Step 3 — implement.** Add `clientLimitClient clientpb.ClientLimitServiceClient` to `BlueprintHandler` + constructor param + main wiring. Rewrite `ApplyBlueprint`:
  1. validate `target_id > 0` (as today).
  2. `bp, err := h.client.GetBlueprint(ctx, &userpb.GetBlueprintRequest{Id: blueprintID})` → map gRPC errors via `handleGRPCError`.
  3. `switch bp.Type`:
     - `"client"`: parse `bp.ValuesJson` into `{DailyLimit, MonthlyLimit, TransferLimit string}`; on parse error → 500 internal. Call `h.clientLimitClient.SetClientLimits(GRPCContextWithChangedBy(c), &clientpb.SetClientLimitRequest{ClientId: uint64(targetID), DailyLimit, MonthlyLimit, TransferLimit, ...})` (match the field names used in `limit_handler.go:291`). Map errors. On success return the same success response shape ApplyBlueprint returns today.
     - default (`"employee"`/`"actuary"`/unknown): call `h.client.ApplyBlueprint(...)` exactly as today (unchanged path).
  Keep the response body/status identical to today's for all types (compat).

- [ ] **Step 4 — run** the blueprint handler tests → PASS; `cd api-gateway && go build ./...`.

- [ ] **Step 5 — Swagger:** update the `ApplyBlueprint` godoc if the description references behavior; run `make swagger` (or `cd api-gateway && swag init -g cmd/main.go --output docs`) and commit regenerated docs.

- [ ] **Step 6 — commit** `feat(gateway): apply client-type blueprints via client-service directly (SP-4 ownership)`.

---

## Task 2: Remove user-service client-limit middle-man

**Files:** `user-service/internal/service/blueprint_service.go`, `user-service/cmd/main.go`; Test `blueprint_service_test.go`.

- [ ] **Step 1 — failing test** in `blueprint_service_test.go`: `TestApplyBlueprint_ClientType_Rejected` — applying a client-type blueprint returns an error (FailedPrecondition-ish sentinel) and the client gRPC stub's `SetClientLimits` is NOT called. (Adjust/remove the existing test that asserted client-type called the client gRPC.) Run → FAIL.

- [ ] **Step 2 — implement.** In `blueprint_service.go`: delete `applyClientBlueprint`; change the `case model.BlueprintTypeClient` in `ApplyBlueprint` to `return fmt.Errorf("ApplyBlueprint: client-type blueprints are applied by client-service, not user-service: %w", ErrClientBlueprintNotApplicable)` (add a sentinel `ErrClientBlueprintNotApplicable` mapped to gRPC `FailedPrecondition` in the handler layer — check how user-service's blueprint gRPC handler maps service errors and add the mapping). Remove the `clientClient` field from `BlueprintService`, its constructor param, and the assignment. Remove now-unused imports (`clientpb`).

- [ ] **Step 3 — main wiring.** In `user-service/cmd/main.go`: remove the `clientClient`/`clientConn` construction that was passed ONLY to `NewBlueprintService` (keep any client conn still used elsewhere — verify with grep; user-service dials client only for this, per the SP-0 audit, so the whole `CLIENT_GRPC_ADDR` dial in user-service may become removable. Confirm: grep user-service for other uses of the client gRPC client. If nothing else uses it, remove the dial + the `CLIENT_GRPC_ADDR` config field + its docker-compose env entry for user-service. If something else uses it, keep the dial and only drop the blueprint wiring.).

- [ ] **Step 4 — run** `cd user-service && CGO_ENABLED=1 go test ./... -count=1 && go build ./...` → PASS (fix any other caller of `NewBlueprintService` for the arity change).

- [ ] **Step 5 — docker-compose:** if `CLIENT_GRPC_ADDR` was removed from user-service config, remove it from user-service's `environment:` and drop the `client-service` entry from user-service's `depends_on` (only if nothing else in user-service needs it).

- [ ] **Step 6 — commit** `refactor(user): remove client-limit write middle-man from BlueprintService (SP-4 ownership)`.

---

## Task 3: Docs, version, full CI

- [ ] **Step 1 — `docs/api/REST_API_v3.md`:** update the apply-blueprint section to note that client-type blueprints are applied by client-service (gateway orchestrates synchronously); request/response unchanged.
- [ ] **Step 2 — `docs/Specification.md`:** §3 (gateway wiring — BlueprintHandler now also holds clientLimitClient; user-service BlueprintService no longer dials client-service) and §21 (business rule: client limits are written only by client-service; client-type blueprint apply is orchestrated by the gateway). Note SP-4 done.
- [ ] **Step 3 — version:** MINOR-ish? This changes internal orchestration only (no external contract change), but removes a service dependency → treat as MINOR. Bump to next minor (`2.18.x` → `2.19.0`); sync `version.go`.
- [ ] **Step 4 — `make ci`** all five jobs green. Fix lint/gofmt/tidy across api-gateway + user-service (+ regenerated swagger).
- [ ] **Step 5 — commit** `docs+chore(sp4): client-limit ownership to client-service; finalize SP-4; bump -> 2.19.0 (CI green)`.

---

## Self-review
- The apply route's external contract (request `{target_id}`, response, status codes) is unchanged for ALL blueprint types → not a breaking change.
- The client-limit write no longer routes through user-service (middle-man removed); gateway→client-service is synchronous; no events used for the write.
- Cap enforcement (client limit ≤ employee MaxClient*Limit) stays in client-service's SetClientLimits — unchanged.
- Ownership verification: the gateway already validates `target_id`; client-service applies caps. No new ownership hole (employee/admin applying a client blueprint is an admin op gated by the blueprint apply permission).

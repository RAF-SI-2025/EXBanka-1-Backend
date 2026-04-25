# On-behalf trading permission split + session revocation on role-permission change

**Date:** 2026-04-25
**Status:** Design — pending implementation
**Branch:** feature/securities-bank-safety

## Problem

1. The on-behalf trading routes (`POST /api/v2/orders` and `POST /api/v2/otc/admin/offers/{id}/buy`) are gated on `securities.manage`, which is also the gate for admin-only data-source / market-simulator endpoints (`/api/v1/admin/stock-source/*`). Today only `EmployeeAdmin` holds it, so neither `EmployeeAgent` nor `EmployeeSupervisor` can place on-behalf trades. Just adding `securities.manage` to those roles would also grant them admin powers — wrong semantics.
2. When a role's permissions change, existing employees with that role keep their old permissions until their access token (15 min) expires or they refresh, because permissions are baked into the JWT and the per-request `ValidateToken` path never re-reads the DB.

## Goals

- Let `EmployeeAgent` and `EmployeeSupervisor` place orders on behalf of clients (and buy OTC offers on behalf of clients) by default, without granting them admin-only data-source powers.
- When a role's permission set changes, force every employee currently holding that role to re-authenticate before their next request goes through. Effective immediately (single-digit-second propagation), not "after access token expiry".

## Non-goals

- Per-permission, per-employee overrides. The existing `employees.permissions` route already covers per-employee additions; this work doesn't change it.
- Revocation when an employee's *individual* permission set changes (not via a role). Out of scope; the same machinery can be reused later.
- Bank-account trading gate. Confirmed in brainstorming: any employee can already pass a bank account ID to `/api/v2/me/orders` (the `enforceOwnership` check is a no-op for `system_type != "client"`), so no change needed.

## Design

### Part A — On-behalf trading permission

Introduce a new permission code `orders.place-on-behalf`.

**Changes:**
- `user-service/internal/service/role_service.go`
  - Add `{"orders.place-on-behalf", "Place orders on behalf of a client", "orders"}` to `AllPermissions`.
  - Add `"orders.place-on-behalf"` to the seed permission list for `EmployeeAgent`, `EmployeeSupervisor`, and `EmployeeAdmin`.
- `api-gateway/internal/router/router_v1.go`
  - Line 589 (`ordersOnBehalf`): change `RequirePermission("securities.manage")` → `RequirePermission("orders.place-on-behalf")`.
  - Line 599 (`otcOnBehalf`): same change.
- v2/v3 routers do not redefine these routes (they fall through to v1), so no further router edits.

`securities.manage` keeps its current scope (admin stock-source endpoints) and remains EmployeeAdmin-only.

The route gate is the only enforcement. Stock-service does not check this permission; that's intentional — gateway is the policy boundary, services trust the gateway-enforced JWT claims.

### Part B — Session revocation on role-permission change

A per-user revocation epoch in Redis. When a role's permissions change, every employee with that role is marked revoked-as-of-now; `ValidateToken` rejects any token whose `iat` precedes that timestamp.

**Data:**
- New Redis key: `user_revoked_at:<employee_id>` → unix-second timestamp (string). TTL = access-token expiry (default 15 min). After TTL, the key disappears safely because no token issued before the cutoff can still be valid.

**New Kafka topic:** `user.role-permissions-changed`
- Payload (in `contract/kafka/messages.go`):
  ```go
  type RolePermissionsChangedMessage struct {
      RoleID              int64   `json:"role_id"`
      RoleName            string  `json:"role_name"`
      AffectedEmployeeIDs []int64 `json:"affected_employee_ids"`
      ChangedAt           int64   `json:"changed_at"` // unix seconds
      Source              string  `json:"source"`     // "update_role_permissions" | "create_role"
  }
  ```
- Topic constant: `TopicUserRolePermissionsChanged = "user.role-permissions-changed"`.

**Producer (user-service):**
- New repo helper: `RoleRepository.ListEmployeeIDsByRole(roleID int64) ([]int64, error)` — single query against `employee_roles` join table.
- `RoleService.UpdateRolePermissions` (and `CreateRole` for completeness): after `SetPermissions`, fetch affected employee IDs, build a `RolePermissionsChangedMessage`, publish via the existing user-service Kafka producer. Publish is best-effort: a Kafka failure is logged, not propagated, since the underlying DB write succeeded — the revocation is the bonus, not the contract.
- Add the new topic to `EnsureTopics(...)` in `user-service/cmd/main.go`.

**Consumer (auth-service):**
- New `internal/consumer/role_perm_change_consumer.go` — same pattern as the existing employee/client consumers. Group ID: `auth-service-role-perm-change`.
- On message: for each `AffectedEmployeeIDs[i]`:
  1. `SET user_revoked_at:<id> <ChangedAt>` with TTL = `JWT_ACCESS_EXPIRY` (read from existing config; default 15 min).
  2. Delete all refresh-token rows for that employee (`auth_db.refresh_tokens` where `user_id = ?`). Forces a full re-login — they cannot get a fresh access token via refresh either, since refresh re-fetches permissions from user-service and this is what we want.
- Add new topic to `EnsureTopics` in `auth-service/cmd/main.go`.
- Wire consumer in `cmd/main.go` alongside the existing `employeeConsumer` / `clientConsumer`.

**Validation gate (auth-service):**
- Extend `AuthService.ValidateToken` (`auth-service/internal/service/auth_service.go:272`):
  - After parsing claims (whether from cache or fresh JWT parse), look up `user_revoked_at:<claims.UserID>`. If the key exists and `claims.IssuedAt.Unix() < revokedAt`, return the existing `"access token has been revoked; please log in again"` error.
  - Done after both the cache hit and the cache miss paths so a stale cached claim still gets rejected.
  - One extra Redis GET per `ValidateToken` call. Acceptable: ValidateToken is already on the cache path.

**Failure modes:**
- Redis down: `user_revoked_at` lookup returns an error, treated as "no revocation key" (fail-open). Same posture as the existing JTI blacklist code at `auth_service.go:281,297` which already swallows blacklist lookup errors. The next-step refresh would still re-fetch permissions, so worst case a revoked session lives until access-token expiry — same as today's behavior, no regression.
- Kafka consumer lag: revocation is delayed by the lag. Acceptable; lag in this cluster is normally sub-second.
- Producer failure on the user-service side: revocation never happens for that change. The next role-permission update will pick up everyone (including the previously-affected employees), so eventual consistency is preserved. We log the failure for ops visibility.

### Out-of-scope follow-ups (deliberately not done here)

- Tracking a per-employee revocation epoch when an individual employee's `additional_permissions` are edited. The same Redis key + same consumer logic applies; trivial to bolt on later.
- Revoking when an employee's *role assignment* changes (e.g., demoted from Supervisor to Basic). Useful but separate flow — `EmployeeService.UpdateEmployee` would need to publish a similar event.
- Admin "kick all sessions" UI button. The Kafka event covers the technical primitive; UI is its own task.

## Testing

**Unit (user-service):**
- `TestRoleService_UpdateRolePermissions_PublishesEvent` — given a role with N employees, `UpdateRolePermissions` triggers a `RolePermissionsChangedMessage` with the right role + employee IDs.
- `TestRoleService_UpdateRolePermissions_KafkaFailureDoesNotFailUpdate` — Kafka producer error is logged but the DB write commits and returns nil error.
- `TestRoleRepository_ListEmployeeIDsByRole` — returns IDs in deterministic order, dedupes, returns empty slice for unknown role.

**Unit (auth-service):**
- `TestValidateToken_RevokedByEpoch_RejectsToken` — claims.IssuedAt < revoked_at → `access token has been revoked` error.
- `TestValidateToken_RevokedByEpoch_FreshTokenStillValid` — token issued AFTER revoked_at passes through.
- `TestValidateToken_RevokedByEpoch_RedisDown_FailOpen` — Redis error on the epoch lookup is swallowed (token still validated, matches existing posture).
- `TestRolePermChangeConsumer_HandleMessage_SetsEpochAndDeletesRefresh` — given a message with three employee IDs, three `user_revoked_at` keys appear in Redis with correct TTL, and the matching `refresh_tokens` rows are gone.

**Integration (test-app/workflows):**
- `TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth` — log in as admin, log in as agent, hit a permission-gated endpoint (200), admin calls `PUT /api/v1/roles/<agent-role>/permissions` with a perm change, agent's next request to the same endpoint returns 401 with `"access token has been revoked"`.
- `TestOnBehalfTrading_AsAgent_PlacesOrder` — agent JWT, `POST /api/v2/orders` with valid client + account → 201, holding lands under client (extends the existing `TestEmployeeOnBehalf_CreateOrder` to also assert against `/api/v1/clients/<id>/portfolio` — closes the test gap noted earlier).
- `TestOnBehalfTrading_AsBasic_Forbidden` — `EmployeeBasic` (does not have `orders.place-on-behalf`) → 403.

## Spec & doc updates

- `Specification.md`:
  - Section 6 (permissions): add `orders.place-on-behalf`.
  - Section 19 (Kafka topics): add `user.role-permissions-changed` + payload.
  - Section 20 (enums): no changes.
  - Section 21 (business rules): note "Role-permission updates revoke active sessions for affected employees within seconds via the role-permissions-changed Kafka event".
  - Section 17 (REST routes): note new permission gate on the two affected routes.
- `docs/api/REST_API_v1.md`: update the auth requirement for `POST /api/v1/orders` and `POST /api/v1/otc/admin/offers/{id}/buy` from `securities.manage` to `orders.place-on-behalf`.
- Swagger annotations on `CreateOrderOnBehalf` + `BuyOTCOfferOnBehalf` handlers, then `make swagger` regen.
- `docker-compose.yml` / `docker-compose-remote.yml`: no env-var or topic changes (Kafka topic creation is in-process via `EnsureTopics`).

## Migration / rollout

- New permission row: created on next user-service startup by the existing `SeedRolesAndPermissions` loop. Roles get the new permission attached on the same startup (the seeder does `SetPermissions` for existing roles too — see `role_service.go:142-148`).
- Active sessions held by Agent/Supervisor at the moment of the seeding aren't revoked automatically (the seed path doesn't go through `UpdateRolePermissions`). They'll pick up the new permission on next refresh / re-login. Acceptable given this is the initial rollout.
- Going forward, any explicit `PUT /api/v1/roles/<id>/permissions` call goes through the new revocation path.

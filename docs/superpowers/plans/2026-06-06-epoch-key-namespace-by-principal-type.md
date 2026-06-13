# Plan: namespace the force-refresh epoch key by principal type

Date: 2026-06-06
Status: DONE (VERSION 2.15.2→2.15.3) — all 7 prod + 5 test files changed; contract/auth/gateway
build + test + lint green.

## Problem

`authredis.UserRevokedAtKey(principalID)` produces `user_revoked_at:<id>`, keyed by the
numeric principal id ONLY — not namespaced by principal type. Employee ids (user_db) and
client ids (client_db) come from independent autoincrement sequences, so employee#5 and
client#5 coexist and share a single revocation epoch. Consequences:

- Disabling client#5 (`SetAccountStatus`) bumps the epoch that also force-refreshes a
  same-id employee.
- An employee role/permission change (role_perm consumer) can force-refresh a same-id client.

Impact is HARMLESS but SYSTEMATIC: the worst case is one extra silent token refresh for a
same-id principal of the other type, which self-heals (its refresh token is not revoked).
It never *under*-revokes. Still, the key should be correct.

## Fix

Thread `principalType` ("employee" | "client") through the writer → key → reader so the
key becomes `user_revoked_at:<principalType>:<principalID>`.

### Producer/key
1. `contract/authredis/keys.go` — `UserRevokedAtKey(principalType string, principalID int64)`.

### auth-service (writer + its own reader via ValidateToken/RefreshToken)
2. `internal/cache/redis.go` — `SetUserRevokedAt(ctx, principalType, userID, atUnix, ttl)`,
   `GetUserRevokedAt(ctx, principalType, userID)`, `userRevokedAtKey(principalType, userID)`.
3. `internal/service/auth_token.go` — `hardRevokeUser(ctx, c, accessExp, principalType, userID)`;
   `checkRevokedByEpoch` reads `c.GetUserRevokedAt(ctx, claims.PrincipalType, claims.PrincipalID)`.
4. `internal/service/auth_session.go` — `RevokeAllSessions(ctx, principalType, accountID, userID, reason)`
   → `hardRevokeUser(..., principalType, userID)`.
5. `internal/service/auth_account.go` — `SessionRevoker` interface gains `principalType`;
   ResetPassword passes `acct.PrincipalType`; `SetAccountStatus` passes its `principalType`.
6. `internal/consumer/role_perm_change_consumer.go` — interface + call pass `"employee"`
   (role/perm changes only affect employees).

### api-gateway (reader)
7. `internal/middleware/tokenverify.go` — `authredis.UserRevokedAtKey(claims.PrincipalType, claims.PrincipalID)`.

### Tests
8. `auth-service/internal/cache/redis_test.go` — key-format test + Set/Get calls.
9. `auth-service/internal/consumer/role_perm_change_consumer_test.go` — stub `SetUserRevokedAt` signature.
10. `auth-service/internal/service/validate_token_revoke_test.go` — `SetUserRevokedAt` calls.
11. `auth-service/internal/service/auth_service_flows_test.go` — `RevokeAllSessions` call.
12. `api-gateway/internal/middleware/tokenverify_test.go` — `UserRevokedAtKey` call.

## Backward compatibility

The Redis key format changes. Old keys (`user_revoked_at:<id>`) are orphaned on deploy but
self-expire via their TTL (= access-token lifetime, ≤15m). During that window a revocation
issued just before deploy is not found under the new key, so the affected token is not
force-refreshed — but the hard-revoke sid blacklist and DB refresh-token revocation still
apply, so it is at most a ≤15m weaker force-refresh on already-issued epochs. Acceptable
clean-break.

## Validation

- `make build` (auth-service, api-gateway, contract).
- `go test ./...` in auth-service, api-gateway, contract.
- `golangci-lint run` on touched services.
- VERSION 2.15.2 → 2.15.3 (PATCH: internal Redis key format fix, no API contract change).

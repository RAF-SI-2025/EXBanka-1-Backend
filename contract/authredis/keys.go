// Package authredis defines the Redis key schema shared between auth-service
// (the writer) and api-gateway (the reader) for access-token revocation. Both
// sides import these helpers so the key formats can never drift apart.
package authredis

import "strconv"

// SessionBlacklistKey marks every access token carrying a given session id
// (sid) as hard-revoked — written on logout / revoke-session. When the gateway
// finds this key it rejects the token with 401 unauthorized (the client logs
// out). Auth sets a TTL equal to the access-token lifetime so it self-cleans.
func SessionBlacklistKey(sid string) string { return "blacklist:sid:" + sid }

// UserRevokedAtKey holds the per-principal revocation epoch (unix seconds). An
// access token whose `iat` is older than this value must be FORCE-REFRESHED:
// permissions/roles/account-active changed (or the principal was revoked-all).
// The gateway returns 401 token_expired so the client silently refreshes rather
// than logging out.
//
// The key is namespaced by principalType ("employee" | "client") because
// employee ids (user_db) and client ids (client_db) are independent sequences —
// without the type prefix, employee#5 and client#5 would share one epoch and
// revoking one would spuriously force-refresh the other.
func UserRevokedAtKey(principalType string, principalID int64) string {
	return "user_revoked_at:" + principalType + ":" + strconv.FormatInt(principalID, 10)
}

// IsStale reports whether a token issued at tokenIATUnix predates the user's
// revocation epoch revokedAtUnix. A zero epoch means no revocation is set.
func IsStale(tokenIATUnix, revokedAtUnix int64) bool {
	return revokedAtUnix != 0 && tokenIATUnix < revokedAtUnix
}

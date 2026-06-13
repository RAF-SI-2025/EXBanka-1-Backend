package authredis

import "testing"

// These tests lock the exact Redis key strings. auth-service (writer) and
// api-gateway (reader) both import this package precisely so the formats can
// never drift — a change here that isn't intended is a cross-service breakage,
// so the literals are asserted verbatim.

func TestSessionBlacklistKey(t *testing.T) {
	if got := SessionBlacklistKey("56"); got != "blacklist:sid:56" {
		t.Fatalf("SessionBlacklistKey = %q, want %q", got, "blacklist:sid:56")
	}
}

func TestUserRevokedAtKey_Format(t *testing.T) {
	if got := UserRevokedAtKey("employee", 5); got != "user_revoked_at:employee:5" {
		t.Fatalf("UserRevokedAtKey(employee,5) = %q, want %q", got, "user_revoked_at:employee:5")
	}
	if got := UserRevokedAtKey("client", 5); got != "user_revoked_at:client:5" {
		t.Fatalf("UserRevokedAtKey(client,5) = %q, want %q", got, "user_revoked_at:client:5")
	}
}

// TestUserRevokedAtKey_NamespacedByType is the regression guard for the
// principal-type namespacing fix: employee ids (user_db) and client ids
// (client_db) are independent sequences, so the same numeric id MUST map to
// distinct keys — otherwise revoking employee#5 would spuriously force-refresh
// client#5 (and vice versa). Verified live on 2026-06-06.
func TestUserRevokedAtKey_NamespacedByType(t *testing.T) {
	if UserRevokedAtKey("employee", 5) == UserRevokedAtKey("client", 5) {
		t.Fatal("employee and client epoch keys collide for the same id — namespacing regression")
	}
}

func TestIsStale(t *testing.T) {
	cases := []struct {
		name         string
		iat, revoked int64
		want         bool
	}{
		{"no epoch set => not stale", 1000, 0, false},
		{"issued before epoch => stale", 999, 1000, true},
		{"issued at epoch => not stale (strict <)", 1000, 1000, false},
		{"issued after epoch => not stale", 1001, 1000, false},
	}
	for _, tc := range cases {
		if got := IsStale(tc.iat, tc.revoked); got != tc.want {
			t.Errorf("%s: IsStale(%d,%d)=%v want %v", tc.name, tc.iat, tc.revoked, got, tc.want)
		}
	}
}

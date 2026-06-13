package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/authredis"
)

type fakeKeys struct {
	kid string
	pub *ecdsa.PublicKey
}

func (f fakeKeys) PublicKey(_ context.Context, kid string) (*ecdsa.PublicKey, bool) {
	if kid == f.kid {
		return f.pub, true
	}
	return nil, false
}
func (f fakeKeys) HasKeys() bool { return true }

func signToken(t *testing.T, priv *ecdsa.PrivateKey, kid string, claims accessClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	require.NoError(t, err)
	return s
}

func newLocalVerifier(t *testing.T, priv *ecdsa.PrivateKey, kid string) (*TokenVerifier, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	keys := fakeKeys{kid: kid, pub: &priv.PublicKey}
	return NewTokenVerifier(keys, rdb, nil), rdb // nil fallback → local-only
}

func baseClaims(sid string, iat time.Time) accessClaims {
	return accessClaims{
		PrincipalID:   7,
		Email:         "e@x",
		Roles:         []string{"EmployeeAgent"},
		Permissions:   []string{"orders.read.own"},
		PrincipalType: "employee",
		Sid:           sid,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-1",
			IssuedAt:  jwt.NewNumericDate(iat),
			ExpiresAt: jwt.NewNumericDate(iat.Add(15 * time.Minute)),
		},
	}
}

func TestVerify_Local_ValidToken(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	v, _ := newLocalVerifier(t, priv, "kid-1")
	tok := signToken(t, priv, "kid-1", baseClaims("100", time.Now()))

	p, kind := v.Verify(context.Background(), tok)
	require.Equal(t, VerifyOK, kind)
	require.NotNil(t, p)
	require.Equal(t, int64(7), p.PrincipalID)
	require.Equal(t, "employee", p.PrincipalType)
	require.Equal(t, []string{"orders.read.own"}, p.Permissions)
	require.Equal(t, "EmployeeAgent", p.Role)
}

func TestVerify_Local_Expired_IsTokenExpired(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	v, _ := newLocalVerifier(t, priv, "kid-1")
	// iat 1h ago → exp 45m ago.
	tok := signToken(t, priv, "kid-1", baseClaims("100", time.Now().Add(-time.Hour)))

	_, kind := v.Verify(context.Background(), tok)
	require.Equal(t, VerifyTokenExpired, kind, "expired token should prompt refresh, not logout")
}

func TestVerify_Local_BadSignature_IsUnauthorized(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	v, _ := newLocalVerifier(t, priv, "kid-1")
	// Signed with a different key but the kid the verifier knows → bad signature.
	tok := signToken(t, other, "kid-1", baseClaims("100", time.Now()))

	_, kind := v.Verify(context.Background(), tok)
	require.Equal(t, VerifyUnauthorized, kind)
}

func TestVerify_Local_BlacklistedSid_IsUnauthorized(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	v, rdb := newLocalVerifier(t, priv, "kid-1")
	tok := signToken(t, priv, "kid-1", baseClaims("100", time.Now()))

	require.NoError(t, rdb.Set(context.Background(), authredis.SessionBlacklistKey("100"), "revoked", time.Minute).Err())
	_, kind := v.Verify(context.Background(), tok)
	require.Equal(t, VerifyUnauthorized, kind, "logout/revoke (sid blacklist) should force re-auth")
}

func TestVerify_Local_StaleByEpoch_IsTokenExpired(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	v, rdb := newLocalVerifier(t, priv, "kid-1")
	// Token issued now; epoch set 1h in the future → stale.
	tok := signToken(t, priv, "kid-1", baseClaims("100", time.Now()))
	require.NoError(t, rdb.Set(context.Background(), authredis.UserRevokedAtKey("employee", 7),
		time.Now().Add(time.Hour).Unix(), time.Minute).Err())

	_, kind := v.Verify(context.Background(), tok)
	require.Equal(t, VerifyTokenExpired, kind, "claims-change epoch should prompt refresh")
}

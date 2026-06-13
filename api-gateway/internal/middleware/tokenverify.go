package middleware

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	authpb "github.com/exbanka/contract/authpb"
	"github.com/exbanka/contract/authredis"
	"github.com/exbanka/contract/identity"
)

// KeyProvider hands out ES256 public keys by kid (implemented by jwks.Cache).
type KeyProvider interface {
	PublicKey(ctx context.Context, kid string) (*ecdsa.PublicKey, bool)
	HasKeys() bool
}

// VerifyKind is the outcome of verifying an access token.
type VerifyKind int

const (
	// VerifyOK — token is valid and active.
	VerifyOK VerifyKind = iota
	// VerifyUnauthorized — bad signature/malformed, or hard-revoked (logout /
	// revoke-session). The client must re-authenticate (→ 401 unauthorized).
	VerifyUnauthorized
	// VerifyTokenExpired — token is past `exp`, or its claims are stale (the
	// per-user revocation epoch moved past its `iat`). The client should
	// silently refresh (→ 401 token_expired), NOT log out.
	VerifyTokenExpired
)

// Principal is the verified caller identity the middleware stamps onto the gin
// context. Built either from a locally-verified JWT or, on fallback, from a
// gRPC ValidateToken response.
type Principal struct {
	PrincipalID       int64
	Email             string
	Role              string
	Roles             []string
	Permissions       []string
	PrincipalType     string
	DeviceID          string
	DeviceType        string
	FirstName         string
	LastName          string
	AccountActive     bool
	BiometricsEnabled bool
}

// accessClaims mirrors the JWT signed by auth-service (service.Claims).
type accessClaims struct {
	PrincipalID       int64    `json:"principal_id"`
	Email             string   `json:"email"`
	Roles             []string `json:"roles"`
	Permissions       []string `json:"permissions"`
	PrincipalType     string   `json:"principal_type"`
	Sid               string   `json:"sid,omitempty"`
	DeviceType        string   `json:"device_type,omitempty"`
	DeviceID          string   `json:"device_id,omitempty"`
	FirstName         string   `json:"first_name,omitempty"`
	LastName          string   `json:"last_name,omitempty"`
	AccountActive     bool     `json:"account_active"`
	BiometricsEnabled bool     `json:"biometrics_enabled,omitempty"`
	jwt.RegisteredClaims
}

// TokenVerifier verifies access tokens. Primary path: verify the ES256
// signature LOCALLY with a cached public key and consult the Redis denylists.
// Fallback path (no local keys available, or for test/mocked setups): delegate
// to auth-service's gRPC ValidateToken, which performs equivalent checks.
type TokenVerifier struct {
	keys     KeyProvider
	redis    *redis.Client
	fallback authpb.AuthServiceClient
}

// NewTokenVerifier wires the local key provider, the Redis client (for the
// denylists), and the gRPC fallback. Any may be nil; with all nil the verifier
// rejects everything.
func NewTokenVerifier(keys KeyProvider, rdb *redis.Client, fallback authpb.AuthServiceClient) *TokenVerifier {
	return &TokenVerifier{keys: keys, redis: rdb, fallback: fallback}
}

// Verify validates a raw bearer token and returns the caller principal.
func (v *TokenVerifier) Verify(ctx context.Context, token string) (*Principal, VerifyKind) {
	if v == nil {
		return nil, VerifyUnauthorized
	}
	// Prefer local verification when we have signing keys.
	if v.keys != nil && v.keys.HasKeys() {
		p, kind, handled := v.verifyLocal(ctx, token)
		if handled {
			return p, kind
		}
		// Not handled (unknown kid even after refresh) → fall through to gRPC.
	}
	if v.fallback != nil {
		return v.verifyRemote(ctx, token)
	}
	return nil, VerifyUnauthorized
}

// verifyLocal returns handled=false only when it cannot make a determination
// (e.g. the token's kid is unknown), so the caller can fall back to gRPC.
func (v *TokenVerifier) verifyLocal(ctx context.Context, token string) (*Principal, VerifyKind, bool) {
	unknownKid := false
	claims := &accessClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		kid, _ := t.Header["kid"].(string)
		pub, ok := v.keys.PublicKey(ctx, kid)
		if !ok {
			unknownKid = true
			return nil, errors.New("unknown kid")
		}
		return pub, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		if unknownKid {
			return nil, VerifyUnauthorized, false // let caller fall back to gRPC
		}
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, VerifyTokenExpired, true // FE refreshes
		}
		return nil, VerifyUnauthorized, true
	}

	// Hard revocation: session blacklisted (logout / revoke-session) → logout.
	if claims.Sid != "" && v.redis != nil {
		if n, rerr := v.redis.Exists(ctx, authredis.SessionBlacklistKey(claims.Sid)).Result(); rerr == nil && n > 0 {
			return nil, VerifyUnauthorized, true
		}
	}
	// Stale claims: per-user revocation epoch moved past the token's iat →
	// force refresh (permissions/roles/account-active changed, or revoke-all).
	if claims.IssuedAt != nil && v.redis != nil {
		if revokedAt, rerr := v.redis.Get(ctx, authredis.UserRevokedAtKey(claims.PrincipalType, claims.PrincipalID)).Int64(); rerr == nil {
			if authredis.IsStale(claims.IssuedAt.Unix(), revokedAt) {
				return nil, VerifyTokenExpired, true
			}
		}
	}

	return &Principal{
		PrincipalID:       claims.PrincipalID,
		Email:             claims.Email,
		Role:              firstOr(claims.Roles, ""),
		Roles:             claims.Roles,
		Permissions:       claims.Permissions,
		PrincipalType:     claims.PrincipalType,
		DeviceID:          claims.DeviceID,
		DeviceType:        claims.DeviceType,
		FirstName:         claims.FirstName,
		LastName:          claims.LastName,
		AccountActive:     claims.AccountActive,
		BiometricsEnabled: claims.BiometricsEnabled,
	}, VerifyOK, true
}

func (v *TokenVerifier) verifyRemote(ctx context.Context, token string) (*Principal, VerifyKind) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := v.fallback.ValidateToken(rctx, &authpb.ValidateTokenRequest{Token: token})
	if err != nil || resp == nil || !resp.Valid {
		return nil, VerifyUnauthorized
	}
	return &Principal{
		PrincipalID:       resp.PrincipalId,
		Email:             resp.Email,
		Role:              resp.Role,
		Roles:             resp.Roles,
		Permissions:       resp.Permissions,
		PrincipalType:     resp.PrincipalType,
		DeviceID:          resp.DeviceId,
		DeviceType:        resp.DeviceType,
		FirstName:         resp.FirstName,
		LastName:          resp.LastName,
		AccountActive:     resp.AccountActive,
		BiometricsEnabled: resp.BiometricsEnabled,
	}, VerifyOK
}

func firstOr(s []string, fallback string) string {
	if len(s) > 0 {
		return s[0]
	}
	return fallback
}

// setPrincipalContext stamps the verified principal onto the gin context using
// the same keys the rest of the gateway reads.
func setPrincipalContext(c *gin.Context, p *Principal) {
	c.Set("principal_id", p.PrincipalID)
	c.Set("email", p.Email)
	c.Set("role", p.Role)
	c.Set("roles", p.Roles)
	c.Set("principal_type", p.PrincipalType)
	c.Set("permissions", p.Permissions)
	c.Set("device_id", p.DeviceID)
	c.Set("first_name", p.FirstName)
	c.Set("last_name", p.LastName)
	c.Set("account_active", p.AccountActive)
	c.Set("biometrics_enabled", p.BiometricsEnabled)

	// OWN-1: propagate the caller identity to downstream services over gRPC
	// metadata so each owning service can enforce ownership of its own
	// resources. Stamped on c.Request's context so both reads (which pass
	// c.Request.Context() directly) and mutations (which wrap it via
	// GRPCContextWithChangedBy) carry it. On-behalf-of-client is layered on
	// per-route where applicable.
	c.Request = c.Request.WithContext(identity.Inject(c.Request.Context(), identity.Caller{
		PrincipalType: p.PrincipalType,
		PrincipalID:   p.PrincipalID,
	}))
}

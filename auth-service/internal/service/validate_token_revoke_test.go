package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/exbanka/auth-service/internal/cache"
	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/repository"
)

func newAuthServiceWithRealCache(t *testing.T) (*AuthService, *cache.RedisCache, *JWTService) {
	t.Helper()
	mr := miniredis.RunT(t)

	c, err := cache.NewRedisCache(mr.Addr())
	if err != nil {
		t.Fatalf("cache: %v", err)
	}

	db := setupAccountStatusTestDB(t)
	tokenRepo := repository.NewTokenRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	loginRepo := repository.NewLoginAttemptRepository(db)
	totpRepo := repository.NewTOTPRepository(db)
	totpSvc := NewTOTPService()
	jwtSvc := NewJWTService(mustTestKeyManager(), 15*time.Minute)
	accountRepo := repository.NewAccountRepository(db)

	svc := NewAuthService(
		tokenRepo, sessionRepo, loginRepo, totpRepo, totpSvc, jwtSvc,
		accountRepo, nil, kafkaprod.NewProducer("localhost:1"), c,
		168*time.Hour, 720*time.Hour,
		"http://localhost:3000", "test-pepper",
	)
	return svc, c, jwtSvc
}

// TestValidateToken_BlacklistedSid_RejectsToken: blacklisting a session's sid
// must cause ValidateToken to reject a token carrying that sid (hard revoke).
func TestValidateToken_BlacklistedSid_RejectsToken(t *testing.T) {
	svc, redis, jwtSvc := newAuthServiceWithRealCache(t)

	tok, err := jwtSvc.GenerateAccessToken(5, "s@id", []string{"EmployeeAgent"}, nil, "employee", TokenProfile{Sid: "777"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Sanity: valid before blacklisting.
	if _, err := svc.ValidateToken(tok); err != nil {
		t.Fatalf("expected valid before blacklist, got %v", err)
	}
	// Blacklist the session id carried in the token.
	if err := redis.Set(context.Background(), sessionBlacklistKey("777"), "revoked", 15*time.Minute); err != nil {
		t.Fatalf("blacklist: %v", err)
	}
	if _, err := svc.ValidateToken(tok); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revoked after sid blacklist, got %v", err)
	}
}

// TestValidateToken_RevokedByEpoch_RejectsToken: setting user_revoked_at AFTER
// the token's iat must cause ValidateToken to reject the token with the
// "access token has been revoked" error.
func TestValidateToken_RevokedByEpoch_RejectsToken(t *testing.T) {
	svc, redis, jwtSvc := newAuthServiceWithRealCache(t)

	tok, err := jwtSvc.GenerateAccessToken(42, "x@y", []string{"EmployeeAgent"}, []string{"clients.read.all"}, "employee", TokenProfile{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Revoke epoch in the future relative to the token's iat (1h ahead).
	if err := redis.SetUserRevokedAt(context.Background(), "employee", 42, time.Now().Add(1*time.Hour).Unix(), 15*time.Minute); err != nil {
		t.Fatalf("set epoch: %v", err)
	}

	_, err = svc.ValidateToken(tok)
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revoked error, got %v", err)
	}
}

// TestValidateToken_RevokedByEpoch_FreshTokenStillValid: a token issued AFTER
// the revocation cutoff must pass.
func TestValidateToken_RevokedByEpoch_FreshTokenStillValid(t *testing.T) {
	svc, redis, jwtSvc := newAuthServiceWithRealCache(t)

	// Cutoff in the past.
	if err := redis.SetUserRevokedAt(context.Background(), "employee", 42, time.Now().Add(-1*time.Hour).Unix(), 15*time.Minute); err != nil {
		t.Fatalf("set epoch: %v", err)
	}

	tok, err := jwtSvc.GenerateAccessToken(42, "x@y", []string{"EmployeeAgent"}, []string{"clients.read.all"}, "employee", TokenProfile{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if _, err := svc.ValidateToken(tok); err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
}

// TestValidateToken_RevokedByEpoch_NoEpochKey_TokenValid: when no
// user_revoked_at key exists for the user, the token must validate normally.
func TestValidateToken_RevokedByEpoch_NoEpochKey_TokenValid(t *testing.T) {
	svc, _, jwtSvc := newAuthServiceWithRealCache(t)

	tok, err := jwtSvc.GenerateAccessToken(99, "y@z", []string{"EmployeeBasic"}, []string{}, "employee", TokenProfile{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if _, err := svc.ValidateToken(tok); err != nil {
		t.Fatalf("expected valid token (no epoch key), got %v", err)
	}
}

// TestValidateToken_RevokedByEpoch_CacheHitPathAlsoChecksEpoch: validate once
// (populates the JWT-cache with claims), then revoke, then call ValidateToken
// again. The second call goes through the cache-hit branch, which must ALSO
// check the epoch and reject.
func TestValidateToken_RevokedByEpoch_CacheHitPathAlsoChecksEpoch(t *testing.T) {
	svc, redis, jwtSvc := newAuthServiceWithRealCache(t)

	tok, err := jwtSvc.GenerateAccessToken(7, "a@b", []string{"EmployeeAgent"}, []string{"clients.read.all"}, "employee", TokenProfile{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// First validate succeeds and populates the claims cache.
	if _, err := svc.ValidateToken(tok); err != nil {
		t.Fatalf("first validate: %v", err)
	}

	// Now revoke.
	if err := redis.SetUserRevokedAt(context.Background(), "employee", 7, time.Now().Add(1*time.Hour).Unix(), 15*time.Minute); err != nil {
		t.Fatalf("set epoch: %v", err)
	}

	// Second validate hits the cache; must reject because of epoch.
	_, err = svc.ValidateToken(tok)
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revoked from cache-hit path, got %v", err)
	}
}

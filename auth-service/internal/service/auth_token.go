package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/exbanka/auth-service/internal/cache"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	"github.com/exbanka/contract/authredis"
	userpb "github.com/exbanka/contract/userpb"
)

// TokenService owns the access/refresh token lifecycle: minting, local-denylist
// validation, and refresh (browser + mobile). Independently constructable and
// testable; the revocation primitives it relies on are package functions so it
// needs no reference to the session or account services.
type TokenService struct {
	tokenRepo        *repository.TokenRepository
	sessionRepo      *repository.SessionRepository
	accountRepo      *repository.AccountRepository
	userClient       userpb.UserServiceClient
	jwtService       *JWTService
	cache            *cache.RedisCache
	refreshExp       time.Duration
	mobileRefreshExp time.Duration
}

// NewTokenService wires the token-lifecycle dependencies.
func NewTokenService(
	tokenRepo *repository.TokenRepository,
	sessionRepo *repository.SessionRepository,
	accountRepo *repository.AccountRepository,
	userClient userpb.UserServiceClient,
	jwtService *JWTService,
	c *cache.RedisCache,
	refreshExp, mobileRefreshExp time.Duration,
) *TokenService {
	return &TokenService{
		tokenRepo:        tokenRepo,
		sessionRepo:      sessionRepo,
		accountRepo:      accountRepo,
		userClient:       userClient,
		jwtService:       jwtService,
		cache:            c,
		refreshExp:       refreshExp,
		mobileRefreshExp: mobileRefreshExp,
	}
}

func (s *TokenService) ValidateToken(tokenString string) (*Claims, error) {
	cacheKey := "token:" + hashToken(tokenString)

	// Try cache first
	if s.cache != nil {
		var cached Claims
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			// Hard revocation: the session (sid) was blacklisted (logout / revoke).
			if cached.Sid != "" {
				blacklisted, _ := s.cache.Exists(context.Background(), sessionBlacklistKey(cached.Sid))
				if blacklisted {
					return nil, fmt.Errorf("access token revoked: %w", ErrTokenRevoked)
				}
			}
			if revoked, _ := checkRevokedByEpoch(s.cache, &cached); revoked {
				return nil, fmt.Errorf("access token revoked: %w", ErrTokenRevoked)
			}
			return &cached, nil
		}
	}

	claims, err := s.jwtService.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Hard revocation: the session (sid) was blacklisted (logout / revoke).
	if claims.Sid != "" && s.cache != nil {
		blacklisted, _ := s.cache.Exists(context.Background(), sessionBlacklistKey(claims.Sid))
		if blacklisted {
			return nil, fmt.Errorf("access token revoked: %w", ErrTokenRevoked)
		}
	}

	if revoked, _ := checkRevokedByEpoch(s.cache, claims); revoked {
		return nil, fmt.Errorf("access token revoked: %w", ErrTokenRevoked)
	}

	// Cache with TTL = remaining token lifetime
	if s.cache != nil && claims.ExpiresAt != nil {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			_ = s.cache.Set(context.Background(), cacheKey, claims, ttl)
		}
	}

	return claims, nil
}

// SigningKeys returns the public JWKS (current + rotation-overlap keys) so the
// GetSigningKeys RPC can hand them to the gateway for local ES256 verification.
func (s *TokenService) SigningKeys() ([]PublicKeyInfo, error) {
	return s.jwtService.Keys().JWKS()
}

// sessionBlacklistKey is the Redis key the gateway and auth both consult to
// hard-revoke every access token carrying a given session id (sid). The format
// is owned by contract/authredis so the two services can't drift.
func sessionBlacklistKey(sid string) string { return authredis.SessionBlacklistKey(sid) }

// blacklistSession hard-revokes every access token tied to a session id. The
// gateway sees the key on its next local verify and returns 401 unauthorized
// (the client logs out). TTL = access-token lifetime: after that no token with
// this sid could still be valid, so the key self-cleans. No-op without Redis.
//
// A package function (not a method) so the session and account services can
// share it without depending on each other or on TokenService.
func blacklistSession(ctx context.Context, c *cache.RedisCache, accessExp time.Duration, sessionID int64) {
	if c == nil || sessionID == 0 {
		return
	}
	key := sessionBlacklistKey(strconv.FormatInt(sessionID, 10))
	if err := c.Set(ctx, key, "revoked", accessExp); err != nil {
		log.Printf("warn: failed to blacklist session %d: %v", sessionID, err)
	}
}

// hardRevokeUser bumps the per-principal revocation epoch so EVERY access token
// the principal currently holds is rejected (used for revoke-all and
// account-disable, where there is no single session to target). Refresh tokens
// are revoked separately by the caller, so the net effect is a full logout.
// principalType ("employee" | "client") namespaces the epoch key.
func hardRevokeUser(ctx context.Context, c *cache.RedisCache, accessExp time.Duration, principalType string, userID int64) {
	if c == nil || userID == 0 || principalType == "" {
		return
	}
	if err := c.SetUserRevokedAt(ctx, principalType, userID, time.Now().Unix(), accessExp); err != nil {
		log.Printf("warn: failed to set revocation epoch for %s %d: %v", principalType, userID, err)
	}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// checkRevokedByEpoch returns true when the given claims' IssuedAt is older
// than the per-user revocation epoch in Redis. Redis errors are swallowed
// (fail-open), matching the existing posture of the JTI blacklist lookup.
// Returns (false, nil) when no epoch is set or the claim has no IssuedAt.
func checkRevokedByEpoch(c *cache.RedisCache, claims *Claims) (bool, error) {
	if claims == nil || claims.IssuedAt == nil || c == nil {
		return false, nil
	}
	revokedAt, err := c.GetUserRevokedAt(context.Background(), claims.PrincipalType, claims.PrincipalID)
	if err != nil || revokedAt == 0 {
		return false, err
	}
	return claims.IssuedAt.Unix() < revokedAt, nil
}

func (s *TokenService) RefreshToken(ctx context.Context, refreshTokenStr, ipAddress, userAgent string) (string, string, error) {
	rt, err := s.tokenRepo.GetRefreshToken(refreshTokenStr)
	if err != nil {
		return "", "", fmt.Errorf("refresh token lookup: %w", ErrInvalidToken)
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", fmt.Errorf("refresh token expired: %w", ErrTokenExpired)
	}

	// Look up account by AccountID
	var acct model.Account
	if err := s.accountRepo.GetByID(rt.AccountID, &acct); err != nil {
		return "", "", fmt.Errorf("refresh account lookup: %w", ErrAccountNotFound)
	}
	if acct.Status != model.AccountStatusActive {
		return "", "", fmt.Errorf("refresh account disabled: %w", ErrAccountDisabled)
	}

	if err := s.tokenRepo.RevokeRefreshToken(refreshTokenStr); err != nil {
		return "", "", fmt.Errorf("failed to revoke old refresh token: %w", err)
	}

	// Update session activity
	if rt.SessionID != nil {
		_ = s.sessionRepo.UpdateLastActive(*rt.SessionID)
	}

	systemType := rt.SystemType
	if systemType == "" {
		systemType = "employee" // backwards compat for existing tokens without system_type
	}

	var accessToken string

	// Carry the same sid forward so the refreshed access token stays tied to
	// its session (per-session revocation keeps working across refreshes).
	sid := ""
	if rt.SessionID != nil {
		sid = strconv.FormatInt(*rt.SessionID, 10)
	}

	if systemType == "client" {
		accessToken, err = s.jwtService.GenerateAccessToken(acct.PrincipalID, acct.Email, []string{"client"}, nil, "client", TokenProfile{
			AccountActive: acct.Status == model.AccountStatusActive,
			Sid:           sid,
		})
		if err != nil {
			return "", "", err
		}
	} else {
		userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: acct.PrincipalID})
		if err != nil {
			return "", "", fmt.Errorf("refresh employee lookup: %w", ErrEmployeeRPCFailed)
		}
		refreshRoles := userResp.Roles
		if len(refreshRoles) == 0 && userResp.Role != "" {
			refreshRoles = []string{userResp.Role}
		}
		accessToken, err = s.jwtService.GenerateAccessToken(
			userResp.Id, userResp.Email, refreshRoles, userResp.Permissions, "employee", TokenProfile{
				FirstName:     userResp.FirstName,
				LastName:      userResp.LastName,
				AccountActive: acct.Status == model.AccountStatusActive,
				Sid:           sid,
			},
		)
		if err != nil {
			return "", "", err
		}
	}

	newRefreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	newRT := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: systemType,
		SessionID:  rt.SessionID, // Inherit session from old token
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
	}
	if err := s.tokenRepo.CreateRefreshToken(newRT); err != nil {
		return "", "", err
	}

	AuthTokensIssuedTotal.WithLabelValues("access").Inc()
	AuthTokensIssuedTotal.WithLabelValues("refresh").Inc()

	return accessToken, newRefreshToken, nil
}

// ValidateRefreshToken returns the refresh token record if valid.
func (s *TokenService) ValidateRefreshToken(token string) (*model.RefreshToken, error) {
	rt, err := s.tokenRepo.GetRefreshToken(token)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if rt.Revoked {
		return nil, errors.New("refresh token revoked")
	}
	if time.Now().After(rt.ExpiresAt) {
		return nil, errors.New("refresh token expired")
	}
	return rt, nil
}

// MobileDeviceLookup is the minimal subset of *MobileDeviceService needed by
// RefreshTokenForMobile. Defined as an interface so handlers and tests can
// inject a stub without depending on the concrete type.
type MobileDeviceLookup interface {
	GetDeviceInfo(userID int64) (*model.MobileDevice, error)
}

// RefreshTokenForMobile validates the refresh token, verifies the device is active and matches,
// revokes the old token, and issues a new mobile token pair.
func (s *TokenService) RefreshTokenForMobile(ctx context.Context, oldRefreshToken, deviceID string, mobileSvc MobileDeviceLookup) (string, string, error) {
	rt, err := s.tokenRepo.GetRefreshToken(oldRefreshToken)
	if err != nil {
		return "", "", fmt.Errorf("RefreshTokenForMobile: lookup token: %v: %w", err, ErrInvalidToken)
	}
	if rt.Revoked {
		return "", "", fmt.Errorf("RefreshTokenForMobile: refresh token revoked: %w", ErrTokenRevoked)
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", fmt.Errorf("RefreshTokenForMobile: refresh token expired at %s: %w", rt.ExpiresAt.Format(time.RFC3339), ErrTokenExpired)
	}

	// Get account to resolve PrincipalID (the actual user ID used in MobileDevice)
	var acct model.Account
	if err := s.accountRepo.GetByID(rt.AccountID, &acct); err != nil {
		return "", "", fmt.Errorf("RefreshTokenForMobile: lookup account %d: %v: %w", rt.AccountID, err, ErrAccountNotFound)
	}
	if acct.Status != model.AccountStatusActive {
		return "", "", fmt.Errorf("RefreshTokenForMobile: account %d status=%s: %w", acct.ID, acct.Status, ErrAccountDisabled)
	}

	// Verify device is active and matches the provided deviceID
	device, err := mobileSvc.GetDeviceInfo(acct.PrincipalID)
	if err != nil {
		return "", "", fmt.Errorf("RefreshTokenForMobile: device lookup for principal %d: %v: %w", acct.PrincipalID, err, ErrDeviceNotFound)
	}
	if device.DeviceID != deviceID {
		return "", "", fmt.Errorf("RefreshTokenForMobile: device id %q does not match registered device for principal %d: %w", deviceID, acct.PrincipalID, ErrDeviceMismatch)
	}

	// Revoke old token
	_ = s.tokenRepo.RevokeRefreshToken(oldRefreshToken)

	// Fetch roles/permissions
	var roles []string
	var permissions []string
	systemType := rt.SystemType
	if systemType == "" {
		systemType = "employee"
	}

	var firstName, lastName string
	if systemType == "employee" {
		emp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: acct.PrincipalID})
		if err == nil {
			roles = emp.Roles
			permissions = emp.Permissions
			firstName = emp.FirstName
			lastName = emp.LastName
		}
	} else {
		roles = []string{"client"}
	}

	// Generate new access token with device claims
	access, err := s.jwtService.GenerateMobileAccessToken(
		acct.PrincipalID, acct.Email, roles, permissions,
		systemType, MobileProfile{
			TokenProfile: TokenProfile{
				FirstName:     firstName,
				LastName:      lastName,
				AccountActive: acct.Status == model.AccountStatusActive,
			},
			DeviceType:        "mobile",
			DeviceID:          deviceID,
			BiometricsEnabled: device.BiometricsEnabled,
		},
	)
	if err != nil {
		return "", "", err
	}

	// Update session activity
	if rt.SessionID != nil {
		_ = s.sessionRepo.UpdateLastActive(*rt.SessionID)
	}

	// Generate new refresh token
	newRefreshStr, err := generateToken()
	if err != nil {
		return "", "", err
	}
	newRT := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: systemType,
		SessionID:  rt.SessionID, // Inherit session
	}
	if err := s.tokenRepo.CreateRefreshToken(newRT); err != nil {
		return "", "", err
	}

	return access, newRefreshStr, nil
}

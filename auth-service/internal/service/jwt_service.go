package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type Claims struct {
	PrincipalID       int64    `json:"principal_id"` // was: user_id; the principal's primary-key id
	Email             string   `json:"email"`
	Roles             []string `json:"roles"`
	Permissions       []string `json:"permissions"`
	PrincipalType     string   `json:"principal_type"`        // was: system_type; "employee" or "client"
	Sid               string   `json:"sid,omitempty"`         // active_session id; enables targeted (per-session) revocation
	DeviceType        string   `json:"device_type,omitempty"` // "mobile" for mobile app tokens, empty for browser
	DeviceID          string   `json:"device_id,omitempty"`   // UUID of registered mobile device
	FirstName         string   `json:"first_name,omitempty"`
	LastName          string   `json:"last_name,omitempty"`
	AccountActive     bool     `json:"account_active"`
	BiometricsEnabled bool     `json:"biometrics_enabled,omitempty"` // only for mobile tokens
	jwt.RegisteredClaims
}

// JWTService signs and verifies access tokens with ES256 (asymmetric). The
// private half lives here in auth-service; the public half is published to the
// gateway via GetSigningKeys so it can verify tokens locally without a gRPC hop.
type JWTService struct {
	keys         *KeyManager
	accessExpiry time.Duration
}

// NewJWTService builds the service around a KeyManager. Tests use
// mustTestKeyManager() to get a throwaway ES256 key.
func NewJWTService(keys *KeyManager, accessExpiry time.Duration) *JWTService {
	return &JWTService{
		keys:         keys,
		accessExpiry: accessExpiry,
	}
}

// Keys exposes the key manager so the handler can serve the public JWKS.
func (s *JWTService) Keys() *KeyManager { return s.keys }

// AccessExpiry is the access-token lifetime. Used as the TTL for revocation
// entries — a blacklisted token can never outlive its own expiry.
func (s *JWTService) AccessExpiry() time.Duration { return s.accessExpiry }

// TokenProfile holds the extra identity fields embedded in every JWT.
type TokenProfile struct {
	FirstName     string
	LastName      string
	AccountActive bool
	// Sid is the active_session id to stamp into the token. Empty leaves the
	// claim out (e.g. flows that do not create a session row).
	Sid string
}

// MobileProfile extends TokenProfile with mobile-specific fields.
type MobileProfile struct {
	TokenProfile
	DeviceType        string
	DeviceID          string
	BiometricsEnabled bool
}

func (s *JWTService) sign(claims *Claims) (string, error) {
	key := s.keys.Current()
	if key == nil {
		return "", fmt.Errorf("no active signing key")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = key.Kid
	return token.SignedString(key.Private)
}

func (s *JWTService) GenerateAccessToken(principalID int64, email string, roles []string, permissions []string, principalType string, prof TokenProfile) (string, error) {
	claims := &Claims{
		PrincipalID:   principalID,
		Email:         email,
		Roles:         roles,
		Permissions:   permissions,
		PrincipalType: principalType,
		Sid:           prof.Sid,
		FirstName:     prof.FirstName,
		LastName:      prof.LastName,
		AccountActive: prof.AccountActive,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        generateJTI(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return s.sign(claims)
}

func (s *JWTService) GenerateMobileAccessToken(principalID int64, email string, roles []string, permissions []string, principalType string, mp MobileProfile) (string, error) {
	claims := &Claims{
		PrincipalID:       principalID,
		Email:             email,
		Roles:             roles,
		Permissions:       permissions,
		PrincipalType:     principalType,
		Sid:               mp.Sid,
		DeviceType:        mp.DeviceType,
		DeviceID:          mp.DeviceID,
		FirstName:         mp.FirstName,
		LastName:          mp.LastName,
		AccountActive:     mp.AccountActive,
		BiometricsEnabled: mp.BiometricsEnabled,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        generateJTI(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return s.sign(claims)
}

func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %w", ErrInvalidToken)
		}
		kid, _ := t.Header["kid"].(string)
		pub, ok := s.keys.PublicKeyByKid(kid)
		if !ok {
			return nil, fmt.Errorf("unknown key id: %w", ErrInvalidToken)
		}
		return pub, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", ErrInvalidToken)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid claims: %w", ErrInvalidToken)
	}
	return claims, nil
}

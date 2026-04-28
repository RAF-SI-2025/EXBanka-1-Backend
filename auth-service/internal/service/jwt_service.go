package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	DeviceType        string   `json:"device_type,omitempty"` // "mobile" for mobile app tokens, empty for browser
	DeviceID          string   `json:"device_id,omitempty"`   // UUID of registered mobile device
	FirstName         string   `json:"first_name,omitempty"`
	LastName          string   `json:"last_name,omitempty"`
	AccountActive     bool     `json:"account_active"`
	BiometricsEnabled bool     `json:"biometrics_enabled,omitempty"` // only for mobile tokens
	jwt.RegisteredClaims
}

type JWTService struct {
	secret       []byte
	accessExpiry time.Duration
}

func NewJWTService(secret string, accessExpiry time.Duration) *JWTService {
	return &JWTService{
		secret:       []byte(secret),
		accessExpiry: accessExpiry,
	}
}

// TokenProfile holds the extra identity fields embedded in every JWT.
type TokenProfile struct {
	FirstName     string
	LastName      string
	AccountActive bool
}

// MobileProfile extends TokenProfile with mobile-specific fields.
type MobileProfile struct {
	TokenProfile
	DeviceType        string
	DeviceID          string
	BiometricsEnabled bool
}

func (s *JWTService) GenerateAccessToken(principalID int64, email string, roles []string, permissions []string, principalType string, prof TokenProfile) (string, error) {
	claims := &Claims{
		PrincipalID:   principalID,
		Email:         email,
		Roles:         roles,
		Permissions:   permissions,
		PrincipalType: principalType,
		FirstName:     prof.FirstName,
		LastName:      prof.LastName,
		AccountActive: prof.AccountActive,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        generateJTI(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *JWTService) GenerateMobileAccessToken(principalID int64, email string, roles []string, permissions []string, principalType string, mp MobileProfile) (string, error) {
	claims := &Claims{
		PrincipalID:       principalID,
		Email:             email,
		Roles:             roles,
		Permissions:       permissions,
		PrincipalType:     principalType,
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
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

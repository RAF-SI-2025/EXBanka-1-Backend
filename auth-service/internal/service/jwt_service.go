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
	UserID      int64    `json:"user_id"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	SystemType  string   `json:"system_type"` // "employee" or "client"
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

func (s *JWTService) GenerateAccessToken(userID int64, email string, roles []string, permissions []string, systemType string) (string, error) {
	// Keep backward-compat: set Role to first role if available
	role := ""
	if len(roles) > 0 {
		role = roles[0]
	}
	claims := &Claims{
		UserID:      userID,
		Email:       email,
		Role:        role,
		Roles:       roles,
		Permissions: permissions,
		SystemType:  systemType,
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

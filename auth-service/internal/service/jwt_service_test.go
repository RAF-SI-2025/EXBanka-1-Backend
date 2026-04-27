package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := svc.GenerateAccessToken(1, "user@test.com", []string{"EmployeeBasic"}, []string{"clients.read.all"}, "employee", TokenProfile{AccountActive: true})
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), claims.UserID)
	assert.Equal(t, "user@test.com", claims.Email)
	assert.Equal(t, []string{"EmployeeBasic"}, claims.Roles)
	assert.Equal(t, []string{"clients.read.all"}, claims.Permissions)
	assert.Equal(t, "employee", claims.SystemType)
}

func TestValidateToken_Invalid(t *testing.T) {
	svc := NewJWTService("test-secret", 15*time.Minute)

	_, err := svc.ValidateToken("invalid.token.string")
	assert.Error(t, err)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	svc1 := NewJWTService("secret-one", 15*time.Minute)
	svc2 := NewJWTService("secret-two", 15*time.Minute)

	token, _ := svc1.GenerateAccessToken(1, "user@test.com", []string{"EmployeeBasic"}, nil, "employee", TokenProfile{AccountActive: true})
	_, err := svc2.ValidateToken(token)
	assert.Error(t, err)
}

func TestValidateToken_Expired(t *testing.T) {
	svc := NewJWTService("test-secret", -1*time.Second)

	token, err := svc.GenerateAccessToken(1, "user@test.com", []string{"EmployeeBasic"}, nil, "employee", TokenProfile{AccountActive: true})
	assert.NoError(t, err)

	_, err = svc.ValidateToken(token)
	assert.Error(t, err)
}

func TestGenerateAccessToken_ClientRole(t *testing.T) {
	svc := NewJWTService("test-secret", 15*time.Minute)

	token, err := svc.GenerateAccessToken(42, "client@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, int64(42), claims.UserID)
	assert.Equal(t, []string{"client"}, claims.Roles)
	assert.Equal(t, "client", claims.SystemType)
}

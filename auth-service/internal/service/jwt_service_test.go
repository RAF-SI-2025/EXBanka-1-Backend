package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := svc.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", []string{"clients.read"})
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), claims.UserID)
	assert.Equal(t, "user@test.com", claims.Email)
	assert.Equal(t, "EmployeeBasic", claims.Role)
	assert.Equal(t, []string{"clients.read"}, claims.Permissions)
}

func TestValidateToken_Invalid(t *testing.T) {
	svc := NewJWTService("test-secret", 15*time.Minute)

	_, err := svc.ValidateToken("invalid.token.string")
	assert.Error(t, err)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	svc1 := NewJWTService("secret-one", 15*time.Minute)
	svc2 := NewJWTService("secret-two", 15*time.Minute)

	token, _ := svc1.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", nil)
	_, err := svc2.ValidateToken(token)
	assert.Error(t, err)
}

func TestValidateToken_Expired(t *testing.T) {
	svc := NewJWTService("test-secret", -1*time.Second)

	token, err := svc.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", nil)
	assert.NoError(t, err)

	_, err = svc.ValidateToken(token)
	assert.Error(t, err)
}

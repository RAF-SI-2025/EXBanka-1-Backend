package service

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
)

func TestGenerateTOTPSecret(t *testing.T) {
	svc := NewTOTPService()
	secret, url, err := svc.GenerateSecret("user@test.com", "EXBanka")
	assert.NoError(t, err)
	assert.NotEmpty(t, secret)
	assert.Contains(t, url, "otpauth://totp/EXBanka:user")
	assert.Contains(t, url, "test.com")
	assert.Contains(t, url, "issuer=EXBanka")
}

func TestValidateTOTPCode_Valid(t *testing.T) {
	svc := NewTOTPService()
	secret, _, _ := svc.GenerateSecret("user@test.com", "EXBanka")

	code, err := totp.GenerateCode(secret, time.Now())
	assert.NoError(t, err)
	assert.True(t, svc.ValidateCode(secret, code))
}

func TestValidateTOTPCode_Invalid(t *testing.T) {
	svc := NewTOTPService()
	secret, _, _ := svc.GenerateSecret("user@test.com", "EXBanka")
	assert.False(t, svc.ValidateCode(secret, "000000"))
}

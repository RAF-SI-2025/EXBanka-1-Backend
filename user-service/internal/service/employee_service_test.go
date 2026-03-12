// user-service/internal/service/employee_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid password", "Abcdef12", false},
		{"valid complex", "MyP@ssw0rd99", false},
		{"too short", "Ab1234", true},
		{"too long", "Abcdefghijklmnopqrstuvwxyz1234567", true},
		{"no uppercase", "abcdef12", true},
		{"no lowercase", "ABCDEF12", true},
		{"only one digit", "Abcdefg1", true},
		{"no digits", "Abcdefgh", true},
		{"empty", "", true},
		{"exactly 8 chars valid", "Abcdef12", false},
		{"exactly 32 chars valid", "Abcdefghijklmnopqrstuvwxyz123456", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "TestPass12", hash)

	// Different calls produce different hashes (bcrypt salt)
	hash2, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, hash2)
}

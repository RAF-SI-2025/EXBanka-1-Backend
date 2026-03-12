package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePassword_AuthService(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid", "Abcdef12", false},
		{"too short", "Ab12", true},
		{"too long", "Abcdefghijklmnopqrstuvwxyz1234567", true},
		{"no uppercase", "abcdef12", true},
		{"no lowercase", "ABCDEF12", true},
		{"one digit only", "Abcdefg1", true},
		{"no digits", "Abcdefgh", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	assert.NoError(t, err)
	assert.Len(t, token1, 64) // 32 bytes hex encoded

	token2, err := generateToken()
	assert.NoError(t, err)
	assert.NotEqual(t, token1, token2) // unique each time
}

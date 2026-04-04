package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateJMBG(t *testing.T) {
	tests := []struct {
		name    string
		jmbg    string
		wantErr bool
		errMsg  string // expected substring in error message (empty = no check)
	}{
		{"valid 13 digits", "0101990710024", false, ""},
		{"empty", "", true, "13 digits"},
		{"too short", "12345", true, "13 digits"},
		{"too long", "12345678901234", true, "13 digits"},
		{"contains letters", "012345678901a", true, "only digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJMBG(tt.jmbg)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "error message should be specific")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
		errMsg  string // expected substring in error message (empty = no check)
	}{
		{"valid email", "test@example.com", false, ""},
		{"empty", "", true, "must not be empty"},
		{"no at sign", "testexample.com", true, "@"},
		{"no domain", "test@", true, "domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmail(tt.email)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "error message should be specific")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

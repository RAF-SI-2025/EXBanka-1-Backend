// user-service/internal/service/jmbg_validator_test.go
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
		errMsg  string
	}{
		{"valid 13 digits", "0101990710024", false, ""},
		{"empty string", "", true, "JMBG must be exactly 13 digits"},
		{"too short", "123456789012", true, "JMBG must be exactly 13 digits"},
		{"too long", "12345678901234", true, "JMBG must be exactly 13 digits"},
		{"contains letters", "012345678901a", true, "JMBG must contain only digits"},
		{"contains spaces", "0123456789 12", true, "JMBG must contain only digits"},
		{"contains special chars", "012345678901!", true, "JMBG must contain only digits"},
		{"all zeros", "0000000000000", false, ""},
		{"valid 13 digits alt", "1234567890123", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJMBG(tt.jmbg)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestValidateTransfer(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		amount  float64
		wantErr bool
	}{
		{"valid transfer", "ACC001", "ACC002", 100.0, false},
		{"same account", "ACC001", "ACC001", 100.0, true},
		{"zero amount", "ACC001", "ACC002", 0.0, true},
		{"negative amount", "ACC001", "ACC002", -50.0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransfer(tt.from, tt.to, decimal.NewFromFloat(tt.amount))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

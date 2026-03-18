package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestCalculateCommission(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		expected float64
	}{
		{"small amount", 100.0, 0.0},    // no commission under 1000
		{"large amount", 10000.0, 10.0}, // 0.1% commission
		{"exact threshold", 1000.0, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculatePaymentCommission(decimal.NewFromFloat(tt.amount))
			expected, _ := result.Float64()
			assert.InDelta(t, tt.expected, expected, 0.001)
		})
	}
}

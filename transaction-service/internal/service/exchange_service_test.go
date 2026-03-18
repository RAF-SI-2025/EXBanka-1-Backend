package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestConvertAmount(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		rate     decimal.Decimal
		expected float64
	}{
		{"standard conversion", decimal.NewFromFloat(100.0), decimal.NewFromFloat(117.0), 11700.0},
		{"reverse conversion", decimal.NewFromFloat(11700.0), decimal.NewFromFloat(1.0 / 117.0), 100.0},
		{"no conversion same rate", decimal.NewFromFloat(100.0), decimal.NewFromFloat(1.0), 100.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertAmount(tt.amount, tt.rate)
			f, _ := result.Float64()
			assert.InDelta(t, tt.expected, f, 0.01)
		})
	}
}

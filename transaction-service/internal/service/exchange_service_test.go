package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertAmount(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		rate     float64
		expected float64
	}{
		{"standard conversion", 100.0, 117.0, 11700.0},
		{"reverse conversion", 11700.0, 1.0 / 117.0, 100.0},
		{"no conversion same rate", 100.0, 1.0, 100.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertAmount(tt.amount, tt.rate)
			assert.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

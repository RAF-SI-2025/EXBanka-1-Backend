package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestWriteSecurityPricePoint_NilClient_NoPanic(t *testing.T) {
	// Must not panic when influxClient is nil
	assert.NotPanics(t, func() {
		writeSecurityPricePoint(
			nil, // nil client
			1, "stock", "AAPL", "NASDAQ",
			decimal.NewFromFloat(165.0),
			decimal.NewFromFloat(167.5),
			decimal.NewFromFloat(163.2),
			decimal.NewFromFloat(-2.3),
			50000,
			time.Now(),
		)
	})
}

func TestPriceToFloat(t *testing.T) {
	tests := []struct {
		input    decimal.Decimal
		expected float64
	}{
		{decimal.NewFromFloat(165.1234), 165.1234},
		{decimal.Zero, 0},
		{decimal.NewFromFloat(-2.5), -2.5},
	}
	for _, tc := range tests {
		got := priceToFloat(tc.input)
		assert.InDelta(t, tc.expected, got, 0.0001)
	}
}

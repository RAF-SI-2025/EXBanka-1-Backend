package shared

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestParseAmount_Valid(t *testing.T) {
	d, err := ParseAmount("1234.56")
	assert.NoError(t, err)
	assert.Equal(t, "1234.5600", FormatAmount(d))
}

func TestParseAmount_Empty(t *testing.T) {
	d, err := ParseAmount("")
	assert.NoError(t, err)
	assert.True(t, d.IsZero())
}

func TestParseAmount_Invalid(t *testing.T) {
	_, err := ParseAmount("abc")
	assert.Error(t, err)
}

func TestFormatAmount(t *testing.T) {
	d := decimal.NewFromFloat(1234.5)
	assert.Equal(t, "1234.5000", FormatAmount(d))
}

func TestAmountIsPositive(t *testing.T) {
	assert.True(t, AmountIsPositive(decimal.NewFromInt(1)))
	assert.False(t, AmountIsPositive(decimal.NewFromInt(0)))
	assert.False(t, AmountIsPositive(decimal.NewFromInt(-1)))
}

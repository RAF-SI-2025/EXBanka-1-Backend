// api-gateway/internal/handler/validation_test.go
package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// oneOf
// ---------------------------------------------------------------------------

func TestOneOf_NormalizesAndAcceptsValid(t *testing.T) {
	got, err := oneOf("account_kind", "CURRENT", "current", "foreign")
	require.NoError(t, err)
	assert.Equal(t, "current", got, "oneOf should normalize value to lowercase")
}

func TestOneOf_AcceptsLowercaseValid(t *testing.T) {
	got, err := oneOf("card_brand", "visa", "visa", "mastercard", "dinacard", "amex")
	require.NoError(t, err)
	assert.Equal(t, "visa", got)
}

func TestOneOf_TrimsWhitespace(t *testing.T) {
	got, err := oneOf("interest_type", "  fixed  ", "fixed", "variable")
	require.NoError(t, err)
	assert.Equal(t, "fixed", got, "oneOf should trim surrounding whitespace")
}

func TestOneOf_RejectsInvalidValue(t *testing.T) {
	_, err := oneOf("account_kind", "savings", "current", "foreign")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_kind")
	assert.Contains(t, err.Error(), "current, foreign")
}

func TestOneOf_RejectsEmptyValue(t *testing.T) {
	_, err := oneOf("loan_type", "", "cash", "housing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loan_type")
}

// ---------------------------------------------------------------------------
// positive
// ---------------------------------------------------------------------------

func TestPositive_AcceptsPositiveValue(t *testing.T) {
	err := positive("amount", 0.01)
	assert.NoError(t, err)
}

func TestPositive_RejectsZero(t *testing.T) {
	err := positive("amount", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount")
	assert.Contains(t, err.Error(), "positive")
}

func TestPositive_RejectsNegativeValue(t *testing.T) {
	err := positive("amount", -5.5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount")
}

// ---------------------------------------------------------------------------
// nonNegative
// ---------------------------------------------------------------------------

func TestNonNegative_AcceptsZero(t *testing.T) {
	err := nonNegative("limit", 0)
	assert.NoError(t, err, "zero should be allowed by nonNegative")
}

func TestNonNegative_AcceptsPositiveValue(t *testing.T) {
	err := nonNegative("limit", 100.5)
	assert.NoError(t, err)
}

func TestNonNegative_RejectsNegativeValue(t *testing.T) {
	err := nonNegative("limit", -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

// ---------------------------------------------------------------------------
// validatePin
// ---------------------------------------------------------------------------

func TestValidatePin_AcceptsExactlyFourDigits(t *testing.T) {
	err := validatePin("1234")
	assert.NoError(t, err)
}

func TestValidatePin_AcceptsLeadingZeros(t *testing.T) {
	err := validatePin("0000")
	assert.NoError(t, err)
}

func TestValidatePin_RejectsTooShort(t *testing.T) {
	err := validatePin("123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4 digits")
}

func TestValidatePin_RejectsTooLong(t *testing.T) {
	err := validatePin("12345")
	require.Error(t, err)
}

func TestValidatePin_RejectsNonDigits(t *testing.T) {
	err := validatePin("12ab")
	require.Error(t, err)
}

func TestValidatePin_RejectsEmpty(t *testing.T) {
	err := validatePin("")
	require.Error(t, err)
}

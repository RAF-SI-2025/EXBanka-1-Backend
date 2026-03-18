package shared

import "github.com/shopspring/decimal"

// ParseAmount converts a string amount to decimal.Decimal.
// Returns zero for empty strings. Used at proto ↔ service boundaries.
func ParseAmount(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// FormatAmount converts a decimal.Decimal to a 4-decimal-place string
// for proto serialization.
func FormatAmount(d decimal.Decimal) string {
	return d.StringFixed(4)
}

// FormatAmountDisplay converts to 2-decimal string for user-facing display.
func FormatAmountDisplay(d decimal.Decimal) string {
	return d.StringFixed(2)
}

// AmountIsPositive returns true if d > 0.
func AmountIsPositive(d decimal.Decimal) bool {
	return d.IsPositive()
}

package provider

import "github.com/shopspring/decimal"

// SupportedCurrencies lists all currencies the exchange service tracks.
// RSD is the base (pivot) currency — not listed here since all rates are relative to it.
var SupportedCurrencies = []string{"EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

// RateProvider fetches mid-market exchange rates from an external source.
// Key: currency code (e.g. "EUR").
// Value: mid-market rate — how many units of that currency equal 1 RSD.
// Example: {"EUR": 0.00851} means 1 RSD ≈ 0.00851 EUR (i.e. 1 EUR ≈ 117.5 RSD).
type RateProvider interface {
	FetchRatesFromRSD() (map[string]decimal.Decimal, error)
}

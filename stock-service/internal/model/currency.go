package model

// SupportedCurrencies is the exact set of ISO codes that exchange-service can
// convert between. Any security or exchange referencing a code outside this set
// will fail at runtime inside exchange.Convert with InvalidArgument. Keeping
// this list in the model package makes it importable from source/, service/,
// and repository/ without creating a dependency cycle.
var SupportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

var supportedCurrencySet = func() map[string]bool {
	m := make(map[string]bool, len(SupportedCurrencies))
	for _, c := range SupportedCurrencies {
		m[c] = true
	}
	return m
}()

// IsSupportedCurrency reports whether the code is in the 8-currency set.
func IsSupportedCurrency(code string) bool {
	return supportedCurrencySet[code]
}

// NormalizeCurrency coerces any unsupported code (empty string, unknown ISO
// codes, or data-provider artefacts) to "USD". The banking stack can always
// handle USD, so this keeps buy/sell paths from failing while still preserving
// the security's listing for display.
func NormalizeCurrency(code string) string {
	if supportedCurrencySet[code] {
		return code
	}
	return "USD"
}

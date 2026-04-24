package model

import "testing"

func TestNormalizeCurrency_KnownCodes(t *testing.T) {
	for _, c := range SupportedCurrencies {
		if got := NormalizeCurrency(c); got != c {
			t.Errorf("NormalizeCurrency(%q) = %q, want %q", c, got, c)
		}
	}
}

func TestNormalizeCurrency_UnknownFallsBackToUSD(t *testing.T) {
	for _, bad := range []string{"", "PLN", "HKD", "CNY", "KRW", "BRL", "PAl", "xyz"} {
		if got := NormalizeCurrency(bad); got != "USD" {
			t.Errorf("NormalizeCurrency(%q) = %q, want USD", bad, got)
		}
	}
}

func TestIsSupportedCurrency(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"USD", true}, {"RSD", true}, {"EUR", true}, {"AUD", true},
		{"", false}, {"PLN", false}, {"usd", false}, {"JPY ", false},
	}
	for _, tc := range cases {
		if got := IsSupportedCurrency(tc.code); got != tc.want {
			t.Errorf("IsSupportedCurrency(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestStockExchange_BeforeSave_NormalizesCurrency(t *testing.T) {
	ex := &StockExchange{Currency: "PLN"}
	if err := ex.BeforeSave(nil); err != nil {
		t.Fatalf("BeforeSave returned error: %v", err)
	}
	if ex.Currency != "USD" {
		t.Errorf("expected currency coerced to USD, got %q", ex.Currency)
	}
}

func TestStockExchange_BeforeSave_KeepsSupported(t *testing.T) {
	ex := &StockExchange{Currency: "EUR"}
	if err := ex.BeforeSave(nil); err != nil {
		t.Fatalf("BeforeSave returned error: %v", err)
	}
	if ex.Currency != "EUR" {
		t.Errorf("expected currency preserved as EUR, got %q", ex.Currency)
	}
}

func TestForexPair_BeforeSave_RejectsUnsupportedBase(t *testing.T) {
	fp := &ForexPair{Ticker: "PLN/USD", BaseCurrency: "PLN", QuoteCurrency: "USD"}
	if err := fp.BeforeSave(nil); err == nil {
		t.Fatal("expected error for unsupported base currency, got nil")
	}
}

func TestForexPair_BeforeSave_RejectsUnsupportedQuote(t *testing.T) {
	fp := &ForexPair{Ticker: "USD/CNY", BaseCurrency: "USD", QuoteCurrency: "CNY"}
	if err := fp.BeforeSave(nil); err == nil {
		t.Fatal("expected error for unsupported quote currency, got nil")
	}
}

func TestForexPair_BeforeSave_RejectsSameBaseQuote(t *testing.T) {
	fp := &ForexPair{Ticker: "USD/USD", BaseCurrency: "USD", QuoteCurrency: "USD"}
	if err := fp.BeforeSave(nil); err == nil {
		t.Fatal("expected error for same base and quote currency, got nil")
	}
}

func TestForexPair_BeforeSave_AllowsSupportedPair(t *testing.T) {
	fp := &ForexPair{Ticker: "EUR/USD", BaseCurrency: "EUR", QuoteCurrency: "USD"}
	if err := fp.BeforeSave(nil); err != nil {
		t.Errorf("expected BeforeSave to pass, got %v", err)
	}
}

package source_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/source"
)

func TestGeneratedSource_Name(t *testing.T) {
	s := source.NewGeneratedSource()
	require.Equal(t, "generated", s.Name())
}

func TestGeneratedSource_FetchExchanges_Returns20(t *testing.T) {
	s := source.NewGeneratedSource()
	ex, err := s.FetchExchanges(context.Background())
	require.NoError(t, err)
	require.Len(t, ex, 20)
	for _, e := range ex {
		require.NotEmpty(t, e.Acronym)
		require.NotEmpty(t, e.MICCode)
		require.NotEmpty(t, e.Name)
	}
}

// TestGeneratedSource_FetchExchanges_CurrenciesAreSupported is the regression
// test for Bug A: any exchange currency that is not in exchange-service's
// supported-currency set (RSD/EUR/CHF/USD/GBP/JPY/CAD/AUD) would cause a
// cross-currency buy order to fail at exchange.Convert with "XXX is not a
// supported currency". The normalization layer must coerce HKD, CNY, INR,
// ZAR, MXN, BRL, KRW, SEK, PLN, COP, RUB to USD before the exchange rows
// hit the DB.
func TestGeneratedSource_FetchExchanges_CurrenciesAreSupported(t *testing.T) {
	s := source.NewGeneratedSource()
	ex, err := s.FetchExchanges(context.Background())
	require.NoError(t, err)
	supported := map[string]bool{
		"RSD": true, "EUR": true, "CHF": true, "USD": true,
		"GBP": true, "JPY": true, "CAD": true, "AUD": true,
	}
	for _, e := range ex {
		require.True(t, supported[e.Currency],
			"exchange %s (%s) has unsupported currency %q; must be normalized",
			e.Acronym, e.MICCode, e.Currency)
	}
}

func TestNormalizeExchangeCurrency(t *testing.T) {
	// Supported codes pass through unchanged.
	for _, c := range []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"} {
		require.Equal(t, c, source.NormalizeExchangeCurrency(c))
	}
	// Unsupported ISO codes and empty/garbage values collapse to USD.
	for _, c := range []string{"", "CNY", "HKD", "INR", "ZAR", "MXN", "BRL", "KRW", "SEK", "PLN", "COP", "RUB", "PAl", "GAE"} {
		require.Equal(t, "USD", source.NormalizeExchangeCurrency(c),
			"unsupported code %q must collapse to USD", c)
	}
}

func TestGeneratedSource_FetchStocks_Returns20WithNonZeroPrice(t *testing.T) {
	s := source.NewGeneratedSource()
	stocks, err := s.FetchStocks(context.Background())
	require.NoError(t, err)
	require.Len(t, stocks, 20)
	for _, sw := range stocks {
		require.False(t, sw.Price.IsZero(), "stock %s has zero price", sw.Stock.Ticker)
		require.NotZero(t, sw.ExchangeID, "stock %s has zero exchange id", sw.Stock.Ticker)
		require.NotEmpty(t, sw.Stock.Name)
	}
}

func TestGeneratedSource_FetchFutures_Returns20(t *testing.T) {
	s := source.NewGeneratedSource()
	futures, err := s.FetchFutures(context.Background())
	require.NoError(t, err)
	require.Len(t, futures, 20)
	for _, fw := range futures {
		require.False(t, fw.Price.IsZero(), "future %s has zero price", fw.Futures.Ticker)
		require.NotZero(t, fw.ExchangeID)
	}
}

func TestGeneratedSource_FetchForex_Returns56(t *testing.T) {
	s := source.NewGeneratedSource()
	fx, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	require.Len(t, fx, 56, "expected 8*7=56 forex pairs")
	for _, fxp := range fx {
		require.False(t, fxp.Price.IsZero(), "forex pair %s has zero rate", fxp.Forex.Ticker)
	}
}

func TestGeneratedSource_IsDeterministic(t *testing.T) {
	a := source.NewGeneratedSource()
	b := source.NewGeneratedSource()
	as, _ := a.FetchStocks(context.Background())
	bs, _ := b.FetchStocks(context.Background())
	require.Equal(t, len(as), len(bs))
	for i := range as {
		require.Equal(t, as[i].Stock.Ticker, bs[i].Stock.Ticker)
		require.True(t, as[i].Price.Equal(bs[i].Price), "price drift on %s: a=%s b=%s", as[i].Stock.Ticker, as[i].Price.String(), bs[i].Price.String())
	}
}

func TestGeneratedSource_RefreshPrices_MovesButStaysInRange(t *testing.T) {
	s := source.NewGeneratedSource()
	stocksBefore, _ := s.FetchStocks(context.Background())
	before := stocksBefore[0].Price

	require.NoError(t, s.RefreshPrices(context.Background()))

	stocksAfter, _ := s.FetchStocks(context.Background())
	after := stocksAfter[0].Price

	// Within ±1% in a single refresh (generator uses ±0.5% walk).
	diff := after.Sub(before).Abs()
	limit := before.Abs().Mul(source.DecFromFloat(0.01))
	require.True(t, diff.LessThanOrEqual(limit),
		"price moved too far: before=%s after=%s diff=%s limit=%s",
		before.String(), after.String(), diff.String(), limit.String())
}

func TestGeneratedSource_FetchOptions_NonEmptyForNonZeroStock(t *testing.T) {
	s := source.NewGeneratedSource()
	stocks, _ := s.FetchStocks(context.Background())
	require.NotEmpty(t, stocks)
	opts, err := s.FetchOptions(context.Background(), &stocks[0].Stock)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

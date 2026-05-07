package source_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/source"
)

// ---------------------------------------------------------------------------
// SimulatorSource — futures / forex / refresh paths
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchFutures_PicksLowestExchangeID(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                      `{"data":{"id":1}}`,
		"/api/market/futures?per_page=200":   `{"data":[{"id":1,"ticker":"CLJ26","name":"Crude Oil April 2026","contract_size":1000,"settlement_date":"2026-04-30T00:00:00Z"}]}`,
		"/api/market/futures/CLJ26/listings": `{"data":[{"id":21,"exchange_id":7,"price":"82.50"},{"id":22,"exchange_id":3,"price":"82.55"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	got, err := s.FetchFutures(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "CLJ26", got[0].Futures.Ticker)
	require.Equal(t, uint64(3), got[0].ExchangeID, "expected lowest exchange_id (3)")
	require.Equal(t, "82.55", got[0].Price.String())
	require.Equal(t, int64(1000), got[0].Futures.ContractSize)
}

func TestSimulatorSource_FetchFutures_DefaultsContractSize(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                      `{"data":{"id":1}}`,
		"/api/market/futures?per_page=200":   `{"data":[{"id":1,"ticker":"GCJ26","name":"Gold","contract_size":0,"settlement_date":"2026-04-30T00:00:00Z"}]}`,
		"/api/market/futures/GCJ26/listings": `{"data":[{"id":21,"exchange_id":7,"price":"2100.00"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	got, err := s.FetchFutures(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].Futures.ContractSize, "zero contract_size must default to 1")
}

func TestSimulatorSource_FetchFutures_SkipsWhenListingsEmpty(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                    `{"data":{"id":1}}`,
		"/api/market/futures?per_page=200": `{"data":[{"id":1,"ticker":"NQX","name":"NQX","contract_size":50,"settlement_date":"2026-04-30T00:00:00Z"}]}`,
		"/api/market/futures/NQX/listings": `{"data":[]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	got, err := s.FetchFutures(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestSimulatorSource_FetchForex_PicksLowestExchangeID(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                      `{"data":{"id":1}}`,
		"/api/market/forex?per_page=200":     `{"data":[{"id":1,"ticker":"EUR/USD","base_currency":"EUR","quote_currency":"USD","rate":"1.085"}]}`,
		"/api/market/forex/EUR/USD/listings": `{"data":[{"id":31,"exchange_id":9,"price":"1.0852"},{"id":32,"exchange_id":5,"price":"1.0851"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	got, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "EUR/USD", got[0].Forex.Ticker)
	require.Equal(t, uint64(5), got[0].ExchangeID)
	require.Equal(t, "1.0851", got[0].Price.String())
	require.Equal(t, "1.085", got[0].Forex.ExchangeRate.String())
}

func TestSimulatorSource_FetchForex_SkipsWhenListingsEmpty(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                      `{"data":{"id":1}}`,
		"/api/market/forex?per_page=200":     `{"data":[{"id":1,"ticker":"USD/JPY","base_currency":"USD","quote_currency":"JPY","rate":"152.0"}]}`,
		"/api/market/forex/USD/JPY/listings": `{"data":[]}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	got, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestSimulatorSource_RefreshPrices_NoOp(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	require.NoError(t, s.RefreshPrices(context.Background()))
}

// ---------------------------------------------------------------------------
// SimulatorSource — error paths
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchExchanges_ServerError(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
		// /api/market/exchanges?per_page=200 not configured → 404
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	_, err := s.FetchExchanges(context.Background())
	require.Error(t, err)
}

func TestSimulatorSource_FetchStocks_ServerError(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	_, err := s.FetchStocks(context.Background())
	require.Error(t, err)
}

func TestSimulatorSource_FetchFutures_ServerError(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	_, err := s.FetchFutures(context.Background())
	require.Error(t, err)
}

func TestSimulatorSource_FetchForex_ServerError(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	_, err := s.FetchForex(context.Background())
	require.Error(t, err)
}

func TestSimulatorSource_FetchOptions_NoCachedTicker(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	// FetchStocks not called → ticker not cached → returns nil with no error
	got, err := s.FetchOptionsByTicker(context.Background(), "AAPL")
	require.NoError(t, err)
	require.Nil(t, got)
}

// ---------------------------------------------------------------------------
// ExternalSource — non-network branches
// ---------------------------------------------------------------------------

func TestExternalSource_FetchStocks_NoResolverReturnsNil(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	got, err := s.FetchStocks(context.Background())
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestExternalSource_FetchStocks_NoAVUsesDefaults(t *testing.T) {
	// Resolver returns 0 for unknown acronyms (mimicking exchange-not-seeded).
	resolver := func(acronym string) (uint64, error) {
		switch acronym {
		case "NYSE":
			return 1, nil
		case "NASDAQ":
			return 2, nil
		}
		return 0, nil
	}
	s := source.NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchStocks(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, got, "default stocks should be returned when no AV API key")
	// Confirm at least one default ticker survived.
	tickers := map[string]bool{}
	for _, sw := range got {
		tickers[sw.Stock.Ticker] = true
	}
	require.True(t, tickers["AAPL"] || tickers["TSLA"], "expected default tickers")
}

func TestExternalSource_FetchFutures_FromJSON(t *testing.T) {
	// Use the real seed JSON the service ships.
	resolver := func(acronym string) (uint64, error) { return 1, nil }
	s := source.NewExternalSource(nil, nil, nil, nil, "", "../../data/futures_seed.json").WithExchangeResolver(resolver)
	got, err := s.FetchFutures(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, got, "JSON seed should yield futures")
}

func TestExternalSource_FetchFutures_BadPath(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "/nonexistent.json")
	_, err := s.FetchFutures(context.Background())
	require.Error(t, err)
}

func TestExternalSource_FetchForex_HardcodedFallback(t *testing.T) {
	resolver := func(acronym string) (uint64, error) {
		if acronym == "FOREX" {
			return 99, nil
		}
		return 0, nil
	}
	s := source.NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	// 8 currencies × 7 = 56 pairs.
	require.Len(t, got, 56)
	for _, fp := range got {
		require.Equal(t, uint64(99), fp.ExchangeID)
		require.NotEmpty(t, fp.Forex.BaseCurrency)
		require.NotEmpty(t, fp.Forex.QuoteCurrency)
		require.NotEqual(t, fp.Forex.BaseCurrency, fp.Forex.QuoteCurrency)
	}
}

func TestExternalSource_FetchForex_NoForexExchange(t *testing.T) {
	// Resolver returns error for everything → FetchForex returns empty.
	resolver := func(acronym string) (uint64, error) {
		return 0, http.ErrNoLocation
	}
	s := source.NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	require.Nil(t, got)
}

// ---------------------------------------------------------------------------
// Simulator HTTP error path: getJSON 5xx
// ---------------------------------------------------------------------------

func TestSimulatorSource_GetJSON_5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/banks/me":
			_, _ = w.Write([]byte(`{"data":{"id":1}}`))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	s := newClientAndSource(t, server)
	_, err := s.FetchStocks(context.Background())
	require.Error(t, err)
}

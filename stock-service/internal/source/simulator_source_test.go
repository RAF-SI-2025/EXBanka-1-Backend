package source_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/source"
)

// newSimulatorTestServer builds a mock Market-Simulator with routes configured
// via a lookup map.
func newSimulatorTestServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip query string for matching.
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "unexpected path: "+key, http.StatusNotFound)
	}))
}

func newClientAndSource(t *testing.T, server *httptest.Server) *source.SimulatorSource {
	t.Helper()
	store := newFakeSettingStore()
	store.data["market_simulator_api_key"] = "ms_test"
	client := source.NewSimulatorClient(server.URL, "ExBanka", store)
	require.NoError(t, client.EnsureRegistered())
	return source.NewSimulatorSource(client)
}

func TestSimulatorSource_Name(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	require.Equal(t, "simulator", s.Name())
}

func TestSimulatorSource_FetchStocks_PicksLowestExchangeID(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                    `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200":  `{"data":[{"id":1,"ticker":"AAPL","name":"Apple"}],"pagination":{"page":1,"per_page":20,"total":1}}`,
		"/api/market/stocks/AAPL/listings": `{"data":[{"id":10,"exchange_id":5,"price":"180.00"},{"id":11,"exchange_id":2,"price":"179.95"},{"id":12,"exchange_id":9,"price":"180.10"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	stocks, err := s.FetchStocks(context.Background())
	require.NoError(t, err)
	require.Len(t, stocks, 1)
	require.Equal(t, "AAPL", stocks[0].Stock.Ticker)
	require.Equal(t, uint64(2), stocks[0].ExchangeID, "expected lowest exchange_id (2)")
	require.Equal(t, "179.95", stocks[0].Price.String())
}

func TestSimulatorSource_FetchExchanges(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                       `{"data":{"id":1}}`,
		"/api/market/exchanges?per_page=200":  `{"data":[{"id":1,"acronym":"NYSE","mic_code":"XNYS","name":"New York Stock Exchange"},{"id":2,"acronym":"NASDAQ","mic_code":"XNAS","name":"Nasdaq"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	exchanges, err := s.FetchExchanges(context.Background())
	require.NoError(t, err)
	require.Len(t, exchanges, 2)
	require.Equal(t, "NYSE", exchanges[0].Acronym)
	require.Equal(t, "XNYS", exchanges[0].MICCode)
}

func TestSimulatorSource_FetchOptions_UsesSimulatorStockID(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                                            `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200":                         `{"data":[{"id":42,"ticker":"AAPL","name":"Apple"}],"pagination":{"page":1,"per_page":20,"total":1}}`,
		"/api/market/stocks/AAPL/listings":                        `{"data":[{"id":10,"exchange_id":2,"price":"180.00"}]}`,
		"/api/market/options?stock_id=42&per_page=200":            `{"data":[{"id":1,"ticker":"AAPL260116C00200000","name":"AAPL Call","stock_id":42,"option_type":"call","strike_price":"200.00","implied_volatility":"0.32","premium":"5.75","settlement_date":"2026-01-16T00:00:00Z"}]}`,
	})
	defer server.Close()

	s := newClientAndSource(t, server)
	// Fetch stocks first so SimulatorSource caches the simulator's stock_id mapping.
	_, err := s.FetchStocks(context.Background())
	require.NoError(t, err)

	// Now fetch options for a local Stock with the same ticker.
	opts, err := s.FetchOptionsByTicker(context.Background(), "AAPL")
	require.NoError(t, err)
	require.Len(t, opts, 1)
	require.Equal(t, "AAPL260116C00200000", opts[0].Ticker)
	require.Equal(t, "call", opts[0].OptionType)
}

package source_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
)

// TestSimulatorSource_FetchOptions_NoCacheReturnsNil covers the early-return
// branch when FetchStocks hasn't been called.
func TestSimulatorSource_FetchOptions_NoCacheReturnsNil(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	got, err := s.FetchOptions(context.Background(), &model.Stock{Ticker: "MISSING"})
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestSimulatorSource_FetchOptionsByTicker_NoCacheReturnsNil mirrors the above
// for the by-ticker variant.
func TestSimulatorSource_FetchOptionsByTicker_NoCacheReturnsNil(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me": `{"data":{"id":1}}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	got, err := s.FetchOptionsByTicker(context.Background(), "MISSING")
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestSimulatorSource_FetchOptions_AfterFetchStocks populates the ticker→id
// cache via FetchStocks then exercises FetchOptions.
func TestSimulatorSource_FetchOptions_AfterFetchStocks(t *testing.T) {
	server := newSimulatorTestServer(t, map[string]string{
		"/api/banks/me":                                `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200":              `{"data":[{"id":42,"ticker":"AAPL","name":"Apple"}]}`,
		"/api/market/stocks/AAPL/listings":             `{"data":[{"id":10,"exchange_id":2,"price":"150.00"}]}`,
		"/api/market/options?stock_id=42&per_page=200": `{"data":[]}`,
	})
	defer server.Close()
	s := newClientAndSource(t, server)
	_, err := s.FetchStocks(context.Background())
	require.NoError(t, err)
	got, err := s.FetchOptions(context.Background(), &model.Stock{Ticker: "AAPL"})
	require.NoError(t, err)
	require.NotNil(t, got)
}

package source_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/source"
)

func TestExternalSource_Name(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	require.Equal(t, "external", s.Name())
}

func TestExternalSource_FetchExchanges_FromCSV(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "../../data/exchanges.csv", "")
	got, err := s.FetchExchanges(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, got, "CSV should yield at least one exchange")
}

func TestExternalSource_FetchOptions_NonZeroPrice(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	stock := &model.Stock{
		ID:     1,
		Ticker: "AAPL",
		Name:   "Apple Inc.",
		Price:  decimal.NewFromFloat(165.00),
	}
	opts, err := s.FetchOptions(context.Background(), stock)
	require.NoError(t, err)
	require.NotEmpty(t, opts, "should generate options for a non-zero priced stock")
}

func TestExternalSource_FetchOptions_ZeroPrice(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	stock := &model.Stock{
		ID:     2,
		Ticker: "ZERO",
		Name:   "Zero Price Stock",
		Price:  decimal.Zero,
	}
	opts, err := s.FetchOptions(context.Background(), stock)
	require.NoError(t, err)
	require.Nil(t, opts, "zero-price stock should produce no options")
}

func TestExternalSource_FetchForex_NoResolver(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	result, err := s.FetchForex(context.Background())
	require.NoError(t, err)
	require.Nil(t, result, "no exchange resolver should return nil")
}

func TestExternalSource_RefreshPrices(t *testing.T) {
	s := source.NewExternalSource(nil, nil, nil, nil, "", "")
	err := s.RefreshPrices(context.Background())
	require.NoError(t, err)
}

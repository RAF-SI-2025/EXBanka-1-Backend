package source

import (
	"context"
	"testing"
)

// TestExternalSource_GetStockTickers_NoAlpacaUsesDefaults exercises the
// no-Alpaca branch (returns provider.DefaultStockTickers).
func TestExternalSource_GetStockTickers_NoAlpacaUsesDefaults(t *testing.T) {
	s := NewExternalSource(nil, nil, nil, nil, "", "")
	got := s.getStockTickers()
	if len(got) == 0 {
		t.Errorf("expected default tickers, got empty")
	}
}

// TestExternalSource_FetchStocks_NoResolverReturnsNil covers the early-return
// branch when exchangeByAcronym is nil.
func TestExternalSource_FetchStocks_NoResolverReturnsNil(t *testing.T) {
	s := NewExternalSource(nil, nil, nil, nil, "", "")
	got, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// fetchDefaultStocks returns nil when exchangeByAcronym is nil.
	if got != nil {
		t.Errorf("expected nil, got %d", len(got))
	}
}

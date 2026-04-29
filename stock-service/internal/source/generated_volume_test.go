package source

import (
	"context"
	"testing"
)

func TestGeneratedSource_Volume_Populated_AllTypes(t *testing.T) {
	g := NewGeneratedSource()
	ctx := context.Background()

	stocks, err := g.FetchStocks(ctx)
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(stocks) == 0 {
		t.Fatal("expected at least one stock")
	}
	for _, s := range stocks {
		if s.Stock.Volume <= 0 {
			t.Errorf("stock %s: Stock.Volume = %d, want > 0", s.Stock.Ticker, s.Stock.Volume)
		}
		if s.Volume <= 0 {
			t.Errorf("stock %s: wrapper.Volume = %d, want > 0", s.Stock.Ticker, s.Volume)
		}
		if s.Stock.Volume < 100_000 || s.Stock.Volume > 50_000_000 {
			t.Errorf("stock %s: Volume %d outside range 100k-50M", s.Stock.Ticker, s.Stock.Volume)
		}
	}

	futures, err := g.FetchFutures(ctx)
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	for _, f := range futures {
		if f.Futures.Volume <= 0 {
			t.Errorf("futures %s: Volume = %d, want > 0", f.Futures.Ticker, f.Futures.Volume)
		}
		if f.Futures.Volume < 1_000 || f.Futures.Volume > 500_000 {
			t.Errorf("futures %s: Volume %d outside range 1k-500k", f.Futures.Ticker, f.Futures.Volume)
		}
	}

	forex, err := g.FetchForex(ctx)
	if err != nil {
		t.Fatalf("FetchForex: %v", err)
	}
	for _, fp := range forex {
		if fp.Forex.Volume <= 0 {
			t.Errorf("forex %s: Volume = %d, want > 0", fp.Forex.Ticker, fp.Forex.Volume)
		}
		if fp.Forex.Volume < 100_000_000 || fp.Forex.Volume > 10_000_000_000 {
			t.Errorf("forex %s: Volume %d outside range 100M-10B", fp.Forex.Ticker, fp.Forex.Volume)
		}
	}
}

func TestGeneratedSource_Volume_Deterministic(t *testing.T) {
	g1 := NewGeneratedSource()
	g2 := NewGeneratedSource()
	ctx := context.Background()

	s1, _ := g1.FetchStocks(ctx)
	s2, _ := g2.FetchStocks(ctx)
	if len(s1) != len(s2) {
		t.Fatalf("different lengths: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i].Stock.Volume != s2[i].Stock.Volume {
			t.Errorf("stock %s: volume differs between instances: %d vs %d",
				s1[i].Stock.Ticker, s1[i].Stock.Volume, s2[i].Stock.Volume)
		}
	}
}

package source

import (
	"errors"
	"testing"
)

// TestAcronymForTicker covers acronym lookup, indirectly via the table mapping.
func TestAcronymForTicker_NonEmpty(t *testing.T) {
	if got := acronymForTicker("AAPL"); got == "" {
		t.Errorf("expected non-empty acronym")
	}
}

// TestGeneratedSource_WithExchangeResolver covers the wiring helper.
func TestGeneratedSource_WithExchangeResolver(t *testing.T) {
	g := NewGeneratedSource()
	called := false
	g2 := g.WithExchangeResolver(func(_ string) (uint64, error) {
		called = true
		return 99, nil
	})
	if g2.exchangeByAcronym == nil {
		t.Fatal("resolver not wired")
	}
	got := g2.resolveExchangeID("AAPL")
	if got != 99 {
		t.Errorf("got %d", got)
	}
	if !called {
		t.Error("resolver not invoked")
	}
}

// TestResolveExchangeID_FallbackOnError exercises the err-fallback branch.
func TestResolveExchangeID_FallbackOnError(t *testing.T) {
	g := NewGeneratedSource().WithExchangeResolver(func(_ string) (uint64, error) {
		return 0, errors.New("not found")
	})
	got := g.resolveExchangeID("AAPL")
	// Falls back to positional hash, which is non-zero.
	if got == 0 {
		t.Error("expected non-zero fallback")
	}
}

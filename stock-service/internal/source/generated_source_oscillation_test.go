package source

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fixedClock returns a func() time.Time that always reports t. Used to drive
// the oscillation phase deterministically.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// timeAtPhase returns a UTC time whose unix-minute index modulo 4 equals
// phase. The base instant is 2026-01-01T00:00:00Z whose unix-minute is
// divisible by 4, so adding `phase` minutes yields the requested phase.
func timeAtPhase(phase int) time.Time {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Snap to a multiple-of-4 minute boundary, then add `phase` minutes.
	offset := int64(phase) - (base.Unix()/60)%4
	return base.Add(time.Duration(offset) * time.Minute)
}

// snapshotStockPrices returns a map ticker->price for the current FetchStocks
// output.
func snapshotStockPrices(t *testing.T, g *GeneratedSource) map[string]decimal.Decimal {
	t.Helper()
	out := make(map[string]decimal.Decimal)
	stocks, err := g.FetchStocks(context.Background())
	require.NoError(t, err)
	for _, sw := range stocks {
		out[sw.Stock.Ticker] = sw.Price
	}
	return out
}

func TestGeneratedSource_Oscillation_AppliesMultiplierForEachPhase(t *testing.T) {
	// Reference instance pinned to phase 1 (multiplier 1.0) so its prices
	// equal the seed base prices.
	base := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	basePx := snapshotStockPrices(t, base)

	for phase, mult := range oscillationMultipliers {
		g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(phase)))
		got := snapshotStockPrices(t, g)
		expectedMult := decimal.NewFromFloat(mult)
		for ticker, b := range basePx {
			want := b.Mul(expectedMult)
			require.True(t, got[ticker].Equal(want),
				"phase=%d ticker=%s want=%s got=%s", phase, ticker, want.String(), got[ticker].String())
		}
	}
}

func TestGeneratedSource_Oscillation_ReturnsToBaseEachCycle(t *testing.T) {
	g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	basePx := snapshotStockPrices(t, g)

	// Walk 100 cycles forward. Every phase-1 instant must equal base.
	for cycle := 0; cycle < 100; cycle++ {
		instant := timeAtPhase(1).Add(time.Duration(cycle*4) * time.Minute)
		g.withClock(fixedClock(instant))
		require.NoError(t, g.RefreshPrices(context.Background()))
		got := snapshotStockPrices(t, g)
		for ticker, b := range basePx {
			require.True(t, got[ticker].Equal(b),
				"cycle=%d ticker=%s should be base %s, got %s", cycle, ticker, b.String(), got[ticker].String())
		}
	}
}

func TestGeneratedSource_Oscillation_NoDriftFromBase(t *testing.T) {
	// A fresh instance at phase 1 establishes the reference base prices.
	ref := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	basePx := snapshotStockPrices(t, ref)

	// Drive RefreshPrices through 1000 phase transitions; then snap back to
	// phase 1 and confirm prices match the reference exactly.
	g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(0)))
	for i := 0; i < 1000; i++ {
		g.withClock(fixedClock(timeAtPhase(i % 4)))
		require.NoError(t, g.RefreshPrices(context.Background()))
	}
	g.withClock(fixedClock(timeAtPhase(1)))
	got := snapshotStockPrices(t, g)
	for ticker, b := range basePx {
		require.True(t, got[ticker].Equal(b),
			"drift on %s: base=%s got=%s", ticker, b.String(), got[ticker].String())
	}
}

func TestGeneratedSource_Oscillation_ForexUnchanged(t *testing.T) {
	// Forex must stay at seed values across all phases — cross-currency
	// conversion (exchange-service) uses these rates and would skew if they
	// swung 10%.
	ref := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	refForex, err := ref.FetchForex(context.Background())
	require.NoError(t, err)
	refByTicker := make(map[string]decimal.Decimal, len(refForex))
	for _, fx := range refForex {
		refByTicker[fx.Forex.Ticker] = fx.Price
	}

	for phase := range oscillationMultipliers {
		g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(phase)))
		fx, err := g.FetchForex(context.Background())
		require.NoError(t, err)
		for _, p := range fx {
			require.True(t, p.Price.Equal(refByTicker[p.Forex.Ticker]),
				"phase=%d ticker=%s forex changed: want=%s got=%s",
				phase, p.Forex.Ticker, refByTicker[p.Forex.Ticker].String(), p.Price.String())
		}
	}
}

func TestGeneratedSource_Oscillation_FuturesOscillate(t *testing.T) {
	base := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	baseFut, err := base.FetchFutures(context.Background())
	require.NoError(t, err)
	basePx := make(map[string]decimal.Decimal, len(baseFut))
	for _, f := range baseFut {
		basePx[f.Futures.Ticker] = f.Price
	}

	// Phase 0 → 0.9× base
	g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(0)))
	fut, err := g.FetchFutures(context.Background())
	require.NoError(t, err)
	for _, f := range fut {
		want := basePx[f.Futures.Ticker].Mul(decimal.NewFromFloat(0.90))
		require.True(t, f.Price.Equal(want),
			"futures %s phase 0: want=%s got=%s", f.Futures.Ticker, want.String(), f.Price.String())
	}
}

func TestGeneratedSource_Oscillation_ChangeReflectsDeviationFromBase(t *testing.T) {
	// Reference at phase 1 establishes base prices (multiplier 1.0).
	ref := NewGeneratedSource().withClock(fixedClock(timeAtPhase(1)))
	refStocks, err := ref.FetchStocks(context.Background())
	require.NoError(t, err)
	basePx := make(map[string]decimal.Decimal, len(refStocks))
	for _, sw := range refStocks {
		basePx[sw.Stock.Ticker] = sw.Price
		require.True(t, sw.Stock.Change.IsZero(),
			"phase 1 (base) Change must be 0 for %s, got %s", sw.Stock.Ticker, sw.Stock.Change.String())
	}

	// Phase 0 (trough, 0.9×): Change = -0.1 × base, High = base, Low = price.
	g := NewGeneratedSource().withClock(fixedClock(timeAtPhase(0)))
	stocks, err := g.FetchStocks(context.Background())
	require.NoError(t, err)
	for _, sw := range stocks {
		want := basePx[sw.Stock.Ticker].Mul(decimal.NewFromFloat(-0.10))
		require.True(t, sw.Stock.Change.Sub(want).Abs().LessThan(decimal.NewFromFloat(0.0001)),
			"trough Change for %s: want≈%s got=%s", sw.Stock.Ticker, want.String(), sw.Stock.Change.String())
		require.True(t, sw.Stock.High.Equal(basePx[sw.Stock.Ticker]),
			"trough High should equal base for %s", sw.Stock.Ticker)
		require.True(t, sw.Stock.Low.Equal(sw.Price),
			"trough Low should equal current price for %s", sw.Stock.Ticker)
	}

	// Phase 2 (peak, 1.1×): Change = +0.1 × base, High = price, Low = base.
	g = NewGeneratedSource().withClock(fixedClock(timeAtPhase(2)))
	stocks, err = g.FetchStocks(context.Background())
	require.NoError(t, err)
	for _, sw := range stocks {
		want := basePx[sw.Stock.Ticker].Mul(decimal.NewFromFloat(0.10))
		require.True(t, sw.Stock.Change.Sub(want).Abs().LessThan(decimal.NewFromFloat(0.0001)),
			"peak Change for %s: want≈%s got=%s", sw.Stock.Ticker, want.String(), sw.Stock.Change.String())
		require.True(t, sw.Stock.High.Equal(sw.Price),
			"peak High should equal current price for %s", sw.Stock.Ticker)
		require.True(t, sw.Stock.Low.Equal(basePx[sw.Stock.Ticker]),
			"peak Low should equal base for %s", sw.Stock.Ticker)
	}
}

func TestOscillationPhase_BoundariesAndModulo(t *testing.T) {
	// Phase index must be in [0,4) regardless of unix minute.
	cases := []struct {
		unixMin int64
		want    int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 3},
		{4, 0},
		{5, 1},
		{1_000_000, int(1_000_000 % 4)},
	}
	for _, c := range cases {
		got := oscillationPhase(time.Unix(c.unixMin*60, 0))
		require.Equal(t, c.want, got, "unixMin=%d", c.unixMin)
	}
}

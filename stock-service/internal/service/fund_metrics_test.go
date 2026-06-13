package service

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func mkPoint(y int, m time.Month, d int, v float64) SnapshotPoint {
	return SnapshotPoint{
		Date:     time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
		ValueRSD: decimal.NewFromFloat(v),
	}
}

func approx(t *testing.T, label string, got decimal.Decimal, want, eps float64) {
	t.Helper()
	g, _ := got.Float64()
	if math.Abs(g-want) > eps {
		t.Errorf("%s = %v, want ~%v (eps %v)", label, g, want, eps)
	}
}

// Monthly series (one month-end point each): 100, 110, 104.5, 114.95.
// Monthly returns: +0.10, -0.05, +0.10 → mean 0.05, sample stddev ~0.08660.
//
//	volatility ≈ 8.660%, reward-to-variability ≈ 0.5774.
//	max drawdown (110→104.5) = 5%.
//	annualized: 114.95/100 over 89 days (Jan31→Apr30) ^(365/89) − 1 ≈ 77.1%.
func TestComputeFundMetrics_KnownSeries(t *testing.T) {
	points := []SnapshotPoint{
		mkPoint(2026, time.January, 31, 100),
		mkPoint(2026, time.February, 28, 110),
		mkPoint(2026, time.March, 31, 104.5),
		mkPoint(2026, time.April, 30, 114.95),
	}
	m, ok := ComputeFundMetrics(points, 2)
	if !ok {
		t.Fatalf("expected metrics available with 3 monthly returns")
	}
	approx(t, "volatility", m.VolatilityPct, 8.6603, 0.05)
	approx(t, "reward", m.RewardToVariability, 0.5774, 0.01)
	approx(t, "maxDrawdown", m.MaxDrawdownPct, 5.0, 0.01)
	approx(t, "annualized", m.AnnualizedReturnPct, 77.1, 1.0)
}

// Below the gate: 2 monthly points = 1 return < min 2 → unavailable.
func TestComputeFundMetrics_BelowGate(t *testing.T) {
	points := []SnapshotPoint{
		mkPoint(2026, time.January, 31, 100),
		mkPoint(2026, time.February, 28, 110),
	}
	if _, ok := ComputeFundMetrics(points, 2); ok {
		t.Fatalf("expected unavailable below the monthly-returns gate")
	}
}

// Single point / empty → unavailable, no panic.
func TestComputeFundMetrics_Degenerate(t *testing.T) {
	if _, ok := ComputeFundMetrics(nil, 2); ok {
		t.Fatalf("nil → unavailable")
	}
	if _, ok := ComputeFundMetrics([]SnapshotPoint{mkPoint(2026, time.January, 31, 100)}, 2); ok {
		t.Fatalf("single point → unavailable")
	}
	// Zero starting value → unavailable (no div by zero).
	pts := []SnapshotPoint{
		mkPoint(2026, time.January, 31, 0),
		mkPoint(2026, time.February, 28, 10),
		mkPoint(2026, time.March, 31, 12),
	}
	if _, ok := ComputeFundMetrics(pts, 2); ok {
		t.Fatalf("zero start → unavailable")
	}
}

// Daily granularity inside a month is resampled to the last point of the month
// for the monthly returns, but max drawdown uses the full daily series.
func TestComputeFundMetrics_DailyDrawdown(t *testing.T) {
	points := []SnapshotPoint{
		mkPoint(2026, time.January, 31, 100),
		mkPoint(2026, time.February, 10, 120), // intra-month peak
		mkPoint(2026, time.February, 20, 90),  // intra-month trough → drawdown (120-90)/120 = 25%
		mkPoint(2026, time.February, 28, 110),
		mkPoint(2026, time.March, 31, 115),
	}
	m, ok := ComputeFundMetrics(points, 2)
	if !ok {
		t.Fatalf("expected available")
	}
	approx(t, "maxDrawdown(daily)", m.MaxDrawdownPct, 25.0, 0.01)
}

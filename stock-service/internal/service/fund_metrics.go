package service

import (
	"math"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// SnapshotPoint is one (date, NAV) observation of a fund's value.
type SnapshotPoint struct {
	Date     time.Time
	ValueRSD decimal.Decimal
}

// FundMetrics holds the risk/return statistics shown on the discovery and
// detail pages (Celina 4 §Statistika fondova). All as percentages except
// RewardToVariability, which is a unitless ratio (higher is better).
type FundMetrics struct {
	AnnualizedReturnPct decimal.Decimal
	VolatilityPct       decimal.Decimal
	RewardToVariability decimal.Decimal
	MaxDrawdownPct      decimal.Decimal
}

// ComputeFundMetrics derives the fund metrics from a NAV series.
//
//   - Annualized return: (V_last/V_first)^(365/days) − 1, over the full daily span.
//   - Volatility: sample std-dev of MONTHLY returns (series resampled to the
//     last NAV of each calendar month), as a percentage.
//   - Reward-to-variability: mean(monthly returns) / std-dev(monthly returns).
//   - Max drawdown: largest peak-to-trough decline over the full DAILY series.
//
// Returns ok=false (and a zero FundMetrics) when there is not enough history:
// fewer than minMonthlyReturns monthly returns (and always < 2, since a sample
// std-dev needs at least two returns), a non-positive starting value, or a
// zero-length date span. Pure function; safe on nil/short input.
func ComputeFundMetrics(points []SnapshotPoint, minMonthlyReturns int) (FundMetrics, bool) {
	if len(points) < 2 {
		return FundMetrics{}, false
	}
	// Defensive: ensure ascending by date.
	pts := make([]SnapshotPoint, len(points))
	copy(pts, points)
	sort.Slice(pts, func(i, j int) bool { return pts[i].Date.Before(pts[j].Date) })

	first := pts[0].ValueRSD
	last := pts[len(pts)-1].ValueRSD
	firstF, _ := first.Float64()
	lastF, _ := last.Float64()
	if firstF <= 0 {
		return FundMetrics{}, false
	}
	days := pts[len(pts)-1].Date.Sub(pts[0].Date).Hours() / 24
	if days <= 0 {
		return FundMetrics{}, false
	}

	// Monthly resample: last NAV of each calendar month, preserving order.
	type ym struct {
		y int
		m time.Month
	}
	var order []ym
	lastOf := map[ym]float64{}
	for _, p := range pts {
		k := ym{p.Date.Year(), p.Date.Month()}
		if _, seen := lastOf[k]; !seen {
			order = append(order, k)
		}
		v, _ := p.ValueRSD.Float64()
		lastOf[k] = v
	}
	monthly := make([]float64, len(order))
	for i, k := range order {
		monthly[i] = lastOf[k]
	}

	// Monthly returns.
	var returns []float64
	for i := 1; i < len(monthly); i++ {
		if monthly[i-1] == 0 {
			continue
		}
		returns = append(returns, monthly[i]/monthly[i-1]-1)
	}
	if len(returns) < minMonthlyReturns || len(returns) < 2 {
		return FundMetrics{}, false
	}

	// Mean + sample std-dev of monthly returns.
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(returns) - 1)
	stddev := math.Sqrt(variance)

	reward := 0.0
	if stddev > 0 {
		reward = mean / stddev
	}

	annualized := math.Pow(lastF/firstF, 365.0/days) - 1

	// Max drawdown over the full daily series.
	peak := firstF
	maxDD := 0.0
	for _, p := range pts {
		v, _ := p.ValueRSD.Float64()
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (peak - v) / peak
			if dd > maxDD {
				maxDD = dd
			}
		}
	}

	return FundMetrics{
		AnnualizedReturnPct: decimal.NewFromFloat(annualized * 100).Round(4),
		VolatilityPct:       decimal.NewFromFloat(stddev * 100).Round(4),
		RewardToVariability: decimal.NewFromFloat(reward).Round(4),
		MaxDrawdownPct:      decimal.NewFromFloat(maxDD * 100).Round(4),
	}, true
}

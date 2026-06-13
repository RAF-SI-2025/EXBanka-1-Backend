package service

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// WithSnapshots wires the fund value-snapshot repo + the minimum number of
// monthly returns required before statistics metrics are shown (SP3).
func (s *FundService) WithSnapshots(repo *repository.FundValueSnapshotRepository, minMonthly int) *FundService {
	s.snapshots = repo
	if minMonthly < 2 {
		minMonthly = 2
	}
	s.metricsMinMonthly = minMonthly
	return s
}

// FundMetricsFor computes a fund's statistics from its NAV snapshot series.
// Returns available=false when snapshots are not wired or there is not enough
// history.
func (s *FundService) FundMetricsFor(fundID uint64) (FundMetrics, bool) {
	if s.snapshots == nil {
		return FundMetrics{}, false
	}
	rows, err := s.snapshots.ListByFundSince(fundID, time.Time{})
	if err != nil {
		return FundMetrics{}, false
	}
	return ComputeFundMetrics(snapshotsToPoints(rows), s.metricsMinMonthly)
}

// FundHistory returns a fund's NAV snapshots ascending by date (empty when
// snapshots are not wired).
func (s *FundService) FundHistory(fundID uint64) []model.FundValueSnapshot {
	if s.snapshots == nil {
		return nil
	}
	rows, err := s.snapshots.ListByFundSince(fundID, time.Time{})
	if err != nil {
		return nil
	}
	return rows
}

// AverageHistory builds the system-average comparison series: every fund's NAV
// is indexed to 100 at its own first snapshot, and the per-date mean across all
// funds is returned ascending by date. This compares funds of different sizes
// on a percentage basis. Empty when snapshots are not wired.
func (s *FundService) AverageHistory() []SnapshotPoint {
	if s.snapshots == nil {
		return nil
	}
	rows, err := s.snapshots.ListAllSince(time.Time{})
	if err != nil || len(rows) == 0 {
		return nil
	}
	// Group by fund, capture each fund's first value for indexing.
	byFund := map[uint64][]model.FundValueSnapshot{}
	for _, r := range rows {
		byFund[r.FundID] = append(byFund[r.FundID], r)
	}
	// date(unix-day) → (sum of indexed values, count).
	type acc struct {
		sum   float64
		count int
		date  time.Time
	}
	buckets := map[string]*acc{}
	for _, series := range byFund {
		// series is already ascending (ListAllSince orders by fund_id, date).
		base, _ := series[0].TotalValueRSD.Float64()
		if base <= 0 {
			continue
		}
		for _, snap := range series {
			v, _ := snap.TotalValueRSD.Float64()
			indexed := v / base * 100
			key := snap.Date.UTC().Format("2006-01-02")
			b := buckets[key]
			if b == nil {
				b = &acc{date: snap.Date.UTC().Truncate(24 * time.Hour)}
				buckets[key] = b
			}
			b.sum += indexed
			b.count++
		}
	}
	out := make([]SnapshotPoint, 0, len(buckets))
	for _, b := range buckets {
		if b.count == 0 {
			continue
		}
		out = append(out, SnapshotPoint{Date: b.date, ValueRSD: decimal.NewFromFloat(b.sum / float64(b.count)).Round(4)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

func snapshotsToPoints(rows []model.FundValueSnapshot) []SnapshotPoint {
	pts := make([]SnapshotPoint, len(rows))
	for i, r := range rows {
		pts[i] = SnapshotPoint{Date: r.Date, ValueRSD: r.TotalValueRSD}
	}
	return pts
}

// fundSortKey returns the float64 sort key for a metric sort, plus whether the
// fund has metrics available (unavailable funds sort last).
func metricSortValue(m FundMetrics, sortBy string) (float64, bool) {
	var d decimal.Decimal
	switch sortBy {
	case "annualized_return":
		d = m.AnnualizedReturnPct
	case "volatility":
		d = m.VolatilityPct
	case "reward_to_variability":
		d = m.RewardToVariability
	case "max_drawdown":
		d = m.MaxDrawdownPct
	default:
		return 0, false
	}
	f, _ := d.Float64()
	return f, true
}

// IsMetricSort reports whether sortBy names one of the SP3 statistics metrics.
func IsMetricSort(sortBy string) bool {
	switch sortBy {
	case "annualized_return", "volatility", "reward_to_variability", "max_drawdown":
		return true
	}
	return false
}

// ListSortedByMetric loads all active funds, computes each one's metrics, and
// returns them sorted by the named metric (funds without metrics sort last),
// paginated. Used by discovery metric-sorts (SP3 §3.4).
func (s *FundService) ListSortedByMetric(search string, sortBy, sortOrder string, page, pageSize int) ([]model.InvestmentFund, []FundMetrics, []bool, int64, error) {
	active := true
	funds, _, err := s.repo.List(search, &active, 1, 100000)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	type row struct {
		fund      model.InvestmentFund
		metrics   FundMetrics
		available bool
		key       float64
	}
	rows := make([]row, len(funds))
	for i := range funds {
		m, ok := s.FundMetricsFor(funds[i].ID)
		k, _ := metricSortValue(m, sortBy)
		rows[i] = row{fund: funds[i], metrics: m, available: ok, key: k}
	}
	desc := sortOrder != "asc"
	sort.SliceStable(rows, func(i, j int) bool {
		// Available funds always rank ahead of unavailable ones.
		if rows[i].available != rows[j].available {
			return rows[i].available
		}
		if !rows[i].available {
			return false
		}
		if desc {
			return rows[i].key > rows[j].key
		}
		return rows[i].key < rows[j].key
	})
	total := int64(len(rows))
	// Paginate.
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	start := (page - 1) * pageSize
	if start > len(rows) {
		start = len(rows)
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	pageRows := rows[start:end]
	outFunds := make([]model.InvestmentFund, len(pageRows))
	outMetrics := make([]FundMetrics, len(pageRows))
	outAvail := make([]bool, len(pageRows))
	for i, r := range pageRows {
		outFunds[i] = r.fund
		outMetrics[i] = r.metrics
		outAvail[i] = r.available
	}
	return outFunds, outMetrics, outAvail, total, nil
}

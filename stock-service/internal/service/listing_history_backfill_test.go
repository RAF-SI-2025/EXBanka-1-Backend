package service

import (
	"sort"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
)

// backfillMockListings serves a fixed listing set.
type backfillMockListings struct {
	listingCronListingMock
}

// backfillMockDaily captures everything UpsertMany writes.
type backfillMockDaily struct {
	listingCronDailyMock
	batches [][]model.ListingDailyPriceInfo
}

func (m *backfillMockDaily) UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error {
	cp := make([]model.ListingDailyPriceInfo, len(infos))
	copy(cp, infos)
	m.batches = append(m.batches, cp)
	for i := range infos {
		row := infos[i]
		m.upserts = append(m.upserts, &row)
	}
	return nil
}

func newBackfillFixture(listings []model.Listing) (*ListingHistoryBackfill, *backfillMockDaily) {
	lr := &backfillMockListings{}
	lr.listings = listings
	dr := &backfillMockDaily{}
	return NewListingHistoryBackfill(lr, dr), dr
}

func priceDec(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }

func TestBackfill_WritesExpectedRowCountPerListing(t *testing.T) {
	listings := []model.Listing{
		{ID: 1, SecurityType: "stock", Price: priceDec(100)},
		{ID: 2, SecurityType: "stock", Price: priceDec(50)},
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	const rowsPerListing = 2112 // 288 intraday + 1824 daily
	assert.Equal(t, 2*rowsPerListing, len(dr.upserts), "expected intraday+daily rows per listing")
}

func TestBackfill_AnchorsAtCurrentPrice(t *testing.T) {
	listings := []model.Listing{{ID: 7, SecurityType: "stock", Price: priceDec(123.45)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	var latest *model.ListingDailyPriceInfo
	for _, row := range dr.upserts {
		if row.ListingID != 7 {
			continue
		}
		if latest == nil || row.Date.After(latest.Date) {
			latest = row
		}
	}
	require.NotNil(t, latest)
	assert.True(t, latest.Price.Equal(priceDec(123.45)),
		"newest row's close must equal listing.Price, got %s", latest.Price.String())
}

func TestBackfill_IsDeterministic(t *testing.T) {
	listings := []model.Listing{{ID: 9, SecurityType: "stock", Price: priceDec(80)}}

	b1, dr1 := newBackfillFixture(listings)
	require.NoError(t, b1.Run())
	b2, dr2 := newBackfillFixture(listings)
	require.NoError(t, b2.Run())

	require.Equal(t, len(dr1.upserts), len(dr2.upserts))
	for i := range dr1.upserts {
		a := dr1.upserts[i]
		b := dr2.upserts[i]
		assert.True(t, a.Price.Equal(b.Price), "row %d price differs: %s vs %s", i, a.Price, b.Price)
		assert.True(t, a.High.Equal(b.High))
		assert.True(t, a.Low.Equal(b.Low))
		assert.Equal(t, a.Volume, b.Volume)
	}
}

func TestBackfill_SkipsZeroPriceListings(t *testing.T) {
	listings := []model.Listing{
		{ID: 1, SecurityType: "stock", Price: priceDec(0)},
		{ID: 2, SecurityType: "stock", Price: priceDec(10)},
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for _, row := range dr.upserts {
		assert.NotEqual(t, uint64(1), row.ListingID, "zero-price listing must produce no rows")
	}
	assert.Equal(t, 2112, len(dr.upserts))
}

func TestBackfill_DatesAreDistinctAndContiguous(t *testing.T) {
	listings := []model.Listing{{ID: 1, SecurityType: "stock", Price: priceDec(10)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	// Total row count
	assert.Equal(t, 2112, len(dr.upserts), "expected 288 intraday + 1824 daily")

	// All dates distinct
	seen := map[time.Time]bool{}
	for _, row := range dr.upserts {
		assert.False(t, seen[row.Date], "duplicate date: %s", row.Date)
		seen[row.Date] = true
	}
	assert.Equal(t, 2112, len(seen))

	// generateHistory appends intraday rows first (indices 0..intradayCount-1)
	// then daily rows (indices intradayCount..2111). Use index-based split.
	intradayRows := dr.upserts[:intradayCount]
	dailyRows := dr.upserts[intradayCount:]

	assert.Equal(t, intradayCount, len(intradayRows), "expected 288 intraday rows")
	assert.Equal(t, dailyBackfillDays, len(dailyRows), "expected 1824 daily rows")

	// Sort intraday desc and check 5-min gaps
	sort.Slice(intradayRows, func(i, j int) bool {
		return intradayRows[i].Date.After(intradayRows[j].Date)
	})
	for i := 1; i < len(intradayRows); i++ {
		gap := intradayRows[i-1].Date.Sub(intradayRows[i].Date)
		assert.Equal(t, time.Duration(intradayMinutes)*time.Minute, gap,
			"intraday row %d gap should be %d min", i, intradayMinutes)
	}

	// Sort daily desc and check 1-day gaps
	sort.Slice(dailyRows, func(i, j int) bool {
		return dailyRows[i].Date.After(dailyRows[j].Date)
	})
	for i := 1; i < len(dailyRows); i++ {
		gap := dailyRows[i-1].Date.Sub(dailyRows[i].Date)
		assert.Equal(t, 24*time.Hour, gap,
			"daily row %d gap should be 24h", i)
	}
}

func TestBackfill_OHLCInvariants(t *testing.T) {
	listings := []model.Listing{{ID: 1, SecurityType: "stock", Price: priceDec(50)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for i, row := range dr.upserts {
		open := row.Price.Sub(row.Change) // open = close - change
		maxOC := decimal.Max(open, row.Price)
		minOC := decimal.Min(open, row.Price)
		assert.True(t, row.High.GreaterThanOrEqual(maxOC),
			"row %d: High %s must be >= max(open,close) %s", i, row.High, maxOC)
		assert.True(t, row.Low.LessThanOrEqual(minOC),
			"row %d: Low %s must be <= min(open,close) %s", i, row.Low, minOC)
	}
}

func TestBackfill_SkipsOptionListings(t *testing.T) {
	listings := []model.Listing{
		{ID: 1, SecurityType: "option", Price: priceDec(5)},  // skip
		{ID: 2, SecurityType: "stock", Price: priceDec(100)}, // include
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for _, row := range dr.upserts {
		assert.NotEqual(t, uint64(1), row.ListingID, "option listing must produce no rows")
	}
	assert.Equal(t, 2112, len(dr.upserts))
}

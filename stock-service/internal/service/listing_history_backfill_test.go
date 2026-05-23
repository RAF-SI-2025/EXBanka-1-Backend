package service

import (
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

	const days = 1825
	assert.Equal(t, 2*days, len(dr.upserts), "expected one row per listing per day for 5y")
}

func TestBackfill_AnchorsAtCurrentPrice(t *testing.T) {
	listings := []model.Listing{{ID: 7, Price: priceDec(123.45)}}
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
	listings := []model.Listing{{ID: 9, Price: priceDec(80)}}

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
		{ID: 1, Price: priceDec(0)},
		{ID: 2, Price: priceDec(10)},
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for _, row := range dr.upserts {
		assert.NotEqual(t, uint64(1), row.ListingID, "zero-price listing must produce no rows")
	}
	assert.Equal(t, 1825, len(dr.upserts))
}

func TestBackfill_DatesAreDistinctAndContiguous(t *testing.T) {
	listings := []model.Listing{{ID: 1, Price: priceDec(10)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	seen := map[time.Time]bool{}
	for _, row := range dr.upserts {
		assert.False(t, seen[row.Date], "duplicate date: %s", row.Date)
		seen[row.Date] = true
	}
	assert.Equal(t, 1825, len(seen))
}

func TestBackfill_OHLCInvariants(t *testing.T) {
	listings := []model.Listing{{ID: 1, Price: priceDec(50)}}
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

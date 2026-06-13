package service

import (
	"log"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const (
	intradayMinutes   = 5                         // 5-minute granularity for today
	intradayCount     = 24 * 60 / intradayMinutes // 288 rows for the last 24h
	dailyBackfillDays = 1824                      // 5 years - 1 day (today is covered by intraday rows)
)

// ListingHistoryBackfill seeds 5 years of deterministic synthetic OHLC history
// for every listing. Used so the stock detail chart renders candles for every
// period (1D..ALL) immediately after `make docker-up`, not after the system has
// been running for years.
//
// Determinism: each listing's history is seeded from its ID, so re-runs (e.g.
// SwitchSource) produce identical rows. Idempotent: upsert-by-(listing_id, date)
// prevents duplicates.
type ListingHistoryBackfill struct {
	listingRepo ListingRepo
	dailyRepo   DailyPriceRepo
	now         func() time.Time // injectable for tests
}

func NewListingHistoryBackfill(listingRepo ListingRepo, dailyRepo DailyPriceRepo) *ListingHistoryBackfill {
	return &ListingHistoryBackfill{
		listingRepo: listingRepo,
		dailyRepo:   dailyRepo,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// Run walks every listing, generates dense intraday rows for the last 24h and
// daily rows for the preceding 1824 days, then batch-upserts them.
func (b *ListingHistoryBackfill) Run() error {
	listings, err := b.listingRepo.ListAll()
	if err != nil {
		return err
	}

	now := b.now()
	totalListings := 0
	totalRows := 0

	for _, l := range listings {
		if l.Price.IsZero() {
			continue
		}
		switch l.SecurityType {
		case "stock", "futures", "forex":
			// supported; generate history
		default:
			continue
		}
		rows := generateHistory(l.ID, l.Price, l.Volume, now)
		if err := b.dailyRepo.UpsertManyByListingAndDate(rows); err != nil {
			log.Printf("WARN: backfill: listing %d: %v", l.ID, err)
			continue
		}
		totalListings++
		totalRows += len(rows)
	}

	log.Printf("listing history backfill: %d listings, %d rows", totalListings, totalRows)
	return nil
}

// generateHistory produces a deterministic synthetic OHLC series for one
// listing: 288 intraday rows over the last 24 hours (5-min stride), then
// 1824 daily rows extending 5 years back. The latest intraday row anchors at
// the listing's current price; each preceding row's close equals the next
// row's open (chained random walk). Seeded by listingID so reseeds produce
// identical history.
func generateHistory(listingID uint64, anchorPrice decimal.Decimal, anchorVolume int64, now time.Time) []model.ListingDailyPriceInfo {
	rows := make([]model.ListingDailyPriceInfo, 0, intradayCount+dailyBackfillDays)

	baseVolume := anchorVolume
	if baseVolume <= 0 {
		baseVolume = 100_000
	}

	price, _ := anchorPrice.Float64()

	// Intraday: walk newest -> oldest in 5-minute steps. Each step's RNG seed
	// derived from (listingID, "intraday", step_index) so the series is
	// deterministic and disjoint from the daily series.
	for i := 0; i < intradayCount; i++ {
		seed := int64(listingID*1_000_003) + int64(i)*7
		rng := newRng(seed)
		// Smaller drift intraday (±0.3% per 5-min step) so 24h doesn't wander wildly.
		drift := (rng.Float64()*2 - 1) * 0.003
		spread := rng.Float64() * 0.005 // 0-0.5% spread per 5-min candle

		cls := price
		open := cls * (1 + drift)
		hi := maxF(open, cls) * (1 + spread)
		lo := minF(open, cls) * (1 - spread)
		vol := int64(float64(baseVolume) * (0.5 + rng.Float64()*1.5) / float64(intradayCount/12)) // share daily volume across 12 hours of candles

		ts := now.Add(-time.Duration(i) * time.Duration(intradayMinutes) * time.Minute)
		rows = append(rows, model.ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      ts,
			Price:     decimal.NewFromFloat(cls),
			High:      decimal.NewFromFloat(hi),
			Low:       decimal.NewFromFloat(lo),
			Change:    decimal.NewFromFloat(cls - open),
			Volume:    vol,
		})
		price = open
	}

	// Daily: continue walking newest->oldest from the price reached at the end
	// of the intraday walk. Each preceding day's close = today's open from
	// where we left off.
	startDay := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	for d := 0; d < dailyBackfillDays; d++ {
		seed := int64(listingID*1_000_003) + int64(d) + 1_000_000
		rng := newRng(seed)
		drift := (rng.Float64()*2 - 1) * 0.01 // ±1% daily
		spread := rng.Float64() * 0.015       // 0-1.5%

		cls := price
		open := cls * (1 + drift)
		hi := maxF(open, cls) * (1 + spread)
		lo := minF(open, cls) * (1 - spread)
		vol := int64(float64(baseVolume) * (0.5 + rng.Float64()*1.5))

		date := startDay.AddDate(0, 0, -d)
		rows = append(rows, model.ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      date,
			Price:     decimal.NewFromFloat(cls),
			High:      decimal.NewFromFloat(hi),
			Low:       decimal.NewFromFloat(lo),
			Change:    decimal.NewFromFloat(cls - open),
			Volume:    vol,
		})
		price = open
	}

	return rows
}

// newRng creates a fresh deterministic PRNG from a seed.
func newRng(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

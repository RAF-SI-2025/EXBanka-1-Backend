package service

import (
	"log"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const backfillDays = 1825 // 5 years

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

// Run walks every listing, generates 5y of synthetic daily OHLC ending at the
// listing's current price, and batch-upserts it.
func (b *ListingHistoryBackfill) Run() error {
	listings, err := b.listingRepo.ListAll()
	if err != nil {
		return err
	}

	today := b.now().Truncate(24 * time.Hour)
	totalListings := 0
	totalRows := 0

	for _, l := range listings {
		if l.Price.IsZero() {
			continue
		}
		rows := generateHistory(l.ID, l.Price, l.Volume, today)
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

// generateHistory produces backfillDays rows of deterministic synthetic OHLC
// for one listing. Newest row's close equals anchorPrice (today). Each
// preceding day steps backwards: today's open becomes yesterday's close.
func generateHistory(listingID uint64, anchorPrice decimal.Decimal, anchorVolume int64, today time.Time) []model.ListingDailyPriceInfo {
	rows := make([]model.ListingDailyPriceInfo, 0, backfillDays)

	baseVolume := anchorVolume
	if baseVolume <= 0 {
		baseVolume = 100_000
	}

	price, _ := anchorPrice.Float64()
	for d := 0; d < backfillDays; d++ {
		rng := rand.New(rand.NewSource(int64(listingID*1_000_003) + int64(d)))
		drift := (rng.Float64()*2 - 1) * 0.01 // ±1%
		spread := rng.Float64() * 0.015        // 0–1.5%

		cls := price
		open := cls * (1 + drift)
		hi := maxF(open, cls) * (1 + spread)
		lo := minF(open, cls) * (1 - spread)
		vol := int64(float64(baseVolume) * (0.5 + rng.Float64()*1.5))

		date := today.AddDate(0, 0, -d)
		rows = append(rows, model.ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      date,
			Price:     decimal.NewFromFloat(cls),
			High:      decimal.NewFromFloat(hi),
			Low:       decimal.NewFromFloat(lo),
			Change:    decimal.NewFromFloat(cls - open),
			Volume:    vol,
		})

		price = open // step backwards
	}
	return rows
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

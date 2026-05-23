package repository

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

func decimalFromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

type ListingDailyPriceRepository struct {
	db *gorm.DB
}

func NewListingDailyPriceRepository(db *gorm.DB) *ListingDailyPriceRepository {
	return &ListingDailyPriceRepository{db: db}
}

func (r *ListingDailyPriceRepository) Create(info *model.ListingDailyPriceInfo) error {
	return r.db.Create(info).Error
}

func (r *ListingDailyPriceRepository) UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ListingDailyPriceInfo
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("listing_id = ? AND date = ?", info.ListingID, info.Date).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(info).Error
			}
			return err
		}
		existing.Price = info.Price
		existing.High = info.High
		existing.Low = info.Low
		existing.Change = info.Change
		existing.Volume = info.Volume
		return tx.Save(&existing).Error
	})
}

// UpsertManyByListingAndDate inserts or updates a batch of price-info rows in a
// single round-trip per chunk. Used by the history backfill to write 5y of
// synthetic OHLC per listing without paying per-row transaction overhead.
//
// Rows MAY span multiple listings. Conflicts on (listing_id, date) overwrite
// price/high/low/change/volume; the existing row's id is preserved.
func (r *ListingDailyPriceRepository) UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error {
	if len(infos) == 0 {
		return nil
	}
	const chunk = 500
	return r.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < len(infos); i += chunk {
			end := i + chunk
			if end > len(infos) {
				end = len(infos)
			}
			batch := infos[i:end]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "listing_id"}, {Name: "date"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"price", "high", "low", "change", "volume",
				}),
			}).Create(&batch).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetHistoryBucketed returns OHLC-aggregated history. Each returned row has
// Date = bucket start, Price = close, Change = close − open, High = max(high)
// in the bucket, Low = min(low), Volume = sum(volume). bucketSeconds is the
// width of each bucket; pass 60 for 1-minute candles, 3600 for hourly, etc.
//
// Used by chart endpoints so different timeframes produce sensible candle
// counts regardless of how frequently the underlying snapshot loop runs.
func (r *ListingDailyPriceRepository) GetHistoryBucketed(listingID uint64, from, to time.Time, bucketSeconds int) ([]model.ListingDailyPriceInfo, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	type row struct {
		Bucket time.Time
		Open   float64
		Close  float64
		High   float64
		Low    float64
		Volume int64
	}
	var rows []row
	q := r.db.Raw(`
		WITH src AS (
			SELECT
				to_timestamp(floor(extract(epoch FROM date) / ?) * ?) AS bucket,
				date AS at,
				price, high, low, volume
			FROM listing_daily_price_infos
			WHERE listing_id = ?
			  AND (? OR date >= ?)
			  AND (? OR date <= ?)
		)
		SELECT
			bucket,
			(array_agg(price ORDER BY at ASC))[1]  AS open,
			(array_agg(price ORDER BY at DESC))[1] AS close,
			MAX(high)   AS high,
			MIN(low)    AS low,
			SUM(volume) AS volume
		FROM src
		GROUP BY bucket
		ORDER BY bucket DESC
		LIMIT 1000
	`,
		bucketSeconds, bucketSeconds,
		listingID,
		from.IsZero(), from,
		to.IsZero(), to,
	)
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ListingDailyPriceInfo, 0, len(rows))
	for _, x := range rows {
		out = append(out, model.ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      x.Bucket,
			Price:     decimalFromFloat(x.Close),
			High:      decimalFromFloat(x.High),
			Low:       decimalFromFloat(x.Low),
			Change:    decimalFromFloat(x.Close - x.Open),
			Volume:    x.Volume,
		})
	}
	return out, nil
}

func (r *ListingDailyPriceRepository) GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	var history []model.ListingDailyPriceInfo
	var total int64

	q := r.db.Model(&model.ListingDailyPriceInfo{}).
		Where("listing_id = ?", listingID)

	if !from.IsZero() {
		q = q.Where("date >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where("date <= ?", to)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 365 {
		pageSize = 30
	}

	if err := q.Order("date DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&history).Error; err != nil {
		return nil, 0, err
	}
	return history, total, nil
}

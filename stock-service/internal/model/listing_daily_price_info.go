package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ListingDailyPriceInfo stores price snapshots for a listing. Historically
// one row per day (written by the 23:55 cron); now also written on every
// refresh tick so charts can render intraday oscillation. The (listing_id, date)
// composite unique index lets the bulk-upsert backfill use ON CONFLICT to
// overwrite the same (listing, timestamp) pair on reseed, while still allowing
// many listings to share the same timestamp.
type ListingDailyPriceInfo struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ListingID uint64          `gorm:"not null;uniqueIndex:idx_listing_daily_listing_id_date,priority:1" json:"listing_id"`
	Listing   Listing         `gorm:"foreignKey:ListingID" json:"-"`
	Date      time.Time       `gorm:"type:timestamp;not null;uniqueIndex:idx_listing_daily_listing_id_date,priority:2" json:"date"`
	Price     decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"price"`
	High      decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"high"`
	Low       decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"low"`
	Change    decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"change"`
	Volume    int64           `gorm:"not null;default:0" json:"volume"`
}

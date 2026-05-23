package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ListingDailyPriceInfo stores price snapshots for a listing. Historically
// one row per day (written by the 23:55 cron); now also written on every
// refresh tick so charts can render intraday oscillation. The Date column is
// stored as a timestamp so multiple snapshots can coexist on the same calendar
// day without colliding.
type ListingDailyPriceInfo struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ListingID uint64          `gorm:"not null;index" json:"listing_id"`
	Listing   Listing         `gorm:"foreignKey:ListingID" json:"-"`
	Date      time.Time       `gorm:"type:timestamp;not null;uniqueIndex:idx_listing_daily_listing_date" json:"date"`
	Price     decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"price"`
	High      decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"high"`
	Low       decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"low"`
	Change    decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"change"`
	Volume    int64           `gorm:"not null;default:0" json:"volume"`
}

package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ListingDailyPriceInfo stores one row per listing per day.
// Populated by the end-of-day cron job.
type ListingDailyPriceInfo struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ListingID uint64          `gorm:"not null;index" json:"listing_id"`
	Listing   Listing         `gorm:"foreignKey:ListingID" json:"-"`
	Date      time.Time       `gorm:"type:date;not null;index" json:"date"`
	Price     decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"price"`
	High      decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"high"`
	Low       decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"low"`
	Change    decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"change"`
	Volume    int64           `gorm:"not null;default:0" json:"volume"`
}

package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Listing is the bridge between a security and the exchange it trades on.
// Orders reference ListingID. Each stock/futures/forex pair has exactly one listing.
// Options do not have listings (displayed via stock detail).
type Listing struct {
	ID           uint64        `gorm:"primaryKey;autoIncrement" json:"id"`
	SecurityID   uint64        `gorm:"not null;index" json:"security_id"`
	SecurityType string        `gorm:"size:10;not null;index" json:"security_type"` // "stock", "futures", "forex"
	ExchangeID   uint64        `gorm:"not null;index" json:"exchange_id"`
	Exchange     StockExchange `gorm:"foreignKey:ExchangeID" json:"-"`
	// Denormalized current price data (mirrored from security model during sync)
	Price       decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"price"`
	High        decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"high"`
	Low         decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"low"`
	Change      decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"change"`
	Volume      int64           `gorm:"not null;default:0" json:"volume"`
	LastRefresh time.Time       `json:"last_refresh"`
	Version     int64           `gorm:"not null;default:1" json:"-"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (l *Listing) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", l.Version)
	l.Version++
	return nil
}

package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ForexPair represents a currency pair (e.g., EUR/USD).
// We support 8 currencies → 56 pairs.
type ForexPair struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker        string          `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name          string          `gorm:"not null" json:"name"`
	BaseCurrency  string          `gorm:"size:3;not null;index" json:"base_currency"`
	QuoteCurrency string          `gorm:"size:3;not null;index" json:"quote_currency"`
	ExchangeRate  decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"exchange_rate"`
	Liquidity     string          `gorm:"size:10;not null;default:'medium'" json:"liquidity"`
	ExchangeID    uint64          `gorm:"index;not null" json:"exchange_id"`
	Exchange      StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Price data (for listing — price = exchange_rate in quote currency)
	High        decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"high"`
	Low         decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"low"`
	Change      decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"change"`
	Volume      int64           `gorm:"not null;default:0" json:"volume"`
	Version     int64           `gorm:"not null;default:1" json:"-"`
	LastRefresh time.Time       `json:"last_refresh"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (fp *ForexPair) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", fp.Version)
	fp.Version++
	return nil
}

// ContractSizeValue for forex is standardly 1000.
func (fp *ForexPair) ContractSizeValue() int64 {
	return 1000
}

// MaintenanceMargin = ContractSize * Price * 10%.
func (fp *ForexPair) MaintenanceMargin() decimal.Decimal {
	return decimal.NewFromInt(fp.ContractSizeValue()).Mul(fp.ExchangeRate).Mul(decimal.NewFromFloat(0.1))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (fp *ForexPair) InitialMarginCost() decimal.Decimal {
	return fp.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}

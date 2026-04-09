package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Stock represents an equity security (e.g., AAPL, MSFT).
// One stock is listed on one exchange in our system.
type Stock struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker            string          `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name              string          `gorm:"not null" json:"name"`
	OutstandingShares int64           `gorm:"not null;default:0" json:"outstanding_shares"`
	DividendYield     decimal.Decimal `gorm:"type:numeric(10,6);not null;default:0" json:"dividend_yield"`
	ExchangeID        uint64          `gorm:"index;not null" json:"exchange_id"`
	Exchange          StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Current price data (updated periodically)
	Price       decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"price"`
	High        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"high"`
	Low         decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"low"`
	Change      decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"change"`
	Volume      int64           `gorm:"not null;default:0" json:"volume"`
	Version     int64           `gorm:"not null;default:1" json:"-"`
	LastRefresh time.Time       `json:"last_refresh"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (s *Stock) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", s.Version)
	s.Version++
	return nil
}

// ContractSize for stocks is always 1.
func (s *Stock) ContractSize() int64 {
	return 1
}

// MaintenanceMargin for stocks = 50% of Price.
func (s *Stock) MaintenanceMargin() decimal.Decimal {
	return s.Price.Mul(decimal.NewFromFloat(0.5))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (s *Stock) InitialMarginCost() decimal.Decimal {
	return s.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}

// MarketCap = OutstandingShares * Price.
func (s *Stock) MarketCap() decimal.Decimal {
	return s.Price.Mul(decimal.NewFromInt(s.OutstandingShares))
}

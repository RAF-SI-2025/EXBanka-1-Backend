package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Option represents a stock option contract (call or put).
// Options are always linked to a Stock. Contract size = 100 shares per option.
type Option struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker            string          `gorm:"uniqueIndex;size:30;not null" json:"ticker"`
	Name              string          `gorm:"not null" json:"name"`
	StockID           uint64          `gorm:"index;not null" json:"stock_id"`
	Stock             Stock           `gorm:"foreignKey:StockID" json:"-"`
	OptionType        string          `gorm:"size:4;not null" json:"option_type"` // "call" or "put"
	StrikePrice       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"strike_price"`
	ImpliedVolatility decimal.Decimal `gorm:"type:numeric(10,6);not null;default:1" json:"implied_volatility"`
	Premium           decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"premium"`
	OpenInterest      int64           `gorm:"not null;default:0" json:"open_interest"`
	SettlementDate    time.Time       `gorm:"not null;index" json:"settlement_date"`
	ListingID         *uint64         `gorm:"index" json:"listing_id,omitempty"`
	Version           int64           `gorm:"not null;default:1" json:"-"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func (o *Option) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", o.Version)
	o.Version++
	return nil
}

// ContractSizeValue for options is always 100 shares.
func (o *Option) ContractSizeValue() int64 {
	return 100
}

// MaintenanceMargin = ContractSize * 50% * Stock Price.
func (o *Option) MaintenanceMargin(stockPrice decimal.Decimal) decimal.Decimal {
	return decimal.NewFromInt(o.ContractSizeValue()).Mul(stockPrice).Mul(decimal.NewFromFloat(0.5))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (o *Option) InitialMarginCost(stockPrice decimal.Decimal) decimal.Decimal {
	return o.MaintenanceMargin(stockPrice).Mul(decimal.NewFromFloat(1.1))
}

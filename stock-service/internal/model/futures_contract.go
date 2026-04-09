package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// FuturesContract represents a futures contract (e.g., CLJ26 — Crude Oil April 2026).
// Each futures is listed on exactly one exchange.
type FuturesContract struct {
	ID             uint64        `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker         string        `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name           string        `gorm:"not null" json:"name"`
	ContractSize   int64         `gorm:"not null" json:"contract_size"`
	ContractUnit   string        `gorm:"size:20;not null" json:"contract_unit"`
	SettlementDate time.Time     `gorm:"not null;index" json:"settlement_date"`
	ExchangeID     uint64        `gorm:"index;not null" json:"exchange_id"`
	Exchange       StockExchange `gorm:"foreignKey:ExchangeID" json:"-"`
	// Current price data
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

func (f *FuturesContract) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", f.Version)
	f.Version++
	return nil
}

// MaintenanceMargin for futures = ContractSize * Price * 10%.
func (f *FuturesContract) MaintenanceMargin() decimal.Decimal {
	return decimal.NewFromInt(f.ContractSize).Mul(f.Price).Mul(decimal.NewFromFloat(0.1))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (f *FuturesContract) InitialMarginCost() decimal.Decimal {
	return f.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}

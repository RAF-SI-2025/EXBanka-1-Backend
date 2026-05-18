package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// FundHolding is the fund-side analogue of Holding. Quantity changes when an
// on-behalf-of-fund order fills (buy: +qty) or when liquidation runs (sell:
// -qty). FIFO order for liquidation = ORDER BY created_at ASC.
type FundHolding struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID           uint64          `gorm:"not null;uniqueIndex:ux_fh_security,priority:1" json:"fund_id"`
	SecurityType     string          `gorm:"size:16;not null;uniqueIndex:ux_fh_security,priority:2" json:"security_type"`
	SecurityID       uint64          `gorm:"not null;uniqueIndex:ux_fh_security,priority:3" json:"security_id"`
	Quantity         int64           `gorm:"not null;default:0" json:"quantity"`
	ReservedQuantity int64           `gorm:"not null;default:0" json:"reserved_quantity"`
	AveragePriceRSD  decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"average_price_rsd"`
	CreatedAt        time.Time       `gorm:"index" json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `gorm:"not null;default:0" json:"-"`
}

func (h *FundHolding) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", h.Version)
	}
	h.Version++
	return nil
}

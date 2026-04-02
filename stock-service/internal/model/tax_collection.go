package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// TaxCollection records a tax collection event for a user for a given month.
// One record per user per month per account.
type TaxCollection struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint64          `gorm:"not null;index:idx_tax_user_year" json:"user_id"`
	SystemType   string          `gorm:"size:10;not null" json:"system_type"`
	Year         int             `gorm:"not null;index:idx_tax_user_year" json:"year"`
	Month        int             `gorm:"not null" json:"month"`
	AccountID    uint64          `gorm:"not null" json:"account_id"`
	Currency     string          `gorm:"size:3;not null" json:"currency"`
	TotalGain    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"total_gain"`
	TaxAmount    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"tax_amount"` // in original currency
	TaxAmountRSD decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"tax_amount_rsd"`
	CollectedAt  time.Time       `gorm:"not null" json:"collected_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

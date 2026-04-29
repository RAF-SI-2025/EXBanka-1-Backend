package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// TaxCollection records a tax collection event for an owner for a given month.
// One record per owner per month per account.
type TaxCollection struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType    OwnerType       `gorm:"size:8;not null;index:idx_tax_owner_year,priority:1;check:owner_type IN ('client','bank')" json:"owner_type"`
	OwnerID      *uint64         `gorm:"index:idx_tax_owner_year,priority:2" json:"owner_id"`
	Year         int             `gorm:"not null;index:idx_tax_owner_year,priority:3" json:"year"`
	Month        int             `gorm:"not null" json:"month"`
	AccountID    uint64          `gorm:"not null" json:"account_id"`
	Currency     string          `gorm:"size:3;not null" json:"currency"`
	TotalGain    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"total_gain"`
	TaxAmount    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"tax_amount"` // in original currency
	TaxAmountRSD decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"tax_amount_rsd"`
	CollectedAt  time.Time       `gorm:"not null" json:"collected_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (t *TaxCollection) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(t.OwnerType, t.OwnerID)
}

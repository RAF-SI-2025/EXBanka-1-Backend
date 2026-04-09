package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type TransferFee struct {
	ID              uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string          `gorm:"size:100;not null" json:"name"`
	FeeType         string          `gorm:"size:20;not null" json:"fee_type"` // "percentage" or "fixed"
	FeeValue        decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"fee_value"`
	MinAmount       decimal.Decimal `gorm:"type:numeric(18,4);default:0" json:"min_amount"` // applies only to txns >= this
	MaxFee          decimal.Decimal `gorm:"type:numeric(18,4);default:0" json:"max_fee"`    // cap (0 = uncapped)
	TransactionType string          `gorm:"size:20;not null" json:"transaction_type"`       // "payment", "transfer", "all"
	CurrencyCode    string          `gorm:"size:3" json:"currency_code"`                    // empty = all currencies
	Active          bool            `gorm:"default:true" json:"active"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

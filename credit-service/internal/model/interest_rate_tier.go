package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type InterestRateTier struct {
	ID           uint64          `gorm:"primaryKey" json:"id"`
	AmountFrom   decimal.Decimal `gorm:"type:decimal(20,4);not null" json:"amount_from"`
	AmountTo     decimal.Decimal `gorm:"type:decimal(20,4);not null" json:"amount_to"` // 0 = unlimited
	FixedRate    decimal.Decimal `gorm:"type:decimal(10,4);not null" json:"fixed_rate"`
	VariableBase decimal.Decimal `gorm:"type:decimal(10,4);not null" json:"variable_base"`
	Active       bool            `gorm:"default:true" json:"active"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

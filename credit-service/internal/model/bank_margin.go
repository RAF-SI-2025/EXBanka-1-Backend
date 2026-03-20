package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type BankMargin struct {
	ID        uint64          `gorm:"primaryKey" json:"id"`
	LoanType  string          `gorm:"size:30;uniqueIndex;not null" json:"loan_type"`
	Margin    decimal.Decimal `gorm:"type:decimal(10,4);not null" json:"margin"`
	Active    bool            `gorm:"default:true" json:"active"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

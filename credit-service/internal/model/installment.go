package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type Installment struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	LoanID       uint64          `gorm:"not null;index" json:"loan_id"`
	Amount       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"amount"`
	InterestRate decimal.Decimal `gorm:"type:numeric(8,4);not null" json:"interest_rate"`
	CurrencyCode string          `gorm:"size:3;not null" json:"currency_code"`
	ExpectedDate time.Time       `json:"expected_date"`
	ActualDate   *time.Time      `json:"actual_date,omitempty"`
	Status       string          `gorm:"size:20;default:'unpaid'" json:"status"`
	Version      int64           `gorm:"not null;default:1"`
}

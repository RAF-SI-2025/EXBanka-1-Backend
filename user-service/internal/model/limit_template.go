package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type LimitTemplate struct {
	ID                    int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                  string          `gorm:"uniqueIndex;size:100;not null" json:"name"`
	Description           string          `gorm:"size:255" json:"description"`
	MaxLoanApprovalAmount decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_loan_approval_amount"`
	MaxSingleTransaction  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_single_transaction"`
	MaxDailyTransaction   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_daily_transaction"`
	MaxClientDailyLimit   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_client_daily_limit"`
	MaxClientMonthlyLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_client_monthly_limit"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

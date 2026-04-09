package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type EmployeeLimit struct {
	ID                    int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	EmployeeID            int64           `gorm:"uniqueIndex;not null" json:"employee_id"`
	MaxLoanApprovalAmount decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_loan_approval_amount"`
	MaxSingleTransaction  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_single_transaction"`
	MaxDailyTransaction   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_daily_transaction"`
	MaxClientDailyLimit   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_client_daily_limit"`
	MaxClientMonthlyLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"max_client_monthly_limit"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	Version               int64           `gorm:"not null;default:1" json:"version"`
}

func (el *EmployeeLimit) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", el.Version)
	el.Version++
	return nil
}

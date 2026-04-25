package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// InvestmentFund is a supervisor-managed pool of cash + securities held in a
// single RSD account at account-service. Clients invest RSD (or any FX
// currency converted to RSD) and receive a proportional share of the fund's
// realised + unrealised P&L. The bank itself can hold a position via the
// 1_000_000_000 owner sentinel.
type InvestmentFund struct {
	ID                     uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                   string          `gorm:"size:128;not null" json:"name"`
	Description            string          `gorm:"type:text;not null;default:''" json:"description"`
	ManagerEmployeeID      int64           `gorm:"not null;index" json:"manager_employee_id"`
	MinimumContributionRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"minimum_contribution_rsd"`
	RSDAccountID           uint64          `gorm:"not null;uniqueIndex" json:"rsd_account_id"`
	Active                 bool            `gorm:"not null;default:true;index" json:"active"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	Version                int64           `gorm:"not null;default:0" json:"-"`
}

func (f *InvestmentFund) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", f.Version)
	}
	f.Version++
	return nil
}

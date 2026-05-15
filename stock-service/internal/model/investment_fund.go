package model

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// FundType discriminates open-ended funds (the original Celina-4 model)
// from closed-end funds with a fundraising window + maturity date.
type FundType string

const (
	FundTypeOpen   FundType = "open"
	FundTypeClosed FundType = "closed"
)

// FundStatus is the lifecycle state of a closed-end fund. Open-ended
// funds always sit in FundStatusOpen.
type FundStatus string

const (
	FundStatusOpen        FundStatus = "open"
	FundStatusFundraising FundStatus = "fundraising"
	FundStatusActive      FundStatus = "active"
	FundStatusMatured     FundStatus = "matured"
	FundStatusLiquidated  FundStatus = "liquidated"
)

// InvestmentFund is a supervisor-managed pool of cash + securities held in a
// single RSD account at account-service. Clients invest RSD (or any FX
// currency converted to RSD) and receive a proportional share of the fund's
// realised + unrealised P&L. The bank itself can hold a position via the
// 1_000_000_000 owner sentinel.
//
// Closed-end fund extension (Celina 4):
//   - FundType=closed funds collect contributions only between
//     FundraisingStart and FundraisingEnd.
//   - After FundraisingEnd they transition Active and stop accepting
//     new investments / redemptions.
//   - On MaturityDate they enter Matured and the supervisor has a
//     7-day grace window to liquidate; on grace-end the system
//     auto-creates Sell Markets for remaining positions, distributes
//     proceeds pro-rata, and flips to Liquidated.
type InvestmentFund struct {
	ID                     uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                   string          `gorm:"size:128;not null" json:"name"`
	Description            string          `gorm:"type:text;not null;default:''" json:"description"`
	ManagerEmployeeID      int64           `gorm:"not null;index" json:"manager_employee_id"`
	MinimumContributionRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"minimum_contribution_rsd"`
	RSDAccountID           uint64          `gorm:"not null;uniqueIndex" json:"rsd_account_id"`
	Active                 bool            `gorm:"not null;default:true;index" json:"active"`

	// Closed-end fund fields. All optional for open-ended funds.
	FundType         FundType        `gorm:"type:varchar(16);not null;default:'open';index" json:"fund_type"`
	FundraisingStart *time.Time      `json:"fundraising_start,omitempty"`
	FundraisingEnd   *time.Time      `json:"fundraising_end,omitempty"`
	MaturityDate     *time.Time      `json:"maturity_date,omitempty"`
	TargetAmountRSD  decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"target_amount_rsd"`
	FundStatus       FundStatus      `gorm:"type:varchar(16);not null;default:'open';index" json:"fund_status"`
	MaturityGraceEnd *time.Time      `json:"maturity_grace_end,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

// BeforeSave enforces the closed-end fund invariants:
//   - closed funds require fundraising_start < fundraising_end < maturity_date.
//   - open funds must not carry any closed-only fields.
func (f *InvestmentFund) BeforeSave(*gorm.DB) error {
	switch f.FundType {
	case "", FundTypeOpen:
		if f.FundraisingStart != nil || f.FundraisingEnd != nil || f.MaturityDate != nil || f.MaturityGraceEnd != nil {
			return errors.New("open funds must not have closed-only date fields")
		}
		if f.FundType == "" {
			f.FundType = FundTypeOpen
		}
		if f.FundStatus == "" {
			f.FundStatus = FundStatusOpen
		}
	case FundTypeClosed:
		if f.FundraisingStart == nil || f.FundraisingEnd == nil || f.MaturityDate == nil {
			return errors.New("closed funds require fundraising_start, fundraising_end, maturity_date")
		}
		if !f.FundraisingStart.Before(*f.FundraisingEnd) {
			return errors.New("fundraising_start must precede fundraising_end")
		}
		if !f.FundraisingEnd.Before(*f.MaturityDate) {
			return errors.New("fundraising_end must precede maturity_date")
		}
		if f.TargetAmountRSD.Sign() <= 0 {
			return errors.New("closed funds require positive target_amount_rsd")
		}
	default:
		return errors.New("unknown fund_type")
	}
	return nil
}

func (f *InvestmentFund) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", f.Version)
	}
	f.Version++
	return nil
}

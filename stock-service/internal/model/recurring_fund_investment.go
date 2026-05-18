package model

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// RecurringFundInvestment is a monthly Dollar-Cost-Averaging template:
// every month on DayOfMonth, the cron runs the client's invest flow into
// FundID with the configured AmountRSD from SourceAccountID. Skip-on-
// insufficient-funds notifies the client and advances NextRun without
// cancelling the recurrence.
type RecurringFundInvestment struct {
	ID              uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID        uint64          `gorm:"not null;index" json:"client_id"`
	FundID          uint64          `gorm:"not null;index" json:"fund_id"`
	AmountRSD       decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"amount_rsd"`
	SourceAccountID uint64          `gorm:"not null" json:"source_account_id"`
	DayOfMonth      int             `gorm:"not null" json:"day_of_month"` // 1..28
	Active          bool            `gorm:"not null;default:true;index" json:"active"`
	LastRun         *time.Time      `json:"last_run,omitempty"`
	NextRun         time.Time       `gorm:"not null;index" json:"next_run"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Version         int64           `gorm:"not null;default:0" json:"-"`
}

func (r *RecurringFundInvestment) BeforeSave(*gorm.DB) error {
	if r.AmountRSD.Sign() <= 0 {
		return errors.New("amount_rsd must be positive")
	}
	if r.DayOfMonth < 1 || r.DayOfMonth > 28 {
		return errors.New("day_of_month must be 1..28")
	}
	return nil
}

func (r *RecurringFundInvestment) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", r.Version)
	}
	r.Version++
	return nil
}

// AdvanceNextRun returns the next-month execution date clamped to
// DayOfMonth (which is already capped at 28 by validation).
func (r *RecurringFundInvestment) AdvanceNextRun(from time.Time) time.Time {
	from = from.UTC()
	return time.Date(from.Year(), from.Month()+1, r.DayOfMonth, 0, 0, 0, 0, time.UTC)
}

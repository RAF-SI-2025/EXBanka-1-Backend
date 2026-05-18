package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	FundDirectionInvest = "invest"
	FundDirectionRedeem = "redeem"

	FundContributionStatusPending   = "pending"
	FundContributionStatusCompleted = "completed"
	FundContributionStatusFailed    = "failed"
)

// FundContribution is one invest or redeem event. Append-mostly: status
// transitions pending → completed | failed under the saga that produced it.
// FX details captured for audit. saga_id links to saga_logs.
type FundContribution struct {
	ID                      uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID                  uint64           `gorm:"not null;index:ix_contrib_fund" json:"fund_id"`
	OwnerType               OwnerType        `gorm:"size:8;not null;index:ix_contrib_owner,priority:1;check:owner_type IN ('client','bank')" json:"owner_type"`
	OwnerID                 *uint64          `gorm:"index:ix_contrib_owner,priority:2" json:"owner_id"`
	ActingEmployeeID        *uint64          `gorm:"index" json:"acting_employee_id,omitempty"`
	Direction               string           `gorm:"size:8;not null" json:"direction"`
	AmountNative            decimal.Decimal  `gorm:"type:numeric(20,4);not null" json:"amount_native"`
	NativeCurrency          string           `gorm:"size:8;not null" json:"native_currency"`
	AmountRSD               decimal.Decimal  `gorm:"type:numeric(20,4);not null" json:"amount_rsd"`
	FxRate                  *decimal.Decimal `gorm:"type:numeric(20,10)" json:"fx_rate,omitempty"`
	FeeRSD                  decimal.Decimal  `gorm:"type:numeric(20,4);not null;default:0" json:"fee_rsd"`
	SourceOrTargetAccountID uint64           `gorm:"not null" json:"source_or_target_account_id"`
	SagaID                  string           `gorm:"size:36;not null;index:ix_contrib_saga" json:"saga_id"`
	Status                  string           `gorm:"size:12;not null" json:"status"`
	CreatedAt               time.Time        `json:"created_at"`
	UpdatedAt               time.Time        `json:"updated_at"`
}

func (c *FundContribution) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(c.OwnerType, c.OwnerID)
}

package model

import (
	"time"

	"github.com/shopspring/decimal"
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
	UserID                  uint64           `gorm:"not null;index:ix_contrib_user,priority:1" json:"user_id"`
	SystemType              string           `gorm:"size:10;not null;index:ix_contrib_user,priority:2" json:"system_type"`
	Direction               string           `gorm:"size:8;not null" json:"direction"`
	AmountNative            decimal.Decimal  `gorm:"type:numeric(20,4);not null" json:"amount_native"`
	NativeCurrency          string           `gorm:"size:8;not null" json:"native_currency"`
	AmountRSD               decimal.Decimal  `gorm:"type:numeric(20,4);not null" json:"amount_rsd"`
	FxRate                  *decimal.Decimal `gorm:"type:numeric(20,10)" json:"fx_rate,omitempty"`
	FeeRSD                  decimal.Decimal  `gorm:"type:numeric(20,4);not null;default:0" json:"fee_rsd"`
	SourceOrTargetAccountID uint64           `gorm:"not null" json:"source_or_target_account_id"`
	SagaID                  uint64           `gorm:"not null;index:ix_contrib_saga" json:"saga_id"`
	Status                  string           `gorm:"size:12;not null" json:"status"`
	CreatedAt               time.Time        `json:"created_at"`
	UpdatedAt               time.Time        `json:"updated_at"`
}

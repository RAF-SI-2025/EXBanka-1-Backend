package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// FundDividendPayment records the dividend flow into a fund from a DividendPayment
// on a security the fund holds. It also stores a JSONB snapshot of each investor's
// pro-rata share at the time of payout so that composeFundPosition can compute
// dividends_received_rsd historically.
//
// The per_investor_snapshot column is a JSON array of objects:
//
//	[{"investor_owner_type":"client","investor_owner_id":42,"pct_at_payment":"35.00","gross_share_rsd":"175.00"}]
//
// UNIQUE (dividend_payment_id, fund_id) prevents duplicate rows.
type FundDividendPayment struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	DividendPaymentID uint64          `gorm:"not null;uniqueIndex:idx_fdp_uniq"`
	FundID            uint64          `gorm:"not null;uniqueIndex:idx_fdp_uniq"`
	AmountRSD         decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	// PerInvestorSnapshot is a JSONB column holding a JSON-encoded
	// []InvestorShareSnapshot slice serialised by DividendService.Payout.
	PerInvestorSnapshot string `gorm:"type:jsonb;not null;default:'[]'"`
	CreatedAt           time.Time
}

// InvestorShareSnapshot is the per-investor element stored in PerInvestorSnapshot.
// It is NOT a GORM model; it is serialised/deserialised as JSON by the service layer.
type InvestorShareSnapshot struct {
	InvestorOwnerType string  `json:"investor_owner_type"`
	InvestorOwnerID   *uint64 `json:"investor_owner_id,omitempty"`
	PctAtPayment      string  `json:"pct_at_payment"`  // decimal as string
	GrossShareRSD     string  `json:"gross_share_rsd"` // decimal as string
}

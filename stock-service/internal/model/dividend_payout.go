package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// DividendPayout records the per-holding credit from a DividendPayment.
// One row per (dividend_payment_id, holding_id); idempotency_key is UNIQUE so
// double-payout attempts are safely rejected.
//
// Tax treatment:
//   - holding_owner_type == "client": tax_amount_rsd = 15 % of gross; net = 85 %.
//   - holding_owner_type == "bank": no tax; tax_amount_rsd = 0, net = gross.
//   - holding_owner_type == "investment_fund": no tax at payout time (deferred);
//     a FundDividendPayment snapshot is written instead.
type DividendPayout struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	DividendPaymentID uint64          `gorm:"not null;index"`
	HoldingOwnerType  string          `gorm:"size:20;not null"` // client | bank | investment_fund
	HoldingOwnerID    *uint64         // nullable for bank
	HoldingID         uint64          `gorm:"not null"`
	Shares            int64           `gorm:"not null"`
	GrossAmountRSD    decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	TaxAmountRSD      decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0"`
	NetAmountRSD      decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	CreditedAccountID uint64          `gorm:"not null"`
	// "dividend-<payment_id>-<holding_id>" — unique to prevent double-payout
	IdempotencyKey string    `gorm:"size:128;not null;uniqueIndex"`
	CreatedAt      time.Time
}

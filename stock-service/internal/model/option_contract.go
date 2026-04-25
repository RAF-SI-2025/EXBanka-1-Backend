package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OptionContractStatusActive    = "ACTIVE"
	OptionContractStatusExercised = "EXERCISED"
	OptionContractStatusExpired   = "EXPIRED"
	OptionContractStatusFailed    = "FAILED"
)

// OptionContract is the executed option that the premium-payment saga
// produces from an OTCOffer. quantity, strike_price, and settlement_date are
// snapshotted from the final accepted revision.
type OptionContract struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID          uint64          `gorm:"not null;uniqueIndex" json:"offer_id"`
	BuyerUserID      int64           `gorm:"not null;index:ix_oc_buyer,priority:1" json:"buyer_user_id"`
	BuyerSystemType  string          `gorm:"size:10;not null;index:ix_oc_buyer,priority:2" json:"buyer_system_type"`
	BuyerBankCode    *string         `gorm:"size:32" json:"buyer_bank_code,omitempty"`
	SellerUserID     int64           `gorm:"not null;index:ix_oc_seller,priority:1" json:"seller_user_id"`
	SellerSystemType string          `gorm:"size:10;not null;index:ix_oc_seller,priority:2" json:"seller_system_type"`
	SellerBankCode   *string         `gorm:"size:32" json:"seller_bank_code,omitempty"`
	StockID          uint64          `gorm:"not null;index" json:"stock_id"`
	Quantity         decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice      decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	PremiumPaid      decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium_paid"`
	PremiumCurrency  string          `gorm:"size:8;not null" json:"premium_currency"`
	StrikeCurrency   string          `gorm:"size:8;not null" json:"strike_currency"`
	SettlementDate   time.Time       `gorm:"type:date;not null;index:ix_oc_settle" json:"settlement_date"`
	Status           string          `gorm:"size:16;not null;index:ix_oc_buyer,priority:3;index:ix_oc_seller,priority:3" json:"status"`
	SagaID           string          `gorm:"size:64;not null" json:"saga_id"`
	PremiumPaidAt    time.Time       `gorm:"not null" json:"premium_paid_at"`
	ExercisedAt      *time.Time      `json:"exercised_at,omitempty"`
	ExpiredAt        *time.Time      `json:"expired_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `gorm:"not null;default:0" json:"-"`
}

func (c *OptionContract) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", c.Version)
	}
	c.Version++
	return nil
}

func (c *OptionContract) IsTerminal() bool {
	switch c.Status {
	case OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed:
		return true
	}
	return false
}

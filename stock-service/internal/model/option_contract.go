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
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID           uint64    `gorm:"not null;uniqueIndex" json:"offer_id"`
	BuyerOwnerType    OwnerType `gorm:"size:8;not null;index:ix_oc_buyer,priority:1;check:buyer_owner_type IN ('client','bank')" json:"buyer_owner_type"`
	BuyerOwnerID      *uint64   `gorm:"index:ix_oc_buyer,priority:2" json:"buyer_owner_id,omitempty"`
	BuyerBankCode     *string   `gorm:"size:32" json:"buyer_bank_code,omitempty"`
	SellerOwnerType   OwnerType `gorm:"size:8;not null;index:ix_oc_seller,priority:1;check:seller_owner_type IN ('client','bank')" json:"seller_owner_type"`
	SellerOwnerID     *uint64   `gorm:"index:ix_oc_seller,priority:2" json:"seller_owner_id,omitempty"`
	SellerBankCode    *string   `gorm:"size:32" json:"seller_bank_code,omitempty"`
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
	// Cross-bank saga linkage (Spec 4 / Celina 5). CrossbankTxID is set on
	// accept; CrossbankExerciseTxID is set on cross-bank exercise.
	CrossbankTxID         *string `gorm:"size:36;index" json:"crossbank_tx_id,omitempty"`
	CrossbankExerciseTxID *string `gorm:"size:36;index" json:"crossbank_exercise_tx_id,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `gorm:"not null;default:0" json:"-"`
}

func (c *OptionContract) BeforeSave(tx *gorm.DB) error {
	if err := ValidateOwner(c.BuyerOwnerType, c.BuyerOwnerID); err != nil {
		return err
	}
	return ValidateOwner(c.SellerOwnerType, c.SellerOwnerID)
}

func (c *OptionContract) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", c.Version)
	}
	c.Version++
	return nil
}

// IsCrossBank reports whether buyer and seller live on different banks.
// Empty bank codes mean same-bank (legacy / pre-Spec-4 rows).
func (c *OptionContract) IsCrossBank() bool {
	bb, sb := "", ""
	if c.BuyerBankCode != nil {
		bb = *c.BuyerBankCode
	}
	if c.SellerBankCode != nil {
		sb = *c.SellerBankCode
	}
	return bb != "" && sb != "" && bb != sb
}

func (c *OptionContract) IsTerminal() bool {
	switch c.Status {
	case OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed:
		return true
	}
	return false
}

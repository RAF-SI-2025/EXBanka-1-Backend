package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// CapitalGain records a realized gain or loss from a sell transaction.
// Used for computing monthly capital gains tax (15%).
type CapitalGain struct {
	ID                 uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID             uint64          `gorm:"not null;index:idx_cg_user_month" json:"user_id"`
	SystemType         string          `gorm:"size:10;not null" json:"system_type"`
	OrderTransactionID uint64          `gorm:"not null" json:"order_transaction_id"` // 0 for OTC
	OTC                bool            `gorm:"not null;default:false" json:"otc"`
	SecurityType       string          `gorm:"size:10;not null" json:"security_type"`
	Ticker             string          `gorm:"size:30;not null" json:"ticker"`
	Quantity           int64           `gorm:"not null" json:"quantity"`
	BuyPricePerUnit    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"buy_price_per_unit"`
	SellPricePerUnit   decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"sell_price_per_unit"`
	TotalGain          decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"total_gain"` // can be negative
	Currency           string          `gorm:"size:3;not null" json:"currency"`
	AccountID          uint64          `gorm:"not null" json:"account_id"`
	TaxYear            int             `gorm:"not null;index:idx_cg_user_month" json:"tax_year"`
	TaxMonth           int             `gorm:"not null;index:idx_cg_user_month" json:"tax_month"`
	// TaxCollectionID links this gain to the TaxCollection row that taxed it.
	// NULL means "not yet taxed" — CollectTax only sums rows where this is NULL
	// so incremental admin collections within the same month tax only the new,
	// uncollected profit.
	TaxCollectionID *uint64   `gorm:"index:idx_cg_tax_collection" json:"tax_collection_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

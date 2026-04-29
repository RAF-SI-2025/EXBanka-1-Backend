package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// OrderTransaction records one executed portion of an order.
// An order may have multiple transactions (partial fills).
type OrderTransaction struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID      uint64          `gorm:"not null;index" json:"order_id"`
	Order        Order           `gorm:"foreignKey:OrderID" json:"-"`
	Quantity     int64           `gorm:"not null" json:"quantity"`
	PricePerUnit decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
	TotalPrice   decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"total_price"`
	// Currency-conversion audit fields populated during fill-saga convert_amount
	// step. NativeAmount mirrors TotalPrice for same-currency fills; for
	// cross-currency fills ConvertedAmount holds the account-currency equivalent
	// and FxRate records the rate used at settle time.
	NativeAmount    *decimal.Decimal `gorm:"type:numeric(18,4)" json:"native_amount,omitempty"`
	NativeCurrency  string           `gorm:"size:3" json:"native_currency,omitempty"`
	ConvertedAmount *decimal.Decimal `gorm:"type:numeric(18,4)" json:"converted_amount,omitempty"`
	AccountCurrency string           `gorm:"size:3" json:"account_currency,omitempty"`
	FxRate          *decimal.Decimal `gorm:"type:numeric(18,8)" json:"fx_rate,omitempty"`
	ExecutedAt      time.Time        `gorm:"not null" json:"executed_at"`
}

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
	ExecutedAt   time.Time       `gorm:"not null" json:"executed_at"`
}

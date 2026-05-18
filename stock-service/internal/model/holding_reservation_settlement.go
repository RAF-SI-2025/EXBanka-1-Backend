package model

import "time"

// HoldingReservationSettlement is one row per partial sell fill. Immutable.
// OrderTransactionID is the idempotency key.
type HoldingReservationSettlement struct {
	ID                   uint64    `gorm:"primaryKey" json:"id"`
	HoldingReservationID uint64    `gorm:"not null;index" json:"holding_reservation_id"`
	OrderTransactionID   uint64    `gorm:"not null;uniqueIndex" json:"order_transaction_id"`
	Quantity             int64     `gorm:"not null" json:"quantity"`
	CreatedAt            time.Time `json:"created_at"`
}

func (HoldingReservationSettlement) TableName() string { return "holding_reservation_settlements" }

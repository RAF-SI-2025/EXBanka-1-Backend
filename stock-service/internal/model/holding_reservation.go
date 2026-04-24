package model

import (
	"time"

	"gorm.io/gorm"
)

// Holding-reservation statuses mirror account-reservation status constants.
const (
	HoldingReservationStatusActive   = "active"
	HoldingReservationStatusReleased = "released"
	HoldingReservationStatusSettled  = "settled"
)

// HoldingReservation locks a quantity of shares on a Holding for a sell
// order. Immutable except for status/version. Mirrors AccountReservation but
// quantity-based instead of amount-based.
type HoldingReservation struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	HoldingID uint64    `gorm:"not null;index" json:"holding_id"`
	OrderID   uint64    `gorm:"not null;uniqueIndex" json:"order_id"` // idempotency
	Quantity  int64     `gorm:"not null" json:"quantity"`             // IMMUTABLE
	Status    string    `gorm:"size:16;not null;index" json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

func (HoldingReservation) TableName() string { return "holding_reservations" }

// BeforeUpdate enforces optimistic locking via Version (CLAUDE.md §Concurrency).
func (h *HoldingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}

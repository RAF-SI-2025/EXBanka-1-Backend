package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Holding-reservation statuses mirror account-reservation status constants.
const (
	HoldingReservationStatusActive   = "active"
	HoldingReservationStatusReleased = "released"
	HoldingReservationStatusSettled  = "settled"
)

// HoldingReservation locks a quantity of shares on a Holding for either:
//   - a sell ORDER (legacy bank-safe-settlement flow): OrderID is set,
//     OTCContractID is nil; or
//   - an OTC option CONTRACT (Celina-4 OTC trading): OTCContractID is set,
//     OrderID is nil.
//
// Exactly one of the two FK columns is non-nil. Enforced at the model layer
// (BeforeCreate hook) and at the DB layer (CHECK constraint installed by
// AutoMigrate / explicit DDL in main.go).
type HoldingReservation struct {
	ID            uint64    `gorm:"primaryKey" json:"id"`
	HoldingID     uint64    `gorm:"not null;index" json:"holding_id"`
	OrderID       *uint64   `gorm:"uniqueIndex:ux_holding_reservation_order" json:"order_id,omitempty"`        // legacy sell-order reservations
	OTCContractID *uint64   `gorm:"uniqueIndex:ux_holding_reservation_otc_contract" json:"otc_contract_id,omitempty"` // OTC option contracts
	Quantity      int64     `gorm:"not null" json:"quantity"`             // IMMUTABLE
	Status        string    `gorm:"size:16;not null;index" json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Version       int64     `gorm:"not null;default:0" json:"-"`
}

func (HoldingReservation) TableName() string { return "holding_reservations" }

// BeforeCreate enforces the "exactly one of OrderID / OTCContractID is set"
// invariant at the model layer.
func (h *HoldingReservation) BeforeCreate(tx *gorm.DB) error {
	hasOrder := h.OrderID != nil
	hasOTC := h.OTCContractID != nil
	if hasOrder == hasOTC {
		return errors.New("holding_reservation requires exactly one of order_id or otc_contract_id")
	}
	return nil
}

// BeforeUpdate enforces optimistic locking via Version (CLAUDE.md §Concurrency).
func (h *HoldingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}

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

// HoldingReservation locks a quantity of shares on a Holding for one of:
//   - a sell ORDER (legacy bank-safe-settlement flow): OrderID is set; or
//   - an intra-bank OTC option CONTRACT (Celina-4 OTC trading): OTCContractID
//     is set; or
//   - a cross-bank OTC option CONTRACT (Celina-5 SI-TX, seller's bank
//     side): PeerOptionContractID is set, referencing peer_option_contracts; or
//   - a cross-bank OTC NEW_TX vote-time hold (Celina-5 SI-TX, seller's bank
//     side, before the contract row exists): CrossbankTxID is set to the SI-TX
//     "<peerCode>:<idem>" identity. At COMMIT_TX the reservation is linked to
//     the freshly-minted peer_option_contracts row by also setting
//     PeerOptionContractID (so the existing consume/release-by-contract-id
//     settlement path keeps working unchanged).
//
// Exactly one of the FK/key columns is non-nil AT CREATE TIME. (The COMMIT-time
// attach later adds PeerOptionContractID to a CrossbankTxID-keyed row, so a
// row may carry both after attach — that is intentional and only happens via
// update, which the create-time invariant below does not re-check.) Enforced at
// the model layer (BeforeCreate hook) and at the DB layer (CHECK constraint
// installed by explicit DDL in main.go).
type HoldingReservation struct {
	ID                   uint64    `gorm:"primaryKey" json:"id"`
	HoldingID            uint64    `gorm:"not null;index" json:"holding_id"`
	OrderID              *uint64   `gorm:"uniqueIndex:ux_holding_reservation_order" json:"order_id,omitempty"`                            // legacy sell-order reservations
	OTCContractID        *uint64   `gorm:"uniqueIndex:ux_holding_reservation_otc_contract" json:"otc_contract_id,omitempty"`              // intra-bank OTC option contracts
	PeerOptionContractID *uint64   `gorm:"uniqueIndex:ux_holding_reservation_peer_otc_contract" json:"peer_option_contract_id,omitempty"` // cross-bank OTC option contracts (seller side)
	CrossbankTxID        *string   `gorm:"size:160;uniqueIndex:ux_holding_reservation_crossbank_tx" json:"crossbank_tx_id,omitempty"`     // cross-bank SI-TX vote-time hold ("<peerCode>:<idem>")
	Quantity             int64     `gorm:"not null" json:"quantity"`                                                                      // IMMUTABLE
	Status               string    `gorm:"size:16;not null;index" json:"status"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	Version              int64     `gorm:"not null;default:0" json:"-"`
}

func (HoldingReservation) TableName() string { return "holding_reservations" }

// BeforeCreate enforces the "exactly one of OrderID / OTCContractID /
// PeerOptionContractID / CrossbankTxID is set" invariant at the model layer.
func (h *HoldingReservation) BeforeCreate(tx *gorm.DB) error {
	count := 0
	if h.OrderID != nil {
		count++
	}
	if h.OTCContractID != nil {
		count++
	}
	if h.PeerOptionContractID != nil {
		count++
	}
	if h.CrossbankTxID != nil {
		count++
	}
	if count != 1 {
		return errors.New("holding_reservation requires exactly one of order_id, otc_contract_id, peer_option_contract_id, or crossbank_tx_id")
	}
	return nil
}

// BeforeUpdate enforces optimistic locking via Version (CLAUDE.md §Concurrency).
func (h *HoldingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}

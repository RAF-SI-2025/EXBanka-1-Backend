package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Holding represents an owner's holding of a security.
// Holdings are aggregated per (owner_type, owner_id, security_type, security_id)
// — one row per owner per security, regardless of which account the owner
// bought from. AccountID is an optional "last-used" audit field populated by
// the most recent buy fill; it is not part of the aggregation key.
type Holding struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType     OwnerType `gorm:"size:8;not null;index:idx_holding_owner,priority:1;check:owner_type IN ('client','bank')" json:"owner_type"`
	OwnerID       *uint64   `gorm:"index:idx_holding_owner,priority:2" json:"owner_id"`
	UserFirstName string    `gorm:"size:100;not null" json:"user_first_name"`
	UserLastName  string    `gorm:"size:100;not null" json:"user_last_name"`
	SecurityType   string          `gorm:"size:10;not null" json:"security_type"` // "stock", "futures", "forex", "option"
	SecurityID     uint64          `gorm:"not null" json:"security_id"`
	ListingID      uint64          `gorm:"not null;index" json:"listing_id"`
	Ticker         string          `gorm:"size:30;not null" json:"ticker"`
	Name           string          `gorm:"size:200;not null" json:"name"`
	Quantity       int64           `gorm:"not null" json:"quantity"`
	AveragePrice   decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"average_price"`
	PublicQuantity int64           `gorm:"not null;default:0" json:"public_quantity"` // OTC: shares available for purchase
	// ReservedQuantity is the running total of units locked by active sell-side
	// HoldingReservations. AvailableQuantity = Quantity - ReservedQuantity.
	ReservedQuantity int64 `gorm:"not null;default:0" json:"reserved_quantity"`
	// AccountID is an audit pointer at the account used by the most recent
	// buy fill. Not part of the aggregation key: a single user buying the
	// same security from two accounts still produces one row. Nullable in
	// practice (zero value treated as unknown) — production always populates
	// it but tests/reservations may leave it as zero.
	AccountID      uint64          `gorm:"index" json:"account_id,omitempty"`
	Version        int64           `gorm:"not null;default:1" json:"-"`
	// SagaID and SagaStep are stamped onto rows created or updated from
	// inside a saga step (read from context.Context via
	// contract/shared/saga). They make cross-service auditing trivial:
	// SELECT * FROM holdings WHERE saga_id = '...'. Both are nullable;
	// non-saga writes (REST handlers, crons) leave them empty.
	SagaID         *string         `gorm:"size:36;index" json:"saga_id,omitempty"`
	SagaStep       *string         `gorm:"size:64" json:"saga_step,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (h *Holding) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(h.OwnerType, h.OwnerID)
}

func (h *Holding) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}

package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Reservation statuses.
const (
	ReservationStatusActive   = "active"
	ReservationStatusReleased = "released"
	ReservationStatusSettled  = "settled"
)

// AccountReservation is the idempotency + state ledger for a single order's
// hold on an account. `amount` is immutable after insert; only `status` (and
// timestamps/version) transition. Running totals live on the account row; this
// table is the source of truth for recovery.
//
// OrderID is the idempotency key: retrying ReserveFunds with the same order_id
// is a safe no-op.
type AccountReservation struct {
	ID           uint64          `gorm:"primaryKey" json:"id"`
	AccountID    uint64          `gorm:"not null;index" json:"account_id"`
	OrderID      uint64          `gorm:"not null;uniqueIndex" json:"order_id"`
	Amount       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"amount"`
	CurrencyCode string          `gorm:"size:3;not null" json:"currency_code"`
	Status       string          `gorm:"size:16;not null;index:idx_account_reservation_status" json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Version      int64           `gorm:"not null;default:0" json:"-"`
}

// TableName overrides default pluralisation.
func (AccountReservation) TableName() string { return "account_reservations" }

// BeforeUpdate enforces optimistic locking via Version column, matching the
// pattern used by Account (CLAUDE.md §Concurrency).
func (r *AccountReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}

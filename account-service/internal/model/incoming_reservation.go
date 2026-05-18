package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// IncomingReservation statuses.
const (
	IncomingReservationStatusPending   = "pending"
	IncomingReservationStatusCommitted = "committed"
	IncomingReservationStatusReleased  = "released"
)

// IncomingReservation is the credit-side analogue of AccountReservation.
//
// On HandlePrepare (receiver), one row is created per inbound inter-bank
// transaction, holding the destination account and the post-FX/post-fees
// amount the peer bank promised. HandleCommit transitions it to committed
// and writes the actual ledger entry; the receiver-timeout cron transitions
// stale rows to released without ledger impact.
//
// Stored separately from AccountReservation to avoid sign confusion in
// queries (debit vs. credit holds) and because the idempotency key shape
// differs: incoming reservations key off the inter-bank transactionId
// (string UUID), whereas debit reservations key off a numeric OrderID.
type IncomingReservation struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber  string          `gorm:"size:32;not null;index"`
	Amount         decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	Currency       string          `gorm:"size:8;not null"`
	ReservationKey string          `gorm:"size:64;not null;uniqueIndex"`
	Status         string          `gorm:"size:16;not null;index"`
	CreatedAt      time.Time       `gorm:"index"`
	UpdatedAt      time.Time
	Version        int64 `gorm:"not null;default:0"`
}

func (IncomingReservation) TableName() string { return "incoming_reservations" }

// BeforeUpdate enforces optimistic locking via the Version column.
func (r *IncomingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}

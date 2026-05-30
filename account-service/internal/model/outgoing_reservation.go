package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// OutgoingReservation statuses.
const (
	OutgoingReservationStatusPending  = "pending"
	OutgoingReservationStatusSettled  = "settled"
	OutgoingReservationStatusReleased = "released"
)

// OutgoingReservation is the debit-side, string-keyed analogue of
// IncomingReservation, used for cross-bank (SI-TX) money DEBIT legs.
//
// Lifecycle (spec: Celina-5 OTC SAGA step 1 / transfer §2):
//   - ReserveOutgoing (prepare/NEW_TX): AvailableBalance -= amount, Balance
//     unchanged; pending row. This is the HOLD — the money can't be spent
//     elsewhere but hasn't left the account yet.
//   - SettleOutgoing (COMMIT_TX): Balance -= amount (Available already reduced),
//     writes a debit ledger entry, marks settled.
//   - ReleaseOutgoing (NO vote / ROLLBACK_TX / timeout): AvailableBalance +=
//     amount, marks released. No ledger entry — the money never left.
//
// Keyed off the SI-TX per-posting idempotency tag ("<peer>:<idem>:<i>"), which
// is unique per debit leg, so multiple debit legs of one TX each get their own
// reservation. Mirrors IncomingReservation's separate-table rationale (avoids
// debit/credit sign confusion and the numeric-OrderID key shape).
type OutgoingReservation struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber  string          `gorm:"size:32;not null;index"`
	Amount         decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	Currency       string          `gorm:"size:8;not null"`
	ReservationKey string          `gorm:"size:160;not null;uniqueIndex"`
	Status         string          `gorm:"size:16;not null;index"`
	CreatedAt      time.Time       `gorm:"index"`
	UpdatedAt      time.Time
	Version        int64 `gorm:"not null;default:0"`
}

func (OutgoingReservation) TableName() string { return "outgoing_reservations" }

// BeforeUpdate enforces optimistic locking via the Version column.
func (r *OutgoingReservation) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}

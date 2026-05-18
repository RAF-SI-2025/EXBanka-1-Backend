package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// AccountReservationSettlement is one row per partial-settle call. Immutable
// once inserted. The OrderTransactionID is an idempotency key from
// stock-service's OrderTransaction.ID; stock-service never reuses these IDs
// (they are serial PKs in its own DB).
type AccountReservationSettlement struct {
	ID                 uint64          `gorm:"primaryKey" json:"id"`
	ReservationID      uint64          `gorm:"not null;index" json:"reservation_id"`
	OrderTransactionID uint64          `gorm:"not null;uniqueIndex" json:"order_transaction_id"`
	Amount             decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"amount"`
	CreatedAt          time.Time       `json:"created_at"`
}

func (AccountReservationSettlement) TableName() string { return "account_reservation_settlements" }

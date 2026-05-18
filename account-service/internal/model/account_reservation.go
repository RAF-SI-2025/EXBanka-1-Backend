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

// Known order_kind values. The full set is enumerated here so the few
// places that need to switch on kind (recovery loop, metrics) stay in
// sync with the callers. New kinds: add a const, run grep callers.
const (
	OrderKindStockOrder  = "stock_order"   // stock-service order placement / forex fill / portfolio fill
	OrderKindOTCPremium  = "otc_premium"   // OTC option accept saga (premium reservation)
	OrderKindOTCStrike   = "otc_strike"    // OTC option exercise saga (strike reservation)
	OrderKindOTCStockBuy = "otc_stock_buy" // OTC stock buy-offer cash reservation
)

// AccountReservation is the idempotency + state ledger for a single order's
// hold on an account. `amount` is immutable after insert; only `status` (and
// timestamps/version) transition. Running totals live on the account row; this
// table is the source of truth for recovery.
//
// (OrderID, OrderKind) is the idempotency key: retrying ReserveFunds with the
// same pair is a safe no-op. OrderKind discriminates callers so two different
// caller-namespaces (stock Order.ID vs OTC OptionContract.ID, both starting at
// 1) cannot collide.
type AccountReservation struct {
	ID           uint64          `gorm:"primaryKey" json:"id"`
	AccountID    uint64          `gorm:"not null;index" json:"account_id"`
	OrderID      uint64          `gorm:"not null;uniqueIndex:ux_account_reservation_order,priority:1" json:"order_id"`
	OrderKind    string          `gorm:"size:32;not null;default:'stock_order';uniqueIndex:ux_account_reservation_order,priority:2" json:"order_kind"`
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

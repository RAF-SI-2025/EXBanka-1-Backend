package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Order represents a buy or sell order for a security listing.
type Order struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType OwnerType `gorm:"size:8;not null;index:idx_order_owner,priority:1;check:owner_type IN ('client','bank')" json:"owner_type"`
	OwnerID   *uint64   `gorm:"index:idx_order_owner,priority:2" json:"owner_id"`
	ListingID uint64    `gorm:"not null;index" json:"listing_id"`
	Listing   Listing   `gorm:"foreignKey:ListingID" json:"-"`
	HoldingID *uint64   `gorm:"index" json:"holding_id"` // for sell orders, references portfolio holding
	// FundID is non-nil when the order was placed on behalf of an investment
	// fund. owner_type is then "bank" (owner_id IS NULL). Fills credit
	// fund_holdings instead of holdings.
	FundID            *uint64          `gorm:"index" json:"fund_id,omitempty"`
	SecurityType      string           `gorm:"size:10;not null" json:"security_type"`
	Ticker            string           `gorm:"size:30;not null" json:"ticker"`
	Direction         string           `gorm:"size:4;not null" json:"direction"`   // "buy" or "sell"
	OrderType         string           `gorm:"size:10;not null" json:"order_type"` // "market", "limit", "stop", "stop_limit"
	Quantity          int64            `gorm:"not null" json:"quantity"`
	ContractSize      int64            `gorm:"not null;default:1" json:"contract_size"`
	PricePerUnit      decimal.Decimal  `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
	ApproximatePrice  decimal.Decimal  `gorm:"type:numeric(18,4);not null" json:"approximate_price"`
	Commission        decimal.Decimal  `gorm:"type:numeric(18,4);not null;default:0" json:"commission"`
	LimitValue        *decimal.Decimal `gorm:"type:numeric(18,4)" json:"limit_value"`
	StopValue         *decimal.Decimal `gorm:"type:numeric(18,4)" json:"stop_value"`
	Status            string           `gorm:"size:10;not null;default:'pending';index" json:"status"` // "pending", "approved", "declined", "cancelled"
	ApprovedBy        string           `gorm:"size:100" json:"approved_by"`                            // supervisor name or "no need for approval"
	IsDone            bool             `gorm:"not null;default:false;index" json:"is_done"`
	RemainingPortions int64            `gorm:"not null" json:"remaining_portions"`
	AfterHours        bool             `gorm:"not null;default:false" json:"after_hours"`
	AllOrNone         bool             `gorm:"not null;default:false" json:"all_or_none"`
	Margin            bool             `gorm:"not null;default:false" json:"margin"`
	AccountID         uint64           `gorm:"not null" json:"account_id"`
	ActingEmployeeID  *uint64          `gorm:"index" json:"acting_employee_id,omitempty"`
	// Reservation metadata populated on placement; read on cancellation and
	// recovery. Nullable because historical orders pre-date Phase 2.
	ReservationAmount    *decimal.Decimal `gorm:"type:numeric(18,4)" json:"reservation_amount,omitempty"`
	ReservationCurrency  string           `gorm:"size:3" json:"reservation_currency,omitempty"`
	ReservationAccountID *uint64          `json:"reservation_account_id,omitempty"`
	// BaseAccountID is used by forex orders only: the user's base-currency
	// account that will be credited on fill. Must be distinct from AccountID
	// (the quote-currency account where funds are reserved).
	BaseAccountID *uint64 `json:"base_account_id,omitempty"`
	// PlacementRate is the audit snapshot of the FX rate at placement time
	// (for cross-currency securities orders). Nullable for same-currency
	// orders.
	PlacementRate *decimal.Decimal `gorm:"type:numeric(18,8)" json:"placement_rate,omitempty"`
	// SagaID links this order to its placement-saga rows in saga_logs.
	SagaID string `gorm:"size:36;index" json:"saga_id,omitempty"`
	// LimitAmountRSD is the RSD-equivalent of the reservation at placement
	// time, used for actuary limit tracking (UpdateUsedLimit on approve /
	// pro-rata refund on cancel). Nil for client orders and for employee
	// orders whose employee has no actuary limit row.
	LimitAmountRSD *decimal.Decimal `gorm:"type:numeric(18,4)" json:"limit_amount_rsd,omitempty"`
	// LimitActuaryID is the actuary_limits.id to UpdateUsedLimit against on
	// approve/cancel. Nil when LimitAmountRSD is nil.
	LimitActuaryID   *uint64   `json:"limit_actuary_id,omitempty"`
	Version          int64     `gorm:"not null;default:1" json:"-"`
	LastModification time.Time `json:"last_modification"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (o *Order) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(o.OwnerType, o.OwnerID)
}

func (o *Order) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", o.Version)
	o.Version++
	return nil
}

// IsAutoApproved returns true if this order is auto-approved.
//
// Client orders without an acting employee are auto-approved (the client
// placed it themselves). Bank orders are auto-approved (the bank is acting
// for itself; the employee placed it under their granted permissions and
// limits). Employee-on-behalf-of-client orders (OwnerType=client +
// ActingEmployeeID != nil) go through manual approval.
func (o *Order) IsAutoApproved() bool {
	if o.OwnerType == OwnerBank {
		return true
	}
	return o.OwnerType == OwnerClient && o.ActingEmployeeID == nil
}

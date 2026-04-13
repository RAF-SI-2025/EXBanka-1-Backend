package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Order represents a buy or sell order for a security listing.
type Order struct {
	ID                uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID            uint64           `gorm:"not null;index" json:"user_id"`
	SystemType        string           `gorm:"size:10;not null" json:"system_type"` // "employee" or "client"
	ListingID         uint64           `gorm:"not null;index" json:"listing_id"`
	Listing           Listing          `gorm:"foreignKey:ListingID" json:"-"`
	HoldingID         *uint64          `gorm:"index" json:"holding_id"` // for sell orders, references portfolio holding
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
	ActingEmployeeID  uint64           `gorm:"default:0" json:"acting_employee_id"`
	Version           int64            `gorm:"not null;default:1" json:"-"`
	LastModification  time.Time        `json:"last_modification"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

func (o *Order) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", o.Version)
	o.Version++
	return nil
}

// IsAutoApproved returns true if this order is auto-approved.
// Client orders and supervisor orders are auto-approved.
func (o *Order) IsAutoApproved() bool {
	return o.SystemType == "client"
}

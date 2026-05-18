// Package model — OTCStockBuyOffer is a public OTC marketplace order for
// BUYING stocks (the inverse of holding.public_quantity-backed sell offers).
// Created via POST /api/v3/me/otc/stocks {direction:"buy"}; filled by
// POST /api/v3/otc/stocks/:id/sell. The buyer's cash is reserved on the
// account-service ledger at create time (via ReserveFunds) so a successful
// seller is guaranteed to be paid at fill time.
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OTCStockBuyOfferStatusActive    = "active"    // funds reserved, fillable
	OTCStockBuyOfferStatusFilled    = "filled"    // remaining_quantity hit 0
	OTCStockBuyOfferStatusCancelled = "cancelled" // explicit cancel by buyer
	OTCStockBuyOfferStatusExpired   = "expired"   // cron-aged out (future use)
)

// OTCStockBuyOffer is a standing offer to buy N shares of a stock at a given
// price. Cash is held in an account-service reservation (keyed by
// AccountReservationOrderID) until either fills consume it or cancel
// releases it. PartialSettleReservation is used per-fill so partial fills
// are supported.
type OTCStockBuyOffer struct {
	ID uint64 `gorm:"primaryKey;autoIncrement" json:"id"`

	BuyerOwnerType OwnerType `gorm:"size:8;not null;index:idx_otcsbo_buyer,priority:1;check:buyer_owner_type IN ('client','bank')" json:"buyer_owner_type"`
	BuyerOwnerID   *uint64   `gorm:"index:idx_otcsbo_buyer,priority:2" json:"buyer_owner_id,omitempty"`
	BuyerFirstName string    `gorm:"size:100;not null;default:''" json:"buyer_first_name"`
	BuyerLastName  string    `gorm:"size:100;not null;default:''" json:"buyer_last_name"`

	BuyerAccountID     uint64 `gorm:"not null;index" json:"buyer_account_id"`
	BuyerAccountNumber string `gorm:"size:32;not null" json:"buyer_account_number"`

	StockID   uint64 `gorm:"not null;index:idx_otcsbo_stock" json:"stock_id"`
	ListingID uint64 `gorm:"not null" json:"listing_id"`
	Ticker    string `gorm:"size:30;not null;index:idx_otcsbo_ticker" json:"ticker"`
	Name      string `gorm:"size:200;not null;default:''" json:"name"`

	OriginalQuantity  int64 `gorm:"not null" json:"original_quantity"`
	RemainingQuantity int64 `gorm:"not null" json:"remaining_quantity"`

	PricePerUnit decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
	CurrencyCode string          `gorm:"size:3;not null" json:"currency_code"`

	ReservedAmount         decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"reserved_amount"`
	OriginalReservedAmount decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"original_reserved_amount"`

	// AccountReservationOrderID is the synthetic order-id passed to
	// account-service ReserveFunds. Allocated from
	// otc_stock_buy_offer_res_seq so it cannot collide with real
	// orders.id values used by other reservation flows.
	AccountReservationOrderID uint64 `gorm:"not null;uniqueIndex" json:"account_reservation_order_id"`

	Status           string  `gorm:"size:16;not null;index" json:"status"`
	ActingEmployeeID *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`
	SagaID           *string `gorm:"size:36;index" json:"saga_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

func (OTCStockBuyOffer) TableName() string { return "otc_stock_buy_offers" }

func (o *OTCStockBuyOffer) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(o.BuyerOwnerType, o.BuyerOwnerID)
}

func (o *OTCStockBuyOffer) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", o.Version)
	}
	o.Version++
	return nil
}

// IsTerminal reports whether the offer is in a terminal status and cannot
// be filled or modified.
func (o *OTCStockBuyOffer) IsTerminal() bool {
	switch o.Status {
	case OTCStockBuyOfferStatusFilled, OTCStockBuyOfferStatusCancelled, OTCStockBuyOfferStatusExpired:
		return true
	}
	return false
}

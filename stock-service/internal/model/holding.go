package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Holding represents a user's ownership of a security.
// Holdings are aggregated per (user_id, security_type, security_id, account_id).
type Holding struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID         uint64          `gorm:"not null;index:idx_holding_user" json:"user_id"`
	SystemType     string          `gorm:"size:10;not null" json:"system_type"` // "employee" or "client"
	UserFirstName  string          `gorm:"size:100;not null" json:"user_first_name"`
	UserLastName   string          `gorm:"size:100;not null" json:"user_last_name"`
	SecurityType   string          `gorm:"size:10;not null" json:"security_type"` // "stock", "futures", "forex", "option"
	SecurityID     uint64          `gorm:"not null" json:"security_id"`
	ListingID      uint64          `gorm:"not null;index" json:"listing_id"`
	Ticker         string          `gorm:"size:30;not null" json:"ticker"`
	Name           string          `gorm:"size:200;not null" json:"name"`
	Quantity       int64           `gorm:"not null" json:"quantity"`
	AveragePrice   decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"average_price"`
	PublicQuantity int64           `gorm:"not null;default:0" json:"public_quantity"` // OTC: shares available for purchase
	AccountID      uint64          `gorm:"not null" json:"account_id"`
	Version        int64           `gorm:"not null;default:1" json:"-"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (h *Holding) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", h.Version)
	h.Version++
	return nil
}

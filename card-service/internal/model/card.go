package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of a Card.
func (c *Card) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", c.Version)
	c.Version++
	return nil
}

type Card struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	CardNumber     string          `gorm:"uniqueIndex;not null" json:"card_number"`
	CardNumberFull string          `gorm:"not null" json:"card_number_full"`
	CVV            string          `gorm:"not null" json:"-"`
	CardType       string          `gorm:"size:20;default:'debit'" json:"card_type"`
	CardName       string          `json:"card_name"`
	CardBrand      string          `gorm:"size:20;not null" json:"card_brand"` // visa, mastercard, dinacard, amex
	AccountNumber  string          `gorm:"not null;index" json:"account_number"`
	OwnerID        uint64          `gorm:"not null" json:"owner_id"`
	OwnerType      string          `gorm:"size:20;not null" json:"owner_type"` // "client" or "authorized_person"
	CardLimit      decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000" json:"card_limit"`
	Status         string          `gorm:"size:20;default:'active'" json:"status"` // active, blocked, deactivated
	Version        int64           `gorm:"not null;default:1"`
	ExpiresAt     time.Time `json:"expires_at"`
	IsVirtual     bool      `gorm:"default:false" json:"is_virtual"`
	UsageType     string    `gorm:"size:20;default:'unlimited'" json:"usage_type"` // "single_use", "multi_use", "unlimited"
	MaxUses       int       `gorm:"default:0" json:"max_uses"`
	UsesRemaining int       `gorm:"default:0" json:"uses_remaining"`
	PinHash       string    `gorm:"size:255" json:"-"`
	PinAttempts   int       `gorm:"default:0" json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

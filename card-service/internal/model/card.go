package model

import (
	"time"

	"github.com/shopspring/decimal"
)

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
	ExpiresAt      time.Time       `json:"expires_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

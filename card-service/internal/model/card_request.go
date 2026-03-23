package model

import "time"

type CardRequest struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID      uint64    `gorm:"not null;index" json:"client_id"`
	AccountNumber string    `gorm:"not null" json:"account_number"`
	CardBrand     string    `gorm:"size:20;not null" json:"card_brand"`
	CardType      string    `gorm:"size:20;default:'debit'" json:"card_type"`
	CardName      string    `json:"card_name"`
	Status        string    `gorm:"size:20;default:'pending'" json:"status"` // pending, approved, rejected
	Reason        string    `gorm:"size:255" json:"reason,omitempty"`
	ApprovedBy    uint64    `json:"approved_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

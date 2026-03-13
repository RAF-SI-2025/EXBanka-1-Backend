package model

import "time"

type PaymentRecipient struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID      uint64    `gorm:"not null;index" json:"client_id"`
	RecipientName string    `gorm:"not null" json:"recipient_name"`
	AccountNumber string    `gorm:"not null" json:"account_number"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

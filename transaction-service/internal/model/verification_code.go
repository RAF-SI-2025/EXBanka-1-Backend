package model

import "time"

type VerificationCode struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID        uint64    `gorm:"not null;index" json:"client_id"`
	TransactionID   uint64    `gorm:"not null" json:"transaction_id"`
	TransactionType string    `gorm:"size:20" json:"transaction_type"` // payment or transfer
	Code            string    `gorm:"not null;size:6" json:"code"`
	ExpiresAt       time.Time `json:"expires_at"`
	Attempts        int       `gorm:"default:0" json:"attempts"`
	Used            bool      `gorm:"default:false" json:"used"`
}

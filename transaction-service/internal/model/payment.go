package model

import "time"

type Payment struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FromAccountNumber string    `gorm:"not null;index" json:"from_account_number"`
	ToAccountNumber   string    `gorm:"not null;index" json:"to_account_number"`
	InitialAmount     float64   `gorm:"not null" json:"initial_amount"`
	FinalAmount       float64   `gorm:"not null" json:"final_amount"`
	Commission        float64   `gorm:"default:0" json:"commission"`
	RecipientName     string    `json:"recipient_name"`
	PaymentCode       string    `gorm:"size:3" json:"payment_code"`
	ReferenceNumber   string    `json:"reference_number"`
	PaymentPurpose    string    `json:"payment_purpose"`
	Status            string    `gorm:"size:20;default:'processing'" json:"status"` // processing, completed, rejected
	Timestamp         time.Time `json:"timestamp"`
}

package model

import "time"

type Transfer struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FromAccountNumber string    `gorm:"not null;index" json:"from_account_number"`
	ToAccountNumber   string    `gorm:"not null;index" json:"to_account_number"`
	InitialAmount     float64   `gorm:"not null" json:"initial_amount"`
	FinalAmount       float64   `gorm:"not null" json:"final_amount"`
	ExchangeRate      float64   `gorm:"default:1" json:"exchange_rate"`
	Commission        float64   `gorm:"default:0" json:"commission"`
	Timestamp         time.Time `json:"timestamp"`
}

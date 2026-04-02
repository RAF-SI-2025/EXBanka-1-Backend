package model

import (
	"time"
)

// StockExchange represents a stock exchange (e.g., NYSE, NASDAQ).
type StockExchange struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string    `gorm:"not null" json:"name"`
	Acronym         string    `gorm:"uniqueIndex;not null" json:"acronym"`
	MICCode         string    `gorm:"uniqueIndex;column:mic_code;not null" json:"mic_code"`
	Polity          string    `gorm:"not null" json:"polity"`
	Currency        string    `gorm:"size:3;not null" json:"currency"`
	TimeZone        string    `gorm:"not null" json:"time_zone"`
	OpenTime        string    `gorm:"size:5;not null" json:"open_time"`
	CloseTime       string    `gorm:"size:5;not null" json:"close_time"`
	PreMarketOpen   string    `gorm:"size:5" json:"pre_market_open"`
	PostMarketClose string    `gorm:"size:5" json:"post_market_close"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

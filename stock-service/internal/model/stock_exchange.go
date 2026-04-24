package model

import (
	"time"

	"gorm.io/gorm"
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

// BeforeSave guarantees no exchange row can be persisted with a currency that
// exchange-service cannot convert. Anything outside the 8-currency set is
// coerced to USD so buy/sell orders on listings attached to this exchange do
// not trip validateCurrency at fill time.
func (e *StockExchange) BeforeSave(tx *gorm.DB) error {
	e.Currency = NormalizeCurrency(e.Currency)
	return nil
}

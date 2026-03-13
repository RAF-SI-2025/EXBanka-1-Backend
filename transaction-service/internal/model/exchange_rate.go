package model

import (
	"time"

	"gorm.io/gorm"
)

type ExchangeRate struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FromCurrency string    `gorm:"size:3;not null;index;uniqueIndex:idx_currency_pair" json:"from_currency"`
	ToCurrency   string    `gorm:"size:3;not null;index;uniqueIndex:idx_currency_pair" json:"to_currency"`
	BuyRate      float64   `gorm:"not null" json:"buy_rate"`
	SellRate     float64   `gorm:"not null" json:"sell_rate"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func SeedExchangeRates(db *gorm.DB) error {
	rates := []ExchangeRate{
		{FromCurrency: "EUR", ToCurrency: "RSD", BuyRate: 117.0, SellRate: 117.5},
		{FromCurrency: "USD", ToCurrency: "RSD", BuyRate: 108.0, SellRate: 108.5},
		{FromCurrency: "CHF", ToCurrency: "RSD", BuyRate: 120.0, SellRate: 120.5},
		{FromCurrency: "GBP", ToCurrency: "RSD", BuyRate: 135.0, SellRate: 135.5},
		{FromCurrency: "JPY", ToCurrency: "RSD", BuyRate: 0.72, SellRate: 0.73},
		{FromCurrency: "CAD", ToCurrency: "RSD", BuyRate: 79.0, SellRate: 79.5},
		{FromCurrency: "AUD", ToCurrency: "RSD", BuyRate: 71.0, SellRate: 71.5},
		{FromCurrency: "RSD", ToCurrency: "EUR", BuyRate: 0.0085, SellRate: 0.0086},
	}
	for _, r := range rates {
		db.FirstOrCreate(&r, ExchangeRate{FromCurrency: r.FromCurrency, ToCurrency: r.ToCurrency})
	}
	return nil
}

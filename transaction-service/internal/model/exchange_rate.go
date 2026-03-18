package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type ExchangeRate struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement"`
	FromCurrency string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	ToCurrency   string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	BuyRate      decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	SellRate     decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	Version      int64           `gorm:"not null;default:1"`
	UpdatedAt    time.Time
}

func SeedExchangeRates(db *gorm.DB) {
	rates := []ExchangeRate{
		{FromCurrency: "EUR", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(117.0), SellRate: decimal.NewFromFloat(117.5)},
		{FromCurrency: "USD", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(108.0), SellRate: decimal.NewFromFloat(108.5)},
		{FromCurrency: "CHF", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(120.0), SellRate: decimal.NewFromFloat(120.5)},
		{FromCurrency: "GBP", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(135.0), SellRate: decimal.NewFromFloat(135.5)},
		{FromCurrency: "JPY", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(0.72), SellRate: decimal.NewFromFloat(0.73)},
		{FromCurrency: "CAD", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(79.0), SellRate: decimal.NewFromFloat(79.5)},
		{FromCurrency: "AUD", ToCurrency: "RSD", BuyRate: decimal.NewFromFloat(71.0), SellRate: decimal.NewFromFloat(71.5)},
		{FromCurrency: "RSD", ToCurrency: "EUR", BuyRate: decimal.NewFromFloat(0.0085), SellRate: decimal.NewFromFloat(0.0086)},
	}
	for _, rate := range rates {
		db.Where("from_currency = ? AND to_currency = ?", rate.FromCurrency, rate.ToCurrency).
			FirstOrCreate(&rate)
	}
}

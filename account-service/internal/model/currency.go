package model

import "gorm.io/gorm"

type Currency struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	Code        string `gorm:"uniqueIndex;size:3;not null" json:"code"`
	Symbol      string `gorm:"size:5;not null" json:"symbol"`
	Country     string `json:"country"`
	Description string `json:"description"`
	Active      bool   `gorm:"default:true" json:"active"`
}

func SeedCurrencies(db *gorm.DB) error {
	currencies := []Currency{
		{Name: "Serbian Dinar", Code: "RSD", Symbol: "din", Country: "Serbia", Active: true},
		{Name: "Euro", Code: "EUR", Symbol: "€", Country: "Eurozone", Active: true},
		{Name: "Swiss Franc", Code: "CHF", Symbol: "Fr", Country: "Switzerland", Active: true},
		{Name: "US Dollar", Code: "USD", Symbol: "$", Country: "United States", Active: true},
		{Name: "British Pound", Code: "GBP", Symbol: "£", Country: "United Kingdom", Active: true},
		{Name: "Japanese Yen", Code: "JPY", Symbol: "¥", Country: "Japan", Active: true},
		{Name: "Canadian Dollar", Code: "CAD", Symbol: "CA$", Country: "Canada", Active: true},
		{Name: "Australian Dollar", Code: "AUD", Symbol: "A$", Country: "Australia", Active: true},
	}
	for _, c := range currencies {
		if err := db.FirstOrCreate(&c, Currency{Code: c.Code}).Error; err != nil {
			return err
		}
	}
	return nil
}

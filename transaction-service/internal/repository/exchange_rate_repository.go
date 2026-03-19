package repository

import (
	"gorm.io/gorm"

	"github.com/shopspring/decimal"

	"github.com/exbanka/transaction-service/internal/model"
)

type ExchangeRateRepository struct {
	db *gorm.DB
}

func NewExchangeRateRepository(db *gorm.DB) *ExchangeRateRepository {
	return &ExchangeRateRepository{db: db}
}

func (r *ExchangeRateRepository) List() ([]model.ExchangeRate, error) {
	var rates []model.ExchangeRate
	if err := r.db.Find(&rates).Error; err != nil {
		return nil, err
	}
	return rates, nil
}

func (r *ExchangeRateRepository) GetByPair(fromCurrency, toCurrency string) (*model.ExchangeRate, error) {
	var rate model.ExchangeRate
	if err := r.db.Where("from_currency = ? AND to_currency = ?", fromCurrency, toCurrency).First(&rate).Error; err != nil {
		return nil, err
	}
	return &rate, nil
}

func (r *ExchangeRateRepository) Upsert(from, to string, buyRate, sellRate decimal.Decimal) error {
	var existing model.ExchangeRate
	err := r.db.Where("from_currency = ? AND to_currency = ?", from, to).First(&existing).Error
	if err != nil {
		return r.db.Create(&model.ExchangeRate{
			FromCurrency: from,
			ToCurrency:   to,
			BuyRate:      buyRate,
			SellRate:     sellRate,
			Version:      1,
		}).Error
	}
	return r.db.Model(&existing).Updates(map[string]interface{}{
		"buy_rate":  buyRate,
		"sell_rate": sellRate,
		"version":   gorm.Expr("version + 1"),
	}).Error
}

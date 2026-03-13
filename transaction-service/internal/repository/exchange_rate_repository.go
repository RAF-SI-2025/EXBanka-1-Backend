package repository

import (
	"gorm.io/gorm"

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

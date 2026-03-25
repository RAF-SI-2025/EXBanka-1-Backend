package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/exchange-service/internal/model"
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

func (r *ExchangeRateRepository) GetByPair(from, to string) (*model.ExchangeRate, error) {
	var rate model.ExchangeRate
	err := r.db.Where("from_currency = ? AND to_currency = ?", from, to).First(&rate).Error
	if err != nil {
		return nil, err
	}
	return &rate, nil
}

// Upsert inserts or updates a rate pair. Version is incremented on update.
func (r *ExchangeRateRepository) Upsert(from, to string, buy, sell decimal.Decimal) error {
	now := time.Now()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ExchangeRate
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("from_currency = ? AND to_currency = ?", from, to).
			First(&existing).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&model.ExchangeRate{
				FromCurrency: from,
				ToCurrency:   to,
				BuyRate:      buy,
				SellRate:     sell,
				Version:      1,
				UpdatedAt:    now,
			}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]interface{}{
			"buy_rate":   buy,
			"sell_rate":  sell,
			"version":    existing.Version + 1,
			"updated_at": now,
		}).Error
	})
}

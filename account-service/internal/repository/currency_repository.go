package repository

import (
	"github.com/exbanka/account-service/internal/model"
	"gorm.io/gorm"
)

type CurrencyRepository struct {
	db *gorm.DB
}

func NewCurrencyRepository(db *gorm.DB) *CurrencyRepository {
	return &CurrencyRepository{db: db}
}

func (r *CurrencyRepository) List() ([]model.Currency, error) {
	var currencies []model.Currency
	if err := r.db.Where("active = ?", true).Find(&currencies).Error; err != nil {
		return nil, err
	}
	return currencies, nil
}

func (r *CurrencyRepository) GetByCode(code string) (*model.Currency, error) {
	var currency model.Currency
	if err := r.db.Where("code = ?", code).First(&currency).Error; err != nil {
		return nil, err
	}
	return &currency, nil
}

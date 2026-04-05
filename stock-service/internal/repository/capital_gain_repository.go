package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type CapitalGainRepository struct {
	db *gorm.DB
}

func NewCapitalGainRepository(db *gorm.DB) *CapitalGainRepository {
	return &CapitalGainRepository{db: db}
}

func (r *CapitalGainRepository) Create(gain *model.CapitalGain) error {
	return r.db.Create(gain).Error
}

// ListByUser returns paginated capital gain records for a user.
func (r *CapitalGainRepository) ListByUser(userID uint64, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var total int64
	r.db.Model(&model.CapitalGain{}).Where("user_id = ?", userID).Count(&total)

	var records []model.CapitalGain
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error
	return records, total, err
}

// SumByUserMonth returns capital gains grouped by (account_id, currency) for a month.
func (r *CapitalGainRepository) SumByUserMonth(userID uint64, year, month int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND tax_year = ? AND tax_month = ?", userID, year, month).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

// SumByUserYear returns capital gains grouped by (account_id, currency) for a year.
func (r *CapitalGainRepository) SumByUserYear(userID uint64, year int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND tax_year = ?", userID, year).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

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
func (r *CapitalGainRepository) ListByUser(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var total int64
	r.db.Model(&model.CapitalGain{}).Where("user_id = ? AND system_type = ?", userID, systemType).Count(&total)

	var records []model.CapitalGain
	err := r.db.Where("user_id = ? AND system_type = ?", userID, systemType).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error
	return records, total, err
}

// SumByUserMonth returns capital gains grouped by (account_id, currency) for a month.
func (r *CapitalGainRepository) SumByUserMonth(userID uint64, systemType string, year, month int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND system_type = ? AND tax_year = ? AND tax_month = ?", userID, systemType, year, month).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

// SumUncollectedByUserMonth returns capital gains grouped by (account_id,
// currency) for a month, considering ONLY rows that have not yet been tied to
// a TaxCollection (tax_collection_id IS NULL). This is the basis for
// incremental tax collection — every CollectTax run taxes only the profit
// realised since the previous run.
func (r *CapitalGainRepository) SumUncollectedByUserMonth(userID uint64, systemType string, year, month int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND system_type = ? AND tax_year = ? AND tax_month = ? AND tax_collection_id IS NULL",
			userID, systemType, year, month).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

// MarkCollected stamps every uncollected capital_gain row matching the tuple
// with the given TaxCollection ID so the next CollectTax run ignores them.
// Scoped to rows where tax_collection_id IS NULL so a crashed+retried run
// doesn't clobber a prior collection's marking.
func (r *CapitalGainRepository) MarkCollected(userID uint64, systemType string, year, month int, accountID uint64, currency string, taxCollectionID uint64) error {
	return r.db.Model(&model.CapitalGain{}).
		Where("user_id = ? AND system_type = ? AND tax_year = ? AND tax_month = ? AND account_id = ? AND currency = ? AND tax_collection_id IS NULL",
			userID, systemType, year, month, accountID, currency).
		Update("tax_collection_id", taxCollectionID).Error
}

// SumByUserYear returns capital gains grouped by (account_id, currency) for a year.
func (r *CapitalGainRepository) SumByUserYear(userID uint64, systemType string, year int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND system_type = ? AND tax_year = ?", userID, systemType, year).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

// SumByUserAllTime returns capital gains grouped by (account_id, currency)
// across every year — the lifetime realised P&L for a user.
func (r *CapitalGainRepository) SumByUserAllTime(userID uint64, systemType string) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	err := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("user_id = ? AND system_type = ?", userID, systemType).
		Group("account_id, currency").
		Find(&results).Error
	return results, err
}

// CountByUserYear returns the number of capital_gains rows (i.e. closed
// trades) a user has logged in the given calendar year.
func (r *CapitalGainRepository) CountByUserYear(userID uint64, systemType string, year int) (int64, error) {
	var count int64
	err := r.db.Model(&model.CapitalGain{}).
		Where("user_id = ? AND system_type = ? AND tax_year = ?", userID, systemType, year).
		Count(&count).Error
	return count, err
}

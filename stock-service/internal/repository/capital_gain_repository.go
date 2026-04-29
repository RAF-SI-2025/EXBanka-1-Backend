package repository

import (
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ActuaryGainRow is one (employee, currency) bucket of summed realised gains.
// Returned by SumByActingEmployee for the Celina-4 actuary-performance read.
type ActuaryGainRow struct {
	EmployeeID int64           `gorm:"column:acting_employee_id"`
	Currency   string          `gorm:"column:currency"`
	TotalGain  decimal.Decimal `gorm:"column:total_gain"`
}

type CapitalGainRepository struct {
	db *gorm.DB
}

func NewCapitalGainRepository(db *gorm.DB) *CapitalGainRepository {
	return &CapitalGainRepository{db: db}
}

func (r *CapitalGainRepository) Create(gain *model.CapitalGain) error {
	return r.db.Create(gain).Error
}

// ListByOwner returns paginated capital gain records for an owner.
func (r *CapitalGainRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var total int64
	q := r.db.Model(&model.CapitalGain{})
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var records []model.CapitalGain
	q2 := r.db.Model(&model.CapitalGain{})
	q2 = scopeOwner(q2, "owner_type", "owner_id", ownerType, ownerID)
	err := q2.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error
	return records, total, err
}

// SumByOwnerMonth returns capital gains grouped by (account_id, currency) for a month.
func (r *CapitalGainRepository) SumByOwnerMonth(ownerType model.OwnerType, ownerID *uint64, year, month int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	q := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("tax_year = ? AND tax_month = ?", year, month)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Group("account_id, currency").Find(&results).Error
	return results, err
}

// SumUncollectedByOwnerMonth returns capital gains grouped by (account_id,
// currency) for a month, considering ONLY rows that have not yet been tied to
// a TaxCollection (tax_collection_id IS NULL). This is the basis for
// incremental tax collection — every CollectTax run taxes only the profit
// realised since the previous run.
func (r *CapitalGainRepository) SumUncollectedByOwnerMonth(ownerType model.OwnerType, ownerID *uint64, year, month int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	q := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("tax_year = ? AND tax_month = ? AND tax_collection_id IS NULL", year, month)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Group("account_id, currency").Find(&results).Error
	return results, err
}

// SumByActingEmployee groups capital_gains by (acting_employee_id, currency)
// and returns one row per bucket. Rows where acting_employee_id IS NULL
// (self-trades) are excluded — those don't belong to any actuary.
func (r *CapitalGainRepository) SumByActingEmployee() ([]ActuaryGainRow, error) {
	var out []ActuaryGainRow
	err := r.db.Model(&model.CapitalGain{}).
		Select("acting_employee_id, currency, SUM(total_gain) as total_gain").
		Where("acting_employee_id IS NOT NULL").
		Group("acting_employee_id, currency").
		Scan(&out).Error
	return out, err
}

// MarkCollected stamps every uncollected capital_gain row matching the tuple
// with the given TaxCollection ID so the next CollectTax run ignores them.
// Scoped to rows where tax_collection_id IS NULL so a crashed+retried run
// doesn't clobber a prior collection's marking.
func (r *CapitalGainRepository) MarkCollected(ownerType model.OwnerType, ownerID *uint64, year, month int, accountID uint64, currency string, taxCollectionID uint64) error {
	q := r.db.Model(&model.CapitalGain{}).
		Where("tax_year = ? AND tax_month = ? AND account_id = ? AND currency = ? AND tax_collection_id IS NULL",
			year, month, accountID, currency)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	return q.Update("tax_collection_id", taxCollectionID).Error
}

// SumByOwnerYear returns capital gains grouped by (account_id, currency) for a year.
func (r *CapitalGainRepository) SumByOwnerYear(ownerType model.OwnerType, ownerID *uint64, year int) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	q := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain").
		Where("tax_year = ?", year)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Group("account_id, currency").Find(&results).Error
	return results, err
}

// SumByOwnerAllTime returns capital gains grouped by (account_id, currency)
// across every year — the lifetime realised P&L for an owner.
func (r *CapitalGainRepository) SumByOwnerAllTime(ownerType model.OwnerType, ownerID *uint64) ([]AccountGainSummary, error) {
	var results []AccountGainSummary
	q := r.db.Model(&model.CapitalGain{}).
		Select("account_id, currency, SUM(total_gain) as total_gain")
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Group("account_id, currency").Find(&results).Error
	return results, err
}

// CountByOwnerYear returns the number of capital_gains rows (i.e. closed
// trades) an owner has logged in the given calendar year.
func (r *CapitalGainRepository) CountByOwnerYear(ownerType model.OwnerType, ownerID *uint64, year int) (int64, error) {
	var count int64
	q := r.db.Model(&model.CapitalGain{}).Where("tax_year = ?", year)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Count(&count).Error
	return count, err
}

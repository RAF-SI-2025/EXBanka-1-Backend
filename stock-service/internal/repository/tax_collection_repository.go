package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

type TaxCollectionRepository struct {
	db *gorm.DB
}

func NewTaxCollectionRepository(db *gorm.DB) *TaxCollectionRepository {
	return &TaxCollectionRepository{db: db}
}

func (r *TaxCollectionRepository) Create(collection *model.TaxCollection) error {
	return r.db.Create(collection).Error
}

// SumByUserYear returns total RSD tax collected for a (user_id, system_type) in a given year.
func (r *TaxCollectionRepository) SumByUserYear(userID uint64, systemType string, year int) (decimal.Decimal, error) {
	var result struct {
		Total decimal.Decimal
	}
	err := r.db.Model(&model.TaxCollection{}).
		Select("COALESCE(SUM(tax_amount_rsd), 0) as total").
		Where("user_id = ? AND system_type = ? AND year = ?", userID, systemType, year).
		Scan(&result).Error
	return result.Total, err
}

// SumByUserMonth returns total RSD tax collected for a (user_id, system_type) in a given month.
func (r *TaxCollectionRepository) SumByUserMonth(userID uint64, systemType string, year, month int) (decimal.Decimal, error) {
	var result struct {
		Total decimal.Decimal
	}
	err := r.db.Model(&model.TaxCollection{}).
		Select("COALESCE(SUM(tax_amount_rsd), 0) as total").
		Where("user_id = ? AND system_type = ? AND year = ? AND month = ?", userID, systemType, year, month).
		Scan(&result).Error
	return result.Total, err
}

func (r *TaxCollectionRepository) GetLastCollection(userID uint64, systemType string) (*model.TaxCollection, error) {
	var tc model.TaxCollection
	err := r.db.Where("user_id = ? AND system_type = ?", userID, systemType).
		Order("collected_at DESC").
		First(&tc).Error
	if err != nil {
		return nil, err
	}
	return &tc, nil
}

// ListUsersWithGains returns users who have capital gains in the given month,
// along with their uncollected tax debt in RSD.
func (r *TaxCollectionRepository) ListUsersWithGains(year, month int, filter TaxFilter) ([]TaxUserSummary, int64, error) {
	type rawResult struct {
		UserID        uint64
		SystemType    string
		UserFirstName string
		UserLastName  string
		TotalGainRSD  decimal.Decimal
		LastCollected *time.Time
	}

	var results []rawResult
	var total int64

	// Base query: users with capital gains this month.
	// The "last collected" subquery matches on (user_id, system_type) so
	// employee/client bookkeeping stays separated when IDs collide across
	// namespaces. The holdings join is similarly keyed.
	baseQuery := r.db.Table("capital_gains cg").
		Select(`
			cg.user_id,
			cg.system_type,
			h.user_first_name,
			h.user_last_name,
			SUM(cg.total_gain) as total_gain_rsd,
			(SELECT MAX(tc.collected_at) FROM tax_collections tc WHERE tc.user_id = cg.user_id AND tc.system_type = cg.system_type) as last_collected
		`).
		Joins("LEFT JOIN holdings h ON h.user_id = cg.user_id AND h.system_type = cg.system_type AND h.id = (SELECT MIN(id) FROM holdings WHERE user_id = cg.user_id AND system_type = cg.system_type)").
		Where("cg.tax_year = ? AND cg.tax_month = ?", year, month)

	if filter.UserType != "" {
		if filter.UserType == "actuary" {
			baseQuery = baseQuery.Where("cg.system_type = 'employee'")
		} else {
			baseQuery = baseQuery.Where("cg.system_type = ?", filter.UserType)
		}
	}
	if filter.Search != "" {
		baseQuery = baseQuery.Where("(h.user_first_name ILIKE ? OR h.user_last_name ILIKE ?)",
			"%"+filter.Search+"%", "%"+filter.Search+"%")
	}

	baseQuery = baseQuery.Group("cg.user_id, cg.system_type, h.user_first_name, h.user_last_name")

	// Count distinct users
	countQuery := r.db.Table("(?) as sub", baseQuery).Select("COUNT(*)")
	if err := countQuery.Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	// Paginate
	q := baseQuery.Order("total_gain_rsd DESC")
	if filter.PageSize > 0 {
		q = q.Limit(filter.PageSize)
		if filter.Page > 1 {
			q = q.Offset((filter.Page - 1) * filter.PageSize)
		}
	}

	if err := q.Find(&results).Error; err != nil {
		return nil, 0, err
	}

	// Convert to TaxUserSummary
	summaries := make([]TaxUserSummary, len(results))
	taxRate := decimal.NewFromFloat(0.15)
	for i, r := range results {
		debt := decimal.Zero
		if r.TotalGainRSD.IsPositive() {
			debt = r.TotalGainRSD.Mul(taxRate).Round(2)
		}
		summaries[i] = TaxUserSummary{
			UserID:         r.UserID,
			SystemType:     r.SystemType,
			UserFirstName:  r.UserFirstName,
			UserLastName:   r.UserLastName,
			TotalDebtRSD:   debt,
			LastCollection: r.LastCollected,
		}
	}

	return summaries, total, nil
}

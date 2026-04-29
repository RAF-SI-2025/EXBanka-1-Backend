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

// SumByOwnerYear returns total RSD tax collected for an owner in a given year.
func (r *TaxCollectionRepository) SumByOwnerYear(ownerType model.OwnerType, ownerID *uint64, year int) (decimal.Decimal, error) {
	var result struct {
		Total decimal.Decimal
	}
	q := r.db.Model(&model.TaxCollection{}).
		Select("COALESCE(SUM(tax_amount_rsd), 0) as total").
		Where("year = ?", year)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Scan(&result).Error
	return result.Total, err
}

// SumByOwnerMonth returns total RSD tax collected for an owner in a given month.
func (r *TaxCollectionRepository) SumByOwnerMonth(ownerType model.OwnerType, ownerID *uint64, year, month int) (decimal.Decimal, error) {
	var result struct {
		Total decimal.Decimal
	}
	q := r.db.Model(&model.TaxCollection{}).
		Select("COALESCE(SUM(tax_amount_rsd), 0) as total").
		Where("year = ? AND month = ?", year, month)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Scan(&result).Error
	return result.Total, err
}

// SumByOwnerAllTime returns total RSD tax collected across every month for an
// owner. Used by the portfolio-summary endpoint so the UI can show
// lifetime-paid vs. lifetime-owed.
func (r *TaxCollectionRepository) SumByOwnerAllTime(ownerType model.OwnerType, ownerID *uint64) (decimal.Decimal, error) {
	var result struct {
		Total decimal.Decimal
	}
	q := r.db.Model(&model.TaxCollection{}).
		Select("COALESCE(SUM(tax_amount_rsd), 0) as total")
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Scan(&result).Error
	return result.Total, err
}

// CountByKey counts how many TaxCollection rows already exist for the
// (owner, year, month, account_id, currency) tuple. CollectTax uses this to
// derive an "attempt number" suffix for the account-service idempotency keys,
// so two incremental collections in the same month produce distinct keys (not
// deduped as replays) while a crash-and-retry of the same incremental batch
// produces the same key (safely deduped).
func (r *TaxCollectionRepository) CountByKey(ownerType model.OwnerType, ownerID *uint64, year, month int, accountID uint64, currency string) (int64, error) {
	var count int64
	q := r.db.Model(&model.TaxCollection{}).
		Where("year = ? AND month = ? AND account_id = ? AND currency = ?",
			year, month, accountID, currency)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Count(&count).Error
	return count, err
}

func (r *TaxCollectionRepository) GetLastCollection(ownerType model.OwnerType, ownerID *uint64) (*model.TaxCollection, error) {
	var tc model.TaxCollection
	q := r.db.Model(&model.TaxCollection{})
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Order("collected_at DESC").First(&tc).Error
	if err != nil {
		return nil, err
	}
	return &tc, nil
}

// ListByOwner returns the tax collection history for one owner in
// reverse-chronological order. Used by GET /api/v1/me/tax so a user can see
// exactly when each month's capital-gains tax was taken.
func (r *TaxCollectionRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, page, pageSize int) ([]model.TaxCollection, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	var total int64
	q := r.db.Model(&model.TaxCollection{})
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.TaxCollection
	q2 := r.db.Model(&model.TaxCollection{})
	q2 = scopeOwner(q2, "owner_type", "owner_id", ownerType, ownerID)
	err := q2.Order("collected_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListOwnersWithGains returns owners who have capital gains in the given
// month, along with their uncollected tax debt in RSD.
func (r *TaxCollectionRepository) ListOwnersWithGains(year, month int, filter TaxFilter) ([]TaxUserSummary, int64, error) {
	type rawResult struct {
		OwnerType     string
		OwnerID       *uint64
		UserFirstName string
		UserLastName  string
		TotalGainRSD  decimal.Decimal
		LastCollected *time.Time
	}

	var results []rawResult
	var total int64

	// Base query: owners with capital gains this month.
	// The "last collected" subquery matches on (owner_type, owner_id) so
	// bank/client bookkeeping stays separated. Holdings join is similarly keyed.
	baseQuery := r.db.Table("capital_gains cg").
		Select(`
			cg.owner_type,
			cg.owner_id,
			h.user_first_name,
			h.user_last_name,
			SUM(cg.total_gain) as total_gain_rsd,
			(SELECT MAX(tc.collected_at) FROM tax_collections tc WHERE tc.owner_type = cg.owner_type AND tc.owner_id IS NOT DISTINCT FROM cg.owner_id) as last_collected
		`).
		Joins("LEFT JOIN holdings h ON h.owner_type = cg.owner_type AND h.owner_id IS NOT DISTINCT FROM cg.owner_id AND h.id = (SELECT MIN(id) FROM holdings WHERE owner_type = cg.owner_type AND owner_id IS NOT DISTINCT FROM cg.owner_id)").
		Where("cg.tax_year = ? AND cg.tax_month = ? AND cg.tax_collection_id IS NULL", year, month)

	if filter.UserType != "" {
		// "actuary" filter no longer maps to system_type=employee; instead it
		// filters by acting_employee_id IS NOT NULL via post-processing.
		// For now: 'client' → owner_type=client, others ignored.
		if filter.UserType == "client" {
			baseQuery = baseQuery.Where("cg.owner_type = 'client'")
		}
	}
	if filter.Search != "" {
		baseQuery = baseQuery.Where("(h.user_first_name ILIKE ? OR h.user_last_name ILIKE ?)",
			"%"+filter.Search+"%", "%"+filter.Search+"%")
	}

	baseQuery = baseQuery.Group("cg.owner_type, cg.owner_id, h.user_first_name, h.user_last_name")

	// Count distinct owners
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
			OwnerType:      r.OwnerType,
			OwnerID:        r.OwnerID,
			UserFirstName:  r.UserFirstName,
			UserLastName:   r.UserLastName,
			TotalDebtRSD:   debt,
			LastCollection: r.LastCollected,
		}
	}

	return summaries, total, nil
}

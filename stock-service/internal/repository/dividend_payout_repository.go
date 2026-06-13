package repository

import (
	"gorm.io/gorm"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// DividendPayoutRepository provides CRUD for the dividend_payouts table.
type DividendPayoutRepository struct {
	db *gorm.DB
}

func NewDividendPayoutRepository(db *gorm.DB) *DividendPayoutRepository {
	return &DividendPayoutRepository{db: db}
}

// Create inserts a DividendPayout row. The unique index on idempotency_key
// prevents double-payout. Uses the provided transaction handle (tx) so the
// caller controls the transaction boundary.
func (r *DividendPayoutRepository) Create(tx *gorm.DB, dp *model.DividendPayout) error {
	return tx.Create(dp).Error
}

// GetByIdempotencyKey returns an existing payout by its idempotency key.
func (r *DividendPayoutRepository) GetByIdempotencyKey(key string) (*model.DividendPayout, error) {
	var dp model.DividendPayout
	if err := r.db.Where("idempotency_key = ?", key).First(&dp).Error; err != nil {
		return nil, err
	}
	return &dp, nil
}

// ListByOwner returns paginated payouts for a (holding_owner_type, holding_owner_id),
// sorted by created_at DESC.
func (r *DividendPayoutRepository) ListByOwner(ownerType string, ownerID *uint64, page, pageSize int) ([]model.DividendPayout, int64, error) {
	var rows []model.DividendPayout
	var total int64
	q := r.db.Model(&model.DividendPayout{}).Where("holding_owner_type = ? AND holding_owner_id IS NOT DISTINCT FROM ?", ownerType, ownerID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// SumNetByOwner returns the sum of net_amount_rsd for a given owner. Used by
// composeFundPosition to compute dividends_received_rsd on direct holdings.
func (r *DividendPayoutRepository) SumNetByOwner(ownerType string, ownerID *uint64) (decimal.Decimal, error) {
	var result struct{ Total decimal.Decimal }
	err := r.db.Model(&model.DividendPayout{}).
		Select("COALESCE(SUM(net_amount_rsd), 0) as total").
		Where("holding_owner_type = ? AND holding_owner_id IS NOT DISTINCT FROM ?", ownerType, ownerID).
		Scan(&result).Error
	return result.Total, err
}

// ListByPaymentID returns all payouts for a given dividend_payment_id.
func (r *DividendPayoutRepository) ListByPaymentID(paymentID uint64) ([]model.DividendPayout, error) {
	var rows []model.DividendPayout
	if err := r.db.Where("dividend_payment_id = ?", paymentID).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

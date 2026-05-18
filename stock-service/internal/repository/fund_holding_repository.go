package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type FundHoldingRepository struct {
	db *gorm.DB
}

func NewFundHoldingRepository(db *gorm.DB) *FundHoldingRepository {
	return &FundHoldingRepository{db: db}
}

// Upsert applies a buy-side weighted-average update: when the row exists,
// quantity += incoming.Quantity and average_price_rsd is recomputed.
func (r *FundHoldingRepository) Upsert(h *model.FundHolding) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fund_id"}, {Name: "security_type"}, {Name: "security_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"quantity": gorm.Expr("fund_holdings.quantity + ?", h.Quantity),
			"average_price_rsd": gorm.Expr(
				"((fund_holdings.average_price_rsd * fund_holdings.quantity) + (? * ?)) / NULLIF(fund_holdings.quantity + ?, 0)",
				h.AveragePriceRSD, h.Quantity, h.Quantity,
			),
			"version": gorm.Expr("fund_holdings.version + 1"),
		}),
	}).Create(h).Error
}

func (r *FundHoldingRepository) DecrementQuantity(holdingID uint64, q int64) error {
	res := r.db.Model(&model.FundHolding{}).
		Where("id = ? AND quantity >= ?", holdingID, q).
		Updates(map[string]interface{}{
			"quantity": gorm.Expr("quantity - ?", q),
			"version":  gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListByFundFIFO returns holdings with quantity > 0, oldest first. Used by
// the liquidation sub-saga to choose which holdings to sell.
func (r *FundHoldingRepository) ListByFundFIFO(fundID uint64) ([]model.FundHolding, error) {
	var out []model.FundHolding
	err := r.db.Where("fund_id = ? AND quantity > 0", fundID).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

func (r *FundHoldingRepository) GetByFundAndSecurity(fundID uint64, securityType string, securityID uint64) (*model.FundHolding, error) {
	var h model.FundHolding
	err := r.db.Where("fund_id = ? AND security_type = ? AND security_id = ?", fundID, securityType, securityID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &h, err
}

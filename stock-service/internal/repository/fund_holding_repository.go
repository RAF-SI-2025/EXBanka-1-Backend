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
			// CAST the two bind parameters to numeric: without an explicit
			// type Postgres sees `$n * $m` as `unknown * unknown` and fails
			// the whole prepared statement with "operator is not unique"
			// (SQLSTATE 42725) — even on the insert (no-conflict) path, since
			// the ON CONFLICT clause is parsed regardless. This silently broke
			// every fund-holding write, leaving fund holdings permanently empty.
			"average_price_rsd": gorm.Expr(
				"((fund_holdings.average_price_rsd * fund_holdings.quantity) + (CAST(? AS numeric) * CAST(? AS numeric))) / NULLIF(fund_holdings.quantity + ?, 0)",
				h.AveragePriceRSD, h.Quantity, h.Quantity,
			),
			"version": gorm.Expr("fund_holdings.version + 1"),
		}),
	}).Create(h).Error
}

func (r *FundHoldingRepository) DecrementQuantity(holdingID uint64, q int64) error {
	// SkipHooks: the FundHolding BeforeUpdate hook adds `WHERE version = ?`
	// using the (zero-value) receiver, i.e. `WHERE version = 0`, which matches
	// no row once a holding has been upserted more than once — silently
	// failing every fund sell/liquidation. The conditional `quantity >= ?` in
	// the WHERE already guards against over-decrement (the UPDATE is atomic at
	// the row level), and we bump version explicitly, so the version-check hook
	// is both unnecessary and harmful here. (CLAUDE.md: intentional
	// version-skipping bulk updates must use SkipHooks.)
	res := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.FundHolding{}).
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

// ListBySecurityID returns all fund holdings with quantity > 0 for a security.
// Used by DividendService.Payout to identify fund-owned holdings.
func (r *FundHoldingRepository) ListBySecurityID(securityID uint64) ([]model.FundHolding, error) {
	var out []model.FundHolding
	if err := r.db.Where("security_id = ? AND quantity > 0", securityID).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *FundHoldingRepository) GetByFundAndSecurity(fundID uint64, securityType string, securityID uint64) (*model.FundHolding, error) {
	var h model.FundHolding
	err := r.db.Where("fund_id = ? AND security_type = ? AND security_id = ?", fundID, securityType, securityID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &h, err
}

package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ClientFundPositionRepository struct {
	db *gorm.DB
}

func NewClientFundPositionRepository(db *gorm.DB) *ClientFundPositionRepository {
	return &ClientFundPositionRepository{db: db}
}

// IncrementContribution adds delta to TotalContributedRSD, creating the row
// if missing. Atomic ON CONFLICT upsert.
func (r *ClientFundPositionRepository) IncrementContribution(fundID uint64, ownerType model.OwnerType, ownerID *uint64, delta decimal.Decimal) error {
	row := &model.ClientFundPosition{
		FundID:              fundID,
		OwnerType:           ownerType,
		OwnerID:             ownerID,
		TotalContributedRSD: delta,
	}
	res := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fund_id"}, {Name: "owner_type"}, {Name: "owner_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"total_contributed_rsd": gorm.Expr("client_fund_positions.total_contributed_rsd + ?", delta),
			"version":               gorm.Expr("client_fund_positions.version + 1"),
		}),
	}).Create(row)
	return res.Error
}

func (r *ClientFundPositionRepository) DecrementContribution(fundID uint64, ownerType model.OwnerType, ownerID *uint64, delta decimal.Decimal) error {
	q := r.db.Model(&model.ClientFundPosition{}).Where("fund_id = ?", fundID)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	res := q.Updates(map[string]interface{}{
		"total_contributed_rsd": gorm.Expr("total_contributed_rsd - ?", delta),
		"version":               gorm.Expr("version + 1"),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *ClientFundPositionRepository) GetByFundAndOwner(fundID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.ClientFundPosition, error) {
	var p model.ClientFundPosition
	q := r.db.Where("fund_id = ?", fundID)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &p, err
}

func (r *ClientFundPositionRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64) ([]model.ClientFundPosition, error) {
	var out []model.ClientFundPosition
	q := r.db.Model(&model.ClientFundPosition{})
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Order("created_at ASC").Find(&out).Error
	return out, err
}

func (r *ClientFundPositionRepository) SumTotalContributed(fundID uint64) (decimal.Decimal, error) {
	var result struct{ Total decimal.Decimal }
	err := r.db.Model(&model.ClientFundPosition{}).
		Select("COALESCE(SUM(total_contributed_rsd), 0) AS total").
		Where("fund_id = ?", fundID).
		Scan(&result).Error
	return result.Total, err
}

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
func (r *ClientFundPositionRepository) IncrementContribution(fundID, userID uint64, systemType string, delta decimal.Decimal) error {
	row := &model.ClientFundPosition{
		FundID:              fundID,
		UserID:              userID,
		SystemType:          systemType,
		TotalContributedRSD: delta,
	}
	res := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fund_id"}, {Name: "user_id"}, {Name: "system_type"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"total_contributed_rsd": gorm.Expr("client_fund_positions.total_contributed_rsd + ?", delta),
			"version":               gorm.Expr("client_fund_positions.version + 1"),
		}),
	}).Create(row)
	return res.Error
}

func (r *ClientFundPositionRepository) DecrementContribution(fundID, userID uint64, systemType string, delta decimal.Decimal) error {
	res := r.db.Model(&model.ClientFundPosition{}).
		Where("fund_id = ? AND user_id = ? AND system_type = ?", fundID, userID, systemType).
		Updates(map[string]interface{}{
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

func (r *ClientFundPositionRepository) GetByOwner(fundID, userID uint64, systemType string) (*model.ClientFundPosition, error) {
	var p model.ClientFundPosition
	err := r.db.Where("fund_id = ? AND user_id = ? AND system_type = ?", fundID, userID, systemType).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &p, err
}

func (r *ClientFundPositionRepository) ListByOwner(userID uint64, systemType string) ([]model.ClientFundPosition, error) {
	var out []model.ClientFundPosition
	err := r.db.Where("user_id = ? AND system_type = ?", userID, systemType).
		Order("created_at ASC").Find(&out).Error
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

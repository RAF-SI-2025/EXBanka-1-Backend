package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type FundContributionRepository struct {
	db *gorm.DB
}

func NewFundContributionRepository(db *gorm.DB) *FundContributionRepository {
	return &FundContributionRepository{db: db}
}

func (r *FundContributionRepository) Create(c *model.FundContribution) error {
	return r.db.Create(c).Error
}

// UpdateStatus performs a status-only column update by id. Skips GORM hooks
// because BeforeSave on FundContribution validates (OwnerType, OwnerID),
// which would fire on the zero-value struct that db.Model(&FundContribution{})
// constructs and reject the update with "invalid owner_type". A status flip
// doesn't change ownership, so skipping hooks is correct here.
func (r *FundContributionRepository) UpdateStatus(id uint64, status string) error {
	res := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.FundContribution{}).
		Where("id = ?", id).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *FundContributionRepository) ListByFund(fundID uint64, page, pageSize int) ([]model.FundContribution, int64, error) {
	var total int64
	q := r.db.Model(&model.FundContribution{}).Where("fund_id = ?", fundID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}
	var out []model.FundContribution
	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

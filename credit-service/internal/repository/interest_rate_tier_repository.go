package repository

import (
	"github.com/exbanka/credit-service/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type InterestRateTierRepository struct {
	db *gorm.DB
}

func NewInterestRateTierRepository(db *gorm.DB) *InterestRateTierRepository {
	return &InterestRateTierRepository{db: db}
}

// FindByAmount finds the active tier where amount is between AmountFrom and AmountTo.
// If AmountTo is 0, the tier is treated as unlimited (no upper bound).
func (r *InterestRateTierRepository) FindByAmount(amount decimal.Decimal) (*model.InterestRateTier, error) {
	var tier model.InterestRateTier
	err := r.db.
		Where("active = ? AND amount_from <= ? AND (amount_to = ? OR amount_to > ?)", true, amount, decimal.Zero, amount).
		First(&tier).Error
	if err != nil {
		return nil, err
	}
	return &tier, nil
}

// ListAll returns all active tiers ordered by AmountFrom ascending.
func (r *InterestRateTierRepository) ListAll() ([]model.InterestRateTier, error) {
	var tiers []model.InterestRateTier
	if err := r.db.Where("active = ?", true).Order("amount_from ASC").Find(&tiers).Error; err != nil {
		return nil, err
	}
	return tiers, nil
}

// Count returns the total number of tiers (including inactive).
func (r *InterestRateTierRepository) Count() (int64, error) {
	var count int64
	if err := r.db.Model(&model.InterestRateTier{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *InterestRateTierRepository) Create(tier *model.InterestRateTier) error {
	return r.db.Create(tier).Error
}

func (r *InterestRateTierRepository) GetByID(id uint64) (*model.InterestRateTier, error) {
	var tier model.InterestRateTier
	if err := r.db.First(&tier, id).Error; err != nil {
		return nil, err
	}
	return &tier, nil
}

func (r *InterestRateTierRepository) Update(tier *model.InterestRateTier) error {
	return r.db.Save(tier).Error
}

func (r *InterestRateTierRepository) Delete(id uint64) error {
	return r.db.Delete(&model.InterestRateTier{}, id).Error
}

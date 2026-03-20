package repository

import (
	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

type BankMarginRepository struct {
	db *gorm.DB
}

func NewBankMarginRepository(db *gorm.DB) *BankMarginRepository {
	return &BankMarginRepository{db: db}
}

// FindByLoanType finds the active margin for the given loan type.
func (r *BankMarginRepository) FindByLoanType(loanType string) (*model.BankMargin, error) {
	var margin model.BankMargin
	if err := r.db.Where("active = ? AND loan_type = ?", true, loanType).First(&margin).Error; err != nil {
		return nil, err
	}
	return &margin, nil
}

// ListAll returns all active bank margins.
func (r *BankMarginRepository) ListAll() ([]model.BankMargin, error) {
	var margins []model.BankMargin
	if err := r.db.Where("active = ?", true).Order("loan_type ASC").Find(&margins).Error; err != nil {
		return nil, err
	}
	return margins, nil
}

// Count returns the total number of margins (including inactive).
func (r *BankMarginRepository) Count() (int64, error) {
	var count int64
	if err := r.db.Model(&model.BankMargin{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *BankMarginRepository) Create(margin *model.BankMargin) error {
	return r.db.Create(margin).Error
}

func (r *BankMarginRepository) GetByID(id uint64) (*model.BankMargin, error) {
	var margin model.BankMargin
	if err := r.db.First(&margin, id).Error; err != nil {
		return nil, err
	}
	return &margin, nil
}

func (r *BankMarginRepository) Update(margin *model.BankMargin) error {
	return r.db.Save(margin).Error
}

func (r *BankMarginRepository) Delete(id uint64) error {
	return r.db.Delete(&model.BankMargin{}, id).Error
}

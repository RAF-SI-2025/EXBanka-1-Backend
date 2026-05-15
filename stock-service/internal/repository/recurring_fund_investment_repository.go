package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type RecurringFundInvestmentRepository struct {
	db *gorm.DB
}

func NewRecurringFundInvestmentRepository(db *gorm.DB) *RecurringFundInvestmentRepository {
	return &RecurringFundInvestmentRepository{db: db}
}

func (r *RecurringFundInvestmentRepository) Create(row *model.RecurringFundInvestment) error {
	return r.db.Create(row).Error
}

func (r *RecurringFundInvestmentRepository) GetByID(id uint64) (*model.RecurringFundInvestment, error) {
	var out model.RecurringFundInvestment
	if err := r.db.First(&out, id).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *RecurringFundInvestmentRepository) Save(row *model.RecurringFundInvestment) error {
	res := r.db.Save(row)
	return CheckRowsAffected(res)
}

func (r *RecurringFundInvestmentRepository) Delete(id uint64, clientID uint64) (bool, error) {
	res := r.db.Where("id = ? AND client_id = ?", id, clientID).Delete(&model.RecurringFundInvestment{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *RecurringFundInvestmentRepository) ListByClient(clientID uint64) ([]model.RecurringFundInvestment, error) {
	var out []model.RecurringFundInvestment
	err := r.db.Where("client_id = ?", clientID).Order("created_at DESC").Find(&out).Error
	return out, err
}

func (r *RecurringFundInvestmentRepository) ListDue(now time.Time) ([]model.RecurringFundInvestment, error) {
	var out []model.RecurringFundInvestment
	err := r.db.Where("active = ? AND next_run <= ?", true, now).Find(&out).Error
	return out, err
}

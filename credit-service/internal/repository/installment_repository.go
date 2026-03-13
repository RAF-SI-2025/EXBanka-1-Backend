package repository

import (
	"time"

	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

type InstallmentRepository struct {
	db *gorm.DB
}

func NewInstallmentRepository(db *gorm.DB) *InstallmentRepository {
	return &InstallmentRepository{db: db}
}

func (r *InstallmentRepository) CreateBatch(installments []model.Installment) error {
	return r.db.Create(&installments).Error
}

func (r *InstallmentRepository) ListByLoan(loanID uint64) ([]model.Installment, error) {
	var installments []model.Installment
	if err := r.db.Where("loan_id = ?", loanID).Order("expected_date asc").Find(&installments).Error; err != nil {
		return nil, err
	}
	return installments, nil
}

func (r *InstallmentRepository) MarkPaid(id uint64) error {
	now := time.Now()
	return r.db.Model(&model.Installment{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      "paid",
			"actual_date": &now,
		}).Error
}

func (r *InstallmentRepository) MarkOverdue() error {
	return r.db.Model(&model.Installment{}).
		Where("status = ? AND expected_date < ?", "unpaid", time.Now()).
		Update("status", "overdue").Error
}

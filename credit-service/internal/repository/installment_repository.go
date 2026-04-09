package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

var ErrInstallmentAlreadyPaid = errors.New("installment already paid")
var ErrInstallmentNotFound = errors.New("installment not found")

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
	result := r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Installment{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      "paid",
			"actual_date": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Check whether the row exists at all to distinguish "not found" from "already paid"
		var inst model.Installment
		if err := r.db.Session(&gorm.Session{SkipHooks: true}).First(&inst, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInstallmentNotFound
			}
			return err
		}
		if inst.Status == "paid" {
			return ErrInstallmentAlreadyPaid
		}
		return fmt.Errorf("mark paid failed for installment %d: no rows affected (status: %s)", id, inst.Status)
	}
	return nil
}

func (r *InstallmentRepository) MarkOverdue() error {
	// SkipHooks: bulk status sweep intentionally bypasses per-row version checks.
	return r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Installment{}).
		Where("status = ? AND expected_date < ?", "unpaid", time.Now()).
		Update("status", "overdue").Error
}

// GetDueInstallments returns unpaid installments whose expected_date is today or earlier.
func (r *InstallmentRepository) GetDueInstallments() ([]model.Installment, error) {
	var installments []model.Installment
	if err := r.db.Where("status = ? AND expected_date <= ?", "unpaid", time.Now()).Find(&installments).Error; err != nil {
		return nil, err
	}
	return installments, nil
}

// CountUnpaidByLoan returns the number of unpaid installments for a given loan.
func (r *InstallmentRepository) CountUnpaidByLoan(loanID uint64) (int64, error) {
	var count int64
	err := r.db.Model(&model.Installment{}).Where("loan_id = ? AND status = ?", loanID, "unpaid").Count(&count).Error
	return count, err
}

// UpdateUnpaidByLoan updates the amount and interest_rate of all unpaid installments for a given loan.
func (r *InstallmentRepository) UpdateUnpaidByLoan(loanID uint64, newAmount, newRate interface{}) error {
	// SkipHooks: bulk update of all unpaid installments for a loan intentionally bypasses per-row version checks.
	return r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Installment{}).
		Where("loan_id = ? AND status = ?", loanID, "unpaid").
		Updates(map[string]interface{}{
			"amount":        newAmount,
			"interest_rate": newRate,
		}).Error
}

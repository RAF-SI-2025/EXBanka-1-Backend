package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/user-service/internal/model"
)

type EmployeeLimitRepository struct {
	db *gorm.DB
}

func NewEmployeeLimitRepository(db *gorm.DB) *EmployeeLimitRepository {
	return &EmployeeLimitRepository{db: db}
}

func (r *EmployeeLimitRepository) Create(limit *model.EmployeeLimit) error {
	return r.db.Create(limit).Error
}

// GetByEmployeeID returns the limit for an employee. If no record exists, it returns
// a zero-value limit (not an error).
func (r *EmployeeLimitRepository) GetByEmployeeID(employeeID int64) (*model.EmployeeLimit, error) {
	var limit model.EmployeeLimit
	err := r.db.Where("employee_id = ?", employeeID).First(&limit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.EmployeeLimit{EmployeeID: employeeID}, nil
		}
		return nil, err
	}
	return &limit, nil
}

func (r *EmployeeLimitRepository) Update(limit *model.EmployeeLimit) error {
	return r.db.Save(limit).Error
}

func (r *EmployeeLimitRepository) Delete(employeeID int64) error {
	return r.db.Where("employee_id = ?", employeeID).Delete(&model.EmployeeLimit{}).Error
}

// ResetDailyUsedLimits resets all per-employee daily usage counters to zero.
// The EmployeeLimit model currently tracks only maximum limits and has no daily usage
// fields; this method is a no-op placeholder that satisfies the interface and is ready
// for when daily usage tracking fields are added to the model.
func (r *EmployeeLimitRepository) ResetDailyUsedLimits() error {
	return nil
}

// Upsert creates or updates the limit record based on employee_id.
// Uses ON CONFLICT DO UPDATE to eliminate the TOCTOU race between SELECT and INSERT.
func (r *EmployeeLimitRepository) Upsert(limit *model.EmployeeLimit) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "employee_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"max_loan_approval_amount", "max_single_transaction",
			"max_daily_transaction", "max_client_daily_limit",
			"max_client_monthly_limit", "updated_at",
		}),
	}).Create(limit).Error
}

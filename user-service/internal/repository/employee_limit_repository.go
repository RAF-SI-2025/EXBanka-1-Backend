package repository

import (
	"errors"

	"gorm.io/gorm"

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

// Upsert creates or updates the limit record based on employee_id.
func (r *EmployeeLimitRepository) Upsert(limit *model.EmployeeLimit) error {
	var existing model.EmployeeLimit
	err := r.db.Where("employee_id = ?", limit.EmployeeID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(limit).Error
		}
		return err
	}
	limit.ID = existing.ID
	limit.CreatedAt = existing.CreatedAt
	return r.db.Save(limit).Error
}

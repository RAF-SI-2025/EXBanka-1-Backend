package repository

import (
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type EmployeeRepository struct {
	db *gorm.DB
}

func NewEmployeeRepository(db *gorm.DB) *EmployeeRepository {
	return &EmployeeRepository{db: db}
}

func (r *EmployeeRepository) Create(emp *model.Employee) error {
	return r.db.Create(emp).Error
}

func (r *EmployeeRepository) GetByID(id int64) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.First(&emp, id).Error
	return &emp, err
}

func (r *EmployeeRepository) GetByEmail(email string) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.Where("email = ?", email).First(&emp).Error
	return &emp, err
}

func (r *EmployeeRepository) Update(emp *model.Employee) error {
	return r.db.Save(emp).Error
}

func (r *EmployeeRepository) SetPassword(userID int64, passwordHash string) error {
	return r.db.Model(&model.Employee{}).Where("id = ?", userID).
		Updates(map[string]interface{}{"password_hash": passwordHash, "activated": true}).Error
}

func (r *EmployeeRepository) List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	var employees []model.Employee
	var total int64

	// Build base query with filters
	base := r.db.Model(&model.Employee{})
	if emailFilter != "" {
		base = base.Where("email ILIKE ?", "%"+emailFilter+"%")
	}
	if nameFilter != "" {
		base = base.Where("first_name ILIKE ? OR last_name ILIKE ?", "%"+nameFilter+"%", "%"+nameFilter+"%")
	}
	if positionFilter != "" {
		base = base.Where("position ILIKE ?", "%"+positionFilter+"%")
	}

	// Count with separate session to avoid query mutation
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	if err := base.Offset(offset).Limit(pageSize).Find(&employees).Error; err != nil {
		return nil, 0, err
	}
	return employees, total, nil
}

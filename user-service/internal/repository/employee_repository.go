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

func (r *EmployeeRepository) GetByJMBG(jmbg string) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.Where("jmbg = ?", jmbg).First(&emp).Error
	return &emp, err
}

func (r *EmployeeRepository) GetByIDWithRoles(id int64) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.Preload("Roles.Permissions").Preload("AdditionalPermissions").First(&emp, id).Error
	return &emp, err
}

func (r *EmployeeRepository) GetByEmailWithRoles(email string) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.Preload("Roles.Permissions").Preload("AdditionalPermissions").Where("email = ?", email).First(&emp).Error
	return &emp, err
}

func (r *EmployeeRepository) SetEmployeeRoles(employeeID int64, roles []model.Role) error {
	var emp model.Employee
	emp.ID = employeeID
	// We need to resolve roles from DB if they are passed by name only
	var resolvedRoles []model.Role
	for _, role := range roles {
		if role.ID != 0 {
			resolvedRoles = append(resolvedRoles, role)
		} else if role.Name != "" {
			var found model.Role
			if err := r.db.Where("name = ?", role.Name).First(&found).Error; err == nil {
				resolvedRoles = append(resolvedRoles, found)
			}
		}
	}
	return r.db.Model(&emp).Association("Roles").Replace(resolvedRoles)
}

func (r *EmployeeRepository) SetAdditionalPermissions(employeeID int64, perms []model.Permission) error {
	var emp model.Employee
	emp.ID = employeeID
	return r.db.Model(&emp).Association("AdditionalPermissions").Replace(perms)
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

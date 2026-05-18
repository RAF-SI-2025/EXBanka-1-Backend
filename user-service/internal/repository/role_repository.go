package repository

import (
	"fmt"

	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type RoleRepository struct {
	db *gorm.DB
}

func NewRoleRepository(db *gorm.DB) *RoleRepository {
	return &RoleRepository{db: db}
}

func (r *RoleRepository) Create(role *model.Role) error {
	return r.db.Create(role).Error
}

func (r *RoleRepository) GetByID(id int64) (*model.Role, error) {
	var role model.Role
	err := r.db.Preload("Permissions").First(&role, id).Error
	return &role, err
}

func (r *RoleRepository) GetByName(name string) (*model.Role, error) {
	var role model.Role
	err := r.db.Preload("Permissions").Where("name = ?", name).First(&role).Error
	return &role, err
}

func (r *RoleRepository) List() ([]model.Role, error) {
	var roles []model.Role
	err := r.db.Preload("Permissions").Order("name").Find(&roles).Error
	return roles, err
}

func (r *RoleRepository) Update(role *model.Role) error {
	return r.db.Save(role).Error
}

func (r *RoleRepository) SetPermissions(roleID int64, permissions []model.Permission) error {
	var role model.Role
	if err := r.db.First(&role, roleID).Error; err != nil {
		return fmt.Errorf("role %d not found: %w", roleID, err)
	}
	return r.db.Model(&role).Association("Permissions").Replace(permissions)
}

func (r *RoleRepository) Delete(id int64) error {
	var role model.Role
	role.ID = id
	if err := r.db.Model(&role).Association("Permissions").Clear(); err != nil {
		return err
	}
	return r.db.Delete(&model.Role{}, id).Error
}

func (r *RoleRepository) GetByNames(names []string) ([]model.Role, error) {
	var roles []model.Role
	if len(names) == 0 {
		return roles, nil
	}
	err := r.db.Preload("Permissions").Where("name IN ?", names).Find(&roles).Error
	return roles, err
}

// ListEmployeeIDsByRole returns every employee ID that currently holds the
// named role via the employee_roles join table. Returns a non-nil empty slice
// when the role exists but has zero employees (or doesn't exist at all).
// Order is unspecified — callers that need determinism should sort.
func (r *RoleRepository) ListEmployeeIDsByRole(roleID int64) ([]int64, error) {
	var ids []int64
	err := r.db.
		Table("employee_roles").
		Where("role_id = ?", roleID).
		Pluck("employee_id", &ids).Error
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}

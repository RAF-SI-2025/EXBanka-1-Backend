package repository

import (
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type PermissionRepository struct {
	db *gorm.DB
}

func NewPermissionRepository(db *gorm.DB) *PermissionRepository {
	return &PermissionRepository{db: db}
}

func (r *PermissionRepository) Create(p *model.Permission) error {
	return r.db.Create(p).Error
}

func (r *PermissionRepository) GetByCode(code string) (*model.Permission, error) {
	var p model.Permission
	err := r.db.Where("code = ?", code).First(&p).Error
	return &p, err
}

func (r *PermissionRepository) GetByID(id int64) (*model.Permission, error) {
	var p model.Permission
	err := r.db.First(&p, id).Error
	return &p, err
}

func (r *PermissionRepository) List() ([]model.Permission, error) {
	var perms []model.Permission
	err := r.db.Order("category, code").Find(&perms).Error
	return perms, err
}

func (r *PermissionRepository) ListByCategory(category string) ([]model.Permission, error) {
	var perms []model.Permission
	err := r.db.Where("category = ?", category).Order("code").Find(&perms).Error
	return perms, err
}

func (r *PermissionRepository) ListByCodes(codes []string) ([]model.Permission, error) {
	var perms []model.Permission
	if len(codes) == 0 {
		return perms, nil
	}
	err := r.db.Where("code IN ?", codes).Find(&perms).Error
	return perms, err
}

func (r *PermissionRepository) Delete(id int64) error {
	return r.db.Delete(&model.Permission{}, id).Error
}

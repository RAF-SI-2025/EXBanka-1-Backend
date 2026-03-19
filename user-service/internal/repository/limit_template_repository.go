package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/user-service/internal/model"
)

type LimitTemplateRepository struct {
	db *gorm.DB
}

func NewLimitTemplateRepository(db *gorm.DB) *LimitTemplateRepository {
	return &LimitTemplateRepository{db: db}
}

func (r *LimitTemplateRepository) Create(t *model.LimitTemplate) error {
	return r.db.Create(t).Error
}

func (r *LimitTemplateRepository) GetByID(id int64) (*model.LimitTemplate, error) {
	var t model.LimitTemplate
	err := r.db.First(&t, id).Error
	return &t, err
}

func (r *LimitTemplateRepository) GetByName(name string) (*model.LimitTemplate, error) {
	var t model.LimitTemplate
	err := r.db.Where("name = ?", name).First(&t).Error
	return &t, err
}

func (r *LimitTemplateRepository) List() ([]model.LimitTemplate, error) {
	var templates []model.LimitTemplate
	err := r.db.Find(&templates).Error
	return templates, err
}

func (r *LimitTemplateRepository) Update(t *model.LimitTemplate) error {
	return r.db.Save(t).Error
}

func (r *LimitTemplateRepository) Delete(id int64) error {
	return r.db.Delete(&model.LimitTemplate{}, id).Error
}

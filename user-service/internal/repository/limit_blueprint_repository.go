package repository

import (
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type LimitBlueprintRepository struct {
	db *gorm.DB
}

func NewLimitBlueprintRepository(db *gorm.DB) *LimitBlueprintRepository {
	return &LimitBlueprintRepository{db: db}
}

func (r *LimitBlueprintRepository) Create(bp *model.LimitBlueprint) error {
	return r.db.Create(bp).Error
}

func (r *LimitBlueprintRepository) GetByID(id uint64) (*model.LimitBlueprint, error) {
	var bp model.LimitBlueprint
	err := r.db.First(&bp, id).Error
	return &bp, err
}

func (r *LimitBlueprintRepository) List(bpType string) ([]model.LimitBlueprint, error) {
	var blueprints []model.LimitBlueprint
	q := r.db
	if bpType != "" {
		q = q.Where("type = ?", bpType)
	}
	err := q.Order("name ASC").Find(&blueprints).Error
	return blueprints, err
}

func (r *LimitBlueprintRepository) GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error) {
	var bp model.LimitBlueprint
	err := r.db.Where("name = ? AND type = ?", name, bpType).First(&bp).Error
	return &bp, err
}

func (r *LimitBlueprintRepository) Update(bp *model.LimitBlueprint) error {
	return r.db.Save(bp).Error
}

func (r *LimitBlueprintRepository) Delete(id uint64) error {
	return r.db.Delete(&model.LimitBlueprint{}, id).Error
}

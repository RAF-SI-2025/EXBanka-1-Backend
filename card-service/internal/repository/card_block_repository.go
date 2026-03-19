package repository

import (
	"time"

	"github.com/exbanka/card-service/internal/model"
	"gorm.io/gorm"
)

type CardBlockRepository struct {
	db *gorm.DB
}

func NewCardBlockRepository(db *gorm.DB) *CardBlockRepository {
	return &CardBlockRepository{db: db}
}

func (r *CardBlockRepository) Create(block *model.CardBlock) error {
	return r.db.Create(block).Error
}

func (r *CardBlockRepository) FindExpiredActive(now time.Time) ([]model.CardBlock, error) {
	var blocks []model.CardBlock
	err := r.db.Where("active = ? AND expires_at IS NOT NULL AND expires_at <= ?", true, now).Find(&blocks).Error
	return blocks, err
}

func (r *CardBlockRepository) Deactivate(id uint64) error {
	return r.db.Model(&model.CardBlock{}).Where("id = ?", id).Update("active", false).Error
}

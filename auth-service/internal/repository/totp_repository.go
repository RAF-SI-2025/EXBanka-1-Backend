package repository

import (
	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type TOTPRepository struct {
	db *gorm.DB
}

func NewTOTPRepository(db *gorm.DB) *TOTPRepository {
	return &TOTPRepository{db: db}
}

func (r *TOTPRepository) Create(secret *model.TOTPSecret) error {
	return r.db.Create(secret).Error
}

func (r *TOTPRepository) GetByUserID(userID int64) (*model.TOTPSecret, error) {
	var secret model.TOTPSecret
	err := r.db.Where("user_id = ?", userID).First(&secret).Error
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

func (r *TOTPRepository) Enable(userID int64) error {
	return r.db.Model(&model.TOTPSecret{}).
		Where("user_id = ?", userID).
		Update("enabled", true).Error
}

func (r *TOTPRepository) Delete(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&model.TOTPSecret{}).Error
}

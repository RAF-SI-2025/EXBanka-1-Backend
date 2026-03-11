package repository

import (
	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type TokenRepository struct {
	db *gorm.DB
}

func NewTokenRepository(db *gorm.DB) *TokenRepository {
	return &TokenRepository{db: db}
}

// Refresh tokens
func (r *TokenRepository) CreateRefreshToken(t *model.RefreshToken) error {
	return r.db.Create(t).Error
}

func (r *TokenRepository) GetRefreshToken(token string) (*model.RefreshToken, error) {
	var t model.RefreshToken
	err := r.db.Where("token = ? AND revoked = false", token).First(&t).Error
	return &t, err
}

func (r *TokenRepository) RevokeRefreshToken(token string) error {
	return r.db.Model(&model.RefreshToken{}).Where("token = ?", token).Update("revoked", true).Error
}

func (r *TokenRepository) RevokeAllForUser(userID int64) error {
	return r.db.Model(&model.RefreshToken{}).Where("user_id = ?", userID).Update("revoked", true).Error
}

// Activation tokens
func (r *TokenRepository) CreateActivationToken(t *model.ActivationToken) error {
	return r.db.Create(t).Error
}

func (r *TokenRepository) GetActivationToken(token string) (*model.ActivationToken, error) {
	var t model.ActivationToken
	err := r.db.Where("token = ? AND used = false", token).First(&t).Error
	return &t, err
}

func (r *TokenRepository) MarkActivationUsed(token string) error {
	return r.db.Model(&model.ActivationToken{}).Where("token = ?", token).Update("used", true).Error
}

// Password reset tokens
func (r *TokenRepository) CreatePasswordResetToken(t *model.PasswordResetToken) error {
	return r.db.Create(t).Error
}

func (r *TokenRepository) GetPasswordResetToken(token string) (*model.PasswordResetToken, error) {
	var t model.PasswordResetToken
	err := r.db.Where("token = ? AND used = false", token).First(&t).Error
	return &t, err
}

func (r *TokenRepository) MarkPasswordResetUsed(token string) error {
	return r.db.Model(&model.PasswordResetToken{}).Where("token = ?", token).Update("used", true).Error
}

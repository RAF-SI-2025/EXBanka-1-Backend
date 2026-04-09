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

func (r *TokenRepository) RevokeAllForAccount(accountID int64) error {
	return r.db.Model(&model.RefreshToken{}).Where("account_id = ?", accountID).Update("revoked", true).Error
}

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

// GetRefreshTokenIncludingRevoked returns a refresh token regardless of revoked status.
// Used by logout to find the session association.
func (r *TokenRepository) GetRefreshTokenIncludingRevoked(token string) (*model.RefreshToken, error) {
	var t model.RefreshToken
	err := r.db.Where("token = ?", token).First(&t).Error
	return &t, err
}

// RevokeAllTokensForSession revokes all non-revoked refresh tokens linked to a session.
func (r *TokenRepository) RevokeAllTokensForSession(sessionID int64) error {
	return r.db.Model(&model.RefreshToken{}).
		Where("session_id = ? AND revoked = false", sessionID).
		Update("revoked", true).Error
}

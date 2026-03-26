package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type LoginAttemptRepository struct {
	db *gorm.DB
}

func NewLoginAttemptRepository(db *gorm.DB) *LoginAttemptRepository {
	return &LoginAttemptRepository{db: db}
}

// RecordAttempt stores a login attempt.
func (r *LoginAttemptRepository) RecordAttempt(email, ip string, success bool) error {
	return r.db.Create(&model.LoginAttempt{
		Email:     email,
		IPAddress: ip,
		Success:   success,
		CreatedAt: time.Now(),
	}).Error
}

// CountRecentFailedAttempts returns the number of failed login attempts
// for the given email within the specified window.
func (r *LoginAttemptRepository) CountRecentFailedAttempts(email string, window time.Duration) (int64, error) {
	var count int64
	err := r.db.Model(&model.LoginAttempt{}).
		Where("email = ? AND success = false AND created_at > ?", email, time.Now().Add(-window)).
		Count(&count).Error
	return count, err
}

// LockAccount creates an account lock record.
func (r *LoginAttemptRepository) LockAccount(email string, duration time.Duration) error {
	lock := model.AccountLock{
		Email:     email,
		LockedAt:  time.Now(),
		ExpiresAt: time.Now().Add(duration),
	}
	return r.db.Create(&lock).Error
}

// GetActiveLock returns the active lock for an email, or nil if not locked.
func (r *LoginAttemptRepository) GetActiveLock(email string) (*model.AccountLock, error) {
	var lock model.AccountLock
	err := r.db.Where("email = ? AND expires_at > ? AND unlocked_at IS NULL", email, time.Now()).
		First(&lock).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lock, nil
}

// UnlockAccount manually unlocks an account by email.
func (r *LoginAttemptRepository) UnlockAccount(email string) error {
	now := time.Now()
	return r.db.Model(&model.AccountLock{}).
		Where("email = ? AND unlocked_at IS NULL", email).
		Update("unlocked_at", &now).Error
}

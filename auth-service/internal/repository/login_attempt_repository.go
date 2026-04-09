package repository

import (
	"hash/fnv"
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
func (r *LoginAttemptRepository) RecordAttempt(email, ip, userAgent, deviceType string, success bool) error {
	return r.db.Create(&model.LoginAttempt{
		Email:      email,
		IPAddress:  ip,
		UserAgent:  userAgent,
		DeviceType: deviceType,
		Success:    success,
		CreatedAt:  time.Now(),
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

// RecordFailureAndCheckLock atomically records a failed login attempt and, if the
// failure count reaches maxAttempts within window, creates an account lock.
// Returns (locked=true, remaining=0) when the lock is newly created or already active.
// Uses a PostgreSQL advisory lock on the email hash to serialize concurrent attempts.
func (r *LoginAttemptRepository) RecordFailureAndCheckLock(email, ip, userAgent, deviceType string, maxAttempts int, window, lockDuration time.Duration) (locked bool, remaining int, err error) {
	err = r.db.Transaction(func(tx *gorm.DB) error {
		// Serialize all failed-login processing for this email using a per-email advisory lock.
		h := fnv.New32a()
		h.Write([]byte(email))
		if e := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(h.Sum32())).Error; e != nil {
			return e
		}

		// Check whether a lock already exists before recording.
		var activeLock model.AccountLock
		lockErr := tx.Where("email = ? AND expires_at > ? AND unlocked_at IS NULL", email, time.Now()).
			First(&activeLock).Error
		if lockErr == nil {
			locked = true
			return nil
		}

		// Record the failed attempt inside the same TX.
		if e := tx.Create(&model.LoginAttempt{
			Email:      email,
			IPAddress:  ip,
			UserAgent:  userAgent,
			DeviceType: deviceType,
			Success:    false,
			CreatedAt:  time.Now(),
		}).Error; e != nil {
			return e
		}

		// Count recent failures (includes the one we just inserted).
		var count int64
		if e := tx.Model(&model.LoginAttempt{}).
			Where("email = ? AND success = false AND created_at > ?", email, time.Now().Add(-window)).
			Count(&count).Error; e != nil {
			return e
		}

		if int(count) >= maxAttempts {
			if e := tx.Create(&model.AccountLock{
				Email:     email,
				LockedAt:  time.Now(),
				ExpiresAt: time.Now().Add(lockDuration),
			}).Error; e != nil {
				return e
			}
			locked = true
			remaining = 0
		} else {
			remaining = maxAttempts - int(count)
		}
		return nil
	})
	return locked, remaining, err
}

// ListRecentByEmail returns the most recent login attempts for an email, ordered newest first.
func (r *LoginAttemptRepository) ListRecentByEmail(email string, limit int) ([]model.LoginAttempt, error) {
	var attempts []model.LoginAttempt
	err := r.db.Where("email = ?", email).
		Order("created_at DESC").
		Limit(limit).
		Find(&attempts).Error
	return attempts, err
}

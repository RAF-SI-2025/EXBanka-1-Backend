package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type SessionRepository struct {
	db *gorm.DB
}

func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a new active session record.
func (r *SessionRepository) Create(s *model.ActiveSession) error {
	return r.db.Create(s).Error
}

// GetByID returns a session by its primary key.
func (r *SessionRepository) GetByID(id int64) (*model.ActiveSession, error) {
	var s model.ActiveSession
	if err := r.db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListByUser returns all non-revoked sessions for a user, ordered by most recent first.
func (r *SessionRepository) ListByUser(userID int64) ([]model.ActiveSession, error) {
	var sessions []model.ActiveSession
	err := r.db.Where("user_id = ? AND revoked_at IS NULL", userID).
		Order("last_active_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// ListAllByUser returns all sessions (including revoked) for a user, ordered by creation time descending.
func (r *SessionRepository) ListAllByUser(userID int64) ([]model.ActiveSession, error) {
	var sessions []model.ActiveSession
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// UpdateLastActive updates the LastActiveAt timestamp for a session.
func (r *SessionRepository) UpdateLastActive(sessionID int64) error {
	return r.db.Model(&model.ActiveSession{}).
		Where("id = ?", sessionID).
		Update("last_active_at", time.Now()).Error
}

// Revoke sets the RevokedAt timestamp on a session.
func (r *SessionRepository) Revoke(sessionID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", &now).Error
}

// RevokeAllForUser revokes all active sessions for a given user.
func (r *SessionRepository) RevokeAllForUser(userID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}

// RevokeAllExcept revokes all active sessions for a user except the given session ID.
func (r *SessionRepository) RevokeAllExcept(userID int64, keepSessionID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND id != ? AND revoked_at IS NULL", userID, keepSessionID).
		Update("revoked_at", &now).Error
}

// RevokeByDeviceID revokes all sessions linked to a specific mobile device.
func (r *SessionRepository) RevokeByDeviceID(deviceID string) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("device_id = ? AND revoked_at IS NULL", deviceID).
		Update("revoked_at", &now).Error
}

// CountActiveByUser returns the number of non-revoked sessions for a user.
func (r *SessionRepository) CountActiveByUser(userID int64) (int64, error) {
	var count int64
	err := r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Count(&count).Error
	return count, err
}

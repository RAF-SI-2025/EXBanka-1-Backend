package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/verification-service/internal/model"
)

type VerificationChallengeRepository struct {
	db *gorm.DB
}

func NewVerificationChallengeRepository(db *gorm.DB) *VerificationChallengeRepository {
	return &VerificationChallengeRepository{db: db}
}

// Create inserts a new VerificationChallenge into the database.
func (r *VerificationChallengeRepository) Create(vc *model.VerificationChallenge) error {
	return r.db.Create(vc).Error
}

// GetByID retrieves a challenge by primary key.
func (r *VerificationChallengeRepository) GetByID(id uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.First(&vc, id).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// GetByIDForUpdate retrieves a challenge by primary key with a SELECT FOR UPDATE lock.
// Must be called within a transaction.
func (r *VerificationChallengeRepository) GetByIDForUpdate(tx *gorm.DB, id uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&vc, id).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// Save persists changes to an existing VerificationChallenge.
// The BeforeUpdate hook enforces optimistic locking.
func (r *VerificationChallengeRepository) Save(vc *model.VerificationChallenge) error {
	return r.db.Save(vc).Error
}

// SaveInTx persists changes within an existing transaction.
func (r *VerificationChallengeRepository) SaveInTx(tx *gorm.DB, vc *model.VerificationChallenge) error {
	return tx.Save(vc).Error
}

// GetPendingByUser returns the most recent pending challenge for a user+device pair.
// Used by mobile app polling to discover challenges that need attention.
func (r *VerificationChallengeRepository) GetPendingByUser(userID uint64, deviceID string) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.Where("user_id = ? AND device_id = ? AND status = ? AND expires_at > ?",
		userID, deviceID, "pending", time.Now()).
		Order("created_at DESC").
		First(&vc).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// ExpireOld transitions all pending challenges past their expiry time to "expired".
// Uses SkipHooks to bypass the optimistic locking BeforeUpdate hook since this is a
// bulk administrative operation, not a concurrent user action.
func (r *VerificationChallengeRepository) ExpireOld() (int64, error) {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.VerificationChallenge{}).
		Where("status = ? AND expires_at < ?", "pending", time.Now()).
		Update("status", "expired")
	return result.RowsAffected, result.Error
}

package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/verification-service/internal/model"
	shared "github.com/exbanka/contract/shared"
)

// ErrOptimisticLock is the typed sentinel returned when a concurrent
// modification is detected on a VerificationChallenge row.
var ErrOptimisticLock = shared.ErrOptimisticLock

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
// The BeforeUpdate hook enforces optimistic locking; RowsAffected==0 means
// a concurrent update won the race.
func (r *VerificationChallengeRepository) Save(vc *model.VerificationChallenge) error {
	saveRes := r.db.Save(vc)
	if saveRes.Error != nil {
		return saveRes.Error
	}
	if saveRes.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// SaveInTx persists changes within an existing transaction.
// The BeforeUpdate hook enforces optimistic locking; RowsAffected==0 means
// a concurrent update won the race.
func (r *VerificationChallengeRepository) SaveInTx(tx *gorm.DB, vc *model.VerificationChallenge) error {
	saveRes := tx.Save(vc)
	if saveRes.Error != nil {
		return saveRes.Error
	}
	if saveRes.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// GetPendingByUser returns the most recent pending, non-expired challenge for a user.
func (r *VerificationChallengeRepository) GetPendingByUser(userID uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.Where("user_id = ? AND status = ? AND expires_at > ?",
		userID, "pending", time.Now()).
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

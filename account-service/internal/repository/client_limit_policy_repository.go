package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
)

// ErrClientLimitPolicyNotFound is returned when a client-limit-policy row is absent.
var ErrClientLimitPolicyNotFound = errors.New("client limit policy not found")

// ClientLimitPolicyRepository manages the local client-limit read-model for
// account-service (SP-5).
type ClientLimitPolicyRepository struct{ db *gorm.DB }

// NewClientLimitPolicyRepository creates a new ClientLimitPolicyRepository
// backed by db.
func NewClientLimitPolicyRepository(db *gorm.DB) *ClientLimitPolicyRepository {
	return &ClientLimitPolicyRepository{db: db}
}

// Upsert applies an event-sourced client-limit policy only if its Version is
// strictly greater than the stored row's (monotonic; tolerates out-of-order /
// duplicate delivery). Returns applied=true iff the row was inserted or updated
// to this version, so the caller propagates to accounts only on a real change.
func (r *ClientLimitPolicyRepository) Upsert(ctx context.Context, in model.ClientLimitPolicy) (bool, error) {
	applied := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.ClientLimitPolicy
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, in.ClientID).Error
		switch {
		case errors.Is(e, gorm.ErrRecordNotFound):
			if err := tx.Create(&in).Error; err != nil {
				return err
			}
			applied = true
			return nil
		case e != nil:
			return e
		}
		if in.Version <= existing.Version {
			return nil // stale or duplicate; not applied
		}
		if err := tx.Model(&existing).Select("DailyLimit", "MonthlyLimit", "Version").Updates(&in).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// GetByClientID fetches a ClientLimitPolicy by client ID.
// Returns ErrClientLimitPolicyNotFound if absent.
func (r *ClientLimitPolicyRepository) GetByClientID(ctx context.Context, id uint64) (model.ClientLimitPolicy, error) {
	var p model.ClientLimitPolicy
	err := r.db.WithContext(ctx).First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ClientLimitPolicy{}, ErrClientLimitPolicyNotFound
	}
	return p, err
}

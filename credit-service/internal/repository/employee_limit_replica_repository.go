package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/credit-service/internal/model"
)

// ErrEmployeeLimitReplicaNotFound is returned when an employee-limit replica row is absent.
var ErrEmployeeLimitReplicaNotFound = errors.New("employee limit replica not found")

// EmployeeLimitReplicaRepository manages the local employee-limit read-model for
// credit-service (SP-2).
type EmployeeLimitReplicaRepository struct{ db *gorm.DB }

// NewEmployeeLimitReplicaRepository creates a new EmployeeLimitReplicaRepository
// backed by db.
func NewEmployeeLimitReplicaRepository(db *gorm.DB) *EmployeeLimitReplicaRepository {
	return &EmployeeLimitReplicaRepository{db: db}
}

// Upsert applies an event-sourced employee-limit snapshot only if its Version is
// strictly greater than the stored row's (monotonic; tolerates out-of-order /
// duplicate Kafka delivery). A first insert always wins. The caller MUST pass a
// full snapshot (all value columns) — the update force-writes the selected columns.
func (r *EmployeeLimitReplicaRepository) Upsert(ctx context.Context, in model.EmployeeLimitReplica) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.EmployeeLimitReplica
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, in.EmployeeID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&in).Error
		case err != nil:
			return err
		}
		if in.Version <= existing.Version {
			return nil // stale or duplicate; ignore
		}
		return tx.Model(&existing).Select(
			"MaxLoanApprovalAmount", "MaxSingleTransaction", "MaxDailyTransaction",
			"MaxClientDailyLimit", "MaxClientMonthlyLimit", "Version",
		).Updates(&in).Error
	})
}

// GetByEmployeeID fetches an EmployeeLimitReplica by employee ID.
// Returns ErrEmployeeLimitReplicaNotFound if absent.
func (r *EmployeeLimitReplicaRepository) GetByEmployeeID(ctx context.Context, id uint64) (model.EmployeeLimitReplica, error) {
	var e model.EmployeeLimitReplica
	err := r.db.WithContext(ctx).First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.EmployeeLimitReplica{}, ErrEmployeeLimitReplicaNotFound
	}
	return e, err
}

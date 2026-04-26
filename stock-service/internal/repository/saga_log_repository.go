package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type SagaLogRepository struct {
	db *gorm.DB
}

func NewSagaLogRepository(db *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: db}
}

// WithTx returns a repository bound to the given DB transaction handle.
func (r *SagaLogRepository) WithTx(tx *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: tx}
}

// RecordStep inserts a saga step row. CreatedAt/UpdatedAt are set here if zero.
func (r *SagaLogRepository) RecordStep(log *model.SagaLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	if log.UpdatedAt.IsZero() {
		log.UpdatedAt = log.CreatedAt
	}
	return r.db.Create(log).Error
}

func (r *SagaLogRepository) GetByID(id uint64) (*model.SagaLog, error) {
	var log model.SagaLog
	if err := r.db.First(&log, id).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

// ListPendingForOrder returns saga steps for an order that are in pending or
// compensating state (i.e., unfinished).
func (r *SagaLogRepository) ListPendingForOrder(orderID uint64) ([]model.SagaLog, error) {
	var out []model.SagaLog
	err := r.db.Where("order_id = ? AND status IN ?", orderID,
		[]string{model.SagaStatusPending, model.SagaStatusCompensating}).Find(&out).Error
	return out, err
}

// GetByStepName returns the most recent (highest step_number) saga row for the
// given order and step name. Used by saga recovery to check whether a step
// already completed elsewhere.
func (r *SagaLogRepository) GetByStepName(orderID uint64, stepName string) (*model.SagaLog, error) {
	var log model.SagaLog
	err := r.db.Where("order_id = ? AND step_name = ?", orderID, stepName).
		Order("step_number DESC").First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// UpdateStatus uses a WHERE version clause (manual optimistic-lock check)
// because GORM's BeforeUpdate hook only fires for full-row updates via Save.
// Updating a subset of columns via Updates+where keeps the transition atomic.
func (r *SagaLogRepository) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	result := r.db.Model(&model.SagaLog{}).
		Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{
			"status":        newStatus,
			"error_message": errMsg,
			"updated_at":    time.Now(),
			"version":       version + 1,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListStuckSagas returns saga steps in pending/compensating status older than
// the given age. Used by the startup recovery reconciler.
func (r *SagaLogRepository) ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error) {
	cutoff := time.Now().Add(-olderThan)
	var out []model.SagaLog
	err := r.db.Where("status IN ? AND updated_at < ?",
		[]string{model.SagaStatusPending, model.SagaStatusCompensating}, cutoff).
		Order("id ASC").Find(&out).Error
	return out, err
}

// IsForwardCompleted reports whether the given (saga_id, step_name)
// pair has already reached completed status on a forward step. Used by
// stocksaga.Recorder.IsCompleted for shared.Saga's restart-resume.
//
// MUST be saga-scoped: a global lookup by step_name alone would match
// ANY prior saga's completed step and incorrectly skip the current
// saga's same-named step. Every saga gets a unique saga_id (UUID), so
// scoping by saga_id is precise.
//
// stepName ignored when sagaID is empty (returns false): callers that
// don't know the saga id always re-execute every step from scratch.
func (r *SagaLogRepository) IsForwardCompleted(sagaID, stepName string) (bool, error) {
	if sagaID == "" {
		return false, nil
	}
	var count int64
	err := r.db.Model(&model.SagaLog{}).
		Where("saga_id = ? AND step_name = ? AND status = ? AND is_compensation = ?",
			sagaID, stepName, model.SagaStatusCompleted, false).
		Count(&count).Error
	return count > 0, err
}

// IncrementRetryCount bumps retry_count + updated_at on a saga row without
// changing status or version. Used by the recovery reconciler after each
// failed retry attempt so repeated failures can be detected and the row left
// alone once a threshold is exceeded.
//
// Does NOT use the BeforeUpdate version hook (UpdateColumns bypasses hooks),
// which is deliberate here: retry bookkeeping is a side-channel on the row
// and must not compete with the forward/compensation path's optimistic
// locking.
func (r *SagaLogRepository) IncrementRetryCount(id uint64) error {
	return r.db.Model(&model.SagaLog{}).
		Where("id = ?", id).
		UpdateColumns(map[string]any{
			"retry_count": gorm.Expr("retry_count + 1"),
			"updated_at":  time.Now(),
		}).Error
}

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

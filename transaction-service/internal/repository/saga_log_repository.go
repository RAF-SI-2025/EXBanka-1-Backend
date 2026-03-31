package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type SagaLogRepository struct {
	db *gorm.DB
}

func NewSagaLogRepository(db *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: db}
}

// RecordStep inserts a new saga step entry. The entry's ID is populated on success.
func (r *SagaLogRepository) RecordStep(step *model.SagaLog) error {
	return r.db.Create(step).Error
}

// CompleteStep marks a step as completed and sets its CompletedAt timestamp.
func (r *SagaLogRepository) CompleteStep(id uint64) error {
	now := time.Now()
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": "completed", "completed_at": &now}).Error
}

// FailStep marks a step as failed and stores the error message.
func (r *SagaLogRepository) FailStep(id uint64, errMsg string) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": "failed", "error_message": errMsg}).Error
}

// FindPendingCompensations returns all saga steps in "compensating" status.
// "dead_letter" entries are excluded automatically because their status differs.
func (r *SagaLogRepository) FindPendingCompensations() ([]model.SagaLog, error) {
	var logs []model.SagaLog
	err := r.db.Where("status = ?", "compensating").Find(&logs).Error
	return logs, err
}

// IncrementRetryCount atomically increments the retry_count for a compensating step.
func (r *SagaLogRepository) IncrementRetryCount(id uint64) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error
}

// MarkDeadLetter sets a saga step's status to "dead_letter" so the recovery loop
// stops retrying it. Called after RetryCount reaches the configured maximum.
func (r *SagaLogRepository) MarkDeadLetter(id uint64, errMsg string) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        "dead_letter",
			"error_message": errMsg,
		}).Error
}

// GetBySagaID returns all steps for a given saga, ordered by step number.
func (r *SagaLogRepository) GetBySagaID(sagaID string) ([]model.SagaLog, error) {
	var logs []model.SagaLog
	err := r.db.Where("saga_id = ?", sagaID).Order("step_number ASC").Find(&logs).Error
	return logs, err
}

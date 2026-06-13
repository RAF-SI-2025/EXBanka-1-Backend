package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/notification-service/internal/model"
)

// ProcessedMessageRepository tracks which Kafka messages (by idempotency key)
// have already been consumed, backing the consumer-side dedup that makes the
// at-least-once pipeline effectively exactly-once for the common redelivery case.
type ProcessedMessageRepository struct {
	db *gorm.DB
}

func NewProcessedMessageRepository(db *gorm.DB) *ProcessedMessageRepository {
	return &ProcessedMessageRepository{db: db}
}

// Seen reports whether the key has already been processed.
func (r *ProcessedMessageRepository) Seen(ctx context.Context, key string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.ProcessedMessage{}).
		Where("idempotency_key = ?", key).
		Count(&count).Error
	return count > 0, err
}

// Mark records the key as processed. ON CONFLICT DO NOTHING so a concurrent or
// retried mark is a harmless no-op rather than a unique-violation error.
func (r *ProcessedMessageRepository) Mark(ctx context.Context, key string) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.ProcessedMessage{IdempotencyKey: key}).Error
}

package model

import "time"

// OutboxEvent is a transactional outbox row written inside the same DB TX as
// the originating state change. A background relay (OutboxRelay) periodically
// publishes unpublished rows to Kafka. Mark-published is idempotent on ID.
type OutboxEvent struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement"`
	AggregateID string     `gorm:"size:64;not null;index"`
	EventType   string     `gorm:"size:64;not null;index"`
	Payload     []byte     `gorm:"type:bytea;not null"`
	CreatedAt   time.Time  `gorm:"not null;index"`
	PublishedAt *time.Time `gorm:"index"`
}

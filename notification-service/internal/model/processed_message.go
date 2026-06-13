package model

import "time"

// ProcessedMessage records the idempotency key of a Kafka message a consumer has
// already handled, so an at-least-once redelivery (consumer crash/rebalance) is
// skipped instead of re-processed (no duplicate emails / inbox rows / audit
// entries). The key is the per-message `idempotency-key` header stamped by the
// shared producer; it is globally unique per publish, so it is the primary key.
type ProcessedMessage struct {
	IdempotencyKey string    `gorm:"column:idempotency_key;primaryKey;size:64"`
	ProcessedAt    time.Time `gorm:"autoCreateTime"`
}

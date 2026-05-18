package model

import "time"

// IdempotencyRecord caches the response of a saga-driven RPC keyed by a
// deterministic idempotency key (typically saga.IdempotencyKey(sagaID, step)).
// When a step is retried with the same key, the cached response is replayed
// instead of re-executing the side-effect.
type IdempotencyRecord struct {
	Key          string    `gorm:"primaryKey;size:128"`
	ResponseBlob []byte    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime"`
}

// TableName pins the table to a stable name independent of struct renames.
func (IdempotencyRecord) TableName() string { return "idempotency_records" }

package model

import "time"

// HoldingCreditMarker makes a holding *credit* idempotent on replay.
//
// The weighted-average Holding.Upsert is NOT naturally idempotent — calling it
// twice adds the quantity twice. Saga steps that credit shares to a holder
// (today: the OTC exercise buyer-credit step) therefore record a marker keyed
// by a deterministic IdempotencyKey before applying the credit, both inside the
// same transaction. A replay (saga retry or crash recovery re-running the step)
// finds the marker already present and skips the mutation, so the credit lands
// exactly once. The paired Backward compensator deletes the marker as it
// reverses the credit, so a later re-credit under the same key applies again.
type HoldingCreditMarker struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	IdempotencyKey string    `gorm:"not null;uniqueIndex" json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

func (HoldingCreditMarker) TableName() string { return "holding_credit_markers" }

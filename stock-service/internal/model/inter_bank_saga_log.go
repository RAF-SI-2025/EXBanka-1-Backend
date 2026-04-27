package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Saga kind constants — one row per (TxID, Phase, Role) triplet. Phase
// strings themselves are no longer enumerated here: post-shared.Saga
// migration the wire-level phase identifier comes from
// contract/shared/saga.StepKind, so handlers and crons reference
// string(saga.StepXxx) directly. Keeping a parallel set of Phase*
// aliases here would let the two drift; deleting the aliases forces
// the single canonical source.
const (
	SagaKindAccept   = "accept"
	SagaKindExercise = "exercise"
	SagaKindExpire   = "expire"

	SagaRoleInitiator = "initiator"
	SagaRoleResponder = "responder"

	IBSagaStatusPending      = "pending"
	IBSagaStatusCompleted    = "completed"
	IBSagaStatusFailed       = "failed"
	IBSagaStatusCompensating = "compensating"
	IBSagaStatusCompensated  = "compensated"
)

// InterBankSagaLog is the durable per-step audit record for a cross-bank
// saga (accept / exercise / expire). One row per (TxID, Phase, Role).
// Optimistic-locked via Version. IdempotencyKey is the canonical
// "<saga_kind>-<tx_id>-<phase>-<role>" string and is unique.
type InterBankSagaLog struct {
	ID             string         `gorm:"primaryKey;size:36" json:"id"`
	TxID           string         `gorm:"size:36;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:1" json:"tx_id"`
	Phase          string         `gorm:"size:32;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:2" json:"phase"`
	Role           string         `gorm:"size:10;not null;uniqueIndex:ux_iblog_tx_phase_role,priority:3" json:"role"`
	RemoteBankCode string         `gorm:"size:3;not null;index" json:"remote_bank_code"`
	Status         string         `gorm:"size:16;not null;index" json:"status"`
	OfferID        *uint64        `gorm:"index" json:"offer_id,omitempty"`
	ContractID     *uint64        `gorm:"index" json:"contract_id,omitempty"`
	SagaKind       string         `gorm:"size:16;not null" json:"saga_kind"`
	PayloadJSON    datatypes.JSON `gorm:"type:jsonb" json:"payload_json"`
	IdempotencyKey string         `gorm:"size:128;not null;uniqueIndex" json:"idempotency_key"`
	ErrorReason    string         `gorm:"type:text" json:"error_reason,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Version        int64          `gorm:"not null;default:0" json:"-"`
}

func (l *InterBankSagaLog) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", l.Version)
	}
	l.Version++
	return nil
}

// IdempotencyKeyFor builds the canonical key for a saga step.
func IdempotencyKeyFor(sagaKind, txID, phase, role string) string {
	return sagaKind + "-" + txID + "-" + phase + "-" + role
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// CrossbankSagaExecutor wraps the InterBankSagaLog repository and provides
// idempotent BeginPhase / CompletePhase / FailPhase primitives used by the
// 5-phase cross-bank OTC saga (accept), the exercise saga, and the 3-phase
// expire saga.
//
// Each (TxID, Phase, Role) tuple maps to exactly one InterBankSagaLog row
// — repeat BeginPhase calls upsert without creating duplicates so saga
// drivers can re-enter on restart without bookkeeping.
type CrossbankSagaExecutor struct {
	logs     *repository.InterBankSagaLogRepository
	producer *kafkaprod.Producer
}

func NewCrossbankSagaExecutor(logs *repository.InterBankSagaLogRepository, producer *kafkaprod.Producer) *CrossbankSagaExecutor {
	return &CrossbankSagaExecutor{logs: logs, producer: producer}
}

// BeginPhase records the start of a phase in pending state. Idempotent on
// (txID, phase, role) — repeat calls won't create duplicate rows.
func (e *CrossbankSagaExecutor) BeginPhase(ctx context.Context, txID, phase, role, sagaKind, remoteBankCode string, offerID, contractID *uint64, payload any) error {
	pj, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	row := &model.InterBankSagaLog{
		TxID: txID, Phase: phase, Role: role,
		RemoteBankCode: remoteBankCode,
		Status:         model.IBSagaStatusPending,
		OfferID:        offerID, ContractID: contractID,
		SagaKind:       sagaKind,
		PayloadJSON:    pj,
	}
	return e.logs.UpsertByTxPhaseRole(row)
}

// CompletePhase marks an existing pending row as completed. Idempotent — a
// retry on an already-completed row is a no-op.
func (e *CrossbankSagaExecutor) CompletePhase(ctx context.Context, txID, phase, role string) error {
	row, err := e.logs.Get(txID, phase, role)
	if err != nil {
		return err
	}
	if row.Status == model.IBSagaStatusCompleted {
		return nil
	}
	row.Status = model.IBSagaStatusCompleted
	return e.logs.UpsertByTxPhaseRole(row)
}

// FailPhase marks the row failed and records the error reason. Idempotent.
func (e *CrossbankSagaExecutor) FailPhase(ctx context.Context, txID, phase, role, reason string) error {
	row, err := e.logs.Get(txID, phase, role)
	if err != nil {
		return err
	}
	row.Status = model.IBSagaStatusFailed
	row.ErrorReason = reason
	return e.logs.UpsertByTxPhaseRole(row)
}

// IsPhaseCompleted lets saga drivers skip phases on retry/restart.
func (e *CrossbankSagaExecutor) IsPhaseCompleted(ctx context.Context, txID, phase, role string) (bool, error) {
	row, err := e.logs.Get(txID, phase, role)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Status == model.IBSagaStatusCompleted, nil
}

// PublishStarted emits otc.crossbank-saga-started.
func (e *CrossbankSagaExecutor) PublishStarted(ctx context.Context, sagaKind, txID, initiator, responder string, offerID, contractID uint64) {
	if e.producer == nil {
		return
	}
	payload := kafkamsg.CrossBankSagaStartedMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: txID, SagaKind: sagaKind, OfferID: offerID, ContractID: contractID,
		InitiatorBankCode: initiator, ResponderBankCode: responder,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = e.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStarted, data)
	}
}

// PublishCommitted emits otc.crossbank-saga-committed.
func (e *CrossbankSagaExecutor) PublishCommitted(ctx context.Context, sagaKind, txID string, contractID uint64) {
	if e.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaCommittedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: txID, SagaKind: sagaKind, ContractID: contractID, CommittedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = e.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaCommitted, data)
	}
}

// PublishRolledBack emits otc.crossbank-saga-rolled-back.
func (e *CrossbankSagaExecutor) PublishRolledBack(ctx context.Context, sagaKind, txID, failingPhase, reason string, compensatedPhases []string) {
	if e.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaRolledBackMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: txID, SagaKind: sagaKind, FailingPhase: failingPhase,
		Reason: reason, CompensatedPhases: compensatedPhases, RolledBackAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = e.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaRolledBack, data)
	}
}

// PublishStuckRollback emits otc.crossbank-saga-stuck-rollback when a
// compensation step has failed beyond the recovery cron's retry budget.
func (e *CrossbankSagaExecutor) PublishStuckRollback(ctx context.Context, txID string, contractID uint64, reason, auditLink string) {
	if e.producer == nil {
		return
	}
	payload := kafkamsg.CrossBankSagaStuckRollbackMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: txID, ContractID: contractID, Reason: reason, AuditLink: auditLink,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = e.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStuckRollback, data)
	}
}

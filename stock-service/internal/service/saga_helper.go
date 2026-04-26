package service

import (
	"context"
	"encoding/json"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"

	"github.com/exbanka/stock-service/internal/model"
)

// SagaLogRepo is the minimum interface the executor needs. Satisfied by
// *repository.SagaLogRepository and any test double.
type SagaLogRepo interface {
	RecordStep(log *model.SagaLog) error
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	// IsForwardCompleted is required by stocksaga.Recorder (shared.Saga's
	// adapter) for restart-resume. Tests that don't exercise resume can
	// return false unconditionally.
	IsForwardCompleted(orderID uint64, stepName string) (bool, error)
}

// SagaExecutor wraps the pending → completed/failed recording pattern for
// saga steps. Each placement or fill saga uses its own executor (one per
// saga_id). Steps are numbered automatically in call order.
type SagaExecutor struct {
	repo     SagaLogRepo
	sagaID   string
	orderID  uint64
	txnID    *uint64
	nextStep int
}

// NewSagaExecutor constructs an executor for the given saga. txnID is set to
// a non-nil pointer for fill sagas (which are scoped to order_id +
// order_transaction_id); placement sagas pass nil.
func NewSagaExecutor(repo SagaLogRepo, sagaID string, orderID uint64, txnID *uint64) *SagaExecutor {
	return &SagaExecutor{
		repo:     repo,
		sagaID:   sagaID,
		orderID:  orderID,
		txnID:    txnID,
		nextStep: 1,
	}
}

// RunStep records the step as pending, runs fn, then transitions the row to
// completed on success or failed on error. Returns fn's error unchanged so
// the caller can chain compensation decisions.
//
// amount and currency are recorded for audit; payload (if non-nil) is
// JSON-serialized into the Payload column.
func (e *SagaExecutor) RunStep(ctx context.Context, name string, amount decimal.Decimal,
	currency string, payload map[string]any, fn func() error) error {

	log := &model.SagaLog{
		SagaID:             e.sagaID,
		OrderID:            e.orderID,
		OrderTransactionID: e.txnID,
		StepNumber:         e.nextStep,
		StepName:           name,
		Status:             model.SagaStatusPending,
		CurrencyCode:       currency,
	}
	if !amount.IsZero() {
		a := amount
		log.Amount = &a
	}
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			log.Payload = datatypes.JSON(b)
		}
	}
	if err := e.repo.RecordStep(log); err != nil {
		return err
	}
	e.nextStep++

	if err := fn(); err != nil {
		_ = e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusFailed, err.Error())
		return err
	}
	return e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompleted, "")
}

// RunCompensation records a compensation step linked to the forward step ID.
// On fn success the step ends in compensated status; on failure, failed.
func (e *SagaExecutor) RunCompensation(ctx context.Context, forwardStepID uint64,
	name string, fn func() error) error {

	log := &model.SagaLog{
		SagaID:             e.sagaID,
		OrderID:            e.orderID,
		OrderTransactionID: e.txnID,
		StepNumber:         e.nextStep,
		StepName:           name,
		Status:             model.SagaStatusCompensating,
		IsCompensation:     true,
		CompensationOf:     &forwardStepID,
	}
	if err := e.repo.RecordStep(log); err != nil {
		return err
	}
	e.nextStep++

	if err := fn(); err != nil {
		_ = e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusFailed, err.Error())
		return err
	}
	return e.repo.UpdateStatus(log.ID, log.Version, model.SagaStatusCompensated, "")
}

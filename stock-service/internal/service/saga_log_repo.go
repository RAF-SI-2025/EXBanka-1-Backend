package service

import "github.com/exbanka/stock-service/internal/model"

// SagaLogRepo is the minimum repository surface the service layer needs to
// drive a saga-step run through the shared saga executor. Satisfied by
// *repository.SagaLogRepository (production) and any test double.
//
// Structurally identical to stocksaga.SagaRepoIF (the recorder-side
// interface): the service-layer types alias here so service constructors
// can keep parameter typing minimal without importing the saga adapter
// package, and stocksaga.NewRecorder accepts the concrete repo because it
// satisfies both interfaces.
//
// IsForwardCompleted is the resume-after-restart hook: shared.Saga checks
// whether a forward step has already completed for a given (saga_id,
// step_name) pair before re-executing on a process restart.
type SagaLogRepo interface {
	RecordStep(log *model.SagaLog) error
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	IsForwardCompleted(sagaID, stepName string) (bool, error)
}

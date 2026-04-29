package service

import (
	"errors"
	"sync"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeSagaRepo is a tiny in-memory stub that satisfies the SagaLogRepo
// interface used by service constructors. Good enough for pure unit tests
// that exercise saga-driven service paths (fund invest, OTC accept, etc.)
// without standing up a real *repository.SagaLogRepository.
//
// Was previously colocated with the now-deleted SagaExecutor in
// saga_helper_test.go; relocated to its own _test.go file because multiple
// service test packages still depend on it.
type fakeSagaRepo struct {
	mu   sync.Mutex
	rows []*model.SagaLog
	// forceFailUpdate, if non-nil, is returned from UpdateStatus to simulate
	// an optimistic-lock failure or downstream error.
	forceFailUpdate error
}

func newFakeSagaRepo() *fakeSagaRepo { return &fakeSagaRepo{} }

func (r *fakeSagaRepo) RecordStep(log *model.SagaLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	log.ID = uint64(len(r.rows) + 1)
	rowCopy := *log
	r.rows = append(r.rows, &rowCopy)
	return nil
}

func (r *fakeSagaRepo) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	if r.forceFailUpdate != nil {
		return r.forceFailUpdate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.ID == id {
			row.Status = newStatus
			row.ErrorMessage = errMsg
			row.Version = version + 1
			return nil
		}
	}
	return errors.New("not found")
}

// IsForwardCompleted satisfies the SagaLogRepo interface for shared.Saga's
// restart-resume. Returns false so each test run re-executes every step.
func (r *fakeSagaRepo) IsForwardCompleted(sagaID, stepName string) (bool, error) {
	return false, nil
}

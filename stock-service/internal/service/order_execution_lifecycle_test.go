package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

// errListOrderRepo errors on ListActiveApproved to exercise Start/WakeAll's
// failure branch; everything else inherits the base fake.
type errListOrderRepo struct{ *fakeBaseCtxOrderRepo }

func (*errListOrderRepo) ListActiveApproved() ([]model.Order, error) {
	return nil, errors.New("db down")
}

func newErrEngine() *OrderExecutionEngine {
	return NewOrderExecutionEngine(
		context.Background(),
		&errListOrderRepo{&fakeBaseCtxOrderRepo{}},
		&fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{},
		&fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
}

func TestEngine_StopOrderExecution(t *testing.T) {
	e := newTestEngine()
	// Inject an active job directly.
	_, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.activeJobs[42] = cancel
	e.mu.Unlock()

	// Stop a non-existent order → no-op.
	e.StopOrderExecution(999)
	e.mu.Lock()
	if len(e.activeJobs) != 1 {
		e.mu.Unlock()
		t.Fatalf("stopping unknown order should not change jobs")
	}
	e.mu.Unlock()

	// Stop the existing job → removed + cancelled.
	e.StopOrderExecution(42)
	e.mu.Lock()
	_, exists := e.activeJobs[42]
	e.mu.Unlock()
	if exists {
		t.Errorf("job 42 should be removed after StopOrderExecution")
	}
}

func TestEngine_WakeAll_BroadcastsAndResumes(t *testing.T) {
	e := newTestEngine()
	old := e.wakeCh
	e.WakeAll() // ListActiveApproved returns nil → no resume; wakeCh replaced.
	select {
	case <-old:
		// closed as expected
	default:
		t.Error("WakeAll should close the old wake channel (broadcast)")
	}
	if e.wakeCh == old {
		t.Error("WakeAll should replace the wake channel")
	}
}

func TestEngine_Start_ListError(t *testing.T) {
	e := newErrEngine()
	e.Start(context.Background()) // error path: logged, returns
	e.WakeAll()                   // error path: logged, returns
}

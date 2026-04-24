package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeSagaRepo is a tiny in-memory stub that satisfies the SagaLogRepo interface
// the SagaExecutor depends on. Good enough for pure unit tests.
type fakeSagaRepo struct {
	mu   sync.Mutex
	rows []*model.SagaLog
	// forceFailUpdate, if non-nil, is returned from UpdateStatus to simulate
	// an optimistic-lock failure.
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

func (r *fakeSagaRepo) all() []*model.SagaLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*model.SagaLog, len(r.rows))
	copy(out, r.rows)
	return out
}

// --- tests ---

func TestSagaExecutor_RunStep_Success(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	err := exec.RunStep(context.Background(), "reserve_funds",
		decimal.NewFromInt(100), "RSD", nil,
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	logs := repo.all()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Status != model.SagaStatusCompleted {
		t.Errorf("final status: got %s want completed", logs[0].Status)
	}
	if logs[0].StepName != "reserve_funds" {
		t.Errorf("step name: got %s", logs[0].StepName)
	}
	if logs[0].Amount == nil || !logs[0].Amount.Equal(decimal.NewFromInt(100)) {
		t.Errorf("amount: got %v", logs[0].Amount)
	}
	if logs[0].CurrencyCode != "RSD" {
		t.Errorf("currency: got %s", logs[0].CurrencyCode)
	}
}

func TestSagaExecutor_RunStep_Error(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	err := exec.RunStep(context.Background(), "reserve_funds",
		decimal.NewFromInt(100), "RSD", nil,
		func() error { return errors.New("boom") })
	if err == nil {
		t.Fatal("expected error")
	}
	logs := repo.all()
	if logs[0].Status != model.SagaStatusFailed {
		t.Errorf("final status: got %s want failed", logs[0].Status)
	}
	if logs[0].ErrorMessage == "" {
		t.Error("expected error message recorded")
	}
}

func TestSagaExecutor_RunCompensation_RecordsCompensationOf(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	_ = exec.RunStep(context.Background(), "settle", decimal.Zero, "", nil,
		func() error { return nil })
	forwardID := repo.all()[0].ID

	err := exec.RunCompensation(context.Background(), forwardID, "compensate_settle",
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	logs := repo.all()
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs (forward + compensation), got %d", len(logs))
	}
	comp := logs[1]
	if !comp.IsCompensation {
		t.Error("IsCompensation should be true")
	}
	if comp.CompensationOf == nil || *comp.CompensationOf != forwardID {
		t.Errorf("CompensationOf not wired: %v", comp.CompensationOf)
	}
	if comp.Status != model.SagaStatusCompensated {
		t.Errorf("final comp status: got %s want compensated", comp.Status)
	}
}

func TestSagaExecutor_StepNumberIncrements(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	_ = exec.RunStep(context.Background(), "one", decimal.Zero, "", nil, func() error { return nil })
	_ = exec.RunStep(context.Background(), "two", decimal.Zero, "", nil, func() error { return nil })
	_ = exec.RunStep(context.Background(), "three", decimal.Zero, "", nil, func() error { return nil })

	logs := repo.all()
	for i, row := range logs {
		want := i + 1
		if row.StepNumber != want {
			t.Errorf("row %d: StepNumber got %d want %d", i, row.StepNumber, want)
		}
	}
}

func TestSagaExecutor_PayloadSerialized(t *testing.T) {
	repo := newFakeSagaRepo()
	exec := NewSagaExecutor(repo, uuid.New().String(), 42, nil)
	err := exec.RunStep(context.Background(), "with_payload", decimal.Zero, "",
		map[string]any{"key": "value", "n": 42},
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	logs := repo.all()
	if len(logs[0].Payload) == 0 {
		t.Fatal("expected non-empty payload")
	}
	s := string(logs[0].Payload)
	if !(contains(s, "\"key\":\"value\"") && contains(s, "\"n\":42")) {
		t.Errorf("payload not serialized as expected: %s", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

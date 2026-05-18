package saga

import (
	"context"
	"testing"
)

// noopRecorder is the test-local stand-in. The exported NoopRecorder is the
// production zero-value; this alias keeps the tests self-documenting.
type noopRecorder = NoopRecorder

func TestNewSaga_MintsItsOwnID(t *testing.T) {
	s := NewSaga(noopRecorder{})
	if s.ID() == "" {
		t.Error("expected NewSaga to mint a non-empty ID")
	}
	other := NewSaga(noopRecorder{})
	if s.ID() == other.ID() {
		t.Error("expected unique IDs across calls")
	}
}

func TestNewSagaWithID_UsesGivenID(t *testing.T) {
	s := NewSagaWithID("specific-id", noopRecorder{})
	if s.ID() != "specific-id" {
		t.Errorf("got %q, want %q", s.ID(), "specific-id")
	}
}

func TestNewSubSaga_DeterministicID(t *testing.T) {
	parent := NewSagaWithID("parent-id", noopRecorder{})
	a := parent.NewSubSaga("commission")
	b := NewSagaWithID("parent-id", noopRecorder{}).NewSubSaga("commission")
	if a.ID() != b.ID() {
		t.Errorf("sub-saga IDs should be deterministic: %s vs %s", a.ID(), b.ID())
	}
	if len(a.ID()) > 36 {
		t.Errorf("sub-saga ID exceeds 36-char ledger limit: %d chars", len(a.ID()))
	}
}

func TestNewSubSaga_DistinctKindsYieldDistinctIDs(t *testing.T) {
	parent := NewSagaWithID("parent-id", noopRecorder{})
	a := parent.NewSubSaga("commission")
	b := parent.NewSubSaga("settlement")
	if a.ID() == b.ID() {
		t.Errorf("expected different sub-saga kinds to mint different IDs, got %s twice", a.ID())
	}
}

func TestNewSubSaga_SameKindSameParentSameID(t *testing.T) {
	parent := NewSagaWithID("parent-id", noopRecorder{})
	a := parent.NewSubSaga("commission")
	b := parent.NewSubSaga("commission")
	if a.ID() != b.ID() {
		t.Errorf("same kind from same parent must yield same ID (idempotent recovery): %s vs %s", a.ID(), b.ID())
	}
}

func TestStep_NameIsStepKind(t *testing.T) {
	s := NewSaga(noopRecorder{})
	s.Add(Step{Name: StepDebitBuyer, Forward: func(ctx context.Context, st *State) error { return nil }})
	steps := s.Steps()
	if len(steps) == 0 || steps[0].Name != StepDebitBuyer {
		t.Errorf("Step.Name should be StepKind, got %v", steps)
	}
}

package saga

import (
	"context"
	"testing"
)

// completedSetRecorder is a test Recorder that reports a fixed set of step
// names as already-completed (the persisted forward state after a crash) and
// records which compensation rows were marked compensated.
type completedSetRecorder struct {
	NoopRecorder
	completed   map[StepKind]bool
	compensated []StepKind
	recordedComp []StepKind
}

func (r *completedSetRecorder) IsCompleted(_ context.Context, _ string, step StepKind) (bool, error) {
	return r.completed[step], nil
}

func (r *completedSetRecorder) RecordCompensation(_ context.Context, _ string, step StepKind, _ int, _ StepHandle, _ *State) (StepHandle, error) {
	r.recordedComp = append(r.recordedComp, step)
	return StepHandle{ID: uint64(len(r.recordedComp))}, nil
}

func (r *completedSetRecorder) MarkCompensated(_ context.Context, h StepHandle) error {
	// Map handle back to the recorded comp step (1-indexed in RecordCompensation).
	if int(h.ID) >= 1 && int(h.ID) <= len(r.recordedComp) {
		r.compensated = append(r.compensated, r.recordedComp[h.ID-1])
	}
	return nil
}

// TestCompensate_OnlyCompletedStepsInReverse proves crash-recovery rollback:
// Compensate runs the Backward of every COMPLETED forward step in reverse
// order, and skips steps that never completed (their Forward never applied, so
// there is nothing to undo).
func TestCompensate_OnlyCompletedStepsInReverse(t *testing.T) {
	var order []StepKind
	mk := func(name StepKind) Step {
		return Step{
			Name:     name,
			Forward:  func(context.Context, *State) error { return nil },
			Backward: func(context.Context, *State) error { order = append(order, name); return nil },
		}
	}
	rec := &completedSetRecorder{completed: map[StepKind]bool{
		StepReserveStrike:       true,
		StepSettleStrikeBuyer:   true,
		StepCreditStrikeSeller:  false, // crashed here — never completed
	}}
	s := NewSagaWithID("saga-1", rec).
		Add(mk(StepReserveStrike)).
		Add(mk(StepSettleStrikeBuyer)).
		Add(mk(StepCreditStrikeSeller))

	if err := s.Compensate(context.Background(), NewState()); err != nil {
		t.Fatalf("Compensate: %v", err)
	}

	// Only the two completed steps compensate, in reverse order.
	want := []StepKind{StepSettleStrikeBuyer, StepReserveStrike}
	if len(order) != len(want) {
		t.Fatalf("compensated %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("compensation order[%d]=%s want %s (full=%v)", i, order[i], want[i], order)
		}
	}
	// Rows transitioned to compensated for the two completed steps.
	if len(rec.compensated) != 2 {
		t.Fatalf("marked %d rows compensated, want 2 (%v)", len(rec.compensated), rec.compensated)
	}
}

// TestCompensate_HaltsAtPivot confirms the reverse walk stops at a pivot, same
// as Execute's rollback — steps below a pivot are not undone.
func TestCompensate_HaltsAtPivot(t *testing.T) {
	var order []StepKind
	mk := func(name StepKind, pivot bool) Step {
		return Step{
			Name:     name,
			Pivot:    pivot,
			Forward:  func(context.Context, *State) error { return nil },
			Backward: func(context.Context, *State) error { order = append(order, name); return nil },
		}
	}
	rec := &completedSetRecorder{completed: map[StepKind]bool{
		StepReserveStrike:      true,
		StepConsumeSellerHolding: true, // pivot
		StepUpsertBuyerHolding: true,
	}}
	s := NewSagaWithID("saga-2", rec).
		Add(mk(StepReserveStrike, false)).
		Add(mk(StepConsumeSellerHolding, true)).
		Add(mk(StepUpsertBuyerHolding, false))

	if err := s.Compensate(context.Background(), NewState()); err != nil {
		t.Fatalf("Compensate: %v", err)
	}
	// Walk: C (upsert) compensates, then hits pivot (consume) → halt; reserve
	// below the pivot is NOT compensated.
	want := []StepKind{StepUpsertBuyerHolding}
	if len(order) != len(want) || order[0] != want[0] {
		t.Fatalf("compensated %v, want %v (pivot must halt the walk)", order, want)
	}
}

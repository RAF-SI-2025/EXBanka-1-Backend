// Package shared — saga.go provides a generic, service-agnostic saga
// abstraction used by every service in this monorepo. It captures the common
// pattern: run a sequence of reversible steps; on failure, roll back the
// completed steps in reverse order; persist each transition to a per-service
// audit log so a recovery loop can reconcile crash survivors.
//
// The shared file owns the *vocabulary* (status strings, the Step / State
// types, the Recorder port, the lifecycle hook) and the *executor* (Saga).
// It does not own *persistence* — each service plugs in its own Recorder
// implementation backed by its own saga_logs table, which keeps this file
// free of GORM, decimal, or domain-specific schemas.
//
// Typical use:
//
//	saga := shared.NewSaga(sagaID, recorder).
//	    WithRetry(shared.DefaultRetryConfig).
//	    Add(s.reserveBuyerFunds()).
//	    Add(s.createContract()).
//	    AddIf(in.Premium.GreaterThan(threshold), s.complianceReview()).
//	    Add(s.transferFunds()).
//	    Add(s.finalize())
//
//	if err := saga.Execute(ctx, state); err != nil {
//	    return err
//	}
package shared

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// SagaStatus is the typed lifecycle state of a saga step row. Stored as
// a string column so adapters can persist it directly without conversion.
type SagaStatus string

const (
	SagaStatusPending      SagaStatus = "pending"
	SagaStatusCompleted    SagaStatus = "completed"
	SagaStatusFailed       SagaStatus = "failed"
	SagaStatusCompensating SagaStatus = "compensating"
	SagaStatusCompensated  SagaStatus = "compensated"
	SagaStatusDeadLetter   SagaStatus = "dead_letter"
)

// IsTerminal reports whether the status represents a final state that the
// recovery loop should not retry.
func (s SagaStatus) IsTerminal() bool {
	switch s {
	case SagaStatusCompleted, SagaStatusFailed,
		SagaStatusCompensated, SagaStatusDeadLetter:
		return true
	}
	return false
}

// Step is one reversible unit of saga work.
//
// Forward is mandatory and performs the business effect.
// Backward is optional. A nil Backward marks the step as non-compensatable
// (e.g., publishing a Kafka event); the executor skips it during rollback.
// Pivot, when true, forbids rollback past this step — used for finalization
// phases whose effect cannot be undone (e.g., closing a contract). The
// executor stops the rollback walk when it reaches a pivot.
type Step struct {
	Name     string
	Forward  func(ctx context.Context, st *State) error
	Backward func(ctx context.Context, st *State) error
	Pivot    bool
}

// State is the shared bag of values passed to every step in a saga. Earlier
// steps Set values; later steps and compensation closures Get them.
//
// Thread-safe so callers can hand the same State to background goroutines
// (status pollers, lifecycle publishers) without external locking.
type State struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewState returns an empty State.
func NewState() *State {
	return &State{data: make(map[string]any)}
}

// Set stores a value under key, overwriting any prior value.
func (s *State) Set(key string, value any) {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
}

// Get returns the value for key and whether it was present.
func (s *State) Get(key string) (any, bool) {
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	return v, ok
}

// MustGet returns the value for key or panics if absent. Intended for
// step closures that have a hard contract on what earlier steps populated.
func (s *State) MustGet(key string) any {
	v, ok := s.Get(key)
	if !ok {
		panic(fmt.Sprintf("saga: missing required state key %q", key))
	}
	return v
}

// Snapshot returns a shallow copy of the current key/value map. Useful for
// Recorder adapters that want to serialize state into a payload column.
func (s *State) Snapshot() map[string]any {
	s.mu.RLock()
	out := make(map[string]any, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	s.mu.RUnlock()
	return out
}

// StepHandle is the opaque per-row identifier returned by Recorder.RecordForward
// and RecordCompensation. The executor passes the same handle back to mark the
// row completed, failed, or compensated. Adapters embed whatever they need
// (DB primary key, optimistic-lock version, etc.).
type StepHandle struct {
	ID      uint64
	Version int64
}

// Recorder is the persistence port. Each service writes a thin adapter over
// its own saga_logs repository.
//
// All methods take ctx so adapters can honor request deadlines and cancellation.
// IsCompleted enables crash-restart resumption: before running a step, the
// executor asks the recorder whether that step already completed in a prior
// run; if so, it is skipped.
type Recorder interface {
	RecordForward(ctx context.Context, sagaID, stepName string, stepNumber int, st *State) (StepHandle, error)
	MarkCompleted(ctx context.Context, h StepHandle) error
	MarkFailed(ctx context.Context, h StepHandle, errMsg string) error

	RecordCompensation(ctx context.Context, sagaID, stepName string, stepNumber int, forward StepHandle, st *State) (StepHandle, error)
	MarkCompensated(ctx context.Context, h StepHandle) error
	MarkCompensationFailed(ctx context.Context, h StepHandle, errMsg string) error

	IsCompleted(ctx context.Context, sagaID, stepName string) (bool, error)
}

// NoopRecorder is a Recorder that does nothing. Useful in tests where saga
// log persistence is not under test, and as a safe default when a service
// has not yet wired its adapter.
type NoopRecorder struct{}

func (NoopRecorder) RecordForward(context.Context, string, string, int, *State) (StepHandle, error) {
	return StepHandle{}, nil
}
func (NoopRecorder) MarkCompleted(context.Context, StepHandle) error          { return nil }
func (NoopRecorder) MarkFailed(context.Context, StepHandle, string) error     { return nil }
func (NoopRecorder) RecordCompensation(context.Context, string, string, int, StepHandle, *State) (StepHandle, error) {
	return StepHandle{}, nil
}
func (NoopRecorder) MarkCompensated(context.Context, StepHandle) error              { return nil }
func (NoopRecorder) MarkCompensationFailed(context.Context, StepHandle, string) error { return nil }
func (NoopRecorder) IsCompleted(context.Context, string, string) (bool, error)      { return false, nil }

// LifecyclePublisher is the optional hook a saga calls at top-level
// transitions: started, committed, rolled back, or stuck (compensation
// itself failed and was left for recovery). Implemented by services that
// emit Kafka events for distributed sagas (cross-bank). Nil-safe via
// NoopPublisher.
type LifecyclePublisher interface {
	OnStarted(ctx context.Context, sagaID string)
	OnCommitted(ctx context.Context, sagaID string)
	OnRolledBack(ctx context.Context, sagaID, failingStep, reason string, compensated []string)
	OnStuck(ctx context.Context, sagaID, failingStep, reason string)
}

// NoopPublisher is the zero-cost default when a saga has no lifecycle hook.
type NoopPublisher struct{}

func (NoopPublisher) OnStarted(context.Context, string)                            {}
func (NoopPublisher) OnCommitted(context.Context, string)                          {}
func (NoopPublisher) OnRolledBack(context.Context, string, string, string, []string) {}
func (NoopPublisher) OnStuck(context.Context, string, string, string)              {}

// LogFunc is the minimal logger contract used by the executor for warnings
// (recorder write failures, skipped steps on resume, compensation errors).
// Defaults to log.Printf with a "[saga]" prefix; replace via WithLogger.
type LogFunc func(format string, args ...any)

func defaultLog(format string, args ...any) {
	log.Printf("[saga] "+format, args...)
}

// Saga is the central executor. Construct with NewSaga, configure and assemble
// with the chainable With*/Add/AddIf methods, then run with Execute.
//
// A Saga is single-use: do not call Execute twice on the same instance.
type Saga struct {
	id        string
	steps     []Step
	recorder  Recorder
	publisher LifecyclePublisher
	retry     RetryConfig // zero-value means "no retry"
	logf      LogFunc
}

// NewSaga returns an empty saga with the given id and recorder. A nil recorder
// is replaced with NoopRecorder so tests can omit it without panicking.
func NewSaga(id string, recorder Recorder) *Saga {
	if recorder == nil {
		recorder = NoopRecorder{}
	}
	return &Saga{
		id:        id,
		recorder:  recorder,
		publisher: NoopPublisher{},
		logf:      defaultLog,
	}
}

// WithRetry wraps every Forward and Backward call in shared.Retry using the
// given config. The zero value (default) means each step is invoked exactly
// once. Use this for transient gRPC failures that shouldn't trigger a full
// compensation cascade.
func (s *Saga) WithRetry(cfg RetryConfig) *Saga {
	s.retry = cfg
	return s
}

// WithLogger overrides the default logger.
func (s *Saga) WithLogger(fn LogFunc) *Saga {
	if fn != nil {
		s.logf = fn
	}
	return s
}

// WithPublisher attaches a lifecycle hook for started / committed / rolled-back
// / stuck transitions. Optional; pass NoopPublisher{} to disable explicitly.
func (s *Saga) WithPublisher(p LifecyclePublisher) *Saga {
	if p == nil {
		p = NoopPublisher{}
	}
	s.publisher = p
	return s
}

// Add appends a step to the saga. A step with an empty Name or nil Forward
// is rejected (silently skipped with a warning) so callers cannot accidentally
// build a saga that runs nothing.
func (s *Saga) Add(step Step) *Saga {
	if step.Name == "" || step.Forward == nil {
		s.logf("saga=%s rejected step with empty name or nil Forward", s.id)
		return s
	}
	s.steps = append(s.steps, step)
	return s
}

// AddIf conditionally appends a step. Used for build-time branching:
//
//	saga.AddIf(in.Premium.GreaterThan(threshold), s.complianceReview())
//
// is shorthand for `if cond { saga.Add(step) }`, kept chainable so the saga
// reads top-to-bottom at the call site.
func (s *Saga) AddIf(cond bool, step Step) *Saga {
	if cond {
		return s.Add(step)
	}
	return s
}

// Steps returns the assembled step list. Primarily for tests and recovery
// glue; production code should not need this.
func (s *Saga) Steps() []Step {
	out := make([]Step, len(s.steps))
	copy(out, s.steps)
	return out
}

// ID returns the saga id passed to NewSaga.
func (s *Saga) ID() string { return s.id }

// completedStep tracks one successful forward step so the executor can
// build its compensation entry on rollback.
type completedStep struct {
	step   Step
	handle StepHandle
	number int
}

// Execute runs every step in order, recording each transition through the
// configured Recorder. If a step fails, every previously-completed step is
// compensated in reverse order, stopping at the first Pivot. The original
// forward error is returned regardless of whether compensations succeeded;
// compensation failures are surfaced through the lifecycle publisher and
// left in compensating status for the recovery loop to retry.
//
// state may be nil; an empty State is created if so. The same state is
// passed to every step closure (forward and backward) so values produced
// by one step are visible to those that follow.
//
// Execute honors ctx cancellation between steps and inside Retry. A
// cancelled context interrupts the saga at the next boundary; in-flight
// steps run to completion (or honor ctx themselves) before the executor
// notices.
func (s *Saga) Execute(ctx context.Context, state *State) error {
	if state == nil {
		state = NewState()
	}
	if len(s.steps) == 0 {
		return nil
	}

	s.publisher.OnStarted(ctx, s.id)
	done := make([]completedStep, 0, len(s.steps))

	for i, step := range s.steps {
		if err := ctx.Err(); err != nil {
			compensated := s.rollback(ctx, state, done)
			s.publisher.OnRolledBack(ctx, s.id, step.Name, err.Error(), compensated)
			return err
		}

		// Restart-resume: a previous run of this saga may already have
		// completed this step. Skip it so retries are idempotent at the
		// step granularity.
		if already, err := s.recorder.IsCompleted(ctx, s.id, step.Name); err == nil && already {
			s.logf("saga=%s step=%s already completed, skipping", s.id, step.Name)
			continue
		}

		handle, err := s.recorder.RecordForward(ctx, s.id, step.Name, i+1, state)
		if err != nil {
			// A failure to *record* the step is treated like a failure of
			// the step itself: roll back what's already done so the saga
			// doesn't sit half-applied with no audit trail.
			s.logf("saga=%s step=%s record forward: %v", s.id, step.Name, err)
			compensated := s.rollback(ctx, state, done)
			s.publisher.OnRolledBack(ctx, s.id, step.Name, err.Error(), compensated)
			return fmt.Errorf("saga: record forward step %s: %w", step.Name, err)
		}

		fwdErr := s.runForward(ctx, step, state)
		if fwdErr != nil {
			if mErr := s.recorder.MarkFailed(ctx, handle, fwdErr.Error()); mErr != nil {
				s.logf("saga=%s step=%s mark failed: %v", s.id, step.Name, mErr)
			}
			compensated := s.rollback(ctx, state, done)
			s.publisher.OnRolledBack(ctx, s.id, step.Name, fwdErr.Error(), compensated)
			return fwdErr
		}

		if mErr := s.recorder.MarkCompleted(ctx, handle); mErr != nil {
			// The step's effect already happened; failing to *record* the
			// completion just leaves a stuck "pending" row for the recovery
			// loop to reconcile. Log and continue.
			s.logf("saga=%s step=%s mark completed: %v", s.id, step.Name, mErr)
		}
		done = append(done, completedStep{step: step, handle: handle, number: i + 1})
	}

	s.publisher.OnCommitted(ctx, s.id)
	return nil
}

// runForward invokes a step's Forward, optionally wrapped in Retry.
func (s *Saga) runForward(ctx context.Context, step Step, state *State) error {
	if s.retry.MaxAttempts > 0 {
		return Retry(ctx, s.retry, func() error { return step.Forward(ctx, state) })
	}
	return step.Forward(ctx, state)
}

// runBackward invokes a step's Backward, optionally wrapped in Retry. A nil
// Backward returns nil (the step declared itself non-compensatable).
func (s *Saga) runBackward(ctx context.Context, step Step, state *State) error {
	if step.Backward == nil {
		return nil
	}
	if s.retry.MaxAttempts > 0 {
		return Retry(ctx, s.retry, func() error { return step.Backward(ctx, state) })
	}
	return step.Backward(ctx, state)
}

// rollback walks completed steps in reverse, compensating each. Returns the
// names of successfully compensated steps for the lifecycle publisher.
//
// Rules:
//   - A step with Pivot=true halts the walk (no rollback past a pivot).
//   - A step with Backward=nil is skipped (declared non-compensatable).
//   - A failed Backward leaves the compensation row in compensating status
//     for the recovery loop; the walk continues so the rest of the chain
//     gets a chance to undo their effects.
func (s *Saga) rollback(ctx context.Context, state *State, done []completedStep) []string {
	compensated := make([]string, 0, len(done))
	stuck := false

	for j := len(done) - 1; j >= 0; j-- {
		d := done[j]
		if d.step.Pivot {
			s.logf("saga=%s pivot at step %s, halting rollback", s.id, d.step.Name)
			break
		}
		if d.step.Backward == nil {
			continue
		}

		// Negative step number marks the row as compensation in the audit
		// log without colliding with forward step numbers.
		compHandle, err := s.recorder.RecordCompensation(ctx, s.id, d.step.Name, -(d.number), d.handle, state)
		if err != nil {
			s.logf("saga=%s step=%s record compensation: %v", s.id, d.step.Name, err)
			stuck = true
			continue
		}

		if cErr := s.runBackward(ctx, d.step, state); cErr != nil {
			s.logf("saga=%s step=%s compensation failed: %v", s.id, d.step.Name, cErr)
			if mErr := s.recorder.MarkCompensationFailed(ctx, compHandle, cErr.Error()); mErr != nil {
				s.logf("saga=%s step=%s mark compensation failed: %v", s.id, d.step.Name, mErr)
			}
			stuck = true
			continue
		}

		if mErr := s.recorder.MarkCompensated(ctx, compHandle); mErr != nil {
			s.logf("saga=%s step=%s mark compensated: %v", s.id, d.step.Name, mErr)
		}
		compensated = append(compensated, d.step.Name)
	}

	if stuck {
		s.publisher.OnStuck(ctx, s.id, "", "compensation incomplete; recovery required")
	}
	return compensated
}

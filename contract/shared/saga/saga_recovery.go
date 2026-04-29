// saga_recovery.go provides the crash-recovery loop that reconciles stuck
// saga steps after an unexpected process exit.
//
// The flow:
//
//  1. RecoveryRunner.Run starts a background goroutine that ticks every
//     Interval, calling Reconcile.
//  2. Reconcile asks the RecoveryRecorder for steps stuck longer than
//     StuckAfter (i.e., pending or compensating with an old updated_at).
//  3. For each stuck step, the runner asks the per-service Classifier what
//     to do: ActionRetry, ActionSkip, ActionDeadLetter, or ActionLeave
//     (human review).
//  4. ActionRetry calls Classifier.Retry, which is the only service-specific
//     logic. The runner handles status transitions, retry counting, and
//     dead-letter escalation.
//
// This file owns the loop, the action vocabulary, and the retry budget; each
// service still owns "what does step X mean" via its Classifier
// implementation.
package saga

import (
	"context"
	"errors"
	"time"
)

// RecoveryAction is the verdict a Classifier returns for one stuck saga step.
type RecoveryAction int

const (
	// ActionLeave means the runner should not touch the row; a human must
	// inspect it. Used for steps whose replay is too risky to automate
	// (e.g., placement-saga steps that would duplicate user intent).
	ActionLeave RecoveryAction = iota

	// ActionRetry means the runner should call Classifier.Retry on this
	// step. The runner records retry attempts and escalates to dead-letter
	// after MaxRetries.
	ActionRetry

	// ActionSkip means the step's effect is known to have completed
	// (e.g., a downstream service confirmed it via a query) and the row
	// should simply be marked completed/compensated without further work.
	ActionSkip

	// ActionDeadLetter means the runner should mark the row dead_letter
	// immediately (no further retries). Used when the Classifier knows the
	// step is permanently unrecoverable.
	ActionDeadLetter
)

// String returns a human-readable name for logs.
func (a RecoveryAction) String() string {
	switch a {
	case ActionLeave:
		return "leave"
	case ActionRetry:
		return "retry"
	case ActionSkip:
		return "skip"
	case ActionDeadLetter:
		return "dead_letter"
	}
	return "unknown"
}

// StuckStep is the snapshot a RecoveryRecorder returns for one row that
// needs reconciliation. Each service maps its own saga_logs row into this
// shape inside its adapter.
type StuckStep struct {
	Handle       StepHandle
	SagaID       string
	StepName     string
	StepNumber   int
	Status       SagaStatus // pending or compensating
	RetryCount   int
	UpdatedAt    time.Time
	Payload      map[string]any // optional snapshot of saga state at record time
	Compensation bool           // true = this row is a compensation step (was rolling back)
}

// RecoveryRecorder extends Recorder with the queries the recovery loop
// needs. Splitting it from Recorder lets services that don't need crash
// recovery (rare in this codebase) skip implementing these methods.
type RecoveryRecorder interface {
	// ListStuck returns rows in pending or compensating status whose
	// updated_at is older than olderThan. The threshold prevents the
	// loop from fighting an in-flight saga.
	ListStuck(ctx context.Context, olderThan time.Duration) ([]StuckStep, error)

	// IncrementRetry bumps the retry_count for a stuck row. Called after
	// each unsuccessful Classifier.Retry attempt so the runner can
	// escalate to dead-letter once MaxRetries is reached.
	IncrementRetry(ctx context.Context, h StepHandle) error

	// MarkDeadLetter transitions the row to dead_letter status. The
	// runner stops retrying these rows and they surface in operator
	// dashboards for human review.
	MarkDeadLetter(ctx context.Context, h StepHandle, reason string) error
}

// Classifier owns the per-service logic of "what should we do with this
// stuck step?" The runner calls Classify first; if the verdict is
// ActionRetry, the runner calls Retry to perform the actual replay.
//
// Retry's semantics:
//   - On nil error: the runner marks the row completed (forward) or
//     compensated (backward) and the saga is reconciled.
//   - On non-nil error: the runner increments retry_count and tries
//     again on the next tick. After MaxRetries the row is dead-lettered.
//
// Implementations should use deterministic idempotency keys so a Retry
// can land safely on a downstream operation that may have already
// committed in the original (crashed) attempt.
type Classifier interface {
	Classify(ctx context.Context, step StuckStep) RecoveryAction
	Retry(ctx context.Context, step StuckStep) error
}

// RecoveryConfig controls runner behavior.
type RecoveryConfig struct {
	// Interval between reconciliation sweeps. 30s is a sensible default
	// — short enough that crash survivors recover quickly, long enough
	// that a dead-lettered row doesn't burn CPU.
	Interval time.Duration

	// StuckAfter is the minimum age of a pending/compensating row before
	// the runner touches it. Prevents racing with an in-flight saga.
	StuckAfter time.Duration

	// MaxRetries is the per-row retry budget. After this many failed
	// Retry attempts the runner escalates the row to dead_letter.
	MaxRetries int

	// RunOnStart, when true, calls Reconcile once immediately before
	// starting the periodic ticker. Useful so a service that just
	// restarted from a crash starts catching up without waiting a full
	// Interval.
	RunOnStart bool
}

// DefaultRecoveryConfig is a reasonable starting point for most services.
var DefaultRecoveryConfig = RecoveryConfig{
	Interval:   30 * time.Second,
	StuckAfter: 30 * time.Second,
	MaxRetries: 5,
	RunOnStart: true,
}

// RecoveryRunner reconciles stuck saga steps after a crash. Construct one
// per service that has a saga_logs table; call Run from cmd/main.go after
// dependencies are wired and before grpc.Serve.
type RecoveryRunner struct {
	recorder   RecoveryRecorder
	classifier Classifier
	cfg        RecoveryConfig
	logf       LogFunc
}

// NewRecoveryRunner constructs a runner. The recorder must be the same one
// the service's sagas write to — this loop reconciles its rows.
//
// EXPERIMENTAL: this generic runner is not yet wired into any service's
// cmd/main.go. Stock-service runs its own SagaRecovery loop today; the
// migration path is to swap that for a NewRecoveryRunner instance plus
// a per-service Classifier (see transaction-service/internal/saga.NewClassifier
// for the template). Tracked as F18 in the future-ideas backlog.
func NewRecoveryRunner(recorder RecoveryRecorder, classifier Classifier, cfg RecoveryConfig) *RecoveryRunner {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultRecoveryConfig.Interval
	}
	if cfg.StuckAfter <= 0 {
		cfg.StuckAfter = DefaultRecoveryConfig.StuckAfter
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = DefaultRecoveryConfig.MaxRetries
	}
	return &RecoveryRunner{
		recorder:   recorder,
		classifier: classifier,
		cfg:        cfg,
		logf:       defaultLog,
	}
}

// WithLogger overrides the default logger.
func (r *RecoveryRunner) WithLogger(fn LogFunc) *RecoveryRunner {
	if fn != nil {
		r.logf = fn
	}
	return r
}

// Run spawns a goroutine that reconciles stuck rows on every tick until
// ctx is cancelled. Safe to call from cmd/main.go with the long-lived
// service ctx so the loop honors graceful shutdown.
func (r *RecoveryRunner) Run(ctx context.Context) {
	go func() {
		if r.cfg.RunOnStart {
			if err := r.Reconcile(ctx); err != nil {
				r.logf("recovery: initial reconcile: %v", err)
			}
		}
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Reconcile(ctx); err != nil {
					r.logf("recovery: periodic reconcile: %v", err)
				}
			}
		}
	}()
}

// Reconcile runs one full sweep. Exported for tests and for callers that
// want to trigger a reconciliation manually (e.g., from an admin RPC).
func (r *RecoveryRunner) Reconcile(ctx context.Context) error {
	if r.recorder == nil || r.classifier == nil {
		return errors.New("recovery: nil recorder or classifier")
	}

	stuck, err := r.recorder.ListStuck(ctx, r.cfg.StuckAfter)
	if err != nil {
		return err
	}
	for _, step := range stuck {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Already past the retry budget — escalate without further work.
		if step.RetryCount >= r.cfg.MaxRetries {
			reason := "exceeded max recovery retries"
			if mErr := r.recorder.MarkDeadLetter(ctx, step.Handle, reason); mErr != nil {
				r.logf("recovery: mark dead_letter saga=%s step=%s: %v",
					step.SagaID, step.StepName, mErr)
			} else {
				r.logf("recovery: ESCALATED saga=%s step=%s to dead_letter (retries=%d)",
					step.SagaID, step.StepName, step.RetryCount)
			}
			continue
		}

		action := r.classifier.Classify(ctx, step)
		r.logf("recovery: saga=%s step=%s status=%s retries=%d -> %s",
			step.SagaID, step.StepName, step.Status, step.RetryCount, action)

		switch action {
		case ActionLeave:
			// Intentional no-op: row stays as-is for human review.

		case ActionSkip:
			// The classifier knows the side effect already landed.
			// Mark the row terminal so it stops appearing in stuck queries.
			r.skipToTerminal(ctx, step)

		case ActionDeadLetter:
			if err := r.recorder.MarkDeadLetter(ctx, step.Handle, "classifier marked unrecoverable"); err != nil {
				r.logf("recovery: mark dead_letter saga=%s step=%s: %v",
					step.SagaID, step.StepName, err)
			}

		case ActionRetry:
			if err := r.classifier.Retry(ctx, step); err != nil {
				r.logf("recovery: retry saga=%s step=%s: %v",
					step.SagaID, step.StepName, err)
				if iErr := r.recorder.IncrementRetry(ctx, step.Handle); iErr != nil {
					r.logf("recovery: increment retry saga=%s step=%s: %v",
						step.SagaID, step.StepName, iErr)
				}
				continue
			}
			// Retry succeeded — transition the row terminal.
			r.skipToTerminal(ctx, step)
		}
	}
	return nil
}

// skipToTerminal moves a row from pending/compensating to its final state.
// Forward rows go to completed; compensation rows go to compensated. The
// recorder's underlying methods are reused so adapters don't have to
// implement separate "skip" methods.
func (r *RecoveryRunner) skipToTerminal(ctx context.Context, step StuckStep) {
	// We need a Recorder, not just RecoveryRecorder, to call MarkCompleted /
	// MarkCompensated. Adapters in practice implement both — assert here so
	// the runner can transition rows. If the assertion fails, fall back to
	// dead-lettering rather than leave the row stuck.
	full, ok := r.recorder.(Recorder)
	if !ok {
		r.logf("recovery: recorder does not implement full Recorder; cannot transition saga=%s step=%s",
			step.SagaID, step.StepName)
		_ = r.recorder.MarkDeadLetter(ctx, step.Handle, "recovery cannot finalize: recorder lacks Mark methods")
		return
	}
	var err error
	if step.Compensation {
		err = full.MarkCompensated(ctx, step.Handle)
	} else {
		err = full.MarkCompleted(ctx, step.Handle)
	}
	if err != nil {
		r.logf("recovery: finalize saga=%s step=%s: %v", step.SagaID, step.StepName, err)
	}
}

// Compile-time guard: NoopRecorder satisfies RecoveryRecorder so tests can
// use it as a stand-in.
var _ RecoveryRecorder = (*noopRecoveryRecorder)(nil)

type noopRecoveryRecorder struct{ NoopRecorder }

func (noopRecoveryRecorder) ListStuck(context.Context, time.Duration) ([]StuckStep, error) {
	return nil, nil
}
func (noopRecoveryRecorder) IncrementRetry(context.Context, StepHandle) error { return nil }
func (noopRecoveryRecorder) MarkDeadLetter(context.Context, StepHandle, string) error {
	return nil
}

// NoopRecoveryRecorder returns a RecoveryRecorder that does nothing. Useful
// in tests and as a default for services that haven't wired their adapter.
func NoopRecoveryRecorder() RecoveryRecorder { return noopRecoveryRecorder{} }

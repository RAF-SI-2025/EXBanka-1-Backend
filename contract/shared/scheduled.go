// Package shared — scheduled.go is the canonical "background goroutine that
// ticks on an interval" runner. Replaces the ~13 hand-written ticker loops
// across services (saga recovery, exchange rate sync, card block expiry,
// notification inbox cleanup, credit overdue marking, etc.).
//
// Every loop in the codebase repeats the same shape: time.NewTicker →
// defer ticker.Stop → for { select { case <-ctx.Done(): return; case <-ticker.C: doWork() } }.
// CLAUDE.md mandates this exact structure (ctx-aware, stopped on shutdown).
// This runner enforces it once.
package shared

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"
)

// ScheduledJob describes a recurring background task.
type ScheduledJob struct {
	// Name is used in log messages so failures are attributable.
	Name string

	// Interval between ticks. Must be > 0.
	Interval time.Duration

	// OnTick is called on every tick. A non-nil error is passed to OnError
	// (if set) and otherwise logged at WARN level. The job continues
	// regardless — one bad tick must not kill the loop.
	OnTick func(ctx context.Context) error

	// OnError, when non-nil, receives every error returned by OnTick. Use
	// for metrics or service-specific reporting. Optional.
	OnError func(err error)

	// RunOnStart, when true, calls OnTick once immediately before the
	// first tick. Useful for jobs whose first run should happen at
	// service boot (e.g., catching up after a crash).
	RunOnStart bool

	// FirstDelay, when > 0, delays the first tick by this duration. Lets
	// callers stagger jobs across services so they don't all fire on the
	// minute. Ignored if RunOnStart is true.
	FirstDelay time.Duration
}

// RunScheduled spawns a goroutine that runs the job until ctx is cancelled.
// Safe to call from cmd/main.go with the long-lived service ctx so the loop
// honors graceful shutdown.
//
// Panics inside OnTick are recovered, logged with a stack trace, and counted
// as a tick error. The loop continues — a panicking tick must not crash the
// service.
func RunScheduled(ctx context.Context, job ScheduledJob) {
	if job.OnTick == nil {
		defaultLog("scheduled: job %q has nil OnTick, not starting", job.Name)
		return
	}
	if job.Interval <= 0 {
		defaultLog("scheduled: job %q has non-positive Interval, not starting", job.Name)
		return
	}

	go func() {
		// Initial run / first delay.
		if job.RunOnStart {
			runTick(ctx, job)
		} else if job.FirstDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(job.FirstDelay):
			}
		}

		ticker := time.NewTicker(job.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runTick(ctx, job)
			}
		}
	}()
}

// runTick invokes OnTick once with panic recovery and error reporting.
func runTick(ctx context.Context, job ScheduledJob) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("scheduled: job %q panicked: %v\n%s", job.Name, r, debug.Stack())
			reportTickError(job, err)
		}
	}()
	if err := job.OnTick(ctx); err != nil {
		reportTickError(job, fmt.Errorf("scheduled: job %q tick: %w", job.Name, err))
	}
}

// reportTickError dispatches to the job's OnError or falls back to the
// default logger.
func reportTickError(job ScheduledJob, err error) {
	if job.OnError != nil {
		job.OnError(err)
		return
	}
	defaultLog("%v", err)
}

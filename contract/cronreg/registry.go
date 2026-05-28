// Package cronreg provides a per-service registry of cron jobs with
// pause/resume/trigger control and GORM-backed persistence for pause state.
package cronreg

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCronNotFound = errors.New("cron not found")
	ErrCronPaused   = errors.New("cron is paused; pass force=true to override")
)

// CronInfo is the read-only view of an entry.
type CronInfo struct {
	Name             string
	Service          string
	Description      string
	Interval         time.Duration
	CronExpression   string
	LastStartedAt    *time.Time
	LastFinishedAt   *time.Time
	LastError        string
	NextScheduledAt  time.Time
	IsPaused         bool
	PausedByEmployee int64
	PausedAt         *time.Time
	RunCount         int64
	ErrorCount       int64
}

// Entry is the live, control surface for one cron job. Returned by Registry.Register.
type Entry struct {
	mu               sync.RWMutex
	name             string
	service          string
	description      string
	interval         time.Duration
	cronExpression   string
	lastStartedAt    *time.Time
	lastFinishedAt   *time.Time
	lastError        string
	nextScheduledAt  time.Time
	isPaused         atomic.Bool
	pausedByEmployee atomic.Int64
	pausedAt         atomic.Pointer[time.Time]
	runCount         atomic.Int64
	errorCount       atomic.Int64
	triggerCh        chan struct{}
}

// IsPaused returns true when the cron has been administratively paused.
func (e *Entry) IsPaused() bool { return e.isPaused.Load() }

// BeginRun returns false if the cron is paused. Cron loops should:
//
//	if !entry.BeginRun() { continue }
//	defer entry.EndRun(err)
func (e *Entry) BeginRun() bool {
	if e.isPaused.Load() {
		return false
	}
	now := time.Now().UTC()
	e.mu.Lock()
	e.lastStartedAt = &now
	e.mu.Unlock()
	return true
}

// EndRun records the result of a completed run, increments counters, and
// advances nextScheduledAt by the entry's interval (when interval > 0).
func (e *Entry) EndRun(err error) {
	now := time.Now().UTC()
	e.mu.Lock()
	e.lastFinishedAt = &now
	if err != nil {
		e.lastError = err.Error()
		e.errorCount.Add(1)
	} else {
		e.lastError = ""
	}
	e.runCount.Add(1)
	if e.interval > 0 {
		e.nextScheduledAt = now.Add(e.interval)
	}
	e.mu.Unlock()
}

// TriggerChan returns a buffered channel (capacity 1) that fires when an admin
// manually triggers this cron. The cron loop should select on it alongside its
// ticker.
func (e *Entry) TriggerChan() <-chan struct{} { return e.triggerCh }

// PauseStore persists pause state across service restarts. Passing nil means
// in-memory only.
type PauseStore interface {
	Load(cronName string) (paused bool, byEmployee int64, at time.Time, err error)
	Save(cronName string, paused bool, byEmployee int64, at time.Time) error
}

// Registry holds every cron entry for a single service instance.
type Registry struct {
	mu      sync.RWMutex
	service string
	entries map[string]*Entry
	store   PauseStore
}

// NewRegistry creates a new Registry for the named service. store may be nil
// for ephemeral (test/dev) use.
func NewRegistry(service string, store PauseStore) *Registry {
	return &Registry{service: service, entries: map[string]*Entry{}, store: store}
}

// Register declares a new cron. Idempotent: calling twice with the same name
// returns the existing entry.
func (r *Registry) Register(name, description string, interval time.Duration) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[name]; ok {
		return existing
	}
	e := &Entry{
		name:            name,
		service:         r.service,
		description:     description,
		interval:        interval,
		nextScheduledAt: time.Now().UTC().Add(interval),
		triggerCh:       make(chan struct{}, 1),
	}
	// Load persisted pause state, if any.
	if r.store != nil {
		if paused, by, at, err := r.store.Load(name); err == nil && paused {
			e.isPaused.Store(true)
			e.pausedByEmployee.Store(by)
			a := at
			e.pausedAt.Store(&a)
		}
	}
	r.entries[name] = e
	return e
}

// List returns a snapshot of all registered cron entries.
func (r *Registry) List() []CronInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CronInfo, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.snapshot())
	}
	return out
}

// Get returns the current snapshot for the named cron, or ErrCronNotFound.
func (r *Registry) Get(name string) (CronInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return CronInfo{}, ErrCronNotFound
	}
	return e.snapshot(), nil
}

// Pause marks the named cron as paused and persists the state via the store.
func (r *Registry) Pause(name string, byEmployee int64) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return ErrCronNotFound
	}
	now := time.Now().UTC()
	e.isPaused.Store(true)
	e.pausedByEmployee.Store(byEmployee)
	e.pausedAt.Store(&now)
	if r.store != nil {
		if err := r.store.Save(name, true, byEmployee, now); err != nil {
			return err
		}
	}
	return nil
}

// Resume marks the named cron as active again and persists the state.
func (r *Registry) Resume(name string) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return ErrCronNotFound
	}
	e.isPaused.Store(false)
	e.pausedByEmployee.Store(0)
	e.pausedAt.Store(nil)
	if r.store != nil {
		if err := r.store.Save(name, false, 0, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// Trigger enqueues a one-shot run on the named cron. If the cron is paused and
// force is false, ErrCronPaused is returned. byEmployee is recorded for audit
// purposes (not stored in-memory beyond the channel signal).
func (r *Registry) Trigger(name string, force bool, byEmployee int64) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return ErrCronNotFound
	}
	if e.isPaused.Load() && !force {
		return ErrCronPaused
	}
	select {
	case e.triggerCh <- struct{}{}:
	default:
		// A signal is already queued; one fire-soon is sufficient.
	}
	return nil
}

func (e *Entry) snapshot() CronInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pa := e.pausedAt.Load()
	return CronInfo{
		Name:             e.name,
		Service:          e.service,
		Description:      e.description,
		Interval:         e.interval,
		CronExpression:   e.cronExpression,
		LastStartedAt:    copyTimePtr(e.lastStartedAt),
		LastFinishedAt:   copyTimePtr(e.lastFinishedAt),
		LastError:        e.lastError,
		NextScheduledAt:  e.nextScheduledAt,
		IsPaused:         e.isPaused.Load(),
		PausedByEmployee: e.pausedByEmployee.Load(),
		PausedAt:         copyTimePtr(pa),
		RunCount:         e.runCount.Load(),
		ErrorCount:       e.errorCount.Load(),
	}
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

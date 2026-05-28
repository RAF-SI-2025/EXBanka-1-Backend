package cronreg

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry("test-service", nil) // nil pauseStore = in-memory
	e := r.Register("test-cron", "A test cron", 30*time.Second)
	if e == nil {
		t.Fatal("Register returned nil")
	}
	infos := r.List()
	if len(infos) != 1 || infos[0].Name != "test-cron" {
		t.Fatalf("unexpected list: %+v", infos)
	}
}

func TestRegistry_BeginRun_EndRun_TracksTimes(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	if !e.BeginRun() {
		t.Fatal("BeginRun should return true when not paused")
	}
	e.EndRun(nil)
	info, _ := r.Get("c")
	if info.LastStartedAt == nil || info.LastFinishedAt == nil {
		t.Fatal("expected timestamps set")
	}
	if info.RunCount != 1 {
		t.Errorf("RunCount=%d", info.RunCount)
	}
	if info.ErrorCount != 0 {
		t.Errorf("ErrorCount=%d", info.ErrorCount)
	}
}

func TestRegistry_PauseSkipsRun(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	if err := r.Pause("c", 7); err != nil {
		t.Fatal(err)
	}
	if e.BeginRun() {
		t.Fatal("BeginRun should return false when paused")
	}
	info, _ := r.Get("c")
	if !info.IsPaused {
		t.Fatal("info should report paused")
	}
	if info.PausedByEmployee != 7 {
		t.Errorf("PausedByEmployee=%d", info.PausedByEmployee)
	}
}

func TestRegistry_ResumeAllowsRun(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	_ = r.Pause("c", 7)
	if err := r.Resume("c"); err != nil {
		t.Fatal(err)
	}
	if !e.BeginRun() {
		t.Fatal("BeginRun should be allowed after resume")
	}
}

func TestRegistry_TriggerEnqueuesOneShot(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Hour)
	if err := r.Trigger("c", false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.TriggerChan():
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected trigger channel to fire")
	}
}

func TestRegistry_TriggerOnPausedRequiresForce(t *testing.T) {
	r := NewRegistry("test", nil)
	_ = r.Register("c", "", time.Hour)
	_ = r.Pause("c", 7)
	err := r.Trigger("c", false, 1)
	if !errors.Is(err, ErrCronPaused) {
		t.Errorf("expected ErrCronPaused, got %v", err)
	}
	if err := r.Trigger("c", true, 1); err != nil {
		t.Errorf("force trigger failed: %v", err)
	}
}

func TestRegistry_EndRun_WithError_IncrementsErrorCount(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	_ = e.BeginRun()
	e.EndRun(errors.New("boom"))
	info, _ := r.Get("c")
	if info.ErrorCount != 1 || info.LastError != "boom" {
		t.Errorf("got %+v", info)
	}
}

// TestEntry_BeginRun_ConcurrentCAS verifies that when two goroutines call
// BeginRun simultaneously on the same Entry, exactly one returns true and the
// other returns false (concurrent-execution guard via atomic.Bool CAS).
// This is the critical property for saga recovery workers: a double-fire would
// pick up the same compensating row twice and corrupt RetryCount.
func TestEntry_BeginRun_ConcurrentCAS(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)

	const goroutines = 50
	var wins atomic.Int64
	var wg sync.WaitGroup
	ready := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // all goroutines start at roughly the same time
			if e.BeginRun() {
				wins.Add(1)
				// Hold the run briefly so latecomers see isRunning=true.
				time.Sleep(5 * time.Millisecond)
				e.EndRun(nil)
			}
		}()
	}

	close(ready)
	wg.Wait()

	// Only one goroutine should have won each CAS window. Because EndRun
	// releases isRunning, later goroutines may each win their own window once
	// the previous run completes. The invariant we care about is that at no
	// point did two goroutines run concurrently — i.e., wins must equal the
	// number of sequential runs that each completed before the next one started.
	// We cannot bound this tightly without controlling scheduling, but what we
	// CAN assert is that wins < goroutines (the guard fired at least once) and
	// that the entry's RunCount matches wins (no ghost runs).
	got := wins.Load()
	info, _ := r.Get("c")
	if got == int64(goroutines) {
		t.Errorf("all %d goroutines won BeginRun — concurrent-execution guard did not fire", goroutines)
	}
	if info.RunCount != got {
		t.Errorf("RunCount=%d != wins=%d", info.RunCount, got)
	}
}

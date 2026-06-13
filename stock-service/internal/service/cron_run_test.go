package service

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/repository"
)

// runCronBriefly starts a cron's Run loop with a short-interval ticker, lets at
// least one tick fire, then cancels and waits for the goroutine to exit. This
// exercises the ticker.C + BeginRun/EndRun + ctx.Done paths.
func runCronBriefly(t *testing.T, run func(context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		run(ctx)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond) // allow a tick (interval ~10ms)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cron Run did not return after cancel")
	}
}

func TestRecurringOrderCron_Run(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewRecurringOrderCron(svc, 10*time.Millisecond, reg)
	runCronBriefly(t, cron.Run)
}

func TestRecurringOrderCron_DefaultsInterval(t *testing.T) {
	svc, _ := newRecurringOrderSvc(t, &fakeOrderPlacer{}, nil)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewRecurringOrderCron(svc, 0, reg) // 0 → defaults to 1h
	if cron.interval != time.Hour {
		t.Errorf("interval = %v, want 1h default", cron.interval)
	}
}

func TestRecurringFundCron_Run(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewRecurringFundCron(svc, 10*time.Millisecond, reg)
	runCronBriefly(t, cron.Run)
}

func TestRecurringFundCron_DefaultsInterval(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewRecurringFundCron(svc, 0, reg)
	if cron.interval != time.Hour {
		t.Errorf("interval = %v, want 1h default", cron.interval)
	}
}

func TestPriceAlertCron_Run(t *testing.T) {
	svc, db, listings, _ := newAlertFixture(t)
	repo := repository.NewPriceAlertRepository(db)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewPriceAlertCron(svc, listings, repo, 10*time.Millisecond, reg)
	runCronBriefly(t, cron.Run)
}

func TestFundLifecycleCron_Run(t *testing.T) {
	db := openLifecycleDB(t)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewFundLifecycleCron(db, &fakeRecurringNotifier{}, 10*time.Millisecond, reg)
	runCronBriefly(t, cron.Run)
}

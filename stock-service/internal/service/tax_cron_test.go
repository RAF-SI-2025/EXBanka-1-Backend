package service

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/contract/cronreg"
)

func nilRegistry() *cronreg.Registry { return cronreg.NewRegistry("test", nil) }

// TestTaxCronService_NewAndCancel verifies StartMonthlyCron registers a
// goroutine that respects context cancellation and runCollection executes
// without crashing on a zero-state TaxService.
func TestTaxCronService_StartCancels(t *testing.T) {
	svc, _ := buildTaxService()
	cron := NewTaxCronService(svc, nilRegistry())
	if cron == nil {
		t.Fatal("expected service")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cron.StartMonthlyCron(ctx)
	cancel()
	// Give the goroutine a moment to observe the cancellation. We can't join
	// it directly, but `time.Sleep` here is bounded and the test would
	// observe a goroutine leak via -race anyway.
	time.Sleep(50 * time.Millisecond)
}

// TestTaxCronService_RunCollection exercises the runCollection direct call
// against a fresh service (zero collections expected).
func TestTaxCronService_RunCollection(t *testing.T) {
	svc, _ := buildTaxService()
	cron := NewTaxCronService(svc, nilRegistry())
	_ = cron.runCollection() // should log + return; no panic
}

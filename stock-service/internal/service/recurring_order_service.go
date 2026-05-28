package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
)

// RecurringOrderCron periodically wakes up and calls RunDue on the
// RecurringOrderService. Honours ctx.Done() and uses time.Ticker.
type RecurringOrderCron struct {
	svc      *RecurringOrderService
	interval time.Duration
	entry    *cronreg.Entry
}

func NewRecurringOrderCron(svc *RecurringOrderService, interval time.Duration, registry *cronreg.Registry) *RecurringOrderCron {
	if interval <= 0 {
		interval = time.Hour
	}
	c := &RecurringOrderCron{svc: svc, interval: interval}
	c.entry = registry.Register("recurring-order-cron", "Execute due recurring stock orders", interval)
	return c
}

func (c *RecurringOrderCron) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	log.Printf("recurring-order cron started (interval=%s)", c.interval)
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if !c.entry.BeginRun() {
				continue
			}
			c.svc.RunDue(ctx, t.UTC())
			c.entry.EndRun(nil)
		case <-c.entry.TriggerChan():
			if !c.entry.BeginRun() {
				continue
			}
			c.svc.RunDue(ctx, time.Now().UTC())
			c.entry.EndRun(nil)
		}
	}
}

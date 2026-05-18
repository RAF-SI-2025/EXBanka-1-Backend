package service

import (
	"context"
	"log"
	"time"
)

// RecurringOrderCron periodically wakes up and calls RunDue on the
// RecurringOrderService. Honours ctx.Done() and uses time.Ticker.
type RecurringOrderCron struct {
	svc      *RecurringOrderService
	interval time.Duration
}

func NewRecurringOrderCron(svc *RecurringOrderService, interval time.Duration) *RecurringOrderCron {
	if interval <= 0 {
		interval = time.Hour
	}
	return &RecurringOrderCron{svc: svc, interval: interval}
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
			c.svc.RunDue(ctx, t.UTC())
		}
	}
}

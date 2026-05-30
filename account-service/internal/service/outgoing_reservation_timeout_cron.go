package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
)

// OutgoingReservationTimeoutCron is the time-safety backstop for cross-bank
// money DEBIT holds. A receiver (or a sender's local apply) that placed an
// outgoing reservation at NEW_TX holds the customer's funds until the peer
// sends COMMIT_TX or ROLLBACK_TX. If the peer never responds, nothing else
// would release the hold — this cron releases any pending OutgoingReservation
// older than TTL, returning the funds to AvailableBalance.
//
// It is a BACKSTOP: the primary release paths are inline (NO vote) and the
// transaction-service OutboundReplayCron (stuck outbound rows). ReleaseOutgoing
// is idempotent + status-guarded (no-op on settled/released), so a release that
// races a legitimate late COMMIT can't double-act: whichever transitions the
// pending row first wins; the loser is a no-op. TTL is therefore chosen
// comfortably longer than the worst-case dispatch+commit window.
type OutgoingReservationTimeoutCron struct {
	svc   *OutgoingReservationService
	ttl   time.Duration
	tick  time.Duration
	batch int
	entry *cronreg.Entry
}

func NewOutgoingReservationTimeoutCron(svc *OutgoingReservationService, ttl time.Duration, registry *cronreg.Registry) *OutgoingReservationTimeoutCron {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	c := &OutgoingReservationTimeoutCron{svc: svc, ttl: ttl, tick: time.Minute, batch: 200}
	c.entry = registry.Register("outgoing-reservation-timeout",
		"Release cross-bank outgoing fund holds with no COMMIT/ROLLBACK after TTL", c.tick)
	return c
}

// WithTickInterval overrides the sweep cadence (default 1m). Tests use this.
func (c *OutgoingReservationTimeoutCron) WithTickInterval(d time.Duration) *OutgoingReservationTimeoutCron {
	if d > 0 {
		c.tick = d
	}
	return c
}

// Start launches the sweep goroutine; exits on ctx cancellation.
func (c *OutgoingReservationTimeoutCron) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *OutgoingReservationTimeoutCron) loop(ctx context.Context) {
	ticker := time.NewTicker(c.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !c.entry.BeginRun() {
				continue
			}
			c.entry.EndRun(c.Sweep(ctx))
		case <-c.entry.TriggerChan():
			if !c.entry.BeginRun() {
				continue
			}
			c.entry.EndRun(c.Sweep(ctx))
		}
	}
}

// Sweep releases all pending outgoing reservations older than TTL. Exported for
// tests and manual triggers. Per-row failures are logged and skipped so one bad
// row doesn't stall the batch.
func (c *OutgoingReservationTimeoutCron) Sweep(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-c.ttl)
	rows, err := c.svc.ListStalePending(cutoff, c.batch)
	if err != nil {
		return err
	}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rerr := c.svc.ReleaseOutgoing(ctx, rows[i].ReservationKey); rerr != nil {
			log.Printf("outgoing-reservation-timeout: release %s failed: %v", rows[i].ReservationKey, rerr)
		} else {
			log.Printf("outgoing-reservation-timeout: released stale hold %s (acct %s amount %s)",
				rows[i].ReservationKey, rows[i].AccountNumber, rows[i].Amount)
		}
	}
	return nil
}

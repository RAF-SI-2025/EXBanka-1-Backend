package service

import (
	"context"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// InterBankTimeoutCron is the receiver-side cron that abandons pending
// reservations whose Commit never arrived within ReceiverWait (Spec 3 §9.5).
type InterBankTimeoutCron struct {
	svc      *InterBankService
	interval time.Duration
	wait     time.Duration
	batchSize int
}

// NewInterBankTimeoutCron constructs the cron.
func NewInterBankTimeoutCron(svc *InterBankService, interval, wait time.Duration) *InterBankTimeoutCron {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if wait == 0 {
		wait = 90 * time.Second
	}
	return &InterBankTimeoutCron{svc: svc, interval: interval, wait: wait, batchSize: 50}
}

// Start launches the cron. Honors ctx cancellation.
func (c *InterBankTimeoutCron) Start(ctx context.Context) {
	shared.RunScheduled(ctx, shared.ScheduledJob{
		Name:     "interbank-receiver-timeout",
		Interval: c.interval,
		OnTick:   c.RunOnce,
		OnError: func(err error) {
			log.Printf("interbank receiver-timeout cron: tick failed: %v", err)
		},
	})
}

// RunOnce releases incoming reservations whose ready_sent age exceeds
// `wait` and transitions the row to abandoned.
func (c *InterBankTimeoutCron) RunOnce(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-c.wait)
	rows, err := c.svc.Repo().ListReceiverStaleReadySent(cutoff, c.batchSize)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		reservationKey := "interbank-in-" + row.TxID
		if err := c.svc.Accounts().ReleaseIncoming(ctx, reservationKey); err != nil {
			log.Printf("interbank timeout: release incoming tx=%s: %v", row.TxID, err)
			continue
		}
		if err := c.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusReadySent, model.StatusAbandoned); err != nil {
			log.Printf("interbank timeout: status update tx=%s: %v", row.TxID, err)
			continue
		}
		_ = c.svc.Repo().SetErrorReason(row.TxID, row.Role, "commit_timeout")
		row.Status = model.StatusAbandoned
		row.ErrorReason = "commit_timeout"
		c.svc.PublishStatusKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row)
	}
	return nil
}

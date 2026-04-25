package service

import (
	"context"
	"log"
	"time"

	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/repository"
)

// CrossbankCheckStatusCron periodically scans for InterBankSagaLog rows in
// pending state past the timeout (default 30s) and asks the peer bank for
// the canonical state. On peer-confirmed COMMITTED rows it transitions our
// row to completed; on peer NOT_FOUND it transitions to failed and runs the
// owning saga's compensation.
type CrossbankCheckStatusCron struct {
	logs     *repository.InterBankSagaLogRepository
	peers    CrossbankPeerRouter
	producer *kafkaprod.Producer
	tickEvery time.Duration
	staleAge  time.Duration
}

func NewCrossbankCheckStatusCron(
	logs *repository.InterBankSagaLogRepository,
	peers CrossbankPeerRouter,
	producer *kafkaprod.Producer,
	tickEvery, staleAge time.Duration,
) *CrossbankCheckStatusCron {
	if tickEvery == 0 {
		tickEvery = 30 * time.Second
	}
	if staleAge == 0 {
		staleAge = 30 * time.Second
	}
	return &CrossbankCheckStatusCron{
		logs: logs, peers: peers, producer: producer,
		tickEvery: tickEvery, staleAge: staleAge,
	}
}

func (c *CrossbankCheckStatusCron) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(c.tickEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.RunOnce(ctx); err != nil {
					log.Printf("WARN: cross-bank CHECK_STATUS run: %v", err)
				}
			}
		}
	}()
}

// RunOnce queries every peer for the canonical state of every pending row.
// On status mismatch it transitions our local row.
func (c *CrossbankCheckStatusCron) RunOnce(ctx context.Context) error {
	if c.logs == nil || c.peers == nil {
		return nil
	}
	stale := time.Now().UTC().Add(-c.staleAge)
	rows, err := c.logs.ListStaleByStatus("pending", stale, 200)
	if err != nil {
		return err
	}
	for _, row := range rows {
		peer, err := c.peers.ClientFor(row.RemoteBankCode)
		if err != nil {
			continue
		}
		resp, err := peer.CheckStatus(ctx, PeerCheckStatusRequest{TxID: row.TxID, Phase: row.Phase})
		if err != nil {
			continue
		}
		if resp.NotFound {
			row.Status = "failed"
			row.ErrorReason = "peer reports unknown saga"
			_ = c.logs.UpsertByTxPhaseRole(&row)
			continue
		}
		if resp.Status != "" && resp.Status != row.Status {
			row.Status = resp.Status
			row.ErrorReason = resp.ErrorReason
			_ = c.logs.UpsertByTxPhaseRole(&row)
		}
	}
	return nil
}

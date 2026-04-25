package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// CrossbankOrphanReservationCron sweeps responder-side phase-2 reservations
// (`reserve_seller_shares` rows in completed status) that never received a
// follow-up TRANSFER_OWNERSHIP — typically because the initiator-side saga
// stopped between phases 2 and 4. Releases the dangling holding reservation
// and marks the saga row failed so it disappears from active queries.
type CrossbankOrphanReservationCron struct {
	logs       *repository.InterBankSagaLogRepository
	holdingRes *HoldingReservationService
	tickEvery  time.Duration
	staleAge   time.Duration
}

func NewCrossbankOrphanReservationCron(
	logs *repository.InterBankSagaLogRepository,
	holdingRes *HoldingReservationService,
	tickEvery, staleAge time.Duration,
) *CrossbankOrphanReservationCron {
	if tickEvery == 0 {
		tickEvery = 5 * time.Minute
	}
	if staleAge == 0 {
		staleAge = 30 * time.Minute
	}
	return &CrossbankOrphanReservationCron{
		logs: logs, holdingRes: holdingRes,
		tickEvery: tickEvery, staleAge: staleAge,
	}
}

func (c *CrossbankOrphanReservationCron) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(c.tickEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.RunOnce(ctx); err != nil {
					log.Printf("WARN: cross-bank orphan reservation run: %v", err)
				}
			}
		}
	}()
}

// RunOnce releases dangling phase-2-completed reservations whose contract
// was never created (i.e. the initiator stopped between phases 2 and 4).
func (c *CrossbankOrphanReservationCron) RunOnce(ctx context.Context) error {
	if c.logs == nil || c.holdingRes == nil {
		return nil
	}
	stale := time.Now().UTC().Add(-c.staleAge)
	rows, err := c.logs.ListStaleByStatus(model.IBSagaStatusCompleted, stale, 200)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Phase != model.PhaseReserveSellerShares || row.Role != model.SagaRoleResponder {
			continue
		}
		// Try releasing — idempotent on already-released reservations.
		if row.ContractID != nil {
			if _, err := c.holdingRes.ReleaseForOTCContract(ctx, *row.ContractID); err != nil {
				log.Printf("WARN: orphan release tx=%s: %v", row.TxID, err)
				continue
			}
		}
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = "orphan reservation swept (no contract within timeout)"
		_ = c.logs.UpsertByTxPhaseRole(&row)
	}
	return nil
}

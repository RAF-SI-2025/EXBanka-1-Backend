package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"gorm.io/gorm"
)

// RecoverAcceptNegotiationSaga drives a crash-stranded OTC contract-formation
// (accept) saga to a terminal state with no human intervention. Called by the
// saga-recovery reconciler for any stuck accept step; idempotent, so being
// invoked once per stuck row of the same saga (or across ticks) converges.
//
// Keyed on sagaID ONLY: the accept saga's first row (reserve_and_contract) is
// recorded BEFORE the contract row exists, so its order_id can be 0. Recovery
// instead loads the contract by its saga_id (OptionContract.SagaID, stamped at
// mint time) and rebuilds the saga under the SAME sagaID so IsCompleted and all
// deterministic idempotency keys line up with the original attempt.
//
// Direction is chosen from the persisted log:
//   - If the saga has compensation rows it was already rolling back when the
//     process died → finish the rollback (Compensate): release the buyer's
//     premium reservation, release the seller's share reservation, refund any
//     settled premium, and delete the contract — leaving prior state restored.
//   - Otherwise the saga crashed mid-forward → resume forward (Execute). The
//     executor skips steps the recorder reports completed and replays only the
//     rest; every forward step is idempotent (idempotent contract create, keyed
//     reserve/settle/credit), so the saga reaches COMMITTED.
//
// If no contract exists for the saga_id yet (crash before step 1 created it),
// there is nothing reserved or moved to recover: log a WARN and return nil.
func (s *OTCOfferService) RecoverAcceptNegotiationSaga(ctx context.Context, sagaID string) error {
	if sagaID == "" {
		return fmt.Errorf("recover accept saga: empty sagaID")
	}
	c, err := s.contracts.GetBySagaID(sagaID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Crashed before step 1 minted the contract — nothing was reserved
			// or moved, so there is nothing to recover.
			log.Printf("WARN: accept saga %s has no contract row (crash before mint); nothing to recover", sagaID)
			return nil
		}
		return fmt.Errorf("recover accept saga %s: load contract: %w", sagaID, err)
	}

	sg, state, err := s.buildAcceptSaga(ctx, sagaID, c)
	if err != nil {
		return fmt.Errorf("recover accept saga %s: rebuild: %w", sagaID, err)
	}

	rollingBack := false
	if chk, ok := s.sagaRepo.(sagaCompensationChecker); ok {
		has, herr := chk.HasCompensations(sagaID)
		if herr != nil {
			return fmt.Errorf("recover accept saga %s: compensation check: %w", sagaID, herr)
		}
		rollingBack = has
	}

	if rollingBack {
		// The original attempt was aborting — finish the rollback.
		return sg.Compensate(ctx, state)
	}
	// Crashed mid-forward — resume forward to completion. All forward steps are
	// idempotent, so replaying the crashed step is safe.
	return sg.Execute(ctx, state)
}

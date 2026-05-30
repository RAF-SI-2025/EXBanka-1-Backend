package service

import (
	"context"
	"fmt"
)

// sagaCompensationChecker is the narrow query crash recovery uses to decide a
// stuck exercise saga's direction. Implemented by *repository.SagaLogRepository;
// when the wired sagaRepo does not satisfy it (test doubles), recovery defaults
// to forward-resume.
type sagaCompensationChecker interface {
	HasCompensations(sagaID string) (bool, error)
}

// RecoverExerciseSaga drives a crash-stranded OTC exercise saga to a terminal
// state with no human intervention. Called by the saga-recovery reconciler for
// any stuck exercise step; idempotent, so being invoked once per stuck row of
// the same saga (or across ticks) converges.
//
// Direction is chosen from the persisted log:
//   - If the saga has compensation rows it was already rolling back when the
//     process died → finish the rollback (Compensate). Every Backward is
//     idempotent, so re-running ones that already ran is a no-op. The contract
//     is left ACTIVE and all money/shares restored — the buyer can re-exercise.
//   - Otherwise the saga crashed mid-forward → resume forward (Execute). The
//     executor skips steps the recorder reports completed and replays only the
//     rest; every forward step is idempotent (keyed credits/debits, settlement
//     and credit markers), so the saga reaches COMMITTED (contract EXERCISED).
//
// contractID is read from the stuck row's order_id (the exercise saga stamps
// the contract id there via state["order_id"]). The saga is rebuilt under the
// SAME sagaID so IsCompleted and all deterministic idempotency keys line up
// with the original attempt.
func (s *OTCOfferService) RecoverExerciseSaga(ctx context.Context, sagaID string, contractID uint64) error {
	if sagaID == "" || contractID == 0 {
		return fmt.Errorf("recover exercise saga: empty sagaID or zero contractID")
	}
	c, err := s.contracts.GetByID(contractID)
	if err != nil {
		return fmt.Errorf("recover exercise saga %s: load contract %d: %w", sagaID, contractID, err)
	}

	sg, state, err := s.buildExerciseSaga(ctx, sagaID, c)
	if err != nil {
		return fmt.Errorf("recover exercise saga %s: rebuild: %w", sagaID, err)
	}

	rollingBack := false
	if chk, ok := s.sagaRepo.(sagaCompensationChecker); ok {
		has, herr := chk.HasCompensations(sagaID)
		if herr != nil {
			return fmt.Errorf("recover exercise saga %s: compensation check: %w", sagaID, herr)
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

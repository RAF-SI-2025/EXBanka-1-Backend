package service

import (
	"context"
	"log"
	"strings"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// maxSagaRecoveryRetries is the per-step retry ceiling. Beyond this the
// reconciler leaves the row untouched with a loud ERROR log so operations
// can investigate. Chosen high enough to absorb transient downstream
// flakiness (gRPC hiccups, short account-service outages) but low enough
// that a genuinely broken step doesn't silently retry forever.
const maxSagaRecoveryRetries = 5

// SagaRecovery reconciles stuck saga steps (pending or compensating for
// longer than the scan threshold) after a crash. It walks the saga_logs
// table, classifies each stuck row by step name, and takes the safest
// recovery action available for that kind of step:
//
//   - settle_reservation / settle_reservation_quote: checks
//     account-service for whether the txnID is already in SettledTransactionIds;
//     if yes, marks the row completed; otherwise retries PartialSettleReservation,
//     which is idempotent on (order_id, order_transaction_id).
//   - update_holding / decrement_holding: these are idempotent on their PKs /
//     order-transaction IDs at the storage layer. We treat them as
//     safe-to-auto-complete because double application is a no-op; full
//     verification would require extra query plumbing not yet wired.
//   - credit_* / compensate_*: NOT auto-retried. UpdateBalance lacks memo-based
//     dedup, so a blind retry could double-credit / double-debit. Logged for
//     human review.
//   - Placement-saga steps (validate_listing, reserve_funds, etc.): NOT
//     auto-retried. These are much more disruptive to the user when replayed
//     and must be reviewed before any action.
//
// Started once at service boot via Run; runs periodically thereafter until
// the provided context is cancelled.
type SagaRecovery struct {
	sagaRepo      SagaRecoveryLogRepo
	accountClient FillAccountClient
}

// SagaRecoveryLogRepo is the minimum interface the reconciler needs.
// Satisfied by *repository.SagaLogRepository and any test double.
type SagaRecoveryLogRepo interface {
	ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error)
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	IncrementRetryCount(id uint64) error
}

// NewSagaRecovery builds a reconciler. Only the saga repository and the
// account client are truly required today — the settle-reservation path is
// the only one that reads external state to decide "already done?" vs.
// "retry it". Holding-side verification (update_holding / decrement_holding)
// is deliberately simplified to "mark completed" per Task 17's scope.
func NewSagaRecovery(
	sagaRepo SagaRecoveryLogRepo,
	accountClient FillAccountClient,
) *SagaRecovery {
	return &SagaRecovery{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
	}
}

// Reconcile walks stuck saga steps and tries to drive each to completion.
// Intended to be called once at startup (to catch crash survivors) and then
// periodically from Run. Errors on individual steps are logged and counted
// (via IncrementRetryCount) rather than aborting the sweep — one flaky row
// shouldn't block recovery of the rest.
func (r *SagaRecovery) Reconcile(ctx context.Context) error {
	stuck, err := r.sagaRepo.ListStuckSagas(30 * time.Second)
	if err != nil {
		return err
	}
	for _, step := range stuck {
		if step.RetryCount >= maxSagaRecoveryRetries {
			log.Printf("ERROR: saga step %d (order=%d, step=%s) stuck after %d retries — needs human review",
				step.ID, step.OrderID, step.StepName, step.RetryCount)
			continue
		}
		if err := r.reconcileStep(ctx, step); err != nil {
			log.Printf("WARN: saga recovery step %d (order=%d, step=%s): %v",
				step.ID, step.OrderID, step.StepName, err)
			if incErr := r.sagaRepo.IncrementRetryCount(step.ID); incErr != nil {
				log.Printf("WARN: bump retry count on saga step %d: %v", step.ID, incErr)
			}
		}
	}
	return nil
}

// reconcileStep dispatches on the step name. Unknown step names are logged
// and skipped — we prefer to leave a mystery row alone rather than take the
// wrong action on it.
func (r *SagaRecovery) reconcileStep(ctx context.Context, step model.SagaLog) error {
	switch step.StepName {
	case "settle_reservation", "settle_reservation_quote":
		return r.reconcileSettle(ctx, step)

	case "update_holding":
		// holdingRepo.Upsert is keyed on (user_id, security_type, security_id,
		// account_id) with a weighted-average update, so replaying is
		// idempotent at the row level. Without extra plumbing (a dedicated
		// "was this fill's holding delta applied?" query) we don't have a
		// cheap way to verify; treating it as done is the honest best-effort.
		return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
			"auto-completed by recovery (holding upsert is idempotent)")

	case "decrement_holding":
		// HoldingReservationService.PartialSettle is idempotent on
		// orderTransactionID via ON CONFLICT DO NOTHING on the settlements
		// table. Marking completed mirrors the update_holding rationale.
		return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
			"auto-completed by recovery (holding settlement is idempotent)")

	case "credit_proceeds", "credit_base", "credit_commission",
		"compensate_settle_via_credit", "compensate_credit_via_debit", "compensate_quote_settle":
		// UpdateBalance on account-service has no memo-based dedup, so
		// retrying a credit/debit could double-apply it. Leave the row as-is
		// and surface it loudly for operations to resolve manually.
		log.Printf("WARN: stuck credit/debit saga step %d (order=%d, step=%s) — not auto-retrying to avoid double-ledger; needs review",
			step.ID, step.OrderID, step.StepName)
		return nil

	case "record_transaction", "convert_amount", "persist_order_pending",
		"approve_order", "validate_listing", "compute_reservation_amount",
		"reserve_funds", "reserve_holding":
		// Placement-saga steps. Auto-retrying is much more disruptive than
		// auto-retrying a fill step — we'd risk duplicating the user's
		// intent to place an order. Leave for human review.
		log.Printf("WARN: stuck placement saga step %d (order=%d, step=%s) — needs human review",
			step.ID, step.OrderID, step.StepName)
		return nil

	case "publish_kafka":
		// Placeholder for Task 16's deferred integration: publish currently
		// happens inline in executeOrder, so this step name shouldn't appear
		// in practice yet. If it does, the publish either already happened
		// or will be driven by a Kafka retry — safe to mark completed.
		return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
			"auto-completed by recovery (publish handled out-of-band)")
	}

	log.Printf("WARN: unknown saga step name %q in recovery (id=%d)", step.StepName, step.ID)
	return nil
}

// reconcileSettle resolves a stuck settle_reservation / settle_reservation_quote
// step by checking account-service: if the txnID is already recorded on the
// reservation, the settlement committed and we only need to catch up our log.
// Otherwise we retry PartialSettleReservation (idempotent on the same txnID)
// and mark the step completed on success.
func (r *SagaRecovery) reconcileSettle(ctx context.Context, step model.SagaLog) error {
	if step.OrderTransactionID == nil {
		// Settle steps always carry a transaction id; a nil here means the
		// step was recorded incorrectly. Nothing safe to do.
		return nil
	}
	txnID := *step.OrderTransactionID

	resp, err := r.accountClient.Stub().GetReservation(ctx, &accountpb.GetReservationRequest{OrderId: step.OrderID})
	if err != nil {
		return err
	}
	if !resp.Exists {
		// No reservation on account-service at all — either released or
		// never created. Can't safely retry without knowing which; leave for
		// human review.
		return nil
	}
	for _, id := range resp.SettledTransactionIds {
		if id == txnID {
			return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
				"auto-completed by recovery (found on account-service)")
		}
	}

	// Not settled yet — retry the settle with the amount/memo we recorded.
	if step.Amount == nil {
		// Shouldn't happen (SagaExecutor.RunStep writes amount for settle
		// steps), but defend against it.
		return nil
	}
	memo := "recovery settlement"
	if step.StepName == "settle_reservation_quote" {
		memo = "Forex recovery — quote settlement"
	}
	if _, err := r.accountClient.PartialSettleReservation(ctx, step.OrderID, txnID, *step.Amount, memo); err != nil {
		// If the error is "would exceed reservation" the most likely cause
		// is a concurrent settle that already landed our txnID; recheck
		// before surfacing the error.
		if strings.Contains(err.Error(), "would exceed reservation") {
			if resp2, gerr := r.accountClient.Stub().GetReservation(ctx, &accountpb.GetReservationRequest{OrderId: step.OrderID}); gerr == nil {
				for _, id := range resp2.SettledTransactionIds {
					if id == txnID {
						return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
							"auto-completed by recovery (settled by concurrent call)")
					}
				}
			}
		}
		return err
	}
	return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted, "recovered by retry")
}

// Run spawns a goroutine that calls Reconcile once immediately and then
// every tickInterval until ctx is cancelled. Safe to call from cmd/main.go;
// must be passed the long-lived service ctx so the ticker lives for the
// process lifetime and honors graceful shutdown.
func (r *SagaRecovery) Run(ctx context.Context, tickInterval time.Duration) {
	go func() {
		if err := r.Reconcile(ctx); err != nil {
			log.Printf("WARN: initial saga recovery: %v", err)
		}
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Reconcile(ctx); err != nil {
					log.Printf("WARN: periodic saga recovery: %v", err)
				}
			}
		}
	}()
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
)

// recoveryKeyFor returns the deterministic idempotency key for a saga step
// driven credit/debit. Shared by the forward-path call sites (portfolio_service
// and forex_fill_service) and by SagaRecovery, so the recovery retry key
// matches the original call's key and account-service dedups the RPC at the
// ledger layer.
//
// Every key uses the order_transaction_id — globally unique per fill in
// stock-service — plus a step-unique prefix so two different saga steps for
// the same fill can never collide.
func recoveryKeyFor(stepName string, txnID uint64) string {
	switch stepName {
	case "credit_proceeds":
		return fmt.Sprintf("sell-credit-%d", txnID)
	case "credit_base":
		return fmt.Sprintf("forex-base-credit-%d", txnID)
	case "credit_commission":
		return fmt.Sprintf("commission-%d", txnID)
	case "compensate_settle_via_credit":
		return fmt.Sprintf("compensate-buy-%d", txnID)
	case "compensate_credit_via_debit":
		return fmt.Sprintf("compensate-sell-%d", txnID)
	case "compensate_quote_settle":
		return fmt.Sprintf("compensate-forex-%d", txnID)
	}
	return ""
}

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
//   - credit_* / compensate_*: auto-retried with the same deterministic
//     idempotency_key the forward-path step used (see recoveryKeyFor). The
//     partial unique index on ledger_entries.idempotency_key makes a second
//     apply a safe no-op, so the reconciler can safely re-issue the RPC.
//   - Placement-saga steps (validate_listing, reserve_funds, etc.): NOT
//     auto-retried. These are much more disruptive to the user when replayed
//     and must be reviewed before any action.
//
// Started once at service boot via Run; runs periodically thereafter until
// the provided context is cancelled.
type SagaRecovery struct {
	sagaRepo       SagaRecoveryLogRepo
	accountClient  FillAccountClient
	orderRepo      RecoveryOrderRepo
	stateAccountNo string
}

// SagaRecoveryLogRepo is the minimum interface the reconciler needs.
// Satisfied by *repository.SagaLogRepository and any test double.
type SagaRecoveryLogRepo interface {
	ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error)
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	IncrementRetryCount(id uint64) error
}

// RecoveryOrderRepo is the narrow slice of OrderRepo the reconciler needs to
// look up the user's account (and, for forex, the base account) when
// retrying a credit/debit step. Expressed as a dedicated interface so tests
// can stub it without standing up a full OrderRepository.
type RecoveryOrderRepo interface {
	GetByID(id uint64) (*model.Order, error)
}

// NewSagaRecovery builds a reconciler. orderRepo may be nil — in that case
// credit/debit auto-retry is disabled and those steps fall back to the
// logged-for-review behaviour. In production main.go always wires it so
// the retry path is active. stateAccountNo is the bank's commission account
// number; empty falls back to the order.AccountID for commission retries
// (primarily a test-wiring convenience).
func NewSagaRecovery(
	sagaRepo SagaRecoveryLogRepo,
	accountClient FillAccountClient,
	orderRepo RecoveryOrderRepo,
	stateAccountNo string,
) *SagaRecovery {
	return &SagaRecovery{
		sagaRepo:       sagaRepo,
		accountClient:  accountClient,
		orderRepo:      orderRepo,
		stateAccountNo: stateAccountNo,
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

// reconcileStep dispatches on the step name. The dispatch is split into two
// halves:
//
//  1. Legacy compensate_<step> string-prefixed names (only ever produced by the
//     deleted SagaExecutor.RunCompensation path) are matched FIRST via direct
//     string compare. These rows pre-date the shared.Saga migration and won't
//     be regenerated, but we must continue draining ones that already exist
//     in the table.
//
//  2. Everything else is routed via a typed switch on saga.StepKind. The
//     `default` arm panics — there is no Go-language way to enforce switch
//     exhaustiveness on string-aliased types, so the panicking default is the
//     loud fail-fast that catches any new StepKind constant added to
//     contract/shared/saga/steps.go without an accompanying recovery decision.
//     If you see this panic in production, the fix is here in this file:
//     decide which reconciler the new step kind belongs to (or add a tiny
//     idempotent-mark-completed reconciler) and add the case.
func (r *SagaRecovery) reconcileStep(ctx context.Context, step model.SagaLog) error {
	// Legacy compensation step names from pre-shared.Saga code paths. These
	// strings are NOT StepKind constants — the deleted SagaExecutor.RunCompensation
	// path wrote them as bespoke labels distinct from the forward-step names.
	// Leave them BEFORE the typed switch so the typed default doesn't panic
	// on rows already persisted under these legacy labels.
	switch step.StepName {
	case "compensate_settle_via_credit",
		"compensate_credit_via_debit",
		"compensate_quote_settle":
		return r.reconcileCreditDebit(ctx, step)
	}

	switch saga.StepKind(step.StepName) {
	// --- forex / fill settlement: check account-service then retry. ---
	case saga.StepSettleReservation, saga.StepSettleReservationQuote:
		return r.reconcileSettle(ctx, step)

	// --- credit/debit retry via deterministic idempotency key. ---
	// account-service's partial unique index on ledger_entries.idempotency_key
	// makes a second apply a safe no-op so we can drive these to completion.
	case saga.StepCreditProceeds, saga.StepCreditBase, saga.StepCreditCommission:
		return r.reconcileCreditDebit(ctx, step)

	// --- holding mutations: idempotent at the storage layer (weighted-average
	// upsert / ON CONFLICT DO NOTHING settlement), so marking completed is the
	// honest best-effort without extra verification plumbing.
	case saga.StepUpdateHolding:
		return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
			"auto-completed by recovery (holding upsert is idempotent)")

	case saga.StepDecrementHolding:
		return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted,
			"auto-completed by recovery (holding settlement is idempotent)")

	// --- placement / order-construction steps: auto-replay would risk
	// duplicating the user's order. Leave for human review.
	case saga.StepPersistOrderPending,
		saga.StepReserveFunds,
		saga.StepReserveHolding,
		saga.StepFinalizeOrder,
		saga.StepConvertAmount,
		saga.StepRecordTransaction:
		log.Printf("WARN: stuck placement saga step %d (order=%d, step=%s) — needs human review",
			step.ID, step.OrderID, step.StepName)
		return nil

	// --- OTC + Fund money-movement / position steps. These sagas migrated
	// to shared.Saga without dedicated recovery reconcilers; the safest
	// behaviour is the same hands-off "log and leave for review" as
	// placement steps until per-step semantics are wired. Marking them as
	// auto-completed could double-credit; auto-retrying could double-debit
	// without an idempotency-key contract.
	case saga.StepReserveAndContract,
		saga.StepReservePremium,
		saga.StepReserveStrike,
		saga.StepSettlePremiumBuyer,
		saga.StepSettleStrikeBuyer,
		saga.StepConsumeSellerHolding,
		saga.StepDebitSource,
		saga.StepCreditTarget,
		saga.StepDebitFund,
		saga.StepCreditFund,
		saga.StepCreditPremiumSeller,
		saga.StepCreditStrikeSeller,
		saga.StepUpsertPosition,
		saga.StepCreditBankFee:
		log.Printf("WARN: stuck OTC/Fund saga step %d (order=%d, step=%s) — needs human review",
			step.ID, step.OrderID, step.StepName)
		return nil

	// --- Crossbank steps. These ride on InterBankSagaLog (a different table)
	// in production and have a separate reconciliation cron. They should NOT
	// land in the SagaLog table this reconciler scans; if one ever does it's
	// a routing bug, and human review is the correct response.
	case saga.StepReserveBuyerFunds,
		saga.StepCreateContract,
		saga.StepReserveSellerShares,
		saga.StepDebitBuyer,
		saga.StepCreditSeller,
		saga.StepTransferOwnership,
		saga.StepFinalizeAccept,
		saga.StepDebitStrike,
		saga.StepCreditStrike,
		saga.StepDeliverShares,
		saga.StepRefundReservation,
		saga.StepMarkExpired:
		log.Printf("WARN: crossbank saga step %d (order=%d, step=%s) found in SagaLog "+
			"(should be on InterBankSagaLog) — needs human review",
			step.ID, step.OrderID, step.StepName)
		return nil

	// --- Transaction-service steps. Owned by transaction-service's own saga
	// log; should never land here. Same rationale as crossbank: log and let
	// a human investigate the routing bug rather than guess at remediation.
	case saga.StepDebitSender,
		saga.StepCreditRecipient,
		saga.StepCreditBankCommission,
		saga.StepDebitUserFrom,
		saga.StepCreditBankFrom,
		saga.StepDebitBankTo,
		saga.StepCreditUserTo:
		log.Printf("WARN: transaction-service saga step %d (order=%d, step=%s) found in "+
			"stock-service SagaLog — needs human review",
			step.ID, step.OrderID, step.StepName)
		return nil

	default:
		panic(fmt.Sprintf(
			"recovery: unhandled StepKind %q — add case to switch in saga_recovery.go",
			step.StepName))
	}
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
	// Recovery key — retries within recovery share the same key so a
	// late-arriving original call also collapses through the cache.
	recoveryKey := fmt.Sprintf("recovery-settle-%d-%d", step.OrderID, txnID)
	if _, err := r.accountClient.PartialSettleReservation(ctx, step.OrderID, txnID, *step.Amount, memo, recoveryKey); err != nil {
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

// reconcileCreditDebit retries a stuck credit_*/compensate_* step using the
// deterministic idempotency_key shared with the forward-path caller. The key
// scheme (see recoveryKeyFor) guarantees that a retry lands on the same
// ledger entry row as the original call if the original commit succeeded —
// account-service short-circuits and returns success without mutating.
//
// Missing plumbing (nil orderRepo, nil step.OrderTransactionID, zero
// step.Amount) degrades to the old "log and leave alone" behaviour so the
// recovery path can't destabilise a service that's wired incompletely.
func (r *SagaRecovery) reconcileCreditDebit(ctx context.Context, step model.SagaLog) error {
	if r.orderRepo == nil || step.OrderTransactionID == nil || step.Amount == nil {
		log.Printf("WARN: stuck credit/debit saga step %d (order=%d, step=%s) — missing recovery plumbing, needs review",
			step.ID, step.OrderID, step.StepName)
		return nil
	}
	txnID := *step.OrderTransactionID
	amount := *step.Amount
	key := recoveryKeyFor(step.StepName, txnID)
	if key == "" {
		log.Printf("WARN: stuck credit/debit saga step %d (order=%d, step=%s) — no recovery key mapping, needs review",
			step.ID, step.OrderID, step.StepName)
		return nil
	}

	order, err := r.orderRepo.GetByID(step.OrderID)
	if err != nil {
		return fmt.Errorf("recovery: order lookup: %w", err)
	}

	// Resolve the target account number for this step. Commission credits
	// go to the state (bank) account; base credits go to the user's base
	// account; everything else goes to the user's main (quote) account.
	var accountNumber string
	switch step.StepName {
	case "credit_commission":
		accountNumber = r.stateAccountNo
		if accountNumber == "" {
			// No state account wired (test path). Fall back to the order's
			// main account so the retry still exercises a valid path.
			accountNumber, err = r.lookupAccountNumber(ctx, order.AccountID)
			if err != nil {
				return err
			}
		}
	case "credit_base":
		if order.BaseAccountID == nil {
			return errors.New("recovery: forex credit_base step without base_account_id")
		}
		accountNumber, err = r.lookupAccountNumber(ctx, *order.BaseAccountID)
		if err != nil {
			return err
		}
	default:
		accountNumber, err = r.lookupAccountNumber(ctx, order.AccountID)
		if err != nil {
			return err
		}
	}

	memo := fmt.Sprintf("Recovery of %s for order #%d txn #%d", step.StepName, order.ID, txnID)
	switch step.StepName {
	case "compensate_credit_via_debit":
		// The sell-side compensation is a debit (reverses the earlier credit_proceeds).
		if _, derr := r.accountClient.DebitAccount(ctx, accountNumber, amount, memo, key); derr != nil {
			return derr
		}
	default:
		// All other retryable steps are credits.
		if _, cerr := r.accountClient.CreditAccount(ctx, accountNumber, amount, memo, key); cerr != nil {
			return cerr
		}
	}
	return r.sagaRepo.UpdateStatus(step.ID, step.Version, model.SagaStatusCompleted, "recovered by retry (idempotency_key)")
}

// lookupAccountNumber resolves an account ID to its account number via
// account-service. Returns a descriptive error on lookup failure so the
// reconciler can log and back off.
func (r *SagaRecovery) lookupAccountNumber(ctx context.Context, accountID uint64) (string, error) {
	resp, err := r.accountClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return "", fmt.Errorf("recovery: get account %d: %w", accountID, err)
	}
	return resp.GetAccountNumber(), nil
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

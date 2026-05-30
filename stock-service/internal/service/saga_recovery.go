package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/orderkind"
	"github.com/exbanka/contract/shared/saga"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
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
// reconciler marks the row dead_letter and publishes to the Kafka
// dead-letter topic so operators can investigate. Chosen high enough to
// absorb transient downstream flakiness (gRPC hiccups, short account-service
// outages) but low enough that a genuinely broken step doesn't silently
// retry forever.
const maxSagaRecoveryRetries = 10

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
// When a step's RetryCount reaches maxSagaRecoveryRetries (10), the row is
// transitioned to "dead_letter" status and a SagaDeadLetterMessage is
// published to stock.saga-dead-letter for operator alerting. This mirrors
// the pattern used by transaction-service's saga recovery.
//
// Started once at service boot via Run; runs periodically thereafter until
// the provided context is cancelled.
type SagaRecovery struct {
	sagaRepo       SagaRecoveryLogRepo
	accountClient  FillAccountClient
	orderRepo      RecoveryOrderRepo
	stateAccountNo string
	// deadLetterProducer publishes dead-letter events when a step exhausts
	// all recovery retries. Optional: when nil, the step is just logged as
	// ERROR (same as the pre-A2.3 behaviour).
	deadLetterProducer *kafkaprod.Producer
	// exerciseRecoverer auto-resolves stuck OTC exercise sagas by re-driving
	// them to a terminal state (forward-resume or rollback). Optional: when
	// nil, exercise steps fall back to logged-for-review. Wired in main.go to
	// the OTCOfferService. See RecoverExerciseSaga.
	exerciseRecoverer ExerciseSagaRecoverer
	// acceptRecoverer auto-resolves stuck OTC accept sagas (forward-resume to a
	// minted contract, or rollback). Optional: nil → accept steps log-for-review.
	acceptRecoverer AcceptSagaRecoverer
	// fundRecoverer auto-resolves stuck fund invest/redeem sagas. Optional: nil
	// → fund steps log-for-review.
	fundRecoverer FundSagaRecoverer
	entry         *cronreg.Entry
}

// ExerciseSagaRecoverer drives a crash-stranded OTC exercise saga to a terminal
// state with no human intervention. Implemented by *OTCOfferService.
type ExerciseSagaRecoverer interface {
	RecoverExerciseSaga(ctx context.Context, sagaID string, contractID uint64) error
}

// AcceptSagaRecoverer drives a crash-stranded OTC accept saga to a terminal
// state with no human intervention. Implemented by *OTCOfferService.
type AcceptSagaRecoverer interface {
	RecoverAcceptSaga(ctx context.Context, sagaID string, offerID uint64) error
}

// FundSagaRecoverer drives a crash-stranded fund invest/redeem saga to a
// terminal state with no human intervention. Implemented by *FundService.
type FundSagaRecoverer interface {
	RecoverFundSaga(ctx context.Context, sagaID string, contribID uint64) error
}

// WithExerciseRecoverer wires the OTC exercise auto-resolver. Returns the same
// *SagaRecovery for chaining at construction time in main.go.
func (r *SagaRecovery) WithExerciseRecoverer(rec ExerciseSagaRecoverer) *SagaRecovery {
	r.exerciseRecoverer = rec
	return r
}

// WithAcceptRecoverer wires the OTC accept auto-resolver.
func (r *SagaRecovery) WithAcceptRecoverer(rec AcceptSagaRecoverer) *SagaRecovery {
	r.acceptRecoverer = rec
	return r
}

// WithFundRecoverer wires the fund invest/redeem auto-resolver.
func (r *SagaRecovery) WithFundRecoverer(rec FundSagaRecoverer) *SagaRecovery {
	r.fundRecoverer = rec
	return r
}

// SagaRecoveryLogRepo is the minimum interface the reconciler needs.
// Satisfied by *repository.SagaLogRepository and any test double.
type SagaRecoveryLogRepo interface {
	ListStuckSagas(olderThan time.Duration) ([]model.SagaLog, error)
	UpdateStatus(id uint64, version int64, newStatus, errMsg string) error
	IncrementRetryCount(id uint64) error
	MarkDeadLetter(id uint64) error
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
//
// producer is the Kafka producer used to publish dead-letter events when a
// step exhausts all recovery retries. Pass nil to disable dead-letter
// publishing (legacy behaviour — only a loud ERROR log is emitted).
func NewSagaRecovery(
	sagaRepo SagaRecoveryLogRepo,
	accountClient FillAccountClient,
	orderRepo RecoveryOrderRepo,
	stateAccountNo string,
	producer *kafkaprod.Producer,
	registry *cronreg.Registry,
) *SagaRecovery {
	s := &SagaRecovery{
		sagaRepo:           sagaRepo,
		accountClient:      accountClient,
		orderRepo:          orderRepo,
		stateAccountNo:     stateAccountNo,
		deadLetterProducer: producer,
	}
	s.entry = registry.Register("saga-recovery", "Reconcile stuck saga steps after crashes", 60*time.Second)
	return s
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
			// Exhausted all recovery retries. Transition to dead_letter and
			// notify operators via Kafka so alerting systems can page on-call.
			if dlErr := r.sagaRepo.MarkDeadLetter(step.ID); dlErr != nil {
				log.Printf("ERROR: saga step %d (order=%d, step=%s) stuck after %d retries; MarkDeadLetter failed: %v — needs human review",
					step.ID, step.OrderID, step.StepName, step.RetryCount, dlErr)
				continue
			}
			log.Printf("ERROR: saga step %d (order=%d, step=%s) moved to dead_letter after %d retries — needs human review",
				step.ID, step.OrderID, step.StepName, step.RetryCount)
			if r.deadLetterProducer != nil {
				amountStr := ""
				if step.Amount != nil {
					amountStr = step.Amount.String()
				}
				dlMsg := kafkamsg.SagaDeadLetterMessage{
					SagaLogID:       step.ID,
					SagaID:          step.SagaID,
					TransactionID:   step.OrderID,
					TransactionType: "stock-saga",
					StepName:        step.StepName,
					AccountNumber:   "",
					Amount:          amountStr,
					RetryCount:      step.RetryCount,
					LastError:       step.ErrorMessage,
				}
				if pubErr := r.deadLetterProducer.PublishSagaDeadLetter(ctx, dlMsg); pubErr != nil {
					log.Printf("WARN: saga dead-letter publish failed for step %d: %v", step.ID, pubErr)
				}
			}
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

	// --- Fund invest/redeem saga steps (assembled by buildInvestSaga /
	// buildRedeemSaga). Auto-resolved by re-driving the whole saga to a terminal
	// state from the persisted contribution row. No human review. See
	// reconcileFund.
	case saga.StepDebitSource,
		saga.StepCreditFund,
		saga.StepUpsertPosition,
		saga.StepDebitFund,
		saga.StepCreditTarget,
		saga.StepCreditBankFee:
		return r.reconcileFund(ctx, step)

	// --- OTC accept saga steps (assembled by buildAcceptSaga). Auto-resolved by
	// re-driving the whole saga to a terminal state (forward-resume to a minted
	// ACTIVE contract, or rollback if it was already aborting). No human review.
	case saga.StepReserveAndContract,
		saga.StepReservePremium,
		saga.StepSettlePremiumBuyer,
		saga.StepCreditPremiumSeller,
		saga.StepMarkOfferAccepted,
		saga.StepRecordSellerPremiumGain,
		saga.StepRecordBuyerPremiumCost,
		saga.StepPublishOTCAccepted:
		return r.reconcileAccept(ctx, step)

	// --- OTC exercise saga steps (assembled by buildExerciseSaga). Every
	// stuck exercise step is auto-resolved by re-driving the whole saga to a
	// terminal state (forward-resume to EXERCISED, or rollback to ACTIVE if it
	// was already aborting). No human review. See reconcileExercise.
	case saga.StepReserveStrike,
		saga.StepSettleStrikeBuyer,
		saga.StepCreditStrikeSeller,
		saga.StepConsumeSellerHolding,
		saga.StepUpsertBuyerHolding,
		saga.StepRecordSellerStrikeGain,
		saga.StepRecordBuyerExerciseCost,
		saga.StepMarkContractExercised,
		saga.StepPublishOTCExercise:
		return r.reconcileExercise(ctx, step)

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

// reconcileExercise auto-resolves a stuck OTC exercise saga step by delegating
// to the exercise recoverer, which re-drives the WHOLE saga (identified by
// step.SagaID, contract = step.OrderID) to a terminal state — forward-resume to
// EXERCISED, or rollback to ACTIVE when it was already aborting. The delegate is
// idempotent, so it is safe to invoke once per stuck row of the same saga (and
// across ticks). On success the row is marked completed/compensated so it drops
// out of the stuck query; the delegate already moved every other row of the
// saga to terminal too.
//
// Falls back to the historical log-and-leave only when no recoverer is wired
// (e.g. a test SagaRecovery), or when the row lacks the contract id needed to
// rebuild the saga.
func (r *SagaRecovery) reconcileExercise(ctx context.Context, step model.SagaLog) error {
	if r.exerciseRecoverer == nil {
		log.Printf("WARN: stuck OTC exercise step %d (order=%d, step=%s) — no recoverer wired, needs review",
			step.ID, step.OrderID, step.StepName)
		return nil
	}
	if step.SagaID == "" || step.OrderID == 0 {
		log.Printf("WARN: stuck OTC exercise step %d (step=%s) — missing saga_id/order_id, cannot rebuild",
			step.ID, step.StepName)
		return nil
	}
	if err := r.exerciseRecoverer.RecoverExerciseSaga(ctx, step.SagaID, step.OrderID); err != nil {
		return fmt.Errorf("recover exercise saga %s (contract=%d): %w", step.SagaID, step.OrderID, err)
	}
	// The delegate drove the saga terminal. Transition this row out of the
	// stuck set: compensation rows go to compensated, forward rows to completed.
	finalStatus := model.SagaStatusCompleted
	if step.IsCompensation {
		finalStatus = model.SagaStatusCompensated
	}
	return r.sagaRepo.UpdateStatus(step.ID, step.Version, finalStatus, "auto-resolved by recovery (saga re-driven to terminal)")
}

// reconcileAccept auto-resolves a stuck OTC accept saga step by delegating to
// the accept recoverer, which re-drives the whole saga (identified by
// step.SagaID, offer = step.OrderID) to a terminal state. Mirrors
// reconcileExercise. Falls back to log-and-leave when no recoverer is wired or
// the row lacks identifiers.
func (r *SagaRecovery) reconcileAccept(ctx context.Context, step model.SagaLog) error {
	if r.acceptRecoverer == nil {
		log.Printf("WARN: stuck OTC accept step %d (order=%d, step=%s) — no recoverer wired, needs review",
			step.ID, step.OrderID, step.StepName)
		return nil
	}
	if step.SagaID == "" {
		log.Printf("WARN: stuck OTC accept step %d (step=%s) — missing saga_id, cannot rebuild",
			step.ID, step.StepName)
		return nil
	}
	if err := r.acceptRecoverer.RecoverAcceptSaga(ctx, step.SagaID, step.OrderID); err != nil {
		return fmt.Errorf("recover accept saga %s (offer=%d): %w", step.SagaID, step.OrderID, err)
	}
	finalStatus := model.SagaStatusCompleted
	if step.IsCompensation {
		finalStatus = model.SagaStatusCompensated
	}
	return r.sagaRepo.UpdateStatus(step.ID, step.Version, finalStatus, "auto-resolved by recovery (saga re-driven to terminal)")
}

// reconcileFund auto-resolves a stuck fund invest/redeem saga step by
// delegating to the fund recoverer, which re-drives the whole saga (identified
// by step.SagaID, contribution = step.OrderID) to a terminal state. Mirrors
// reconcileExercise/reconcileAccept.
func (r *SagaRecovery) reconcileFund(ctx context.Context, step model.SagaLog) error {
	if r.fundRecoverer == nil {
		log.Printf("WARN: stuck fund saga step %d (order=%d, step=%s) — no recoverer wired, needs review",
			step.ID, step.OrderID, step.StepName)
		return nil
	}
	if step.SagaID == "" {
		log.Printf("WARN: stuck fund saga step %d (step=%s) — missing saga_id, cannot rebuild",
			step.ID, step.StepName)
		return nil
	}
	if err := r.fundRecoverer.RecoverFundSaga(ctx, step.SagaID, step.OrderID); err != nil {
		return fmt.Errorf("recover fund saga %s (contrib=%d): %w", step.SagaID, step.OrderID, err)
	}
	finalStatus := model.SagaStatusCompleted
	if step.IsCompensation {
		finalStatus = model.SagaStatusCompensated
	}
	return r.sagaRepo.UpdateStatus(step.ID, step.Version, finalStatus, "auto-resolved by recovery (saga re-driven to terminal)")
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
	if _, err := r.accountClient.PartialSettleReservation(ctx, step.OrderID, txnID, *step.Amount, memo, recoveryKey, orderkind.StockOrder); err != nil {
		// If the error is "would exceed reservation" the most likely cause
		// is a concurrent settle that already landed our txnID; recheck
		// before surfacing the error.
		if strings.Contains(err.Error(), "would exceed reservation") {
			if resp2, gerr := r.accountClient.Stub().GetReservation(ctx, &accountpb.GetReservationRequest{OrderId: step.OrderID, OrderKind: orderkind.StockOrder}); gerr == nil {
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
		// Run immediately at startup.
		if r.entry.BeginRun() {
			err := r.Reconcile(ctx)
			r.entry.EndRun(err)
			if err != nil {
				log.Printf("WARN: initial saga recovery: %v", err)
			}
		}
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.entry.BeginRun() {
					continue
				}
				err := r.Reconcile(ctx)
				r.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: periodic saga recovery: %v", err)
				}
			case <-r.entry.TriggerChan():
				if !r.entry.BeginRun() {
					continue
				}
				err := r.Reconcile(ctx)
				r.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: triggered saga recovery: %v", err)
				}
			}
		}
	}()
}

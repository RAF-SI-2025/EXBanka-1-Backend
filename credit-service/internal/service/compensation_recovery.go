package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/repository"
)

const (
	// maxLoanCompensationRetries is the number of recovery-loop failures after
	// which a compensating step is moved to the dead-letter queue.
	maxLoanCompensationRetries = 10

	// loanCompensationTickInterval controls how often the recovery worker polls
	// for stuck saga_log rows.
	loanCompensationTickInterval = 5 * time.Minute
)

// sagaDeadLetterPublisher is the subset of *kafka.Producer used by the recovery worker.
type sagaDeadLetterPublisher interface {
	PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error
}

// CompensationRecovery periodically retries all saga_log entries in
// "compensating" status for the loan disbursement saga. Mirrors
// transaction-service's StartCompensationRecovery pattern.
type CompensationRecovery struct {
	sagaRepo    *repository.SagaLogRepository
	disbursment *LoanDisbursementSaga
	dlPublisher sagaDeadLetterPublisher
	entry       *cronreg.Entry
}

// NewCompensationRecovery constructs a recovery worker.
//
//   - sagaRepo:    the credit_saga_logs repository.
//   - disbursment: the LoanDisbursementSaga whose steps are retried.
//   - dlPublisher: Kafka producer for dead-letter events (may be nil).
//   - registry:    cronreg Registry for pause/trigger control.
func NewCompensationRecovery(
	sagaRepo *repository.SagaLogRepository,
	disbursment *LoanDisbursementSaga,
	dlPublisher *kafka.Producer,
	registry *cronreg.Registry,
) *CompensationRecovery {
	r := &CompensationRecovery{
		sagaRepo:    sagaRepo,
		disbursment: disbursment,
		dlPublisher: dlPublisher,
	}
	r.entry = registry.Register("loan-saga-recovery", "Retry stuck loan disbursement saga compensations", loanCompensationTickInterval)
	return r
}

// Start launches the recovery goroutine. The goroutine runs one immediate tick
// at startup (to catch any compensations that survived a crash) then every
// loanCompensationTickInterval until ctx is cancelled. Each tick is gated by
// the cronreg Entry so the job can be paused / manually triggered.
func (r *CompensationRecovery) Start(ctx context.Context) {
	go func() {
		// Immediate catch-up pass.
		if r.entry.BeginRun() {
			r.runRecoveryTick(ctx)
			r.entry.EndRun(nil)
		}
		ticker := time.NewTicker(loanCompensationTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.entry.BeginRun() {
					continue
				}
				r.runRecoveryTick(ctx)
				r.entry.EndRun(nil)
			case <-r.entry.TriggerChan():
				if !r.entry.BeginRun() {
					continue
				}
				r.runRecoveryTick(ctx)
				r.entry.EndRun(nil)
			}
		}
	}()
}

// runRecoveryTick fetches all pending compensations and retries them.
func (r *CompensationRecovery) runRecoveryTick(ctx context.Context) {
	if r.sagaRepo == nil {
		return
	}

	comps, err := r.sagaRepo.FindPendingCompensations()
	if err != nil {
		log.Printf("loan saga recovery: failed to list pending compensations: %v", err)
		return
	}

	for _, comp := range comps {
		if comp.RetryCount >= maxLoanCompensationRetries {
			dlMsg := kafkamsg.SagaDeadLetterMessage{
				SagaLogID:  comp.ID,
				SagaID:     comp.SagaID,
				StepName:   comp.StepName,
				RetryCount: comp.RetryCount,
				LastError:  comp.ErrorMessage,
			}
			// Mark dead_letter in DB first; if this fails, skip publishing to
			// avoid duplicate Kafka events on the next tick.
			if dlErr := r.sagaRepo.MarkDeadLetter(comp.ID, comp.ErrorMessage); dlErr != nil {
				log.Printf("loan saga recovery: failed to mark comp %d as dead_letter: %v — will retry next tick", comp.ID, dlErr)
				continue
			}
			if r.dlPublisher != nil {
				if pubErr := r.dlPublisher.PublishSagaDeadLetter(ctx, dlMsg); pubErr != nil {
					log.Printf("loan saga recovery: failed to publish dead-letter for comp %d: %v", comp.ID, pubErr)
				}
			}
			log.Printf("loan saga recovery: compensation %d moved to dead-letter after %d retries (saga %s step %s)",
				comp.ID, comp.RetryCount, comp.SagaID, comp.StepName)
			continue
		}

		// Re-run compensation via Disburse (idempotency keys ensure no double-action).
		if r.disbursment != nil {
			loan, loanErr := r.disbursment.loanRepo.GetByID(comp.LoanID)
			if loanErr != nil {
				log.Printf("loan saga recovery: failed to load loan %d for comp %d: %v", comp.LoanID, comp.ID, loanErr)
				if incErr := r.sagaRepo.IncrementRetryCount(comp.ID); incErr != nil {
					log.Printf("loan saga recovery: failed to increment retry count for comp %d: %v", comp.ID, incErr)
				}
				continue
			}

			retryErr := r.disbursment.Disburse(ctx, loan)
			if retryErr != nil {
				if incErr := r.sagaRepo.IncrementRetryCount(comp.ID); incErr != nil {
					log.Printf("loan saga recovery: failed to increment retry count for comp %d: %v — skipping dead-letter check", comp.ID, incErr)
					continue
				}
				updatedCount := comp.RetryCount + 1
				log.Printf("loan saga recovery: compensation %d still failing (retry %d/%d) for loan %d saga %s: %v",
					comp.ID, updatedCount, maxLoanCompensationRetries, comp.LoanID, comp.SagaID, retryErr)
			} else {
				log.Printf("loan saga recovery: compensation %d succeeded for loan %d saga %s",
					comp.ID, comp.LoanID, comp.SagaID)
			}
		}
	}
}

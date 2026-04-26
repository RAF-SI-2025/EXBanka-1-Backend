package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
)

// InterBankRecovery is the one-shot startup routine that re-runs after a
// transaction-service restart. It looks at every non-terminal row and
// either nudges sender rows past the prepare/commit timeout into
// `reconciling` or completes a stuck `commit_received` receiver row by
// re-running CommitIncoming (idempotent).
type InterBankRecovery struct {
	svc            *InterBankService
	prepareTimeout time.Duration
	commitTimeout  time.Duration
}

// NewInterBankRecovery constructs the recovery routine.
func NewInterBankRecovery(svc *InterBankService, prepareTimeout, commitTimeout time.Duration) *InterBankRecovery {
	if prepareTimeout == 0 {
		prepareTimeout = 30 * time.Second
	}
	if commitTimeout == 0 {
		commitTimeout = 30 * time.Second
	}
	return &InterBankRecovery{svc: svc, prepareTimeout: prepareTimeout, commitTimeout: commitTimeout}
}

// RunOnce executes the recovery sweep. Spec 3 §9.6.
func (r *InterBankRecovery) RunOnce(ctx context.Context) error {
	rows, err := r.svc.Repo().ListNonTerminal()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range rows {
		row := &rows[i]
		switch {
		case row.Role == model.RoleSender && row.Status == model.StatusPreparing &&
			now.Sub(row.UpdatedAt) > r.prepareTimeout:
			if err := r.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusPreparing, model.StatusReconciling); err != nil {
				log.Printf("recovery: sender preparing→reconciling tx=%s: %v", row.TxID, err)
			}

		case row.Role == model.RoleSender && row.Status == model.StatusCommitting &&
			now.Sub(row.UpdatedAt) > r.commitTimeout:
			if err := r.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusCommitting, model.StatusReconciling); err != nil {
				log.Printf("recovery: sender committing→reconciling tx=%s: %v", row.TxID, err)
			}

		case row.Role == model.RoleReceiver && row.Status == model.StatusCommitReceived:
			// Re-run CommitIncoming (idempotent on reservationKey) to finish
			// the credit that was interrupted by the crash. Then move to
			// committed.
			reservationKey := "interbank-in-" + row.TxID
			if err := r.svc.Accounts().CommitIncoming(ctx, reservationKey); err != nil {
				log.Printf("recovery: commit_incoming tx=%s: %v", row.TxID, err)
				continue
			}
			if err := r.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusCommitReceived, model.StatusCommitted); err != nil {
				log.Printf("recovery: receiver commit_received→committed tx=%s: %v", row.TxID, err)
			}
		}
	}
	return nil
}

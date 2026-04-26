package service

import (
	"context"
	"errors"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// InterBankReconciler is the sender-side cron that probes the peer's
// /internal/inter-bank/check-status endpoint for any local row stuck in
// `reconciling` and advances it according to Spec 3 §9.4.
type InterBankReconciler struct {
	svc           *InterBankService
	interval      time.Duration
	maxRetries    int
	staleAfter    time.Duration
	prepareTimeout time.Duration
	batchSize     int
}

// NewInterBankReconciler constructs the cron.
func NewInterBankReconciler(svc *InterBankService, interval, prepareTimeout, staleAfter time.Duration, maxRetries int) *InterBankReconciler {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if maxRetries == 0 {
		maxRetries = 10
	}
	if staleAfter == 0 {
		staleAfter = 24 * time.Hour
	}
	return &InterBankReconciler{
		svc: svc, interval: interval,
		maxRetries: maxRetries, staleAfter: staleAfter,
		prepareTimeout: prepareTimeout, batchSize: 50,
	}
}

// Start launches the reconcile loop. Honors ctx cancellation.
func (r *InterBankReconciler) Start(ctx context.Context) {
	shared.RunScheduled(ctx, shared.ScheduledJob{
		Name:     "interbank-reconciler",
		Interval: r.interval,
		OnTick:   r.RunOnce,
		OnError: func(err error) {
			log.Printf("interbank reconciler: tick failed: %v", err)
		},
	})
}

// RunOnce processes a single batch. Public so tests can drive it directly.
func (r *InterBankReconciler) RunOnce(ctx context.Context) error {
	rows, err := r.svc.Repo().ListReconcilable(r.batchSize)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		r.reconcileOne(ctx, row)
	}
	return nil
}

func (r *InterBankReconciler) reconcileOne(ctx context.Context, row *model.InterBankTransaction) {
	// Give-up path: too many retries OR row too old.
	if row.RetryCount >= r.maxRetries || time.Since(row.UpdatedAt) > r.staleAfter {
		r.rollback(ctx, row, "max_retries_or_stale")
		return
	}

	client, err := r.svc.PeerRouter().ClientFor(row.RemoteBankCode)
	if err != nil {
		_ = r.svc.Repo().IncrementRetry(row.TxID, row.Role)
		return
	}
	resp, err := client.SendCheckStatus(ctx, row.TxID)
	if err != nil {
		if errors.Is(err, ErrPeerNotFound) {
			// Peer has no record. If we've waited long enough past the
			// prepare timeout, give up and roll back.
			grace := r.prepareTimeout * 2
			if grace == 0 {
				grace = 60 * time.Second
			}
			if time.Since(row.UpdatedAt) > grace {
				r.rollback(ctx, row, "peer_unknown")
				return
			}
		}
		_ = r.svc.Repo().IncrementRetry(row.TxID, row.Role)
		return
	}

	switch resp.Status {
	case model.StatusCommitted:
		// Peer committed — we should too. Nothing to credit; the local
		// debit happened at Initiate time. Just advance status.
		_ = r.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusReconciling, model.StatusCommitted)
		row.Status = model.StatusCommitted
		r.svc.PublishStatusKafka(ctx, kafkamsg.TopicTransferInterbankCommitted, row)

	case model.StatusFinalNotReady, model.StatusAbandoned, model.StatusRolledBack:
		r.rollback(ctx, row, "peer_"+resp.Status)

	case model.StatusReadySent, model.StatusCommitReceived, model.StatusPrepareReceived, model.StatusValidated:
		_ = r.svc.Repo().IncrementRetry(row.TxID, row.Role)

	default:
		_ = r.svc.Repo().IncrementRetry(row.TxID, row.Role)
	}
}

func (r *InterBankReconciler) rollback(ctx context.Context, row *model.InterBankTransaction, reason string) {
	if err := r.svc.CreditBackByTx(ctx, row.TxID); err != nil {
		// Best-effort; another tick will retry.
		log.Printf("interbank reconciler: credit-back tx=%s failed: %v", row.TxID, err)
		_ = r.svc.Repo().IncrementRetry(row.TxID, row.Role)
		return
	}
	_ = r.svc.Repo().UpdateStatus(row.TxID, row.Role, model.StatusReconciling, model.StatusRolledBack)
	_ = r.svc.Repo().SetErrorReason(row.TxID, row.Role, reason)
	row.Status = model.StatusRolledBack
	row.ErrorReason = reason
	r.svc.PublishStatusKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row)
}

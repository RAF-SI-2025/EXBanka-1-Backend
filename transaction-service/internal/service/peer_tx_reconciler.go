package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// PeerTxReconciler periodically checks the status of outbound SI-TX
// transactions that are stuck in "pending" by querying the peer bank via
// the Celina-5 CHECK_STATUS mechanism (GET /interbank/:txID/status).
//
// This is the receiver-initiated counterpart to OutboundReplayCron: while
// the replay cron resends NEW_TX/COMMIT_TX from the sender side,
// PeerTxReconciler asks the peer "what do you think the state is?" and
// applies the answer locally — useful when Bank A crashes after sending
// COMMIT_TX but before confirming receipt, leaving both sides unsure.
//
// Tick interval: 10 minutes (configurable).
// Eligible rows: pending, last_attempt_at older than 2 minutes (ensures
// OutboundReplayCron has had at least one chance to resolve it first).
type PeerTxReconciler struct {
	outRepo      *repository.OutboundPeerTxRepository
	httpClient   *sitx.PeerHTTPClient
	peerLookup   PeerLookupFunc
	reverseLocal LocalReversalFunc
	commitLocal  LocalCommitFunc
	tickInterval time.Duration
	minAge       time.Duration
	entry        *cronreg.Entry
}

// NewPeerTxReconciler creates a PeerTxReconciler with the given dependencies.
// tickInterval defaults to 10 minutes; minAge defaults to 2 minutes.
func NewPeerTxReconciler(
	outRepo *repository.OutboundPeerTxRepository,
	httpClient *sitx.PeerHTTPClient,
	peerLookup PeerLookupFunc,
	registry *cronreg.Registry,
) *PeerTxReconciler {
	r := &PeerTxReconciler{
		outRepo:      outRepo,
		httpClient:   httpClient,
		peerLookup:   peerLookup,
		tickInterval: 10 * time.Minute,
		minAge:       2 * time.Minute,
	}
	r.entry = registry.Register("peer-tx-reconciler", "Poll peers for status of stuck outbound SI-TX rows (every 10 min)", 10*time.Minute)
	return r
}

// WithTickInterval overrides the default 10-minute interval (for testing).
func (r *PeerTxReconciler) WithTickInterval(d time.Duration) *PeerTxReconciler {
	r.tickInterval = d
	return r
}

// WithMinAge overrides the minimum pending age before a row is eligible
// for reconciliation (default 2 minutes).
func (r *PeerTxReconciler) WithMinAge(d time.Duration) *PeerTxReconciler {
	r.minAge = d
	return r
}

// WithLocalReversal wires the local credit-back callback used on terminal
// non-committed paths. Optional — omitting it skips the credit-back step
// (tests that don't exercise money movement may omit it).
func (r *PeerTxReconciler) WithLocalReversal(fn LocalReversalFunc) *PeerTxReconciler {
	r.reverseLocal = fn
	return r
}

// WithLocalCommit wires the local commit/settle callback used when the peer
// reports a transaction as committed. Under reserve-then-settle this MUST be
// set so the reconciler settles the sender's outgoing hold (the money actually
// leaves) before marking the row committed — otherwise the hold lingers pending
// and the timeout cron would later release it, refunding a sender whose
// recipient was already credited. Optional only for tests that don't move money.
func (r *PeerTxReconciler) WithLocalCommit(fn LocalCommitFunc) *PeerTxReconciler {
	r.commitLocal = fn
	return r
}

// Start launches the reconciler loop and an immediate startup tick.
// Returns immediately; the loop runs until ctx cancels. Each tick is gated
// by the cronreg Entry so the job can be paused / manually triggered.
func (r *PeerTxReconciler) Start(ctx context.Context) {
	go func() {
		// Run once at startup so freshly-deployed instances catch up.
		if r.entry.BeginRun() {
			r.tick(ctx)
			r.entry.EndRun(nil)
		}

		t := time.NewTicker(r.tickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !r.entry.BeginRun() {
					continue
				}
				r.tick(ctx)
				r.entry.EndRun(nil)
			case <-r.entry.TriggerChan():
				if !r.entry.BeginRun() {
					continue
				}
				r.tick(ctx)
				r.entry.EndRun(nil)
			}
		}
	}()
}

// Tick is exported for testing.
func (r *PeerTxReconciler) Tick(ctx context.Context) { r.tick(ctx) }

func (r *PeerTxReconciler) tick(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.minAge)
	// Resume pending AND committing rows. For committing rows the rollback
	// branch is a no-op (MarkRolledBack is guarded to status='pending'), so a
	// committing row can only be driven forward (peer "committed" → settle
	// local + MarkCommitted) — never compensated.
	rows, err := r.outRepo.ListResumableOlderThan(cutoff)
	if err != nil {
		log.Printf("peer-tx-reconciler: list resumable err: %v", err)
		return
	}
	for i := range rows {
		r.processRow(ctx, rows[i].IdempotenceKey, rows[i].PeerBankCode)
	}
}

func (r *PeerTxReconciler) processRow(ctx context.Context, idem, peerCode string) {
	target, err := r.peerLookup(ctx, peerCode)
	if err != nil || target == nil {
		// Peer unreachable or not found — skip; OutboundReplayCron handles re-send.
		log.Printf("peer-tx-reconciler: peer lookup %s: %v (skipping)", peerCode, err)
		return
	}

	statusResp, err := r.httpClient.CheckStatus(ctx, target, idem)
	if err != nil {
		// Peer unreachable — log and skip; will retry on next tick.
		log.Printf("peer-tx-reconciler: CHECK_STATUS %s/%s: %v (skipping)", peerCode, idem, err)
		return
	}

	switch statusResp.State {
	case "committed":
		// Peer has committed → settle our local hold FIRST (reserve-then-settle:
		// the money actually leaves now), THEN mark the row committed. Settling
		// before marking means a settle failure leaves the row pending so a later
		// tick (or OutboundReplayCron) retries — never a committed row with an
		// un-settled hold (which the timeout cron would wrongly refund). commitLocal
		// is idempotent, so racing OutboundReplayCron's own settle is harmless.
		if r.commitLocal != nil {
			row, dbErr := r.outRepo.GetByIdempotenceKey(idem)
			if dbErr != nil {
				log.Printf("peer-tx-reconciler: fetch row %s for local commit: %v", idem, dbErr)
				return
			}
			if row != nil {
				if cerr := r.commitLocal(ctx, row); cerr != nil {
					log.Printf("peer-tx-reconciler: local commit/settle %s failed: %v (leaving pending for retry)", idem, cerr)
					return
				}
			}
		}
		// MarkCommitted is guarded by AND status='pending', so a concurrent
		// resolution by OutboundReplayCron will surface as ErrPeerTxAlreadyResolved
		// (not an error worth logging at error level).
		if err := r.outRepo.MarkCommitted(idem); err != nil {
			if errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
				log.Printf("peer-tx-reconciler: %s already resolved, skipping commit", idem)
			} else {
				log.Printf("peer-tx-reconciler: MarkCommitted %s: %v", idem, err)
			}
		} else {
			log.Printf("peer-tx-reconciler: reconciled %s → committed (peer confirmed)", idem)
		}

	case "rolled_back", "unknown", "dead_letter":
		// Safe ordering to prevent double-reverse race with OutboundReplayCron:
		//  1. Mark rolled_back FIRST (status guard: AND status='pending').
		//     If we lose the race, ErrPeerTxAlreadyResolved is returned and
		//     we skip localReverse entirely — preventing double credit-back.
		//  2. Only call localReverse AFTER successfully claiming the row.
		//     If localReverse fails, the row is already terminal; log + alert.
		row, dbErr := r.outRepo.GetByIdempotenceKey(idem)
		if dbErr != nil {
			log.Printf("peer-tx-reconciler: fetch row %s: %v", idem, dbErr)
			return
		}
		reason := "peer reports: " + statusResp.State
		if statusResp.LastError != "" {
			reason += " (" + statusResp.LastError + ")"
		}
		if err := r.outRepo.MarkRolledBack(idem, reason); err != nil {
			if errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
				log.Printf("peer-tx-reconciler: %s already resolved by concurrent worker, skipping reversal", idem)
				return
			}
			log.Printf("peer-tx-reconciler: MarkRolledBack %s: %v (will retry)", idem, err)
			_ = r.outRepo.MarkAttempt(idem, reason+" [MarkRolledBack failed: "+err.Error()+"]")
			return
		}
		log.Printf("peer-tx-reconciler: reconciled %s → rolled_back (%s)", idem, reason)
		// Row is now terminal; perform local credit-back. If this fails, the
		// row is already rolled_back so next tick won't retry — manual recovery needed.
		if r.reverseLocal != nil && row != nil {
			if rerr := r.reverseLocal(ctx, row); rerr != nil {
				log.Printf("peer-tx-reconciler: ALERT reversal %s failed after MarkRolledBack: %v — manual recovery required", idem, rerr)
			}
		}
		// Tell the peer to release any reservation it still holds for this tx
		// (idempotent no-op when it already rolled back / has no record).
		dispatchPeerRollback(ctx, r.httpClient, r.peerLookup, peerCode, idem, "peer-tx-reconciler")

	case "prepared":
		// Peer is still processing — OutboundReplayCron handles re-send.
		// No-op here so we don't race with the cron.
		log.Printf("peer-tx-reconciler: %s still prepared at peer, leaving for replay cron", idem)

	default:
		log.Printf("peer-tx-reconciler: unknown state %q for %s (skipping)", statusResp.State, idem)
	}
}

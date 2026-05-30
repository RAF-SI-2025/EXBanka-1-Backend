package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// OutboundReplayCron periodically resumes outbound SI-TX transfers that
// were left in `pending` state (sender-side crash, network error, peer
// 5xx). Backoff: rows whose last_attempt_at is older than minRetryGap
// (60s default) are eligible. Hard cap: maxAttempts (4 default) — after
// that the row is marked `failed`.
type OutboundReplayCron struct {
	repo         *repository.OutboundPeerTxRepository
	httpClient   *sitx.PeerHTTPClient
	peerLookup   PeerLookupFunc
	reverseLocal LocalReversalFunc
	commitLocal  LocalCommitFunc
	tickInterval time.Duration
	minRetryGap  time.Duration
	maxAttempts  int
	entry        *cronreg.Entry
}

// PeerLookupFunc resolves a peer-bank-code to a PeerHTTPTarget. Provided
// by cmd/main.go so the cron doesn't take a direct gRPC dependency.
type PeerLookupFunc func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error)

// LocalReversalFunc undoes the local balance effects that were applied at
// initiation time for an outbound row (the immediate sender debit, or the
// OTC multi-leg reservations + debits). It is called on the cron's terminal
// non-committed paths — peer NO vote and max-retries-exceeded — so the money
// the sender was debited up front is returned. Provided by cmd/main.go,
// which wires it to PeerTxGRPCHandler.ReverseOutboundLocal so the credit-back
// idempotency keys match the inline dispatch path. Must be idempotent: the
// cron may call it more than once for the same row across ticks.
type LocalReversalFunc func(ctx context.Context, row *model.OutboundPeerTx) error

// LocalCommitFunc finalises the local CREDIT-leg reservations applied at
// initiation time for an OTC outbound row. Called after the peer votes YES
// but before the cron sends COMMIT_TX, mirroring the inline dispatch path.
// Safe-and-required to call: without this hook, an OTC outbound row that is
// resumed by the cron commits on the peer but leaves the local reservation
// pending indefinitely. Must be idempotent — uses the same key as the inline
// path so duplicate calls are a no-op. Wired by cmd/main.go to
// PeerTxGRPCHandler.CommitOutboundLocal.
type LocalCommitFunc func(ctx context.Context, row *model.OutboundPeerTx) error

func NewOutboundReplayCron(
	repo *repository.OutboundPeerTxRepository,
	httpClient *sitx.PeerHTTPClient,
	peerLookup PeerLookupFunc,
	registry *cronreg.Registry,
) *OutboundReplayCron {
	c := &OutboundReplayCron{
		repo:         repo,
		httpClient:   httpClient,
		peerLookup:   peerLookup,
		tickInterval: 30 * time.Second,
		minRetryGap:  60 * time.Second,
		maxAttempts:  4,
	}
	c.entry = registry.Register("outbound-replay-cron", "Retry pending outbound SI-TX transfers (every 30s)", 30*time.Second)
	return c
}

func (c *OutboundReplayCron) WithTickInterval(d time.Duration) *OutboundReplayCron {
	c.tickInterval = d
	return c
}
func (c *OutboundReplayCron) WithMinRetryGap(d time.Duration) *OutboundReplayCron {
	c.minRetryGap = d
	return c
}
func (c *OutboundReplayCron) WithMaxAttempts(n int) *OutboundReplayCron {
	c.maxAttempts = n
	return c
}

// WithLocalReversal wires the local credit-back used on terminal
// non-committed paths. Optional — left nil, the cron preserves its prior
// behaviour of marking the row terminal without reversing local effects
// (used by tests that don't exercise money movement). Production always
// wires it; see LocalReversalFunc.
func (c *OutboundReplayCron) WithLocalReversal(fn LocalReversalFunc) *OutboundReplayCron {
	c.reverseLocal = fn
	return c
}

// WithLocalCommit wires the local commit hook for OTC rows. Optional — left
// nil, the cron skips the local commit step (acceptable for tests that don't
// exercise reservation lifecycle). Production always wires it.
func (c *OutboundReplayCron) WithLocalCommit(fn LocalCommitFunc) *OutboundReplayCron {
	c.commitLocal = fn
	return c
}

// Start launches the cron loop. Returns immediately; loop runs until ctx cancels.
func (c *OutboundReplayCron) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *OutboundReplayCron) loop(ctx context.Context) {
	t := time.NewTicker(c.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !c.entry.BeginRun() {
				continue
			}
			c.tick(ctx)
			c.entry.EndRun(nil)
		case <-c.entry.TriggerChan():
			if !c.entry.BeginRun() {
				continue
			}
			c.tick(ctx)
			c.entry.EndRun(nil)
		}
	}
}

// Tick is exported for testing.
func (c *OutboundReplayCron) Tick(ctx context.Context) { c.tick(ctx) }

func (c *OutboundReplayCron) tick(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-c.minRetryGap)
	rows, err := c.repo.ListPendingOlderThan(cutoff)
	if err != nil {
		log.Printf("outbound-replay: list err: %v", err)
		return
	}
	for i := range rows {
		c.processRow(ctx, &rows[i])
	}
}

func (c *OutboundReplayCron) processRow(ctx context.Context, row *model.OutboundPeerTx) {
	if row.AttemptCount >= c.maxAttempts {
		// Terminal failure after exhausting retries. The peer never
		// committed, but the sender was debited at initiation — reverse
		// that before parking the row in `failed`. If the reversal itself
		// fails we still mark failed (we're out of retries) but record the
		// reversal error so ops can recover the stranded money.
		reason := "max retries exceeded"
		if rerr := c.reverse(ctx, row); rerr != nil {
			reason += " (creditback failed: " + rerr.Error() + ")"
		}
		// Tell the peer to release any reservation it placed on a prior YES vote
		// (it never got our COMMIT). Idempotent + best-effort.
		dispatchPeerRollback(ctx, c.httpClient, c.peerLookup, row.PeerBankCode, row.IdempotenceKey, "outbound-replay")
		_ = c.repo.MarkFailed(row.IdempotenceKey, reason)
		return
	}
	target, err := c.peerLookup(ctx, row.PeerBankCode)
	if err != nil || target == nil {
		_ = c.repo.MarkAttempt(row.IdempotenceKey, "peer lookup failed: "+errString(err))
		return
	}
	var postings []contractsitx.Posting
	if err := json.Unmarshal([]byte(row.PostingsJSON), &postings); err != nil {
		_ = c.repo.MarkFailed(row.IdempotenceKey, "corrupt postings_json: "+err.Error())
		return
	}
	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{
			RoutingNumber:       target.OwnRouting,
			LocallyGeneratedKey: row.IdempotenceKey,
		},
		MessageType: contractsitx.MessageTypeNewTx,
		Message:     contractsitx.Transaction{Postings: postings},
	}
	vote, err := c.httpClient.PostNewTx(ctx, target, envelope)
	if err != nil {
		_ = c.repo.MarkAttempt(row.IdempotenceKey, err.Error())
		return
	}
	if vote.Type == contractsitx.VoteYes {
		// Finalise the local CREDIT-leg reservations BEFORE sending COMMIT_TX,
		// mirroring the inline dispatch path. Without this, an OTC row resumed
		// here would commit on the peer but leave the local reservation in
		// "pending" forever — funds/shares locked with no auto-recovery.
		// LocalCommitFunc is idempotent (uses the same key as the inline path)
		// so this is safe even if the inline path already ran it.
		if c.commitLocal != nil {
			if cerr := c.commitLocal(ctx, row); cerr != nil {
				_ = c.repo.MarkAttempt(row.IdempotenceKey, "local commit: "+cerr.Error())
				return
			}
		}
		commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
			IdempotenceKey: contractsitx.IdempotenceKey{
				RoutingNumber:       target.OwnRouting,
				LocallyGeneratedKey: row.IdempotenceKey,
			},
			MessageType: contractsitx.MessageTypeCommitTx,
			Message:     contractsitx.CommitTransaction{TransactionID: row.IdempotenceKey},
		}
		if err := c.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
			_ = c.repo.MarkAttempt(row.IdempotenceKey, "commit_tx: "+err.Error())
			return
		}
		_ = c.repo.MarkCommitted(row.IdempotenceKey)
		return
	}
	reason := "peer voted NO"
	if len(vote.NoVotes) > 0 {
		reason = "peer voted NO: " + vote.NoVotes[0].Reason
	}
	// Safe ordering for NO-vote resolution to prevent double-reverse race:
	//  1. Mark rolled_back FIRST (status guard: AND status='pending').
	//     If another goroutine (PeerTxReconciler) already resolved this row,
	//     MarkRolledBack returns ErrPeerTxAlreadyResolved — we skip reversal.
	//  2. Only call localReverse AFTER successfully claiming the row.
	//     localReverse uses its own idempotency keys so it is safe to retry,
	//     but we must not call it when we didn't win the status-guard race.
	if err := c.repo.MarkRolledBack(row.IdempotenceKey, reason); err != nil {
		if errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
			log.Printf("outbound-replay: %s already resolved by concurrent worker, skipping reversal", row.IdempotenceKey)
			return
		}
		_ = c.repo.MarkAttempt(row.IdempotenceKey, reason+" (MarkRolledBack failed: "+err.Error()+")")
		return
	}
	// Row is now in rolled_back; perform the local credit-back. If the
	// reversal fails, log and alert — the row is already terminal so the
	// next tick won't retry. Ops must recover the stranded balance manually
	// or via the dead-letter recovery path.
	if rerr := c.reverse(ctx, row); rerr != nil {
		log.Printf("outbound-replay: ALERT reversal failed for %s after MarkRolledBack: %v — manual recovery required", row.IdempotenceKey, rerr)
	}
	// The peer received our NEW_TX and voted NO; if it partially reserved
	// before the failing posting, ROLLBACK_TX releases it. Idempotent no-op
	// otherwise.
	dispatchPeerRollback(ctx, c.httpClient, c.peerLookup, row.PeerBankCode, row.IdempotenceKey, "outbound-replay")
}

// reverse credits back the local effects of an outbound row via the wired
// LocalReversalFunc. No-op (nil error) when no reversal func is wired.
func (c *OutboundReplayCron) reverse(ctx context.Context, row *model.OutboundPeerTx) error {
	if c.reverseLocal == nil {
		return nil
	}
	return c.reverseLocal(ctx, row)
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// dispatchPeerRollback sends a ROLLBACK_TX to the peer for a terminally
// abandoned outbound row, so the peer releases any reservation it placed when
// it voted YES on our NEW_TX (incoming-credit hold and/or seller-share hold).
// Without this, a sender that dispatched NEW_TX (peer voted YES, reserved) and
// then gave up — max-attempts `failed`, or a NO/rolled_back resolution — would
// leave the peer holding a dangling reservation forever (real share locks on
// the OTC path; benign-but-stale credit holds on the transfer path).
//
// Best-effort + idempotent: the peer's HandleRollbackTx releases by key and
// is a no-op when there's no record or it already rolled back, so this is safe
// to call on every terminal non-committed transition and safe to retry. A
// dispatch failure is logged (the peer's own timeout sweeper is the backstop)
// and never blocks the local terminal transition. Shared by OutboundReplayCron
// and PeerTxReconciler.
func dispatchPeerRollback(ctx context.Context, httpClient *sitx.PeerHTTPClient, peerLookup PeerLookupFunc, peerCode, idem, who string) {
	if httpClient == nil || peerLookup == nil {
		return
	}
	target, err := peerLookup(ctx, peerCode)
	if err != nil || target == nil {
		log.Printf("%s: ROLLBACK_TX peer lookup %s failed: %v (peer may retain reservation)", who, peerCode, err)
		return
	}
	env := contractsitx.Message[contractsitx.RollbackTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: target.OwnRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeRollbackTx,
		Message:        contractsitx.RollbackTransaction{TransactionID: idem},
	}
	if err := httpClient.PostRollbackTx(ctx, target, env); err != nil {
		log.Printf("%s: ROLLBACK_TX to %s for %s failed (peer may retain reservation until its sweeper): %v", who, peerCode, idem, err)
		return
	}
	log.Printf("%s: sent ROLLBACK_TX to %s for abandoned tx %s", who, peerCode, idem)
}

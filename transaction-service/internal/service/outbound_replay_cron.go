package service

import (
	"context"
	"encoding/json"
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
	// Atomicity (Celina 5: "ili u celosti, ili ne uopšte"): the sender was
	// debited at initiation, so a NO vote must credit them back. If the
	// reversal fails, keep the row pending (MarkAttempt) so a later tick
	// retries it rather than stranding the money in a terminal row — the
	// credit-back is idempotent, so replaying the NO+reverse loop is safe.
	if rerr := c.reverse(ctx, row); rerr != nil {
		_ = c.repo.MarkAttempt(row.IdempotenceKey, reason+" (creditback failed, will retry: "+rerr.Error()+")")
		return
	}
	_ = c.repo.MarkRolledBack(row.IdempotenceKey, reason)
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

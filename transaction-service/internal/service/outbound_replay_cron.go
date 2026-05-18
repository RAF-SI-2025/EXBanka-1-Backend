package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

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
	tickInterval time.Duration
	minRetryGap  time.Duration
	maxAttempts  int
}

// PeerLookupFunc resolves a peer-bank-code to a PeerHTTPTarget. Provided
// by cmd/main.go so the cron doesn't take a direct gRPC dependency.
type PeerLookupFunc func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error)

func NewOutboundReplayCron(
	repo *repository.OutboundPeerTxRepository,
	httpClient *sitx.PeerHTTPClient,
	peerLookup PeerLookupFunc,
) *OutboundReplayCron {
	return &OutboundReplayCron{
		repo:         repo,
		httpClient:   httpClient,
		peerLookup:   peerLookup,
		tickInterval: 30 * time.Second,
		minRetryGap:  60 * time.Second,
		maxAttempts:  4,
	}
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
			c.tick(ctx)
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
		_ = c.repo.MarkFailed(row.IdempotenceKey, "max retries exceeded")
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
	_ = c.repo.MarkRolledBack(row.IdempotenceKey, reason)
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

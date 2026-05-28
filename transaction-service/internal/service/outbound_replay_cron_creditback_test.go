package service_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
	"github.com/exbanka/transaction-service/internal/sitx"
)

const transferPostings = `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`

func noVoteServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"NO","noVotes":[{"reason":"INSUFFICIENT_ASSET"}]}`))
	}))
}

// TestOutboundReplayCron_PeerVotesNO_CreditsBackBeforeRollback verifies the
// fix for Bug 1: the sender was debited at initiation, so when the cron's
// retry gets a NO vote it must reverse that local debit before marking the
// row rolled_back — otherwise the money stays gone (manual intervention).
func TestOutboundReplayCron_PeerVotesNO_CreditsBackBeforeRollback(t *testing.T) {
	srv := noVoteServer(t)
	defer srv.Close()

	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-no-cb",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   transferPostings,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	var reversed []string
	cron := service.NewOutboundReplayCron(repo, sitx.NewPeerHTTPClient(http.DefaultClient),
		func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
			return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
		}, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(_ context.Context, r *model.OutboundPeerTx) error {
			reversed = append(reversed, r.IdempotenceKey)
			return nil
		})
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-no-cb")
	if got.Status != "rolled_back" {
		t.Errorf("expected rolled_back, got %s", got.Status)
	}
	if len(reversed) != 1 || reversed[0] != "cron-no-cb" {
		t.Errorf("expected local reversal (credit-back) exactly once, got %v", reversed)
	}
}

// TestOutboundReplayCron_MaxAttempts_CreditsBackBeforeFailed verifies that a
// row exhausting its retries (e.g. repeated network errors) also reverses the
// initiation-time debit before going to the terminal `failed` state — the
// peer never committed, so the money must come back.
func TestOutboundReplayCron_MaxAttempts_CreditsBackBeforeFailed(t *testing.T) {
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-fail-cb",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   transferPostings,
		Status:         "pending",
		AttemptCount:   4,
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	reversed := 0
	cron := service.NewOutboundReplayCron(repo, sitx.NewPeerHTTPClient(http.DefaultClient),
		func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
			return nil, errors.New("peer lookup must not be called past max attempts")
		}, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(_ context.Context, _ *model.OutboundPeerTx) error { reversed++; return nil })
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-fail-cb")
	if got.Status != "failed" {
		t.Errorf("expected failed, got %s", got.Status)
	}
	if reversed != 1 {
		t.Errorf("expected exactly one credit-back on the failed terminal, got %d", reversed)
	}
}

// TestOutboundReplayCron_PeerVotesNO_ReversalFails_MarkedRolledBack verifies the
// H2 safe-ordering fix: MarkRolledBack is called FIRST (with the AND status='pending'
// guard), then localReverse. When localReverse fails, the row is ALREADY in
// rolled_back (terminal), not pending — this is intentional. The alternative
// (keeping the row pending) risks a double-reverse race where both
// OutboundReplayCron and PeerTxReconciler call localReverse on the same row,
// double-crediting the sender. The trade-off: reversal failure after MarkRolledBack
// requires manual recovery (logged as ALERT), not automatic retry.
func TestOutboundReplayCron_PeerVotesNO_ReversalFails_MarkedRolledBack(t *testing.T) {
	srv := noVoteServer(t)
	defer srv.Close()

	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-no-cbfail",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   transferPostings,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	cron := service.NewOutboundReplayCron(repo, sitx.NewPeerHTTPClient(http.DefaultClient),
		func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
			return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
		}, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(_ context.Context, _ *model.OutboundPeerTx) error {
			return errors.New("account-service down")
		})
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-no-cbfail")
	// With the H2 fix, the row must be rolled_back (MarkRolledBack won the
	// status guard), even though localReverse subsequently failed. This prevents
	// PeerTxReconciler from also calling localReverse on the same row (double
	// reverse). The reversal failure is logged as ALERT for manual ops recovery.
	if got.Status != "rolled_back" {
		t.Errorf("expected rolled_back (H2 safe ordering: mark-first-then-reverse), got %s", got.Status)
	}
}

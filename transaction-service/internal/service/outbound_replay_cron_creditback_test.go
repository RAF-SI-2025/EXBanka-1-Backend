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
		}).
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
		}).
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

// TestOutboundReplayCron_PeerVotesNO_ReversalFails_StaysPending verifies that
// if the credit-back itself fails (e.g. account-service transiently down), the
// row is NOT marked rolled_back — it stays pending so a later tick retries the
// reversal, rather than stranding the debited money in a terminal row nothing
// revisits. The credit-back is idempotent, so re-running the whole NO+reverse
// loop is safe.
func TestOutboundReplayCron_PeerVotesNO_ReversalFails_StaysPending(t *testing.T) {
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
		}).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(_ context.Context, _ *model.OutboundPeerTx) error {
			return errors.New("account-service down")
		})
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-no-cbfail")
	if got.Status != "pending" {
		t.Errorf("expected row to stay pending after reversal failure, got %s", got.Status)
	}
	if got.AttemptCount != 1 {
		t.Errorf("expected attempt_count incremented to 1, got %d", got.AttemptCount)
	}
	if got.LastError == "" {
		t.Errorf("expected last_error to record the reversal failure")
	}
}

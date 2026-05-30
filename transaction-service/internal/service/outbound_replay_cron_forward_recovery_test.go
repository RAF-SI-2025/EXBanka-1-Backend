package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// yesPeer is a peer that votes YES on NEW_TX and 204s COMMIT/ROLLBACK.
func yesPeer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	rollbacks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		switch probe["messageType"] {
		case contractsitx.MessageTypeNewTx:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"YES"}`))
		case contractsitx.MessageTypeRollbackTx:
			rollbacks++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	return srv, &rollbacks
}

// TestForwardRecovery_CommittingRow_NeverCompensated is THE saga-bulletproofing
// regression: once a row is in the `committing` phase (the YES-vote pivot was
// crossed and money may have settled), a failing local commit must be RETRIED
// forever — never compensated (reverse) and never marked failed/rolled_back.
// Compensating settled money strands it; rolling back desyncs from a peer that
// may have committed.
func TestForwardRecovery_CommittingRow_NeverCompensated(t *testing.T) {
	srv, rollbacks := yesPeer(t)
	defer srv.Close()
	_, repo := newCronTestDB(t)
	// Seed a row ALREADY in the committing phase, at/over the attempt cap.
	row := &model.OutboundPeerTx{
		IdempotenceKey: "commit-stuck-1",
		PeerBankCode:   "222",
		TxKind:         "otc-exercise",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`,
		Status:         "committing",
		AttemptCount:   9, // well past maxAttempts
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	reverseCalled := 0
	commitCalled := 0
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(context.Context, *model.OutboundPeerTx) error { reverseCalled++; return nil }).
		WithLocalCommit(func(context.Context, *model.OutboundPeerTx) error {
			commitCalled++
			return errors.New("settle/materialise still failing (e.g. account-service down)")
		})
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("commit-stuck-1")
	if got.Status == "failed" || got.Status == "rolled_back" {
		t.Errorf("committing row was COMPENSATED to %q — settled money would be stranded; must stay committing", got.Status)
	}
	if got.Status != "committing" {
		t.Errorf("expected status to remain committing, got %q", got.Status)
	}
	if reverseCalled != 0 {
		t.Errorf("reverse/compensation must NEVER run for a committing row, ran %d times", reverseCalled)
	}
	if *rollbacks != 0 {
		t.Errorf("ROLLBACK_TX must NEVER be sent for a committing row, sent %d", *rollbacks)
	}
	if commitCalled == 0 {
		t.Error("forward recovery should have attempted the local commit")
	}
}

// TestForwardRecovery_CommittingRow_DrivesToCommitted verifies the happy
// forward path: a committing row whose local commit now succeeds is driven to
// committed (commitLocal + COMMIT_TX + MarkCommitted), no compensation.
func TestForwardRecovery_CommittingRow_DrivesToCommitted(t *testing.T) {
	srv, _ := yesPeer(t)
	defer srv.Close()
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "commit-ok-1", PeerBankCode: "222", TxKind: "otc-exercise",
		PostingsJSON: `[]`, Status: "committing", AttemptCount: 1,
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(context.Context, *model.OutboundPeerTx) error {
			t.Fatal("reverse must not run for committing")
			return nil
		}).
		WithLocalCommit(func(context.Context, *model.OutboundPeerTx) error { return nil })
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("commit-ok-1")
	if got.Status != "committed" {
		t.Errorf("expected committed via forward recovery, got %q", got.Status)
	}
}

// TestForwardRecovery_PendingYes_PivotsToCommitting verifies the pivot: a
// pending row that gets a YES is moved to committing BEFORE the commit work,
// then driven to committed — and reverse is never called.
func TestForwardRecovery_PendingYes_PivotsToCommitting(t *testing.T) {
	srv, _ := yesPeer(t)
	defer srv.Close()
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "pivot-1", PeerBankCode: "222", TxKind: "payment",
		PostingsJSON: `[]`, Status: "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup, nilRegistry()).
		WithMinRetryGap(0).WithMaxAttempts(4).
		WithLocalReversal(func(context.Context, *model.OutboundPeerTx) error {
			t.Fatal("reverse must not run after YES")
			return nil
		}).
		WithLocalCommit(func(context.Context, *model.OutboundPeerTx) error { return nil })
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("pivot-1")
	if got.Status != "committed" {
		t.Errorf("expected committed after YES pivot, got %q", got.Status)
	}
}

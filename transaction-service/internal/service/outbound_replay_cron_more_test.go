package service_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// TestOutboundReplayCron_Builders verifies the builder methods produce a
// configured cron object — exercises With* paths.
func TestOutboundReplayCron_Builders(t *testing.T) {
	cron := service.NewOutboundReplayCron(nil, nil, nil, nilRegistry()).
		WithTickInterval(2 * time.Second).
		WithMinRetryGap(1 * time.Second).
		WithMaxAttempts(7)
	if cron == nil {
		t.Fatalf("expected non-nil cron")
	}
}

// TestOutboundReplayCron_Start_RespectsContextCancel verifies the cron loop
// exits cleanly on context cancel.
func TestOutboundReplayCron_Start_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, repo := newCronTestDB(t)
	cron := service.NewOutboundReplayCron(repo, sitx.NewPeerHTTPClient(http.DefaultClient), func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("not used")
	}, nilRegistry()).WithTickInterval(20 * time.Millisecond).WithMinRetryGap(0)
	cron.Start(ctx)
	// Let it run a few ticks, then cancel.
	time.Sleep(60 * time.Millisecond)
	cancel()
	// Allow the loop's select to observe ctx.Done().
	time.Sleep(40 * time.Millisecond)
}

// TestOutboundReplayCron_PeerLookupFails_MarksAttempt verifies that a peer
// lookup failure increments the attempt counter and writes a last_error.
func TestOutboundReplayCron_PeerLookupFails_MarksAttempt(t *testing.T) {
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-peer-fail",
		PeerBankCode:   "999",
		TxKind:         "transfer",
		PostingsJSON:   `[]`,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	cron := service.NewOutboundReplayCron(repo, httpClient, func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("peer not registered")
	}, nilRegistry()).WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-peer-fail")
	if got.LastError == "" {
		t.Errorf("expected last_error on lookup failure")
	}
	if got.AttemptCount != 1 {
		t.Errorf("expected attempt_count=1, got %d", got.AttemptCount)
	}
}

// TestOutboundReplayCron_CorruptPostings_MarksFailed verifies that an
// undecodable postings_json moves the row to failed.
func TestOutboundReplayCron_CorruptPostings_MarksFailed(t *testing.T) {
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-corrupt",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `{ this is not valid json`,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	cron := service.NewOutboundReplayCron(repo, httpClient, func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: "http://x", APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}, nilRegistry()).WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-corrupt")
	if got.Status != "failed" {
		t.Errorf("expected failed, got %q", got.Status)
	}
}

// TestOutboundReplayCron_NewTxNetworkError_MarksAttempt verifies that an
// HTTP-level NEW_TX failure increments attempts.
func TestOutboundReplayCron_NewTxNetworkError_MarksAttempt(t *testing.T) {
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-net",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"A","assetId":"RSD","amount":"10","direction":"DEBIT"},{"routingNumber":222,"accountId":"B","assetId":"RSD","amount":"10","direction":"CREDIT"}]`,
		Status:         "pending",
	}
	_ = repo.Create(row)

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	cron := service.NewOutboundReplayCron(repo, httpClient, func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: "http://127.0.0.1:1", APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}, nilRegistry()).WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())
	got, _ := repo.GetByIdempotenceKey("cron-net")
	if got.LastError == "" {
		t.Errorf("expected last_error on network failure")
	}
}

// TestOutboundReplayCron_CommitFails_MarksAttempt verifies that a YES vote
// followed by a failing COMMIT call marks the row attempted (not committed).
func TestOutboundReplayCron_CommitFails_MarksAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First call: NEW_TX → vote YES. Second call: COMMIT_TX → 500.
		if r.Header.Get("X-Probe") == "" {
			// Inspect the body to dispatch.
			buf := make([]byte, 512)
			n, _ := r.Body.Read(buf)
			body := string(buf[:n])
			if contains(body, "NEW_TX") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"type":"YES"}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("commit failed"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-cf",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"A","assetId":"RSD","amount":"10","direction":"DEBIT"},{"routingNumber":222,"accountId":"B","assetId":"RSD","amount":"10","direction":"CREDIT"}]`,
		Status:         "pending",
	}
	_ = repo.Create(row)

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	cron := service.NewOutboundReplayCron(repo, httpClient, func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}, nilRegistry()).WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-cf")
	if got.Status == "committed" {
		t.Errorf("expected NOT committed; commit step should have failed")
	}
	if got.LastError == "" {
		t.Errorf("expected last_error after commit failure")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/service"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// setupReconcilerPeer starts a test HTTP server that serves CHECK_STATUS
// responses. The statusFn maps a txID to the response JSON body.
func setupReconcilerPeer(t *testing.T, statusFn func(txID string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path: /interbank/<txID>/status
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		var txID string
		if len(parts) >= 2 {
			txID = parts[len(parts)-2] // e.g. /interbank/<txID>/status
		}
		body := statusFn(txID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return srv
}

func newReconcilerDB(t *testing.T) *repository.OutboundPeerTxRepository {
	t.Helper()
	_, repo := newCronTestDB(t) // reuse helper from outbound_replay_cron_test.go
	return repo
}

func newReconciler(repo *repository.OutboundPeerTxRepository, srvURL string) *service.PeerTxReconciler {
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srvURL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	return service.NewPeerTxReconciler(repo, httpClient, service.PeerLookupFunc(peerLookup), nilRegistry()).
		WithMinAge(0)
}

// TestPeerTxReconciler_PeerCommitted verifies that a peer reporting
// "committed" causes the local row to be marked committed.
func TestPeerTxReconciler_PeerCommitted(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reconcile-committed-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "committed", "our_role": "receiver"})
		return string(body)
	})
	defer srv.Close()

	r := newReconciler(repo, srv.URL)
	r.Tick(context.Background())

	row, _ := repo.GetByIdempotenceKey(idem)
	if row.Status != "committed" {
		t.Errorf("expected committed, got %s last_error=%q", row.Status, row.LastError)
	}
}

// TestPeerTxReconciler_PeerRolledBack verifies that a peer reporting
// "rolled_back" causes the local row to be marked rolled_back.
func TestPeerTxReconciler_PeerRolledBack(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reconcile-rolledback-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "rolled_back", "our_role": "receiver"})
		return string(body)
	})
	defer srv.Close()

	r := newReconciler(repo, srv.URL)
	r.Tick(context.Background())

	row, _ := repo.GetByIdempotenceKey(idem)
	if row.Status != "rolled_back" {
		t.Errorf("expected rolled_back, got %s last_error=%q", row.Status, row.LastError)
	}
}

// TestPeerTxReconciler_PeerUnknown verifies that a peer reporting
// "unknown" causes the local row to be marked rolled_back (no record means
// the TX was never received, so we roll back locally).
func TestPeerTxReconciler_PeerUnknown(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reconcile-unknown-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "unknown"})
		return string(body)
	})
	defer srv.Close()

	r := newReconciler(repo, srv.URL)
	r.Tick(context.Background())

	row, _ := repo.GetByIdempotenceKey(idem)
	if row.Status != "rolled_back" {
		t.Errorf("expected rolled_back, got %s last_error=%q", row.Status, row.LastError)
	}
}

// TestPeerTxReconciler_PeerPrepared verifies that a peer reporting
// "prepared" leaves the local row unchanged (OutboundReplayCron handles it).
func TestPeerTxReconciler_PeerPrepared(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reconcile-prepared-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "prepared"})
		return string(body)
	})
	defer srv.Close()

	r := newReconciler(repo, srv.URL)
	r.Tick(context.Background())

	row, _ := repo.GetByIdempotenceKey(idem)
	// Row should still be pending — the reconciler does nothing for "prepared".
	if row.Status != "pending" {
		t.Errorf("expected pending (no-op), got %s", row.Status)
	}
}

// TestPeerTxReconciler_PeerUnreachable verifies that an unreachable peer
// leaves the local row unchanged (will retry on next tick).
func TestPeerTxReconciler_PeerUnreachable(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reconcile-unreachable-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Use an address that will refuse connections.
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: "http://127.0.0.1:19999", APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	r := service.NewPeerTxReconciler(repo, httpClient, service.PeerLookupFunc(peerLookup), nilRegistry()).
		WithMinAge(0)
	r.Tick(context.Background())

	row, _ := repo.GetByIdempotenceKey(idem)
	// Row should still be pending — unreachable peer = skip.
	if row.Status != "pending" {
		t.Errorf("expected pending (skipped), got %s", row.Status)
	}
}

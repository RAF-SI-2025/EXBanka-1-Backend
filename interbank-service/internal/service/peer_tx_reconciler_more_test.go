package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/service"
	"github.com/exbanka/interbank-service/internal/sitx"
)

// TestPeerTxReconciler_Start_StartupTickResolves verifies Start launches the
// loop and runs the immediate startup tick, which reconciles a pending row whose
// peer reports "committed" — then honours context cancellation. Synchronisation
// is via the local-commit hook (signalled on a channel) so the test never reads
// the SQLite DB concurrently with the reconciler goroutine.
func TestPeerTxReconciler_Start_StartupTickResolves(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "start-committed-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem, PeerBankCode: "222", TxKind: "transfer",
		PostingsJSON: "[]", Status: "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "committed", "our_role": "receiver"})
		return string(body)
	})
	defer srv.Close()

	committed := make(chan string, 1)
	r := newReconciler(repo, srv.URL).
		// Short interval so the ticker branch of the loop also fires (a no-op once
		// the row is committed); the startup tick does the actual reconciliation.
		WithTickInterval(20 * time.Millisecond).
		WithLocalCommit(func(_ context.Context, row *model.OutboundPeerTx) error {
			select {
			case committed <- row.IdempotenceKey:
			default:
			}
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	select {
	case got := <-committed:
		if got != idem {
			t.Errorf("startup tick committed %q, want %q", got, idem)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup tick did not reconcile the row within deadline")
	}

	// Cancel and let the loop observe ctx.Done() and exit before the test's DB
	// is torn down — avoids any concurrent SQLite access from the goroutine.
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestPeerTxReconciler_WithLocalReversal_FiresOnRolledBack verifies the
// WithLocalReversal callback wired into the reconciler runs the credit-back
// after a peer reports "rolled_back" and the row is claimed.
func TestPeerTxReconciler_WithLocalReversal_FiresOnRolledBack(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reverse-cb-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem, PeerBankCode: "222", TxKind: "transfer",
		PostingsJSON: "[]", Status: "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "rolled_back", "last_error": "peer aborted"})
		return string(body)
	})
	defer srv.Close()

	reversed := false
	var reversedKey string
	r := newReconciler(repo, srv.URL).WithLocalReversal(func(_ context.Context, row *model.OutboundPeerTx) error {
		reversed = true
		reversedKey = row.IdempotenceKey
		return nil
	})
	r.Tick(context.Background())

	if !reversed {
		t.Fatalf("expected local reversal callback to fire on rolled_back")
	}
	if reversedKey != idem {
		t.Errorf("reversal got row %q, want %q", reversedKey, idem)
	}
	if row, _ := repo.GetByIdempotenceKey(idem); row.Status != "rolled_back" {
		t.Errorf("expected rolled_back, got %s", row.Status)
	}
}

// TestPeerTxReconciler_UnknownState_Skips verifies an unrecognized peer state
// hits the default branch and leaves the row pending (no resolution).
func TestPeerTxReconciler_UnknownState_Skips(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "unknown-state-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem, PeerBankCode: "222", TxKind: "transfer",
		PostingsJSON: "[]", Status: "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv := setupReconcilerPeer(t, func(_ string) string {
		body, _ := json.Marshal(map[string]string{"state": "wat-is-this"})
		return string(body)
	})
	defer srv.Close()

	newReconciler(repo, srv.URL).Tick(context.Background())

	if row, _ := repo.GetByIdempotenceKey(idem); row.Status != "pending" {
		t.Errorf("expected pending on unknown state, got %s", row.Status)
	}
}

// TestPeerTxReconciler_RolledBack_ReverseFails_StaysTerminal covers the
// rolled_back branch where the local credit-back hook returns an error AND the
// peer's ROLLBACK_TX dispatch fails: the row is already terminal (rolled_back)
// so neither failure resurrects it — both are logged best-effort.
func TestPeerTxReconciler_RolledBack_ReverseFails_StaysTerminal(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "reverse-fail-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem, PeerBankCode: "222", TxKind: "transfer",
		PostingsJSON: "[]", Status: "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// GET status → rolled_back; POST /interbank (ROLLBACK_TX) → 500 (dispatch
	// failure path). Discriminate by HTTP method.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := json.Marshal(map[string]string{"state": "rolled_back"})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := newReconciler(repo, srv.URL).WithLocalReversal(func(_ context.Context, _ *model.OutboundPeerTx) error {
		return errors.New("credit-back failed")
	})
	r.Tick(context.Background())

	// Despite both the reversal and the peer rollback failing, the row is
	// terminal rolled_back (claimed once) — never re-driven.
	if row, _ := repo.GetByIdempotenceKey(idem); row.Status != "rolled_back" {
		t.Errorf("expected terminal rolled_back, got %s", row.Status)
	}
}

// TestPeerTxReconciler_PeerLookupError_Skips verifies a peer-lookup failure
// leaves the row untouched (OutboundReplayCron handles re-send).
func TestPeerTxReconciler_PeerLookupError_Skips(t *testing.T) {
	repo := newReconcilerDB(t)
	idem := "lookup-err-001"
	if err := repo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem, PeerBankCode: "222", TxKind: "transfer",
		PostingsJSON: "[]", Status: "pending",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(_ context.Context, _ string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("peer not registered")
	}
	r := service.NewPeerTxReconciler(repo, httpClient, service.PeerLookupFunc(peerLookup), nilRegistry()).
		WithMinAge(0)
	r.Tick(context.Background())

	if row, _ := repo.GetByIdempotenceKey(idem); row.Status != "pending" {
		t.Errorf("expected pending (lookup error skipped), got %s", row.Status)
	}
}

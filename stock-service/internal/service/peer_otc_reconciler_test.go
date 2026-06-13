package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	contractkafka "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// Fake repos
// ---------------------------------------------------------------------------

type fakeNegRepo struct {
	mu      sync.Mutex
	rows    []model.OTCNegotiation
	updates []updateCall
}

type updateCall struct {
	routing   int64
	foreignID string
	status    string
}

func (f *fakeNegRepo) ListRemoteNegOngoing() ([]model.OTCNegotiation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.OTCNegotiation, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeNegRepo) UpdateRemoteNegStatus(routing int64, native, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, updateCall{routing, native, status})
	return nil
}

// remoteNegRow builds a REMOTE model.OTCNegotiation (SP-2a) the reconciler can
// read: parties live in the Remote* columns, the foreign id in NativeID, and
// RoutingNumber is the counterparty/peer routing (the side != ownRouting). The
// ownRouting passed is the bank's own routing so the helper can pick which side
// is the peer.
func remoteNegRow(id uint64, ownRouting, buyerRouting int64, buyerID string, sellerRouting int64, sellerID, foreignID, status string) model.OTCNegotiation {
	bR := buyerRouting
	sR := sellerRouting
	bID := buyerID
	sID := sellerID
	native := foreignID
	peerRouting := buyerRouting
	if buyerRouting == ownRouting {
		peerRouting = sellerRouting
	}
	return model.OTCNegotiation{
		ID:                  id,
		RoutingNumber:       peerRouting,
		NativeID:            &native,
		Status:              status,
		RemoteBuyerRouting:  &bR,
		RemoteBuyerID:       &bID,
		RemoteSellerRouting: &sR,
		RemoteSellerID:      &sID,
	}
}

func (f *fakeNegRepo) getUpdates() []updateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]updateCall, len(f.updates))
	copy(out, f.updates)
	return out
}

// fakeContractChecker implements peerContractChecker for tests.
type fakeContractChecker struct {
	mu          sync.Mutex
	calls       []contractCheckCall
	hasContract bool
	err         error
}

type contractCheckCall struct {
	routing int64
	negID   string
}

func (f *fakeContractChecker) HasRemoteContractForNegotiation(routing int64, negID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, contractCheckCall{routing, negID})
	return f.hasContract, f.err
}

func (f *fakeContractChecker) getCalls() []contractCheckCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contractCheckCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// ---------------------------------------------------------------------------
// Fake notifier
// ---------------------------------------------------------------------------

type fakeReconcilerNotifier struct {
	mu   sync.Mutex
	msgs []contractkafka.GeneralNotificationMessage
}

func (n *fakeReconcilerNotifier) PublishGeneralNotification(_ context.Context, m contractkafka.GeneralNotificationMessage) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, m)
	return nil
}

func (n *fakeReconcilerNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.msgs)
}

// ---------------------------------------------------------------------------
// Helper: build a reconciler with injected fakes (no gRPC, no HTTP)
// ---------------------------------------------------------------------------

func newTestReconciler(
	repo peerOtcNegRepo,
	fetcher PeerNegStatusFetcher,
	ownRouting int64,
) *PeerOTCNegotiationReconciler {
	r := &PeerOTCNegotiationReconciler{
		repo:       repo,
		peerAdmin:  nil, // won't be called — peerMap is bypassed in reconcileRow tests
		fetcher:    fetcher,
		ownRouting: ownRouting,
		interval:   time.Hour, // irrelevant for unit tests
	}
	return r
}

func newTestReconcilerWithChecker(
	repo peerOtcNegRepo,
	checker peerContractChecker,
	fetcher PeerNegStatusFetcher,
	ownRouting int64,
) *PeerOTCNegotiationReconciler {
	r := &PeerOTCNegotiationReconciler{
		repo:            repo,
		contractChecker: checker,
		peerAdmin:       nil,
		fetcher:         fetcher,
		ownRouting:      ownRouting,
		interval:        time.Hour,
	}
	return r
}

// ---------------------------------------------------------------------------
// Test: Run() returns promptly when ctx is cancelled
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_RunCancelsCleanly(t *testing.T) {
	repo := &fakeNegRepo{}
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		return true, nil // never called in this test
	}

	r := &PeerOTCNegotiationReconciler{
		repo:       repo,
		peerAdmin:  nil,
		fetcher:    fetcher,
		ownRouting: 111,
		interval:   10 * time.Millisecond, // very short to prove the loop respects cancel
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Cancel immediately and ensure Run exits within 1 second.
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Fatal("Run() did not return within 1s after ctx cancel")
	}
}

// ---------------------------------------------------------------------------
// Test: fetcher reports "cancelled" → row is flipped
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_ReconcileRow_FetcherReportsCancelled_FlipsStatus(t *testing.T) {
	const (
		ownRouting  int64 = 111
		peerRouting int64 = 222
		peerCode          = "222"
		foreignID         = "neg-abc-123"
		buyerID           = "client-99" // on peer bank (routing 222)
		sellerID          = "client-42" // on our bank (routing 111)
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(1, ownRouting, peerRouting, buyerID, ownRouting, sellerID, foreignID, "ongoing"),
		},
	}

	// Fetcher always says not-ongoing (terminal state on peer).
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		return false, nil
	}

	r := newTestReconciler(repo, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "secret"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updates := repo.getUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 UpdateStatus call, got %d", len(updates))
	}
	u := updates[0]
	if u.routing != peerRouting || u.foreignID != foreignID || u.status != "cancelled" {
		t.Errorf("unexpected update: %+v", u)
	}
}

// ---------------------------------------------------------------------------
// Test: fetcher returns error → row stays ongoing (false-cancel guard)
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_ReconcileRow_FetcherError_NoStatusChange(t *testing.T) {
	const (
		ownRouting  int64 = 111
		peerRouting int64 = 222
		peerCode          = "222"
		foreignID         = "neg-xyz-456"
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(2, ownRouting, peerRouting, "client-7", ownRouting, "client-5", foreignID, "ongoing"),
		},
	}

	// Fetcher returns a transport error.
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		return false, errors.New("connection refused")
	}

	r := newTestReconciler(repo, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "secret"},
	}

	// reconcileRow should return the error (caller logs and skips).
	err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap)
	if err == nil {
		t.Fatal("expected error from reconcileRow when fetcher fails")
	}

	// No UpdateStatus should have been called.
	if got := repo.getUpdates(); len(got) != 0 {
		t.Errorf("expected 0 UpdateStatus calls on fetcher error, got %d: %+v", len(got), got)
	}
}

// ---------------------------------------------------------------------------
// Test: fetcher reports "ongoing" → no change
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_ReconcileRow_FetcherReportsOngoing_NoChange(t *testing.T) {
	const (
		ownRouting  int64 = 111
		peerRouting int64 = 222
		peerCode          = "222"
		foreignID         = "neg-still-going"
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(3, ownRouting, peerRouting, "client-10", ownRouting, "client-20", foreignID, "ongoing"),
		},
	}

	// Fetcher says still ongoing.
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		return true, nil
	}

	r := newTestReconciler(repo, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "secret"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := repo.getUpdates(); len(got) != 0 {
		t.Errorf("expected 0 UpdateStatus calls when peer reports ongoing, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Test: intra-bank row (both routings == ownRouting) → skipped
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_ReconcileRow_IntraBank_Skipped(t *testing.T) {
	const ownRouting int64 = 111

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(4, ownRouting, ownRouting, "client-1", ownRouting, "client-2", "neg-intra", "ongoing"),
		},
	}

	fetcherCalled := false
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		fetcherCalled = true
		return false, nil
	}

	r := newTestReconciler(repo, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		"111": {baseURL: "http://self:8080/api/v3", apiKey: "key"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fetcherCalled {
		t.Error("fetcher should NOT be called for intra-bank rows (peerRoutingForRow returns 0)")
	}
	if got := repo.getUpdates(); len(got) != 0 {
		t.Errorf("expected 0 updates for intra-bank row, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Test: notification is sent when local party can be resolved
// ---------------------------------------------------------------------------

func TestPeerOTCReconciler_ReconcileRow_NotifiesLocalParty(t *testing.T) {
	const (
		ownRouting  int64 = 111
		peerRouting int64 = 222
		peerCode          = "222"
		foreignID         = "neg-notif-test"
		sellerID          = "client-42" // local seller on ownRouting
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(5, ownRouting, peerRouting, "client-7", ownRouting, sellerID, foreignID, "ongoing"),
		},
	}

	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) {
		return false, nil // peer reports terminal
	}

	notifier := &fakeReconcilerNotifier{}
	r := newTestReconciler(repo, fetcher, ownRouting)
	r.notifier = notifier

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "secret"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notifier.count() != 1 {
		t.Errorf("expected 1 notification, got %d", notifier.count())
	}
}

// ---------------------------------------------------------------------------
// Test: peerRoutingForRow picks the non-own routing
// ---------------------------------------------------------------------------

func TestPeerRoutingForRow(t *testing.T) {
	r := &PeerOTCNegotiationReconciler{ownRouting: 111}

	tests := []struct {
		name            string
		buyerRouting    int64
		sellerRouting   int64
		expectedRouting int64
	}{
		{"buyer is peer", 222, 111, 222},
		{"seller is peer", 111, 333, 333},
		{"both own (intra-bank)", 111, 111, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bR := tc.buyerRouting
			sR := tc.sellerRouting
			row := &model.OTCNegotiation{
				RemoteBuyerRouting:  &bR,
				RemoteSellerRouting: &sR,
			}
			got := r.peerRoutingForRow(row)
			if got != tc.expectedRouting {
				t.Errorf("got %d, want %d", got, tc.expectedRouting)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fix 2: Buyer-side (outbound) topology — fetcher called with SELLER (peer)
// routing as rid, and cancel/accept behave correctly.
// ---------------------------------------------------------------------------

// TestPeerOTCReconciler_ReconcileRow_BuyerSide_FetcherCalledWithSellerRouting
// verifies that for an OUTBOUND row (we are the buyer: buyerRouting==ownRouting,
// sellerRouting==peerRouting), the fetcher is invoked with the SELLER's
// (peer's) routing as rid — not our own routing.
func TestPeerOTCReconciler_ReconcileRow_BuyerSide_FetcherCalledWithSellerRouting(t *testing.T) {
	const (
		ownRouting    int64 = 111
		sellerRouting int64 = 222 // peer's routing
		peerCode            = "222"
		foreignID           = "neg-outbound-1"
		buyerID             = "client-5" // local buyer (ownRouting)
		sellerID            = "client-9" // peer seller (sellerRouting)
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			// WE are the buyer (ownRouting); the PEER is the seller (sellerRouting).
			remoteNegRow(10, ownRouting, ownRouting, buyerID, sellerRouting, sellerID, foreignID, "ongoing"),
		},
	}

	var capturedRID string
	// Fetcher records the rid argument, then reports terminal (peer cancelled).
	fetcher := func(_ context.Context, _, _, rid, _ string) (bool, error) {
		capturedRID = rid
		return false, nil
	}

	r := newTestReconciler(repo, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "key"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The rid must be the SELLER's (peer's) routing number, not our own.
	wantRID := "222"
	if capturedRID != wantRID {
		t.Errorf("fetcher called with rid=%q, want %q (seller/peer routing)", capturedRID, wantRID)
	}
}

// TestPeerOTCReconciler_ReconcileRow_BuyerSide_PeerCancelled_Cancelled verifies
// that for a buyer-side outbound row, when the peer reports terminal and no
// contract exists locally, the row is reconciled to "cancelled".
func TestPeerOTCReconciler_ReconcileRow_BuyerSide_PeerCancelled_Cancelled(t *testing.T) {
	const (
		ownRouting    int64 = 111
		sellerRouting int64 = 222
		peerCode            = "222"
		foreignID           = "neg-outbound-cancel"
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(11, ownRouting, ownRouting, "client-3", sellerRouting, "client-4", foreignID, "ongoing"),
		},
	}

	// Fetcher reports terminal; no local contract → expect cancel.
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) { return false, nil }
	checker := &fakeContractChecker{hasContract: false}

	r := newTestReconcilerWithChecker(repo, checker, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "key"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updates := repo.getUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 UpdateStatus call, got %d", len(updates))
	}
	if updates[0].status != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", updates[0].status)
	}
}

// ---------------------------------------------------------------------------
// Fix 1: peer reports accepted/terminal-non-cancelled → row is NOT cancelled.
// ---------------------------------------------------------------------------

// TestPeerOTCReconciler_ReconcileRow_PeerNonOngoing_ContractExists_Accepted
// verifies the core Fix-1 invariant: when the peer reports isOngoing=false
// AND a local peer_option_contracts row exists for the negotiation, the
// reconciler sets status to "accepted" — NEVER "cancelled".
func TestPeerOTCReconciler_ReconcileRow_PeerNonOngoing_ContractExists_Accepted(t *testing.T) {
	const (
		ownRouting    int64 = 111
		sellerRouting int64 = 222
		peerCode            = "222"
		foreignID           = "neg-was-accepted"
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			// stuck "ongoing" due to a missed MarkNegotiationAccepted webhook.
			remoteNegRow(12, ownRouting, ownRouting, "client-6", sellerRouting, "client-8", foreignID, "ongoing"),
		},
	}

	// Peer reports terminal (isOngoing=false) — could be accepted or cancelled.
	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) { return false, nil }
	// But we have a local contract → accepted!
	checker := &fakeContractChecker{hasContract: true}

	r := newTestReconcilerWithChecker(repo, checker, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "key"},
	}

	if err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updates := repo.getUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 UpdateStatus call, got %d", len(updates))
	}
	if updates[0].status != "accepted" {
		t.Errorf("Fix-1 violated: expected status=accepted when contract exists, got %q", updates[0].status)
	}

	// Verify the checker was called with the correct routing (seller/peer routing).
	calls := checker.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 contract check call, got %d", len(calls))
	}
	if calls[0].routing != sellerRouting {
		t.Errorf("contract check called with routing=%d, want %d (seller/peer routing)", calls[0].routing, sellerRouting)
	}
	if calls[0].negID != foreignID {
		t.Errorf("contract check called with negID=%q, want %q", calls[0].negID, foreignID)
	}
}

// TestPeerOTCReconciler_ReconcileRow_ContractCheckError_Skipped verifies that
// a DB error from the contract checker triggers the false-cancel guard —
// the row is skipped (no UpdateStatus call) and an error is returned.
func TestPeerOTCReconciler_ReconcileRow_ContractCheckError_Skipped(t *testing.T) {
	const (
		ownRouting    int64 = 111
		sellerRouting int64 = 222
		peerCode            = "222"
		foreignID           = "neg-checker-error"
	)

	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(13, ownRouting, ownRouting, "client-11", sellerRouting, "client-22", foreignID, "ongoing"),
		},
	}

	fetcher := func(_ context.Context, _, _, _, _ string) (bool, error) { return false, nil }
	checker := &fakeContractChecker{err: errors.New("db connection lost")}

	r := newTestReconcilerWithChecker(repo, checker, fetcher, ownRouting)

	peerMap := map[string]peerEntry{
		peerCode: {baseURL: "http://peer:8080/api/v3", apiKey: "key"},
	}

	err := r.reconcileRow(context.Background(), &repo.rows[0], peerMap)
	if err == nil {
		t.Fatal("expected error from reconcileRow when contract checker fails")
	}

	if got := repo.getUpdates(); len(got) != 0 {
		t.Errorf("expected 0 UpdateStatus calls on contract check error, got %d: %+v", len(got), got)
	}
}

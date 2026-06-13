package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
)

// fakePeerAdmin implements transactionpb.PeerBankAdminServiceClient for the
// reconciler's buildPeerMap step. Only ListPeerBanks + ResolvePeerByBankCode
// are exercised; the rest satisfy the interface.
type fakePeerAdmin struct {
	listErr    error
	resolveErr error
	banks      []*transactionpb.PeerBank
	resolved   map[string]*transactionpb.PeerBankFull
}

func (f *fakePeerAdmin) ListPeerBanks(_ context.Context, _ *transactionpb.ListPeerBanksRequest, _ ...grpc.CallOption) (*transactionpb.ListPeerBanksResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &transactionpb.ListPeerBanksResponse{PeerBanks: f.banks}, nil
}
func (f *fakePeerAdmin) GetPeerBank(context.Context, *transactionpb.GetPeerBankRequest, ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, nil
}
func (f *fakePeerAdmin) CreatePeerBank(context.Context, *transactionpb.CreatePeerBankRequest, ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, nil
}
func (f *fakePeerAdmin) UpdatePeerBank(context.Context, *transactionpb.UpdatePeerBankRequest, ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, nil
}
func (f *fakePeerAdmin) DeletePeerBank(context.Context, *transactionpb.DeletePeerBankRequest, ...grpc.CallOption) (*transactionpb.DeletePeerBankResponse, error) {
	return nil, nil
}
func (f *fakePeerAdmin) ResolvePeerByAPIToken(context.Context, *transactionpb.ResolvePeerByAPITokenRequest, ...grpc.CallOption) (*transactionpb.ResolvePeerByAPITokenResponse, error) {
	return nil, nil
}
func (f *fakePeerAdmin) ResolvePeerByBankCode(_ context.Context, in *transactionpb.ResolvePeerByBankCodeRequest, _ ...grpc.CallOption) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	full := f.resolved[in.GetBankCode()]
	return &transactionpb.ResolvePeerByBankCodeResponse{PeerBank: full}, nil
}

func TestPeerOTCReconciler_RunOnce_FullCycle_FlipsCancelled(t *testing.T) {
	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			// own=111, buyer=111(us), seller=222(peer) → peer routing 222.
			remoteNegRow(1, 111, 111, "buyer-1", 222, "seller-1", "fid-1", "ongoing"),
		},
	}
	fetcher := func(_ context.Context, baseURL, apiKey, rid, fid string) (bool, error) {
		// Peer reports terminal (not ongoing) → row should be cancelled.
		return false, nil
	}
	admin := &fakePeerAdmin{
		banks: []*transactionpb.PeerBank{{BankCode: "222"}},
		resolved: map[string]*transactionpb.PeerBankFull{
			"222": {BaseUrl: "http://peer/", ApiTokenPlaintext: "tok", Active: true},
		},
	}
	r := &PeerOTCNegotiationReconciler{
		repo:       repo,
		peerAdmin:  admin,
		fetcher:    fetcher,
		ownRouting: 111,
		interval:   time.Hour,
	}

	r.RunOnce(context.Background())

	updates := repo.getUpdates()
	if len(updates) != 1 || updates[0].status != "cancelled" {
		t.Fatalf("expected one cancelled update, got %+v", updates)
	}
	if updates[0].routing != 222 {
		t.Errorf("update routing = %d, want 222", updates[0].routing)
	}
}

func TestPeerOTCReconciler_RunOnce_NoRows_NoOp(t *testing.T) {
	repo := &fakeNegRepo{}
	r := &PeerOTCNegotiationReconciler{
		repo: repo, peerAdmin: &fakePeerAdmin{}, ownRouting: 111, interval: time.Hour,
		fetcher: func(context.Context, string, string, string, string) (bool, error) { return true, nil },
	}
	r.RunOnce(context.Background())
	if len(repo.getUpdates()) != 0 {
		t.Errorf("no rows should produce no updates")
	}
}

func TestPeerOTCReconciler_Reconcile_BuildPeerMapError_Skips(t *testing.T) {
	repo := &fakeNegRepo{
		rows: []model.OTCNegotiation{
			remoteNegRow(1, 111, 111, "b", 222, "s", "fid", "ongoing"),
		},
	}
	r := &PeerOTCNegotiationReconciler{
		repo:       repo,
		peerAdmin:  &fakePeerAdmin{listErr: errors.New("grpc down")},
		fetcher:    func(context.Context, string, string, string, string) (bool, error) { return true, nil },
		ownRouting: 111, interval: time.Hour,
	}
	r.RunOnce(context.Background())
	if len(repo.getUpdates()) != 0 {
		t.Errorf("buildPeerMap error should skip all rows")
	}
}

func TestNewPeerOTCNegotiationReconciler_DefaultsAndWithNotifier(t *testing.T) {
	// nil httpClient → default client + newHTTPStatusFetcher; interval<=0 → 2m.
	r := NewPeerOTCNegotiationReconciler(nil, nil, &fakePeerAdmin{}, nil, 111, 0)
	if r.interval != 2*time.Minute {
		t.Errorf("interval = %v, want 2m default", r.interval)
	}
	if r.fetcher == nil {
		t.Error("expected a default HTTP fetcher wired")
	}
	notif := &fakeReconcilerNotifier{}
	if r.WithNotifier(notif).notifier == nil {
		t.Error("WithNotifier did not wire the notifier")
	}
}

package otccache

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

// fakeOptionLister is a no-op OptionOfferLister used when local offers are not
// relevant to the test (e.g. cross-bank shell ingest tests).
type fakeOptionLister struct{}

func (f *fakeOptionLister) ListOpenForCache(_ int) ([]model.OTCOffer, error) { return nil, nil }

// fakePathEgressClient routes ProxyToPeer calls by path, returning the stored
// response (or 404 by default). Used by the cross-bank refresh test to control
// the peer's /public-stock response.
type fakePathEgressClient struct {
	byPath map[string]*transactionpb.ProxyToPeerResponse
}

func (f *fakePathEgressClient) ProxyToPeer(_ context.Context, in *transactionpb.ProxyToPeerRequest, _ ...grpc.CallOption) (*transactionpb.ProxyToPeerResponse, error) {
	if r, ok := f.byPath[in.GetPath()]; ok {
		return r, nil
	}
	return &transactionpb.ProxyToPeerResponse{StatusCode: 404, Body: []byte("not found")}, nil
}

func (f *fakePathEgressClient) CheckPeerReachability(_ context.Context, _ *transactionpb.CheckPeerReachabilityRequest, _ ...grpc.CallOption) (*transactionpb.PeerReachability, error) {
	return nil, errors.New("not used")
}

func (f *fakePathEgressClient) GetPeersState(_ context.Context, _ *transactionpb.GetPeersStateRequest, _ ...grpc.CallOption) (*transactionpb.GetPeersStateResponse, error) {
	return nil, errors.New("not used")
}

// fakePeerBankAdminClient implements transactionpb.PeerBankAdminServiceClient for
// the cache-refresh path: the refresher only calls ListPeerBanks (to enumerate
// which peers to poll); per-peer HTTP fetch goes through PeerEgressService.
// (Relocated 2026-06-11 from the retired otc-stock-marketplace refresher_test.go.)
type fakePeerBankAdminClient struct {
	listResp *transactionpb.ListPeerBanksResponse
	listErr  error
}

func (f *fakePeerBankAdminClient) ListPeerBanks(ctx context.Context, in *transactionpb.ListPeerBanksRequest, opts ...grpc.CallOption) (*transactionpb.ListPeerBanksResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}
func (f *fakePeerBankAdminClient) GetPeerBank(ctx context.Context, in *transactionpb.GetPeerBankRequest, opts ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, errors.New("not used")
}
func (f *fakePeerBankAdminClient) CreatePeerBank(ctx context.Context, in *transactionpb.CreatePeerBankRequest, opts ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, errors.New("not used")
}
func (f *fakePeerBankAdminClient) UpdatePeerBank(ctx context.Context, in *transactionpb.UpdatePeerBankRequest, opts ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	return nil, errors.New("not used")
}
func (f *fakePeerBankAdminClient) DeletePeerBank(ctx context.Context, in *transactionpb.DeletePeerBankRequest, opts ...grpc.CallOption) (*transactionpb.DeletePeerBankResponse, error) {
	return nil, errors.New("not used")
}
func (f *fakePeerBankAdminClient) ResolvePeerByAPIToken(ctx context.Context, in *transactionpb.ResolvePeerByAPITokenRequest, opts ...grpc.CallOption) (*transactionpb.ResolvePeerByAPITokenResponse, error) {
	return nil, errors.New("not used")
}
func (f *fakePeerBankAdminClient) ResolvePeerByBankCode(ctx context.Context, in *transactionpb.ResolvePeerByBankCodeRequest, opts ...grpc.CallOption) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
	return nil, errors.New("not used")
}

// ---------------------------------------------------------------------------
// buildAndMirrorRemoteStockShells (synthesized sell_initiated shells)
// ---------------------------------------------------------------------------

type fakeShellMirror struct {
	upserts            []*model.OTCOffer
	shellReconcilePeer int64
	shellReconcileSeen []string
}

func (f *fakeShellMirror) UpsertRemote(o *model.OTCOffer, _ time.Time) (uint64, error) {
	f.upserts = append(f.upserts, o)
	return uint64(len(f.upserts)), nil
}
func (f *fakeShellMirror) UpsertRemoteShell(o *model.OTCOffer, t time.Time) (uint64, error) {
	return f.UpsertRemote(o, t)
}
func (f *fakeShellMirror) ReconcileRemoteNotSeen(_ int64, _ []string) (int64, error) {
	return 0, nil
}
func (f *fakeShellMirror) ReconcileRemoteShellsNotSeen(peer int64, seen []string) (int64, error) {
	f.shellReconcilePeer = peer
	f.shellReconcileSeen = seen
	return 0, nil
}

func TestBuildAndMirrorRemoteStockShells(t *testing.T) {
	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake}
	stocks := []sitx.PublicStock{{
		Stock:   sitx.StockDescription{Ticker: "AAPL"},
		Sellers: []sitx.PublicSeller{{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: "client-5"}, Amount: 100}},
	}}
	out := r.buildAndMirrorRemoteStockShells("bank222", 222, stocks)
	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1", len(out))
	}
	got := fake.upserts[0]
	if got.NativeID == nil || *got.NativeID != "ps:222:client-5:AAPL" {
		t.Fatalf("native_id = %v", got.NativeID)
	}
	if got.Direction != model.OTCDirectionSellInitiated {
		t.Fatalf("direction = %s", got.Direction)
	}
	if fake.shellReconcilePeer != 222 || len(fake.shellReconcileSeen) != 1 {
		t.Fatalf("shell reconcile scope wrong")
	}
}

// TestBuildAndMirrorRemoteStockShells_DuplicateSellerTickerAggregated reproduces
// the bug where a peer's /public-stock lists the SAME (seller, ticker) more than
// once with different amounts (e.g. seller 3 selling OPK as 5 and 70). The §3.1
// schema has no per-offer key — (seller, ticker) is the only listing identity — so
// both entries map to native_id "ps:444:3:OPK". Before the fix this produced two
// cache rows colliding on the same native_id/local id, so a bid on one silently
// targeted the other. The fix aggregates duplicates into ONE shell (summed amount),
// guaranteeing every emitted row maps 1:1 to a distinct id.
func TestBuildAndMirrorRemoteStockShells_DuplicateSellerTickerAggregated(t *testing.T) {
	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake}
	stocks := []sitx.PublicStock{{
		Stock: sitx.StockDescription{Ticker: "OPK"},
		Sellers: []sitx.PublicSeller{
			{Seller: sitx.ForeignBankId{RoutingNumber: 444, ID: "3"}, Amount: 5},
			{Seller: sitx.ForeignBankId{RoutingNumber: 444, ID: "3"}, Amount: 70},
		},
	}}
	out := r.buildAndMirrorRemoteStockShells("444", 444, stocks)

	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1 (duplicate (seller,ticker) must collapse to one shell)", len(out))
	}
	if out[0].OfferID != "ps:444:3:OPK" {
		t.Fatalf("offer_id = %q, want ps:444:3:OPK", out[0].OfferID)
	}
	if out[0].Amount != 75 {
		t.Fatalf("amount = %d, want 75 (aggregated 5+70)", out[0].Amount)
	}
	if len(fake.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1 (one DB row per unique native_id)", len(fake.upserts))
	}
	if !fake.upserts[0].Quantity.Equal(decimal.NewFromInt(75)) {
		t.Fatalf("persisted quantity = %s, want 75", fake.upserts[0].Quantity)
	}
	if len(fake.shellReconcileSeen) != 1 {
		t.Fatalf("shell reconcile seen = %d, want 1", len(fake.shellReconcileSeen))
	}
}

// ---------------------------------------------------------------------------
// /public-stock shells are the SOLE cross-bank option source
// ---------------------------------------------------------------------------

// TestOptionRefresher_IngestsPublicStockShells verifies that a peer whose
// /public-stock returns data has its synthesized shells ingested into the cache
// AND is counted toward PeersReached. Since the /public-option-offers ingestion
// was removed, /public-stock shells are now the only cross-bank option source.
func TestOptionRefresher_IngestsPublicStockShells(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	stocksBody, err := json.Marshal(sitx.PublicStocksResponse{{
		Stock:   sitx.StockDescription{Ticker: "AAPL"},
		Sellers: []sitx.PublicSeller{{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: "client-5"}, Amount: 10}},
	}})
	if err != nil {
		t.Fatalf("marshal stocks: %v", err)
	}

	egress := &fakePathEgressClient{
		byPath: map[string]*transactionpb.ProxyToPeerResponse{
			"/public-stock": {StatusCode: http.StatusOK, Body: stocksBody},
		},
	}
	peerAdmin := &fakePeerBankAdminClient{
		listResp: &transactionpb.ListPeerBanksResponse{
			PeerBanks: []*transactionpb.PeerBank{{BankCode: "222", RoutingNumber: 222}},
		},
	}

	c := NewOptionCache()
	r := NewOptionRefresher(c, &fakeOptionLister{}, nil, peerAdmin, egress, "111", 111, time.Minute)
	r.refresh(context.Background())

	snap := c.Get()
	var shells []OptionOffer
	for _, o := range snap.Offers {
		// Shells synthesised from a peer's /public-stock are the remote rows.
		if o.Kind == "remote" {
			shells = append(shells, o)
		}
	}
	if len(shells) != 1 {
		t.Fatalf("expected 1 shell from /public-stock, got %d (total offers=%d)", len(shells), len(snap.Offers))
	}
	if shells[0].Ticker != "AAPL" {
		t.Errorf("shell ticker = %q, want AAPL", shells[0].Ticker)
	}
	// A peer reachable via /public-stock must count toward PeersReached.
	if snap.PeersTotal != 1 || snap.PeersReached != 1 {
		t.Errorf("peers total/reached = %d/%d, want 1/1 (peer reachable via /public-stock must count)", snap.PeersTotal, snap.PeersReached)
	}
}

// ---------------------------------------------------------------------------
// MINOR fix: native_id uses seller routing, not peer routing
// ---------------------------------------------------------------------------

// TestBuildAndMirrorRemoteStockShells_UsesSellerRouting verifies that two sellers
// with the same ID but at DIFFERENT origin banks produce DIFFERENT native_ids.
// The native_id must key on s.Seller.RoutingNumber (the seller's bank), not on
// the peerRouting (the bank we polled), so a seller at routing 333 listed on
// peer 222 gets native_id "ps:333:...:..." and cannot collide with a seller 222
// native_id "ps:222:...:...".
func TestBuildAndMirrorRemoteStockShells_UsesSellerRouting(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake, ownRouting: 111}
	stocks := []sitx.PublicStock{{
		Stock: sitx.StockDescription{Ticker: "AAPL"},
		Sellers: []sitx.PublicSeller{
			// Seller at routing 333 — different from the peer routing (222).
			{Seller: sitx.ForeignBankId{RoutingNumber: 333, ID: "client-5"}, Amount: 100},
		},
	}}
	out := r.buildAndMirrorRemoteStockShells("bank222", 222, stocks)
	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1", len(out))
	}
	got := fake.upserts[0]
	// native_id must use the SELLER's routing (333), not the peer routing (222).
	want := "ps:333:client-5:AAPL"
	if got.NativeID == nil || *got.NativeID != want {
		t.Fatalf("native_id = %q, want %q (must use seller routing, not peer routing)", safeNativeID(got.NativeID), want)
	}
	// The in-memory row's OfferID must also reflect the seller routing.
	if out[0].OfferID != want {
		t.Errorf("in-memory OfferID = %q, want %q", out[0].OfferID, want)
	}
}

func safeNativeID(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

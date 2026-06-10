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
// response (or 404 by default). Used by the cross-bank refresh tests to control
// which endpoints succeed and which fail independently (Bug-1 fix).
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

type fakeMirror struct {
	nextID     uint64
	byKey      map[string]uint64
	reconciled map[int64][]string
}

func newFakeMirror() *fakeMirror {
	return &fakeMirror{byKey: map[string]uint64{}, reconciled: map[int64][]string{}}
}
func (m *fakeMirror) UpsertRemote(o *model.OTCOffer, _ time.Time) (uint64, error) {
	key := ""
	if o.NativeID != nil {
		key = *o.NativeID
	}
	if id, ok := m.byKey[key]; ok {
		return id, nil
	}
	m.nextID++
	m.byKey[key] = m.nextID
	return m.nextID, nil
}
func (m *fakeMirror) UpsertRemoteShell(o *model.OTCOffer, t time.Time) (uint64, error) {
	return m.UpsertRemote(o, t)
}
func (m *fakeMirror) ReconcileRemoteNotSeen(peerRouting int64, seen []string) (int64, error) {
	m.reconciled[peerRouting] = seen
	return 0, nil
}
func (m *fakeMirror) ReconcileRemoteShellsNotSeen(_ int64, _ []string) (int64, error) {
	return 0, nil
}

func TestBuildAndMirrorRemoteOffers_StampsIDsAndReconciles(t *testing.T) {
	m := newFakeMirror()
	r := (&OptionRefresher{}).WithMirror(m)
	offers := []sitx.PublicOptionOffer{
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "1"}, SellerID: sitx.ForeignBankId{ID: "employee-1"}, Ticker: "BAC", Amount: 7, StrikePrice: decimal.RequireFromString("100"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("10"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "2"}, SellerID: sitx.ForeignBankId{ID: "client-9"}, Ticker: "AAPL", Amount: 3, StrikePrice: decimal.RequireFromString("200"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("5"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
	}
	rows := r.buildAndMirrorRemoteOffers("111", 111, offers)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Kind != "remote" || row.LocalID == 0 {
			t.Fatalf("row not stamped: %+v", row)
		}
	}
	if got := m.reconciled[111]; len(got) != 2 {
		t.Fatalf("reconcile seen-list = %v, want 2 entries", got)
	}
}

func TestBuildAndMirrorRemoteOffers_EmptyReconcilesAll(t *testing.T) {
	m := newFakeMirror()
	r := (&OptionRefresher{}).WithMirror(m)
	rows := r.buildAndMirrorRemoteOffers("111", 111, nil)
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
	got, ok := m.reconciled[111]
	if !ok || len(got) != 0 {
		t.Fatalf("expected reconcile called with empty seen for peer 111; got %v ok=%v", got, ok)
	}
}

type errMirror struct{ reconciled map[int64][]string }

func (m *errMirror) UpsertRemote(_ *model.OTCOffer, _ time.Time) (uint64, error) {
	return 0, errors.New("db down")
}
func (m *errMirror) UpsertRemoteShell(o *model.OTCOffer, t time.Time) (uint64, error) {
	return m.UpsertRemote(o, t)
}
func (m *errMirror) ReconcileRemoteNotSeen(peerRouting int64, seen []string) (int64, error) {
	if m.reconciled == nil {
		m.reconciled = map[int64][]string{}
	}
	m.reconciled[peerRouting] = seen
	return 0, nil
}
func (m *errMirror) ReconcileRemoteShellsNotSeen(_ int64, _ []string) (int64, error) {
	return 0, nil
}

func TestBuildAndMirrorRemoteOffers_UpsertFailureLeavesRowUnstamped(t *testing.T) {
	m := &errMirror{}
	r := (&OptionRefresher{}).WithMirror(m)
	offers := []sitx.PublicOptionOffer{
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "1"}, SellerID: sitx.ForeignBankId{ID: "employee-1"}, Ticker: "BAC", Amount: 7, StrikePrice: decimal.RequireFromString("100"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("10"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
	}
	rows := r.buildAndMirrorRemoteOffers("111", 111, offers)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].LocalID != 0 {
		t.Fatalf("failed upsert should leave LocalID=0, got %d", rows[0].LocalID)
	}
	if got := m.reconciled[111]; len(got) != 0 {
		t.Fatalf("failed-upsert offer must not be in seen list; got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SP-2a Task 7 Part B — ingestion collision guards
// ---------------------------------------------------------------------------

// TestBuildAndMirrorRemoteOffers_OwnRoutingPeer_Skipped verifies that when a
// peer's peerRouting equals OwnRouting(), the entire payload is rejected and
// nil is returned. Persisting such a payload would stamp routing_number=OwnRouting()
// on the mirror rows, making them look LOCAL.
func TestBuildAndMirrorRemoteOffers_OwnRoutingPeer_Skipped(t *testing.T) {
	model.SetOwnRouting("111")
	m := newFakeMirror()
	r := (&OptionRefresher{ownRouting: 111}).WithMirror(m)
	offers := []sitx.PublicOptionOffer{
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "bad-1"}, SellerID: sitx.ForeignBankId{ID: "client-9"}, Ticker: "AAPL", Amount: 3, StrikePrice: decimal.RequireFromString("200"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("5"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "bad-2"}, SellerID: sitx.ForeignBankId{ID: "client-3"}, Ticker: "MSFT", Amount: 1, StrikePrice: decimal.RequireFromString("50"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("1"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
	}
	// peerRouting == OwnRouting() → entire peer payload must be skipped.
	rows := r.buildAndMirrorRemoteOffers("111", 111, offers)
	if rows != nil {
		t.Errorf("expected nil (whole peer skipped), got %d rows", len(rows))
	}
	// No mirror calls should have been made.
	if len(m.byKey) != 0 {
		t.Errorf("expected no upsert calls, got %d entries in mirror", len(m.byKey))
	}
}

// ---------------------------------------------------------------------------
// SP-2b — buildAndMirrorRemoteStockShells (synthesized sell_initiated shells)
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
	if got.HasPresetTerms {
		t.Fatalf("shell HasPresetTerms = true, want false")
	}
	if got.Direction != model.OTCDirectionSellInitiated {
		t.Fatalf("direction = %s", got.Direction)
	}
	if !got.StrikePrice.IsZero() || !got.Premium.IsZero() {
		t.Fatalf("shell must have zero terms")
	}
	if got.StrikeCurrency != nil || got.PremiumCurrency != nil {
		t.Fatalf("shell currencies must be nil")
	}
	if fake.shellReconcilePeer != 222 || len(fake.shellReconcileSeen) != 1 {
		t.Fatalf("shell reconcile scope wrong")
	}
}

// TestBuildAndMirrorRemoteOffers_OwnRoutingOffer_Skipped verifies that an
// individual offer claiming our own routing in its OfferID is skipped even
// when the overall peerRouting differs (defense-in-depth per-offer guard).
func TestBuildAndMirrorRemoteOffers_OwnRoutingOffer_Skipped(t *testing.T) {
	model.SetOwnRouting("111")
	m := newFakeMirror()
	// peerRouting = 222 (legitimate peer); one offer claims OfferID.RoutingNumber=111.
	r := (&OptionRefresher{ownRouting: 111}).WithMirror(m)
	offers := []sitx.PublicOptionOffer{
		// Legitimate offer (different routing in OfferID — or zero, which is fine).
		{OfferID: sitx.ForeignBankId{RoutingNumber: 222, ID: "good-1"}, SellerID: sitx.ForeignBankId{ID: "client-9"}, Ticker: "AAPL", Amount: 3, StrikePrice: decimal.RequireFromString("200"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("5"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
		// Collision offer: OfferID.RoutingNumber == OwnRouting() — must be skipped.
		{OfferID: sitx.ForeignBankId{RoutingNumber: 111, ID: "bad-1"}, SellerID: sitx.ForeignBankId{ID: "client-3"}, Ticker: "MSFT", Amount: 1, StrikePrice: decimal.RequireFromString("50"), StrikeCurrency: "USD", Premium: decimal.RequireFromString("1"), PremiumCurrency: "USD", Direction: "sell_initiated", SettlementDate: "2026-06-11T00:00:00Z", CreatedAt: "2026-06-04T18:02:16Z"},
	}
	rows := r.buildAndMirrorRemoteOffers("222", 222, offers)
	if len(rows) != 1 {
		t.Errorf("expected 1 row (good offer only), got %d", len(rows))
	}
	if len(rows) > 0 && rows[0].OfferID != "good-1" {
		t.Errorf("expected good-1, got %q", rows[0].OfferID)
	}
}

// ---------------------------------------------------------------------------
// Bug-1 fix: /public-stock shells ingested even when /public-option-offers fails
// ---------------------------------------------------------------------------

// TestOptionRefresher_ShellsIngestedWhenOptionOffersFails verifies that a base-spec
// peer (which 404s /public-option-offers but serves /public-stock) still contributes
// shell offers to the cache. Before the fix, the per-peer goroutine returned early on
// the option-offers error and never called fetchPeerStocks.
func TestOptionRefresher_ShellsIngestedWhenOptionOffersFails(t *testing.T) {
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
			// Base-spec peer: /public-option-offers returns 404, /public-stock returns data.
			"/public-option-offers": {StatusCode: http.StatusNotFound, Body: []byte("not found")},
			"/public-stock":         {StatusCode: http.StatusOK, Body: stocksBody},
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
		if !o.HasPresetTerms {
			shells = append(shells, o)
		}
	}
	if len(shells) != 1 {
		t.Fatalf("expected 1 shell from /public-stock when option-offers 404s, got %d (total offers=%d); option-offers failure must NOT suppress stock fetch",
			len(shells), len(snap.Offers))
	}
	if shells[0].Ticker != "AAPL" {
		t.Errorf("shell ticker = %q, want AAPL", shells[0].Ticker)
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

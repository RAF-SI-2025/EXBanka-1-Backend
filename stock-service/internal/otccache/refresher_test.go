package otccache

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	contractsitx "github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// fakeOTCLister returns canned holdings for fetchLocal.
type fakeOTCLister struct {
	rows []model.Holding
	err  error
}

func (f *fakeOTCLister) ListOffers(_ service.OTCFilter) ([]model.Holding, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, int64(len(f.rows)), nil
}

// fakePeerBankAdminClient implements transactionpb.PeerBankAdminServiceClient
// for the cache refresh path. ListPeerBanks returns canned peers; resolve
// returns canned full-detail PeerBank rows.
type fakePeerBankAdminClient struct {
	listResp     *transactionpb.ListPeerBanksResponse
	listErr      error
	resolveByMap map[string]*transactionpb.PeerBankFull
	resolveErr   error
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
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	pb := f.resolveByMap[in.GetBankCode()]
	return &transactionpb.ResolvePeerByBankCodeResponse{PeerBank: pb}, nil
}

// TestNewRefresher builds a Refresher and verifies field defaults.
func TestNewRefresher(t *testing.T) {
	c := New()
	r := NewRefresher(c, &fakeOTCLister{}, &fakePeerBankAdminClient{}, "111", 5*time.Minute)
	if r.cache != c {
		t.Errorf("cache wired wrong")
	}
	if r.ownBankCode != "111" {
		t.Errorf("ownBankCode = %q", r.ownBankCode)
	}
	if r.interval != 5*time.Minute {
		t.Errorf("interval = %v", r.interval)
	}
	if r.httpClient == nil {
		t.Error("httpClient should be initialized")
	}
}

// TestRefresher_FetchLocal_Maps verifies fetchLocal converts model.Holding
// rows to the unified Offer shape with the local bank code.
func TestRefresher_FetchLocal_Maps(t *testing.T) {
	uid := uint64(7)
	holding := model.Holding{
		ID: 99, OwnerID: &uid,
		UserFirstName: "Jane", UserLastName: "Doe",
		SecurityType:   "stock",
		Ticker:         "AAPL",
		Name:           "Apple",
		PublicQuantity: 5,
		AveragePrice:   decimal.NewFromFloat(123.45),
		CreatedAt:      time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
	}
	r := NewRefresher(New(), &fakeOTCLister{rows: []model.Holding{holding}}, nil, "111", time.Minute)
	out, err := r.fetchLocal()
	if err != nil {
		t.Fatalf("fetchLocal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d offers", len(out))
	}
	o := out[0]
	if o.Kind != "local" || o.BankCode != "111" {
		t.Errorf("kind=%s bank=%s", o.Kind, o.BankCode)
	}
	if o.SellerID != 7 || o.SellerName != "Jane Doe" {
		t.Errorf("seller=%d name=%q", o.SellerID, o.SellerName)
	}
	if o.PricePerUnit != "123.45" {
		t.Errorf("price=%q", o.PricePerUnit)
	}
	if o.Ticker != "AAPL" {
		t.Errorf("ticker=%q", o.Ticker)
	}
}

// TestRefresher_FetchLocal_ErrorPropagates ensures an error from the OTCLister
// surface propagates so the caller can log + skip the local source.
func TestRefresher_FetchLocal_ErrorPropagates(t *testing.T) {
	r := NewRefresher(New(), &fakeOTCLister{err: errors.New("boom")}, nil, "111", time.Minute)
	if _, err := r.fetchLocal(); err == nil {
		t.Fatal("expected error")
	}
}

// TestRefresher_Refresh_LocalOnly_NoPeers covers the path where local fetch
// works and there are no peers — the cache should still be populated.
func TestRefresher_Refresh_LocalOnly_NoPeers(t *testing.T) {
	uid := uint64(7)
	holding := model.Holding{
		ID: 99, OwnerID: &uid, UserFirstName: "A", UserLastName: "B",
		SecurityType: "stock", Ticker: "AAPL",
		PublicQuantity: 5, AveragePrice: decimal.NewFromInt(100),
	}
	cache := New()
	peerAdmin := &fakePeerBankAdminClient{
		listResp: &transactionpb.ListPeerBanksResponse{PeerBanks: nil},
	}
	r := NewRefresher(cache, &fakeOTCLister{rows: []model.Holding{holding}}, peerAdmin, "111", time.Minute)
	r.refresh(context.Background())

	snap := cache.Get()
	if len(snap.Offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(snap.Offers))
	}
	if snap.Offers[0].Kind != "local" {
		t.Errorf("expected local offer")
	}
	if snap.PeersTotal != 0 || snap.PeersReached != 0 {
		t.Errorf("peers total/reached = %d/%d", snap.PeersTotal, snap.PeersReached)
	}
	if snap.LastRefresh.IsZero() {
		t.Errorf("LastRefresh not set")
	}
}

// TestRefresher_Refresh_LocalFetchFails covers the log-and-continue branch in
// refresh when fetchLocal returns an error.
func TestRefresher_Refresh_LocalFetchFails(t *testing.T) {
	cache := New()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{PeerBanks: nil}}
	r := NewRefresher(cache, &fakeOTCLister{err: errors.New("local boom")}, peerAdmin, "111", time.Minute)
	r.refresh(context.Background())
	snap := cache.Get()
	if len(snap.Offers) != 0 {
		t.Errorf("expected 0 offers, got %d", len(snap.Offers))
	}
}

// TestRefresher_Refresh_PeerListFails covers the branch where ListPeerBanks
// returns an error — local offers still get cached.
func TestRefresher_Refresh_PeerListFails(t *testing.T) {
	cache := New()
	peerAdmin := &fakePeerBankAdminClient{listErr: errors.New("peer list fail")}
	r := NewRefresher(cache, &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	r.refresh(context.Background())
	snap := cache.Get()
	if snap.PeersTotal != 0 {
		t.Errorf("peers total = %d (want 0 when list fails)", snap.PeersTotal)
	}
}

// TestRefresher_FetchPeer_HappyPath sets up a real httptest server returning
// PublicStocksResponse JSON, and asserts the offers come back as remote
// entries with the right bank code + ticker mapping.
func TestRefresher_FetchPeer_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/public-stock") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "tok-222" {
			t.Errorf("missing X-Api-Key header: %v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(contractsitx.PublicStocksResponse{
			Stocks: []contractsitx.PublicStock{
				{
					OwnerID:       contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-3"},
					Ticker:        "MSFT",
					Amount:        50,
					PricePerStock: decimal.NewFromFloat(200.5),
					Currency:      "USD",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	peer := &transactionpb.PeerBankFull{BankCode: "222", BaseUrl: server.URL, Active: true, ApiTokenPlaintext: "tok-222"}
	peerAdmin := &fakePeerBankAdminClient{
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": peer},
	}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	out, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err != nil {
		t.Fatalf("fetchPeer: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d offers", len(out))
	}
	if out[0].Kind != "remote" || out[0].BankCode != "222" || out[0].Ticker != "MSFT" {
		t.Errorf("offer = %+v", out[0])
	}
	if out[0].OwnerID != "client-3" {
		t.Errorf("owner_id = %q want client-3", out[0].OwnerID)
	}
	if out[0].Currency != "USD" {
		t.Errorf("currency=%s", out[0].Currency)
	}
}

// TestRefresher_FetchPeer_InactivePeer ensures inactive peers are skipped
// silently (return nil, nil).
func TestRefresher_FetchPeer_InactivePeer(t *testing.T) {
	peer := &transactionpb.PeerBankFull{BankCode: "222", BaseUrl: "http://nope", Active: false}
	peerAdmin := &fakePeerBankAdminClient{
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": peer},
	}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	out, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil for inactive peer, got %+v", out)
	}
}

// TestRefresher_FetchPeer_ResolveError surfaces a resolve RPC error.
func TestRefresher_FetchPeer_ResolveError(t *testing.T) {
	peerAdmin := &fakePeerBankAdminClient{resolveErr: errors.New("rpc fail")}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	_, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRefresher_FetchPeer_BadStatusCode surfaces a non-200 from the peer.
func TestRefresher_FetchPeer_BadStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	t.Cleanup(server.Close)
	peer := &transactionpb.PeerBankFull{BankCode: "222", BaseUrl: server.URL, Active: true, ApiTokenPlaintext: "x"}
	peerAdmin := &fakePeerBankAdminClient{
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": peer},
	}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	_, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRefresher_FetchPeer_BadJSON surfaces a JSON-parse error from the peer.
func TestRefresher_FetchPeer_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(server.Close)
	peer := &transactionpb.PeerBankFull{BankCode: "222", BaseUrl: server.URL, Active: true, ApiTokenPlaintext: "x"}
	peerAdmin := &fakePeerBankAdminClient{
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": peer},
	}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	_, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRefresher_FetchPeer_NilFullPeer covers the resolve-returns-nil edge.
func TestRefresher_FetchPeer_NilFullPeer(t *testing.T) {
	peerAdmin := &fakePeerBankAdminClient{
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": nil},
	}
	r := NewRefresher(New(), &fakeOTCLister{}, peerAdmin, "111", time.Minute)
	out, err := r.fetchPeer(context.Background(), &transactionpb.PeerBank{BankCode: "222"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil for nil full peer")
	}
}

// TestRefresher_Refresh_WithReachablePeer wires a single peer through an
// httptest server and verifies both local and remote offers land in cache.
func TestRefresher_Refresh_WithReachablePeer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(contractsitx.PublicStocksResponse{
			Stocks: []contractsitx.PublicStock{
				{
					OwnerID:       contractsitx.ForeignBankId{RoutingNumber: 222, ID: "0"},
					Ticker:        "GOOG",
					Amount:        12,
					PricePerStock: decimal.NewFromInt(2500),
					Currency:      "USD",
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	peer := &transactionpb.PeerBankFull{BankCode: "222", BaseUrl: server.URL, Active: true, ApiTokenPlaintext: "x"}
	peerAdmin := &fakePeerBankAdminClient{
		listResp: &transactionpb.ListPeerBanksResponse{
			PeerBanks: []*transactionpb.PeerBank{{BankCode: "222"}},
		},
		resolveByMap: map[string]*transactionpb.PeerBankFull{"222": peer},
	}
	cache := New()
	uid := uint64(7)
	holding := model.Holding{
		OwnerID: &uid, UserFirstName: "X", UserLastName: "Y",
		SecurityType: "stock", Ticker: "AAPL",
		PublicQuantity: 1, AveragePrice: decimal.NewFromInt(150),
	}
	r := NewRefresher(cache, &fakeOTCLister{rows: []model.Holding{holding}}, peerAdmin, "111", time.Minute)
	r.refresh(context.Background())
	snap := cache.Get()
	if len(snap.Offers) != 2 {
		t.Fatalf("expected 2 offers (1 local, 1 remote), got %d", len(snap.Offers))
	}
	if snap.PeersTotal != 1 || snap.PeersReached != 1 {
		t.Errorf("peers total/reached = %d/%d", snap.PeersTotal, snap.PeersReached)
	}
}

// TestSetForTest verifies the test seam writes through to internal state.
func TestSetForTest(t *testing.T) {
	c := New()
	SetForTest(c, Snapshot{
		Offers:       []Offer{{Kind: "remote", Ticker: "X"}},
		LastRefresh:  time.Now(),
		PeersTotal:   2,
		PeersReached: 1,
	})
	got := c.Get()
	if len(got.Offers) != 1 || got.Offers[0].Ticker != "X" {
		t.Errorf("got %+v", got.Offers)
	}
	if got.PeersTotal != 2 || got.PeersReached != 1 {
		t.Errorf("peers totals wrong")
	}
}

// TestRefresher_Run_StopsOnContextCancel verifies the goroutine returns when
// context is cancelled — the test must not hang.
func TestRefresher_Run_StopsOnContextCancel(t *testing.T) {
	cache := New()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{PeerBanks: nil}}
	r := NewRefresher(cache, &fakeOTCLister{}, peerAdmin, "111", 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

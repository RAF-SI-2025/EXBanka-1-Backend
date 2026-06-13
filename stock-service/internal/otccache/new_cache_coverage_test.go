package otccache

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Helpers — fakes not already defined in option_cache_test.go
// ---------------------------------------------------------------------------

// listingFaker is a fake OptionOfferLister that returns configurable offers.
type listingFaker struct {
	offers []model.OTCOffer
	err    error
}

func (f *listingFaker) ListOpenForCache(_ int) ([]model.OTCOffer, error) {
	return f.offers, f.err
}

// fakeCurrencyResolver is a fake OptionCurrencyResolver.
type fakeCurrencyResolver struct {
	currency string
	err      error
}

func (f *fakeCurrencyResolver) CurrencyForStock(_ uint64) (string, error) {
	return f.currency, f.err
}

type cacheTestErr struct{ msg string }

func (e *cacheTestErr) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// SetOptionForTest
// ---------------------------------------------------------------------------

func TestSetOptionForTest_Seeds(t *testing.T) {
	c := NewOptionCache()
	snap := OptionSnapshot{
		PeersTotal:   3,
		PeersReached: 2,
		Offers:       []OptionOffer{{Ticker: "AAPL", Kind: "local"}},
	}
	SetOptionForTest(c, snap)
	got := c.Get()
	if got.PeersTotal != 3 || got.PeersReached != 2 {
		t.Errorf("peers total/reached = %d/%d, want 3/2", got.PeersTotal, got.PeersReached)
	}
	if len(got.Offers) != 1 || got.Offers[0].Ticker != "AAPL" {
		t.Errorf("offers = %+v", got.Offers)
	}
}

// ---------------------------------------------------------------------------
// WithAggregateBids / WithMirror — builder methods
// ---------------------------------------------------------------------------

func TestWithAggregateBids_WiresField(t *testing.T) {
	c := NewOptionCache()
	r := NewOptionRefresher(c, &fakeOptionLister{}, nil, nil, nil, "111", 111, time.Minute)
	if r.aggregateBids != nil {
		t.Fatal("aggregateBids should be nil before wiring")
	}
	called := false
	out := r.WithAggregateBids(func(_ []uint64) (map[uint64]OfferAggregate, error) {
		called = true
		return nil, nil
	})
	if out != r {
		t.Error("WithAggregateBids should return the same refresher")
	}
	if r.aggregateBids == nil {
		t.Fatal("aggregateBids should be set after WithAggregateBids")
	}
	_, _ = r.aggregateBids(nil)
	if !called {
		t.Error("expected the injected function to be called")
	}
}

func TestWithMirror_WiresField(t *testing.T) {
	c := NewOptionCache()
	r := NewOptionRefresher(c, &fakeOptionLister{}, nil, nil, nil, "111", 111, time.Minute)
	if r.mirror != nil {
		t.Fatal("mirror should be nil before wiring")
	}
	m := &fakeShellMirror{}
	out := r.WithMirror(m)
	if out != r {
		t.Error("WithMirror should return the same refresher")
	}
	if r.mirror != m {
		t.Errorf("mirror = %v, want %v", r.mirror, m)
	}
}

// ---------------------------------------------------------------------------
// Run — stops on context cancellation
// ---------------------------------------------------------------------------

func TestOptionRefresher_Run_StopsOnContextCancel(t *testing.T) {
	c := NewOptionCache()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{}}
	r := NewOptionRefresher(c, &fakeOptionLister{}, nil, peerAdmin,
		&fakePathEgressClient{byPath: map[string]*transactionpb.ProxyToPeerResponse{}},
		"111", 111, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop within 2 s after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Refresh — exported single-cycle wrapper
// ---------------------------------------------------------------------------

func TestOptionRefresher_Refresh_UpdatesLastRefresh(t *testing.T) {
	c := NewOptionCache()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{}}
	r := NewOptionRefresher(c, &fakeOptionLister{}, nil, peerAdmin,
		&fakePathEgressClient{byPath: map[string]*transactionpb.ProxyToPeerResponse{}},
		"111", 111, time.Minute)
	before := time.Now()
	r.Refresh(context.Background())
	snap := c.Get()
	if snap.LastRefresh.Before(before) {
		t.Errorf("LastRefresh = %v, expected >= %v", snap.LastRefresh, before)
	}
}

// ---------------------------------------------------------------------------
// fetchLocal with aggregateBids — enriches offers with bid aggregates
// ---------------------------------------------------------------------------

func TestOptionRefresher_FetchLocal_WithAggregateBids_SellInitiated(t *testing.T) {
	ownerID := uint64(42)
	offers := []model.OTCOffer{
		{
			ID:                          7,
			StockID:                     1,
			Ticker:                      "AAPL",
			Direction:                   model.OTCDirectionSellInitiated,
			InitiatorOwnerType:          model.OwnerClient,
			InitiatorOwnerID:            &ownerID,
			Quantity:                    decimal.NewFromInt(100),
			Status:                      model.OTCOfferStatusOpen,
			LastModifiedByPrincipalType: "client",
		},
	}
	aggCalled := false
	agg := func(_ []uint64) (map[uint64]OfferAggregate, error) {
		aggCalled = true
		return map[uint64]OfferAggregate{
			7: {BestBid: "10.50", BestAsk: "11.00", ActiveCount: 2},
		}, nil
	}

	c := NewOptionCache()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{}}
	r := NewOptionRefresher(c, &listingFaker{offers: offers}, nil, peerAdmin,
		&fakePathEgressClient{byPath: map[string]*transactionpb.ProxyToPeerResponse{}},
		"111", 111, time.Minute).WithAggregateBids(agg)

	r.Refresh(context.Background())
	if !aggCalled {
		t.Fatal("aggregateBids function was not called")
	}
	snap := c.Get()
	if len(snap.Offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(snap.Offers))
	}
	o := snap.Offers[0]
	if o.Ticker != "AAPL" {
		t.Errorf("ticker = %q, want AAPL", o.Ticker)
	}
	if o.BestBid != "10.50" {
		t.Errorf("BestBid = %q, want 10.50", o.BestBid)
	}
	if o.ActiveChainsCount != 2 {
		t.Errorf("ActiveChainsCount = %d, want 2", o.ActiveChainsCount)
	}
	if o.BestAsk != "" {
		t.Errorf("BestAsk should be empty for sell_initiated, got %q", o.BestAsk)
	}
}

func TestOptionRefresher_FetchLocal_WithAggregateBids_BuyInitiated(t *testing.T) {
	ownerID := uint64(42)
	offers := []model.OTCOffer{
		{
			ID:                          8,
			StockID:                     1,
			Ticker:                      "MSFT",
			Direction:                   model.OTCDirectionBuyInitiated,
			InitiatorOwnerType:          model.OwnerClient,
			InitiatorOwnerID:            &ownerID,
			Quantity:                    decimal.NewFromInt(50),
			Status:                      model.OTCOfferStatusOpen,
			LastModifiedByPrincipalType: "client",
		},
	}
	agg := func(_ []uint64) (map[uint64]OfferAggregate, error) {
		return map[uint64]OfferAggregate{
			8: {BestBid: "5.00", BestAsk: "6.00", ActiveCount: 1},
		}, nil
	}

	c := NewOptionCache()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{}}
	r := NewOptionRefresher(c, &listingFaker{offers: offers}, nil, peerAdmin,
		&fakePathEgressClient{byPath: map[string]*transactionpb.ProxyToPeerResponse{}},
		"111", 111, time.Minute).WithAggregateBids(agg)

	r.Refresh(context.Background())
	snap := c.Get()
	if len(snap.Offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(snap.Offers))
	}
	o := snap.Offers[0]
	if o.BestAsk != "6.00" {
		t.Errorf("BestAsk = %q, want 6.00", o.BestAsk)
	}
	if o.BestBid != "" {
		t.Errorf("BestBid should be empty for buy_initiated, got %q", o.BestBid)
	}
}

func TestOptionRefresher_FetchLocal_AggregateError_FallsBackToEmpty(t *testing.T) {
	ownerID := uint64(42)
	offers := []model.OTCOffer{
		{
			ID:                          9,
			StockID:                     1,
			Ticker:                      "TSLA",
			Direction:                   model.OTCDirectionSellInitiated,
			InitiatorOwnerType:          model.OwnerClient,
			InitiatorOwnerID:            &ownerID,
			Quantity:                    decimal.NewFromInt(10),
			Status:                      model.OTCOfferStatusOpen,
			LastModifiedByPrincipalType: "client",
		},
	}
	agg := func(_ []uint64) (map[uint64]OfferAggregate, error) {
		return nil, &cacheTestErr{"aggregate DB error"}
	}

	c := NewOptionCache()
	peerAdmin := &fakePeerBankAdminClient{listResp: &transactionpb.ListPeerBanksResponse{}}
	r := NewOptionRefresher(c, &listingFaker{offers: offers}, nil, peerAdmin,
		&fakePathEgressClient{byPath: map[string]*transactionpb.ProxyToPeerResponse{}},
		"111", 111, time.Minute).WithAggregateBids(agg)

	r.Refresh(context.Background())
	snap := c.Get()
	if len(snap.Offers) != 1 {
		t.Fatalf("expected 1 offer despite agg error, got %d", len(snap.Offers))
	}
	if snap.Offers[0].BestBid != "" || snap.Offers[0].BestAsk != "" {
		t.Error("expected empty best_bid/best_ask when aggregation failed")
	}
}

// ---------------------------------------------------------------------------
// resolveCurrency — all four branches
// ---------------------------------------------------------------------------

func TestResolveCurrency_NilResolver_ReturnsUSD(t *testing.T) {
	r := &OptionRefresher{}
	if got := r.resolveCurrency(1); got != "USD" {
		t.Errorf("got %q, want USD", got)
	}
}

func TestResolveCurrency_ResolverError_ReturnsUSD(t *testing.T) {
	r := &OptionRefresher{currency: &fakeCurrencyResolver{err: &cacheTestErr{"error"}}}
	if got := r.resolveCurrency(1); got != "USD" {
		t.Errorf("got %q, want USD", got)
	}
}

func TestResolveCurrency_ResolverEmpty_ReturnsUSD(t *testing.T) {
	r := &OptionRefresher{currency: &fakeCurrencyResolver{currency: ""}}
	if got := r.resolveCurrency(1); got != "USD" {
		t.Errorf("got %q, want USD (empty string fallback)", got)
	}
}

func TestResolveCurrency_ResolverSuccess(t *testing.T) {
	r := &OptionRefresher{currency: &fakeCurrencyResolver{currency: "EUR"}}
	if got := r.resolveCurrency(2); got != "EUR" {
		t.Errorf("got %q, want EUR", got)
	}
}

// ---------------------------------------------------------------------------
// composeSellerID — all three branches
// ---------------------------------------------------------------------------

func TestComposeSellerID_BankOwner(t *testing.T) {
	o := &model.OTCOffer{InitiatorOwnerType: model.OwnerBank}
	if got := composeSellerID(o); got != "bank" {
		t.Errorf("got %q, want bank", got)
	}
}

func TestComposeSellerID_NilClientID(t *testing.T) {
	o := &model.OTCOffer{InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: nil}
	if got := composeSellerID(o); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestComposeSellerID_ClientWithID(t *testing.T) {
	id := uint64(42)
	o := &model.OTCOffer{InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &id}
	if got := composeSellerID(o); got != "client-42" {
		t.Errorf("got %q, want client-42", got)
	}
}

// ---------------------------------------------------------------------------
// peerRoutingOf — RoutingNumber field vs BankCode parse fallback
// ---------------------------------------------------------------------------

func TestPeerRoutingOf_UsesRoutingNumberField(t *testing.T) {
	peer := &transactionpb.PeerBank{RoutingNumber: 333, BankCode: "999"}
	if got := peerRoutingOf(peer); got != 333 {
		t.Errorf("got %d, want 333", got)
	}
}

func TestPeerRoutingOf_FallsBackToBankCodeParse(t *testing.T) {
	peer := &transactionpb.PeerBank{BankCode: "444"}
	if got := peerRoutingOf(peer); got != 444 {
		t.Errorf("got %d, want 444 (parsed from BankCode)", got)
	}
}

// ---------------------------------------------------------------------------
// fetchPeerStocks — non-OK status and unmarshal error
// ---------------------------------------------------------------------------

func TestOptionRefresher_FetchPeerStocks_NonOKStatus(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	eg := &fakePathEgressClient{
		byPath: map[string]*transactionpb.ProxyToPeerResponse{
			"/public-stock": {StatusCode: http.StatusServiceUnavailable, Body: []byte("down")},
		},
	}
	r := &OptionRefresher{egress: eg}
	_, err := r.fetchPeerStocks(context.Background(),
		&transactionpb.PeerBank{BankCode: "222", RoutingNumber: 222})
	if err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestOptionRefresher_FetchPeerStocks_UnmarshalError(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	eg := &fakePathEgressClient{
		byPath: map[string]*transactionpb.ProxyToPeerResponse{
			"/public-stock": {StatusCode: http.StatusOK, Body: []byte("not-valid-json")},
		},
	}
	r := &OptionRefresher{egress: eg}
	_, err := r.fetchPeerStocks(context.Background(),
		&transactionpb.PeerBank{BankCode: "222", RoutingNumber: 222})
	if err == nil {
		t.Fatal("expected error on invalid JSON body")
	}
}

// ---------------------------------------------------------------------------
// buildAndMirrorRemoteStockShells — own-routing skip, own-seller skip, empty ID
// ---------------------------------------------------------------------------

func TestBuildAndMirrorRemoteStockShells_SkipsOwnRouting(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake, ownRouting: 111}
	out := r.buildAndMirrorRemoteStockShells("111", 111, nil)
	if out != nil {
		t.Errorf("expected nil for own-routing peer, got %v", out)
	}
	if len(fake.upserts) != 0 {
		t.Error("expected no upserts for own-routing peer")
	}
}

func TestBuildAndMirrorRemoteStockShells_SkipsOwnSellerRouting(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake, ownRouting: 111}
	stocks := []sitx.PublicStock{{
		Stock: sitx.StockDescription{Ticker: "AAPL"},
		Sellers: []sitx.PublicSeller{
			{Seller: sitx.ForeignBankId{RoutingNumber: 111, ID: "client-5"}, Amount: 100},
			{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: "client-7"}, Amount: 50},
		},
	}}
	out := r.buildAndMirrorRemoteStockShells("222", 222, stocks)
	if len(out) != 1 {
		t.Fatalf("expected 1 shell (own-bank seller filtered), got %d", len(out))
	}
	if out[0].SellerID != "client-7" {
		t.Errorf("sellerID = %q, want client-7", out[0].SellerID)
	}
}

func TestBuildAndMirrorRemoteStockShells_SkipsEmptySellerID(t *testing.T) {
	prev := model.OwnRouting()
	model.SetOwnRouting("111")
	t.Cleanup(func() { model.SetOwnRouting(strconv.FormatInt(prev, 10)) })

	fake := &fakeShellMirror{}
	r := &OptionRefresher{mirror: fake, ownRouting: 111}
	stocks := []sitx.PublicStock{{
		Stock: sitx.StockDescription{Ticker: "NVDA"},
		Sellers: []sitx.PublicSeller{
			{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: ""}, Amount: 10},
			{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: "client-3"}, Amount: 20},
		},
	}}
	out := r.buildAndMirrorRemoteStockShells("222", 222, stocks)
	if len(out) != 1 {
		t.Fatalf("expected 1 shell (empty-ID seller filtered), got %d", len(out))
	}
	if out[0].Amount != 20 {
		t.Errorf("amount = %d, want 20", out[0].Amount)
	}
}

// Package handler — freshness guard + shell-bid tests for /public-stock shell offers.
// Shell offers are termless rows synthesized from a peer's /public-stock
// endpoint. Because the mirror can lag a refresh cycle, openRemoteNegotiation
// re-fetches the LIVE /public-stock before dispatching to avoid a doomed bid.
// These tests exercise that guard plus the shell-bid currency derivation path:
//   - stale listing (seller/ticker absent) → FailedPrecondition, no dispatch,
//   - live listing (seller/ticker present)  → guard passes, dispatch fires once,
//   - nil-currency shell bid → currency derived from bidder account, offer carries it.
package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/stock-service/internal/model"
)

// seedShellRemoteOffer inserts a termless shell OTCOffer row (ticker AAPL,
// seller {222,"client-5"}). Shells carry no preset terms — the buyer derives
// the currency from their bound account when bidding (Bug-3 fix).
func seedShellRemoteOffer(t *testing.T, db *gorm.DB) uint64 {
	t.Helper()
	nid := "shell-offer-1"
	bankCode := "222"
	sellerID := "client-5"
	o := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &nid,
		InitiatorBankCode:           &bankCode,
		RemoteSellerID:              &sellerID, // shell: synthesized from /public-stock, no preset terms
		InitiatorOwnerType:          model.OwnerBank,
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10), // shells are termless (buyer proposes terms)
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "system",
		LastModifiedByPrincipalID:   0,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed shell remote offer: %v", err)
	}
	return o.ID
}

// TestOpenNegotiation_ShellFreshness_BlocksWhenSellerGone asserts that a bid on a
// shell offer is rejected with FailedPrecondition (and no CreateNegotiation
// dispatch) when the peer's live /public-stock no longer lists the seller+ticker.
// The freshness guard uses PublicStock (not Proxy) so it cannot accidentally
// build /negotiations///public-stock on the peer (Bug-2 fix).
func TestOpenNegotiation_ShellFreshness_BlocksWhenSellerGone(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		routing:           222,
		foreignID:         "neg-shell",
		publicStockResp:   []byte("[]"), // empty array — seller+ticker not present
		publicStockStatus: 200,
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(7)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedShellRemoteOffer(t, db)

	_, err := h.OpenNegotiation(context.Background(), openReq(parentID, 7, "client"))
	if err == nil {
		t.Fatal("expected FailedPrecondition from stale shell guard, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code: got %v, want FailedPrecondition", status.Code(err))
	}
	// PublicStock must have been called exactly once (real guard path).
	if len(dispatcher.publicStockCalls) != 1 {
		t.Errorf("PublicStock calls: got %d, want 1", len(dispatcher.publicStockCalls))
	}
	// CreateNegotiation must NOT have fired (bid was blocked before dispatch).
	if dispatcher.calls != 0 {
		t.Errorf("CreateNegotiation calls: got %d, want 0", dispatcher.calls)
	}
}

// TestOpenNegotiation_ShellFreshness_PassesWhenSellerLive asserts that a bid on a
// shell offer proceeds (guard passes) when the peer's live /public-stock still
// lists the seller+ticker, and that CreateNegotiation is dispatched exactly once.
// Guard uses PublicStock (Bug-2 fix); nil-currency shell derives currency from
// account (Bug-3 fix).
func TestOpenNegotiation_ShellFreshness_PassesWhenSellerLive(t *testing.T) {
	// Build a /public-stock response that lists AAPL with seller "client-5" at routing 222.
	liveResp, _ := json.Marshal(contractsitx.PublicStocksResponse{
		{
			Stock: contractsitx.StockDescription{Ticker: "AAPL"},
			Sellers: []contractsitx.PublicSeller{
				{Seller: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-5"}, Amount: 10},
			},
		},
	})
	dispatcher := &fakePeerDispatcher{
		routing:           222,
		foreignID:         "neg-shell-live",
		publicStockResp:   liveResp,
		publicStockStatus: 200,
	}
	// Bidder is client-7 with an active USD account; currency is derived from the account (Bug-3).
	accounts := &fakeOTCAccountClient{acct: usdAccount(7)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedShellRemoteOffer(t, db)

	_, err := h.OpenNegotiation(context.Background(), openReq(parentID, 7, "client"))
	if err != nil {
		t.Fatalf("expected success with live seller, got: %v", err)
	}
	// PublicStock was called (real guard path — not Proxy).
	if len(dispatcher.publicStockCalls) != 1 {
		t.Errorf("PublicStock calls: got %d, want 1", len(dispatcher.publicStockCalls))
	}
	// Freshness guard passed → CreateNegotiation dispatched exactly once.
	if dispatcher.calls != 1 {
		t.Errorf("CreateNegotiation calls: got %d, want 1", dispatcher.calls)
	}
}

// TestOpenNegotiation_ShellBid_CurrencyDerivedFromAccount verifies that a bid on a
// nil-currency shell offer succeeds and carries the bidder account's currency in
// both pricePerUnit and premium of the composed SI-TX OtcOffer.
// This exercises the Bug-3 fix: shells have nil strike/premium currencies; the
// handler must derive currency from the bound account instead of rejecting.
func TestOpenNegotiation_ShellBid_CurrencyDerivedFromAccount(t *testing.T) {
	// Build a live /public-stock response (freshness guard passes).
	liveResp, _ := json.Marshal(contractsitx.PublicStocksResponse{
		{
			Stock: contractsitx.StockDescription{Ticker: "AAPL"},
			Sellers: []contractsitx.PublicSeller{
				{Seller: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-5"}, Amount: 10},
			},
		},
	})
	dispatcher := &fakePeerDispatcher{
		routing:           222,
		foreignID:         "neg-shell-eur",
		publicStockResp:   liveResp,
		publicStockStatus: 200,
	}
	// Bidder is client-7 with an active EUR account. The shell has nil currencies —
	// the handler must derive premiumCurrency and strikeCurrency from this account.
	eurAccount := eurClientAccount(7)
	accounts := &fakeOTCAccountClient{acct: eurAccount}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedShellRemoteOffer(t, db)

	_, err := h.OpenNegotiation(context.Background(), openReq(parentID, 7, "client"))
	if err != nil {
		t.Fatalf("expected success for EUR-account shell bid, got: %v", err)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("CreateNegotiation calls: got %d, want 1", dispatcher.calls)
	}
	// The composed SI-TX OtcOffer MUST carry EUR in both pricePerUnit and premium.
	ppu, _ := dispatcher.gotOffer["pricePerUnit"].(map[string]any)
	if ppu == nil || ppu["currency"] != "EUR" {
		t.Errorf("pricePerUnit.currency = %v, want EUR (derived from bidder account)", ppu)
	}
	prem, _ := dispatcher.gotOffer["premium"].(map[string]any)
	if prem == nil || prem["currency"] != "EUR" {
		t.Errorf("premium.currency = %v, want EUR (derived from bidder account)", prem)
	}
}

// eurClientAccount returns an active EUR account owned by the given client ID.
func eurClientAccount(ownerID uint64) *accountpb.AccountResponse {
	return &accountpb.AccountResponse{
		Id:            5002,
		OwnerId:       ownerID,
		AccountNumber: "111-0000000002-EUR",
		CurrencyCode:  "EUR",
		Status:        "active",
	}
}

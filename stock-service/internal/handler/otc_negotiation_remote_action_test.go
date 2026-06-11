// Package handler — cross-bank (REMOTE) dispatch tests for the negotiation
// ACTIONS (counter / accept / reject / cancel) on a folded-in REMOTE chain
// (Unified OTC SP-2b Task 4). These exercise the REMOTE branch of each action
// RPC: when :nid resolves to a peer-hosted chain, the action is proxied to the
// counterparty bank over SI-TX and the local mirror is updated to match.
//
//   - counter PUTs the new terms + mirrors the offer JSON,
//   - accept GETs /accept + flips the mirror to accepted + cascade-cancels
//     siblings (a fake sibling asserts the DELETE proxy fired),
//   - reject/cancel DELETE + mirror cancelled,
//   - a NON-party caller → NotFound (existence not leaked),
//   - the composed counter offer body uses JSON-NUMBER amounts (SI-TX §2.5).
package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// seedRemoteNeg inserts a folded-in REMOTE OTCNegotiation chain into the unified
// table. We host the BUYER (buyerRouting == own=111); the seller is on peer 222.
// parentNative groups the chain for cascade-cancel; pass "" for a free-form
// chain. Returns the surrogate id.
func seedRemoteNeg(t *testing.T, db *gorm.DB, foreignID, buyerID, sellerID, parentNative string) uint64 {
	t.Helper()
	offer := contractsitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-07-01",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: sellerID},
	}
	offerJSON, _ := json.Marshal(offer)
	var parentRoutingPtr *int64
	var parentNativePtr *string
	if parentNative != "" {
		pr := int64(222)
		pn := parentNative
		parentRoutingPtr = &pr
		parentNativePtr = &pn
	}
	// peerRouting=222 (the issuing/seller bank), buyer hosted locally (111).
	row := buildRemoteNeg(
		222, foreignID, offer, string(offerJSON),
		111, buyerID, // buyer hosted by us
		222, sellerID, // seller on the peer
		parentRoutingPtr, parentNativePtr, "ongoing",
	)
	if err := repository.NewOTCNegotiationRepository(db).UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed remote neg %q: %v", foreignID, err)
	}
	got, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, foreignID)
	if err != nil {
		t.Fatalf("read seeded remote neg %q: %v", foreignID, err)
	}
	return got.ID
}

// seedBankHostedRemoteNeg inserts a folded-in REMOTE chain where the side WE
// host (the BUYER, routing 111) is BANK-owned: its wire id is "employee-<N>"
// and the row carries ActingEmployeeID = the ORIGINATOR employee. The seller is
// a client on peer 222. This is the chain shape produced by openRemoteNegotiation
// for a bank bid (SP-3 Task 4). Returns the surrogate id.
func seedBankHostedRemoteNeg(t *testing.T, db *gorm.DB, foreignID string, originatorEmployeeID uint64, sellerID, parentNative string) uint64 {
	t.Helper()
	buyerID := "employee-" + strconv.FormatUint(originatorEmployeeID, 10)
	offer := contractsitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-07-01",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: sellerID},
	}
	offerJSON, _ := json.Marshal(offer)
	var parentRoutingPtr *int64
	var parentNativePtr *string
	if parentNative != "" {
		pr := int64(222)
		pn := parentNative
		parentRoutingPtr = &pr
		parentNativePtr = &pn
	}
	row := buildRemoteNeg(
		222, foreignID, offer, string(offerJSON),
		111, buyerID, // bank buyer hosted by us (employee-<N>)
		222, sellerID, // client seller on the peer
		parentRoutingPtr, parentNativePtr, "ongoing",
	)
	emp := originatorEmployeeID
	row.ActingEmployeeID = &emp // stamp the stable wire-identity originator
	if err := repository.NewOTCNegotiationRepository(db).UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed bank-hosted remote neg %q: %v", foreignID, err)
	}
	got, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, foreignID)
	if err != nil {
		t.Fatalf("read seeded bank-hosted remote neg %q: %v", foreignID, err)
	}
	return got.ID
}

// bankCounterReq builds a CounterNegotiationRequest for a caller acting AS THE
// BANK (caller_owner_type "bank", caller_owner_id 0, acting employee = the
// employee PERFORMING the counter, which may differ from the originator).
func bankCounterReq(nid, performingEmployeeID uint64) *stockpb.CounterNegotiationRequest {
	return &stockpb.CounterNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "bank",
		CallerOwnerId:       0,
		Quantity:            "10",
		StrikePrice:         "155.5",
		Premium:             "22.5",
		SettlementDate:      "2026-07-01",
		ActingPrincipalType: "employee",
		ActingPrincipalId:   performingEmployeeID,
		ActingEmployeeId:    performingEmployeeID,
	}
}

func counterReq(nid, callerID uint64) *stockpb.CounterNegotiationRequest {
	return &stockpb.CounterNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       callerID,
		Quantity:            "10",
		StrikePrice:         "155.5",
		Premium:             "22.5",
		SettlementDate:      "2026-07-01",
		ActingPrincipalType: "client",
		ActingPrincipalId:   callerID,
	}
}

func TestCounterNegotiation_RemoteChain_PutsAndMirrors(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-1", "client-9", "client-77", "")

	resp, err := h.CounterNegotiation(context.Background(), counterReq(nid, 9))
	if err != nil {
		t.Fatalf("CounterNegotiation: %v", err)
	}

	// A single PUT to /negotiations/222/neg-1 on the counterparty (seller=222).
	if len(dispatcher.proxyCalls) != 1 {
		t.Fatalf("proxy called %d times, want 1", len(dispatcher.proxyCalls))
	}
	pc := dispatcher.proxyCalls[0]
	if pc.method != "PUT" || pc.subpath != "" {
		t.Errorf("proxy method/subpath: got %q %q, want PUT \"\"", pc.method, pc.subpath)
	}
	if pc.peerBankCode != "222" || pc.rid != "222" || pc.foreignID != "neg-1" {
		t.Errorf("proxy target: got peer=%q rid=%q fid=%q", pc.peerBankCode, pc.rid, pc.foreignID)
	}

	// The composed offer body uses JSON-NUMBER amounts (SI-TX §2.5).
	wire := string(pc.body)
	if strings.Contains(wire, `"amount":"`) {
		t.Errorf("counter body has quoted amount (string), want bare number; wire: %s", wire)
	}
	var parsed map[string]any
	if err := json.Unmarshal(pc.body, &parsed); err != nil {
		t.Fatalf("unmarshal counter body: %v", err)
	}
	ppu, _ := parsed["pricePerUnit"].(map[string]any)
	if ppu == nil || ppu["amount"].(float64) != 155.5 {
		t.Errorf("pricePerUnit.amount: got %v, want 155.5", parsed["pricePerUnit"])
	}
	prem, _ := parsed["premium"].(map[string]any)
	if prem == nil || prem["amount"].(float64) != 22.5 {
		t.Errorf("premium.amount: got %v, want 22.5", parsed["premium"])
	}

	// The local mirror offer JSON was refreshed with the new strike/premium.
	mirror, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-1")
	var mo contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(remoteOfferJSONOf(mirror)), &mo)
	if !mo.PricePerStock.Equal(decimal.RequireFromString("155.5")) {
		t.Errorf("mirror strike: got %s, want 155.5", mo.PricePerStock)
	}
	if !mo.Premium.Equal(decimal.RequireFromString("22.5")) {
		t.Errorf("mirror premium: got %s, want 22.5", mo.Premium)
	}

	if resp.GetKind() != "remote" || resp.GetId() != nid {
		t.Errorf("response: kind=%q id=%d, want remote / %d", resp.GetKind(), resp.GetId(), nid)
	}
}

func TestAcceptNegotiation_RemoteChain_AcceptsFlipsAndCascades(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-1","status":"accepted"}`), status: 200},
			"DELETE ":     {resp: []byte(``), status: 204},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// The chain we accept + a sibling chain sharing the same lot key under the
	// same seller (client-77). The sibling's buyer is on a DIFFERENT bank (333)
	// so the cascade DELETE targets that bidder's bank.
	winNID := seedRemoteNeg(t, db, "neg-win", "client-9", "client-77", "lot-abc")
	sibNID := seedRemoteNeg(t, db, "neg-sib", "client-55", "client-77", "lot-abc")
	// Point the sibling's buyer routing at bank 333 (a different bidder bank).
	bumpSiblingBuyerRouting(t, db, sibNID, 333)

	req := &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       winNID,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	}
	resp, err := h.AcceptNegotiationChain(context.Background(), req)
	if err != nil {
		t.Fatalf("AcceptNegotiationChain: %v", err)
	}

	// The accept GET fired to the seller (222).
	var sawAccept bool
	var sawSiblingDelete bool
	for _, pc := range dispatcher.proxyCalls {
		if pc.method == "GET" && pc.subpath == "/accept" && pc.foreignID == "neg-win" && pc.peerBankCode == "222" {
			sawAccept = true
		}
		if pc.method == "DELETE" && pc.foreignID == "neg-sib" && pc.peerBankCode == "333" {
			sawSiblingDelete = true
		}
	}
	if !sawAccept {
		t.Errorf("expected GET /accept to seller 222; calls: %+v", dispatcher.proxyCalls)
	}
	if !sawSiblingDelete {
		t.Errorf("expected cascade DELETE to sibling bidder bank 333; calls: %+v", dispatcher.proxyCalls)
	}

	// The local mirror for the winning chain flipped to accepted.
	win, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-win")
	if win.Status != "accepted" {
		t.Errorf("winning mirror status: got %q, want accepted", win.Status)
	}
	// The sibling mirror flipped to cancelled.
	sib, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-sib")
	if sib.Status != "cancelled" {
		t.Errorf("sibling mirror status: got %q, want cancelled", sib.Status)
	}

	// The response carries parent_status=accepted + the cancelled sibling.
	if resp.GetParentStatus() != "accepted" {
		t.Errorf("parent_status: got %q, want accepted", resp.GetParentStatus())
	}
	if len(resp.GetCancelledSiblings()) != 1 {
		t.Fatalf("cancelled_siblings: got %d, want 1", len(resp.GetCancelledSiblings()))
	}
	if got := resp.GetCancelledSiblings()[0].GetId(); got != sibNID {
		t.Errorf("cancelled sibling id: got %d, want %d", got, sibNID)
	}
	// SP-2b T4 review fix #1 — the peer's transactionId is surfaced so the FE
	// can poll cross-bank settlement during the accept→contract-mirror window.
	if resp.GetCrossBankTransactionId() != "tx-1" {
		t.Errorf("cross_bank_transaction_id: got %q, want tx-1", resp.GetCrossBankTransactionId())
	}
}

// seedRemoteNegSellerLocal inserts a folded-in REMOTE chain for the inverse
// (Direction 2) topology: the SELLER is local to this bank (routing 111) and the
// BUYER is on peer 222. This is the shape CreateNegotiation persists for an
// inbound peer BID on one of our /public-stock listings. LastModifiedBy is the
// BUYER (so the local seller may accept the buyer's last-proposed terms without
// tripping the anti-self-accept guard). Returns the surrogate id.
func seedRemoteNegSellerLocal(t *testing.T, db *gorm.DB, foreignID, buyerID, sellerID string) uint64 {
	t.Helper()
	offer := contractsitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          5,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "EUR",
		Premium:         decimal.RequireFromString("8"),
		PremiumCurrency: "EUR",
		SettlementDate:  "2026-12-31",
		// The remote BUYER last proposed — the local seller is the one accepting.
		LastModifiedBy: contractsitx.ForeignBankId{RoutingNumber: 222, ID: buyerID},
	}
	offerJSON, _ := json.Marshal(offer)
	// peerRouting=222 (buyer's bank == counterparty == RoutingNumber for a
	// seller-hosted chain), buyer remote (222), seller local (111).
	row := buildRemoteNeg(
		222, foreignID, offer, string(offerJSON),
		222, buyerID, // buyer on the peer
		111, sellerID, // seller hosted by us
		nil, nil, "ongoing",
	)
	if err := repository.NewOTCNegotiationRepository(db).UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed seller-local remote neg %q: %v", foreignID, err)
	}
	got, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, foreignID)
	if err != nil {
		t.Fatalf("read seeded seller-local remote neg %q: %v", foreignID, err)
	}
	return got.ID
}

// TestAcceptNegotiation_RemoteChain_LocalSeller_ConsumesLocalListing covers the
// Direction-2 accept (our user is the SELLER; a peer bidder's bid is accepted).
// After the cross-bank contract forms, the LOCAL /public-stock listing the
// contract was written against MUST be consumed — otherwise the listing keeps
// advertising inventory already under contract (the offer-not-lowered bug). The
// termless /public-stock model carries no offer id on the wire, so the listing is
// resolved by the seller's (owner, ticker, sell_initiated) unique key.
func TestAcceptNegotiation_RemoteChain_LocalSeller_ConsumesLocalListing(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-d2","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(1)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// A LOCAL open option listing posted by the seller (client-1) for AAPL.
	offerRepo := repository.NewOTCOfferRepository(db)
	sellerID := uint64(1)
	listing := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &sellerID,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(8),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   sellerID,
		InitiatorAccountID:          17,
		Public:                      true,
		Local:                       true,
	}
	if err := offerRepo.Create(listing); err != nil {
		t.Fatalf("seed local listing: %v", err)
	}

	// A remote bid (buyer "2"@222) on our local seller (client-1).
	nid := seedRemoteNegSellerLocal(t, db, "neg-d2", "2", "client-1")

	// The local seller (client-1) accepts the remote buyer's bid.
	if _, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       1,
		ActingPrincipalType: "client",
		ActingPrincipalId:   1,
		AcceptorAccountId:   17,
	}); err != nil {
		t.Fatalf("AcceptNegotiationChain: %v", err)
	}

	// The LOCAL listing must now be consumed (removed from the marketplace).
	got, gerr := offerRepo.GetByID(listing.ID)
	if gerr != nil {
		t.Fatalf("reload listing: %v", gerr)
	}
	if got.Status != model.OTCOfferStatusConsumed {
		t.Errorf("local listing status after cross-bank accept: got %q, want %q",
			got.Status, model.OTCOfferStatusConsumed)
	}
}

// TestAcceptNegotiation_RemoteChain_LocalSeller_RejectsSecondAcceptAfterConsume
// proves the termless orphan-accept guard: once the first Direction-2 accept
// consumes the local listing, a second still-"ongoing" peer bid on the SAME
// listing must be rejected (FailedPrecondition) rather than forming another
// contract that over-commits the seller's shares.
func TestAcceptNegotiation_RemoteChain_LocalSeller_RejectsSecondAcceptAfterConsume(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-d2","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(1)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	offerRepo := repository.NewOTCOfferRepository(db)
	sellerID := uint64(1)
	listing := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &sellerID,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(8),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   sellerID,
		InitiatorAccountID:          17,
		Public:                      true,
		Local:                       true,
	}
	if err := offerRepo.Create(listing); err != nil {
		t.Fatalf("seed local listing: %v", err)
	}

	// Two distinct peer bids (buyers "2" and "3") on the same local listing.
	nid1 := seedRemoteNegSellerLocal(t, db, "neg-d2a", "2", "client-1")
	nid2 := seedRemoteNegSellerLocal(t, db, "neg-d2b", "3", "client-1")

	accept := func(nid uint64) error {
		_, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
			NegotiationId:       nid,
			CallerOwnerType:     "client",
			CallerOwnerId:       1,
			ActingPrincipalType: "client",
			ActingPrincipalId:   1,
			AcceptorAccountId:   17,
		})
		return err
	}

	// First accept consumes the listing.
	if err := accept(nid1); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Second accept on the now-consumed listing must be rejected.
	err := accept(nid2)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("second accept on consumed listing: got code %v, want FailedPrecondition", status.Code(err))
	}
}

// TestAcceptNegotiation_RemoteChain_CrossBankTxId asserts the peer's
// transactionId is parsed out of the /accept body and surfaced on the response
// (review fix #1). Uses a distinct id from the cascade test to keep them
// independent.
func TestAcceptNegotiation_RemoteChain_CrossBankTxId(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"TX-CB-123","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-cbtx", "client-9", "client-77", "")

	resp, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	})
	if err != nil {
		t.Fatalf("AcceptNegotiationChain: %v", err)
	}
	if resp.GetCrossBankTransactionId() != "TX-CB-123" {
		t.Errorf("cross_bank_transaction_id: got %q, want TX-CB-123", resp.GetCrossBankTransactionId())
	}
}

// TestAcceptNegotiation_RemoteChain_NonJSONPeerBody — a non-JSON peer /accept
// body must NOT fail the accept (the contract already formed on the peer); the
// cross_bank_transaction_id is simply left empty (review fix #1, defensive).
func TestAcceptNegotiation_RemoteChain_NonJSONPeerBody(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`not-json`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-badbody", "client-9", "client-77", "")

	resp, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	})
	if err != nil {
		t.Fatalf("accept must succeed despite a malformed peer body: %v", err)
	}
	if resp.GetCrossBankTransactionId() != "" {
		t.Errorf("cross_bank_transaction_id: got %q, want empty for a non-JSON peer body", resp.GetCrossBankTransactionId())
	}
	// The mirror still flipped to accepted.
	win, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-badbody")
	if win.Status != "accepted" {
		t.Errorf("mirror status: got %q, want accepted", win.Status)
	}
}

// TestAcceptNegotiation_LocalChain_NoCrossBankTxId — a LOCAL accept has no
// cross-bank transaction, so cross_bank_transaction_id is empty (review fix #1).
func TestAcceptNegotiation_LocalChain_NoCrossBankTxId(t *testing.T) {
	// No peer dispatch needed for the local path.
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// Seed a LOCAL listing (poster client-1) and open a negotiation (bidder
	// client-7) through the wired service, then the poster accepts the bidder's
	// terms — the canonical "poster accepts last-mover bidder" local flow.
	negRepo := repository.NewOTCNegotiationRepository(db)
	offerRepo := repository.NewOTCOfferRepository(db)
	posterID := uint64(1)
	listing := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &posterID,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   posterID,
		InitiatorAccountID:          100,
		Public:                      true,
	}
	if err := offerRepo.Create(listing); err != nil {
		t.Fatalf("seed local listing: %v", err)
	}
	bidderID := uint64(7)
	neg, err := h.negotiations.OpenNegotiation(context.Background(), service.OpenNegotiationInput{
		ParentOfferID:       listing.ID,
		BidderOwnerType:     model.OwnerClient,
		BidderOwnerID:       &bidderID,
		BidderAccountID:     200,
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(150.0),
		Premium:             decimal.NewFromFloat(5.0),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   bidderID,
	})
	if err != nil {
		t.Fatalf("open local negotiation: %v", err)
	}
	_ = negRepo // repo handle kept for symmetry with the remote tests

	// Poster (client-1) accepts the bidder's last-mover terms via the handler.
	resp, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       neg.ID,
		CallerOwnerType:     "client",
		CallerOwnerId:       posterID,
		ActingPrincipalType: "client",
		ActingPrincipalId:   posterID,
	})
	if err != nil {
		t.Fatalf("local AcceptNegotiationChain: %v", err)
	}
	if resp.GetCrossBankTransactionId() != "" {
		t.Errorf("cross_bank_transaction_id: got %q, want empty for a local accept", resp.GetCrossBankTransactionId())
	}
}

// bumpSiblingBuyerRouting points a seeded sibling's buyer routing at a different
// bidder bank so the cascade DELETE targets that bank (not our own 111).
func bumpSiblingBuyerRouting(t *testing.T, db *gorm.DB, id uint64, routing int64) {
	t.Helper()
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCNegotiation{}).
		Where("id = ?", id).
		Update("remote_buyer_routing", routing).Error; err != nil {
		t.Fatalf("bump sibling buyer routing: %v", err)
	}
}

func TestRejectNegotiation_RemoteChain_DeletesAndMirrors(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-r", "client-9", "client-77", "")

	resp, err := h.RejectNegotiation(context.Background(), &stockpb.RejectNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
	})
	if err != nil {
		t.Fatalf("RejectNegotiation: %v", err)
	}
	assertSingleDelete(t, dispatcher, "neg-r")
	assertMirrorCancelled(t, db, "neg-r")
	if resp.GetStatus() != "cancelled" {
		t.Errorf("response status: got %q, want cancelled", resp.GetStatus())
	}
}

func TestCancelNegotiation_RemoteChain_DeletesAndMirrors(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-c", "client-9", "client-77", "")

	resp, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
	})
	if err != nil {
		t.Fatalf("CancelNegotiation: %v", err)
	}
	assertSingleDelete(t, dispatcher, "neg-c")
	assertMirrorCancelled(t, db, "neg-c")
	if resp.GetStatus() != "cancelled" {
		t.Errorf("response status: got %q, want cancelled", resp.GetStatus())
	}
}

func assertSingleDelete(t *testing.T, d *fakePeerDispatcher, foreignID string) {
	t.Helper()
	if len(d.proxyCalls) != 1 {
		t.Fatalf("proxy called %d times, want 1; calls: %+v", len(d.proxyCalls), d.proxyCalls)
	}
	pc := d.proxyCalls[0]
	if pc.method != "DELETE" || pc.subpath != "" || pc.foreignID != foreignID || pc.peerBankCode != "222" {
		t.Errorf("proxy call: got %+v, want DELETE \"\" %s on 222", pc, foreignID)
	}
}

func assertMirrorCancelled(t *testing.T, db *gorm.DB, foreignID string) {
	t.Helper()
	m, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, foreignID)
	if err != nil {
		t.Fatalf("read mirror %q: %v", foreignID, err)
	}
	if m.Status != "cancelled" {
		t.Errorf("mirror status: got %q, want cancelled", m.Status)
	}
}

func TestRemoteNegAction_NonParty_NotFound(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	// We host buyer client-9; caller client-42 is NOT a party.
	nid := seedRemoteNeg(t, db, "neg-x", "client-9", "client-77", "")

	_, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       42, // not a party
		ActingPrincipalType: "client",
		ActingPrincipalId:   42,
	})
	if err == nil {
		t.Fatal("expected NotFound for a non-party caller, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("code: got %v, want NotFound", status.Code(err))
	}
	if len(dispatcher.proxyCalls) != 0 {
		t.Errorf("proxy should NOT fire for a non-party caller: %+v", dispatcher.proxyCalls)
	}
}

// --- SP-3 Task 5: bank-party authorization on a bank-hosted chain ----------

// TestCounterNegotiation_BankHostedChain_EmployeeMNotN is the wire-id-stability
// test: a bank-hosted chain was OPENED by employee 42 (ActingEmployeeID=42,
// RemoteBuyerID "employee-42"); a DIFFERENT employee 99 (acting AS THE BANK)
// performs a counter. The counter is authorized AND the composed wire buyerId +
// lastModifiedBy carry the ROW's stable "employee-42", NOT "employee-99".
func TestCounterNegotiation_BankHostedChain_EmployeeMNotN(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	// Originator employee 42; seller client-77 on the peer.
	nid := seedBankHostedRemoteNeg(t, db, "neg-bh-1", 42, "client-77", "")

	// Employee 99 (≠ 42), acting as the bank, performs the counter.
	resp, err := h.CounterNegotiation(context.Background(), bankCounterReq(nid, 99))
	if err != nil {
		t.Fatalf("bank counter (employee 99 ≠ originator 42): %v", err)
	}

	// A single PUT to the seller (222).
	if len(dispatcher.proxyCalls) != 1 {
		t.Fatalf("proxy called %d times, want 1", len(dispatcher.proxyCalls))
	}
	pc := dispatcher.proxyCalls[0]
	if pc.method != "PUT" || pc.subpath != "" || pc.peerBankCode != "222" || pc.foreignID != "neg-bh-1" {
		t.Errorf("proxy target: got %+v, want PUT \"\" on 222/neg-bh-1", pc)
	}

	var parsed map[string]any
	if err := json.Unmarshal(pc.body, &parsed); err != nil {
		t.Fatalf("unmarshal counter body: %v", err)
	}
	// STABILITY: the buyer (the side we host) carries the ROW's employee-42, not
	// the performing employee 99.
	buyer, _ := parsed["buyerId"].(map[string]any)
	if buyer == nil || buyer["id"] != "employee-42" {
		t.Errorf("wire buyerId.id: got %v, want employee-42 (the ROW's stable id, NOT employee-99)", parsed["buyerId"])
	}
	// lastModifiedBy must also be the stable employee-42, NOT employee-99.
	lastBy, _ := parsed["lastModifiedBy"].(map[string]any)
	if lastBy == nil || lastBy["id"] != "employee-42" {
		t.Errorf("wire lastModifiedBy.id: got %v, want employee-42 (NOT the performing employee-99)", parsed["lastModifiedBy"])
	}
	// The counterparty (seller) is the stored remote client id.
	seller, _ := parsed["sellerId"].(map[string]any)
	if seller == nil || seller["id"] != "client-77" {
		t.Errorf("wire sellerId.id: got %v, want client-77", parsed["sellerId"])
	}

	// The local mirror reflects the new terms and the stable wire id.
	mirror, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-bh-1")
	var mo contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(remoteOfferJSONOf(mirror)), &mo)
	if mo.LastModifiedBy.ID != "employee-42" {
		t.Errorf("mirror lastModifiedBy.id: got %q, want employee-42", mo.LastModifiedBy.ID)
	}
	if !mo.PricePerStock.Equal(decimal.RequireFromString("155.5")) {
		t.Errorf("mirror strike: got %s, want 155.5", mo.PricePerStock)
	}
	if resp.GetKind() != "remote" || resp.GetId() != nid {
		t.Errorf("response: kind=%q id=%d, want remote / %d", resp.GetKind(), resp.GetId(), nid)
	}
}

// TestAcceptNegotiation_BankHostedChain_BankCaller — a caller acting AS THE BANK
// accepts a bank-hosted chain: authorized, GET /accept proxied to the seller,
// mirror flips to accepted.
func TestAcceptNegotiation_BankHostedChain_BankCaller(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-bh","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedBankHostedRemoteNeg(t, db, "neg-bh-acc", 42, "client-77", "")

	resp, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "bank",
		CallerOwnerId:       0,
		ActingPrincipalType: "employee",
		ActingPrincipalId:   99, // any employee may drive the bank's chain
		ActingEmployeeId:    99,
		AcceptorAccountId:   5001,
	})
	if err != nil {
		t.Fatalf("bank accept: %v", err)
	}
	var sawAccept bool
	for _, pc := range dispatcher.proxyCalls {
		if pc.method == "GET" && pc.subpath == "/accept" && pc.foreignID == "neg-bh-acc" && pc.peerBankCode == "222" {
			sawAccept = true
		}
	}
	if !sawAccept {
		t.Errorf("expected GET /accept to seller 222; calls: %+v", dispatcher.proxyCalls)
	}
	win, _ := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-bh-acc")
	if win.Status != "accepted" {
		t.Errorf("mirror status: got %q, want accepted", win.Status)
	}
	if resp.GetParentStatus() != "accepted" {
		t.Errorf("parent_status: got %q, want accepted", resp.GetParentStatus())
	}
	if resp.GetCrossBankTransactionId() != "tx-bh" {
		t.Errorf("cross_bank_transaction_id: got %q, want tx-bh", resp.GetCrossBankTransactionId())
	}
}

// TestRejectNegotiation_BankHostedChain_BankCaller — a bank caller rejects a
// bank-hosted chain: authorized, DELETE proxied, mirror cancelled.
func TestRejectNegotiation_BankHostedChain_BankCaller(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedBankHostedRemoteNeg(t, db, "neg-bh-rej", 42, "client-77", "")

	resp, err := h.RejectNegotiation(context.Background(), &stockpb.RejectNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "bank",
		CallerOwnerId:       0,
		ActingPrincipalType: "employee",
		ActingPrincipalId:   99,
		ActingEmployeeId:    99,
	})
	if err != nil {
		t.Fatalf("bank reject: %v", err)
	}
	assertSingleDelete(t, dispatcher, "neg-bh-rej")
	assertMirrorCancelled(t, db, "neg-bh-rej")
	if resp.GetStatus() != "cancelled" {
		t.Errorf("response status: got %q, want cancelled", resp.GetStatus())
	}
}

// TestCancelNegotiation_BankHostedChain_BankCaller — a bank caller cancels a
// bank-hosted chain: authorized, DELETE proxied, mirror cancelled.
func TestCancelNegotiation_BankHostedChain_BankCaller(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedBankHostedRemoteNeg(t, db, "neg-bh-can", 42, "client-77", "")

	resp, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "bank",
		CallerOwnerId:       0,
		ActingPrincipalType: "employee",
		ActingPrincipalId:   99,
		ActingEmployeeId:    99,
	})
	if err != nil {
		t.Fatalf("bank cancel: %v", err)
	}
	assertSingleDelete(t, dispatcher, "neg-bh-can")
	assertMirrorCancelled(t, db, "neg-bh-can")
	if resp.GetStatus() != "cancelled" {
		t.Errorf("response status: got %q, want cancelled", resp.GetStatus())
	}
}

// TestRemoteNegAction_BankCallerOnClientChain_NotFound — a caller acting AS THE
// BANK on a CLIENT-hosted chain (hosted side is "client-9", NOT a bank id) is
// NOT a party → NotFound, no proxy. (Existence must not leak; the bank may only
// drive bank-owned chains.)
func TestRemoteNegAction_BankCallerOnClientChain_NotFound(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	// Client-hosted chain: we host buyer client-9.
	nid := seedRemoteNeg(t, db, "neg-cli", "client-9", "client-77", "")

	_, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "bank",
		CallerOwnerId:       0,
		ActingPrincipalType: "employee",
		ActingPrincipalId:   99,
		ActingEmployeeId:    99,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("bank caller on a client chain: expected NotFound, got %v", err)
	}
	if len(dispatcher.proxyCalls) != 0 {
		t.Errorf("proxy must NOT fire for a bank caller on a client-hosted chain: %+v", dispatcher.proxyCalls)
	}
}

// TestRemoteNegAction_ClientCallerOnBankChain_NotFound — a CLIENT caller on a
// BANK-hosted chain (hosted side "employee-42") is NOT the party → NotFound, no
// proxy. The bank chain is only driveable by a bank caller.
func TestRemoteNegAction_ClientCallerOnBankChain_NotFound(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 204}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedBankHostedRemoteNeg(t, db, "neg-bh-cli", 42, "client-77", "")

	_, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       42, // a client whose id collides with the employee number — still not the bank
		ActingPrincipalType: "client",
		ActingPrincipalId:   42,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("client caller on a bank chain: expected NotFound, got %v", err)
	}
	if len(dispatcher.proxyCalls) != 0 {
		t.Errorf("proxy must NOT fire for a client caller on a bank-hosted chain: %+v", dispatcher.proxyCalls)
	}
}

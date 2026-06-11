// Package handler — cross-bank (REMOTE) bid dispatch tests for OpenNegotiation
// (Unified OTC SP-2b / SP-3 Task 4). The bid route dispatches local vs
// cross-bank in stock-service based on whether the parent :id is a local or a
// folded-in remote OTCOffer. Every remote offer is uniform termless inventory:
// the freshness guard runs unconditionally and the premium/strike currency is
// ALWAYS derived from the bidder's bound account (never the listing's legacy
// preset currency columns). These tests exercise the REMOTE branch:
//   - a client bid on a remote listing dispatches to the (fake) peer and
//     records a remote OTCNegotiation mirror row (buyerId "client-<N>"),
//   - a bank bid (employee acting as the bank) dispatches with buyerId
//     "employee-<N>", settles against a BANK account, and persists
//     ActingEmployeeID (SP-3 Task 4),
//   - a bank bid against a non-bank account is rejected,
//   - a bank bid with acting_employee_id == 0 is rejected,
//   - the dispatched currency equals the bidder account's currency even when the
//     listing row carries a different (now-ignored) preset currency,
//   - a bidder account with no resolvable currency is rejected.
package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// fakeOTCAccountClient implements OTCAccountClient: returns a single canned
// account for any GetAccount / GetAccountByNumber call.
type fakeOTCAccountClient struct {
	acct *accountpb.AccountResponse
	err  error
}

func (f *fakeOTCAccountClient) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.acct, nil
}

func (f *fakeOTCAccountClient) GetAccountByNumber(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.acct, nil
}

// fakePeerDispatcher implements PeerNegotiationDispatcher: records the args it
// was called with and returns a canned (routing, foreignID) or an error.
type fakePeerDispatcher struct {
	gotPeerBankCode string
	gotOffer        map[string]any
	calls           int
	routing         int64
	foreignID       string
	err             error

	// Proxy capture (SP-2b Task 4): each Proxy call is appended to proxyCalls
	// so tests can assert which verb/subpath/peer fired (e.g. the cascade
	// DELETE to a sibling bidder's bank). proxyResp/proxyStatus/proxyErr are
	// the canned return; proxyByKey overrides per "method subpath" route.
	proxyCalls  []proxyCall
	proxyResp   []byte
	proxyStatus int
	proxyErr    error
	proxyByKey  map[string]proxyResult

	// PublicStock capture (Bug-2 fix): freshness guard calls PublicStock instead
	// of Proxy so the guard can't accidentally build /negotiations///public-stock.
	publicStockResp   []byte
	publicStockStatus int
	publicStockErr    error
	publicStockCalls  []string // peer bank codes fetched
}

type proxyCall struct {
	peerBankCode string
	rid          string
	foreignID    string
	method       string
	subpath      string
	body         []byte
}

type proxyResult struct {
	resp   []byte
	status int
	err    error
}

func (f *fakePeerDispatcher) CreateNegotiation(_ context.Context, peerBankCode string, offer map[string]any) (int64, string, error) {
	f.calls++
	f.gotPeerBankCode = peerBankCode
	f.gotOffer = offer
	if f.err != nil {
		return 0, "", f.err
	}
	return f.routing, f.foreignID, nil
}

func (f *fakePeerDispatcher) PublicStock(_ context.Context, peerBankCode string) ([]byte, int, error) {
	f.publicStockCalls = append(f.publicStockCalls, peerBankCode)
	if f.publicStockErr != nil {
		return nil, 0, f.publicStockErr
	}
	st := f.publicStockStatus
	if st == 0 {
		st = 200
	}
	return f.publicStockResp, st, nil
}

func (f *fakePeerDispatcher) Proxy(_ context.Context, peerBankCode, rid, foreignID, method, subpath string, body []byte) ([]byte, int, error) {
	f.proxyCalls = append(f.proxyCalls, proxyCall{
		peerBankCode: peerBankCode, rid: rid, foreignID: foreignID,
		method: method, subpath: subpath, body: body,
	})
	if r, ok := f.proxyByKey[method+" "+subpath]; ok {
		return r.resp, r.status, r.err
	}
	if f.proxyErr != nil {
		return nil, f.proxyStatus, f.proxyErr
	}
	st := f.proxyStatus
	if st == 0 {
		st = 200
	}
	return f.proxyResp, st, nil
}

// newRemoteBidFixture builds an OTCOptionsHandler wired for SP-2b remote bid
// dispatch: a sqlite-backed negotiation service (so the LOCAL path reports
// NotFound for a remote parent), the remote-offer getter, the peer dispatcher,
// the remote-neg writer, and the account client. OwnRouting is set to 111;
// remote rows use peer routing 222.
func newRemoteBidFixture(t *testing.T, dispatcher PeerNegotiationDispatcher, accounts OTCAccountClient) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCNegotiation{}, &model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	negRepo := repository.NewOTCNegotiationRepository(db)
	negSvc := service.NewOTCNegotiationService(db, offerRepo, negRepo)

	h := NewOTCOptionsHandler(nil, nil).
		WithNegotiations(negSvc).
		WithPeerContracts(nil, 111). // sets ownRouting=111
		WithRemoteOffers(offerRepo, "111").
		WithPeerOTCDispatch(dispatcher, negRepo, accounts)
	return h, db
}

// seedRemoteOffer inserts a folded-in remote OTCOffer row (peer routing 222)
// and returns its surrogate id. Remote rows are uniform termless inventory: the
// handler always runs the freshness guard and always derives currency from the
// bidder account.
func seedRemoteOffer(t *testing.T, db *gorm.DB) uint64 {
	t.Helper()
	nid := "peer-offer-1"
	bankCode := "222"
	sellerID := "client-9"
	o := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &nid,
		InitiatorBankCode:           &bankCode,
		RemoteSellerID:              &sellerID, // legacy flag — handler now ignores it (always shell-path)
		InitiatorOwnerType:          model.OwnerBank,
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "system",
		LastModifiedByPrincipalID:   0,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed remote offer: %v", err)
	}
	return o.ID
}

func usdAccount(ownerID uint64) *accountpb.AccountResponse {
	return &accountpb.AccountResponse{
		Id:            5001,
		OwnerId:       ownerID,
		AccountNumber: "111-0000000001-22",
		CurrencyCode:  "USD",
		Status:        "active",
	}
}

// liveAAPLStock builds a /public-stock response body that lists AAPL with the
// seedRemoteOffer seller (routing 222, id "client-9"). Because the freshness
// guard now runs for EVERY remote offer (uniform termless inventory), tests that
// expect a bid to proceed past the guard must seed a live listing for the seller.
func liveAAPLStock(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(contractsitx.PublicStocksResponse{
		{
			Stock: contractsitx.StockDescription{Ticker: "AAPL"},
			Sellers: []contractsitx.PublicSeller{
				{Seller: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-9"}, Amount: 10},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal live public stock: %v", err)
	}
	return body
}

// usdBankAccount returns an active USD account flagged as a BANK account
// (account_kind == "bank", owner_id == bank sentinel). Used by the SP-3 bank-bid
// tests, where the bidder is the bank and must settle against a bank account.
func usdBankAccount() *accountpb.AccountResponse {
	return &accountpb.AccountResponse{
		Id:            5001,
		OwnerId:       1_000_000_000, // bank sentinel
		AccountNumber: "111-BANK-USD-01",
		CurrencyCode:  "USD",
		Status:        "active",
		AccountKind:   "bank",
	}
}

func openReq(parentID, bidderID uint64, ownerType string) *stockpb.OpenNegotiationRequest {
	return &stockpb.OpenNegotiationRequest{
		ParentOfferId:       parentID,
		BidderOwnerType:     ownerType,
		BidderOwnerId:       bidderID,
		BidderAccountId:     5001,
		Quantity:            "10",
		StrikePrice:         "150",
		Premium:             "20",
		SettlementDate:      "2026-07-01",
		ActingPrincipalType: "client",
		ActingPrincipalId:   bidderID,
	}
}

// bankBidReq builds an OpenNegotiationRequest for an employee acting as the
// bank: bidder_owner_type=bank, bidder_owner_id=0 (resolves to nil),
// acting_principal_type=employee, and the acting_employee_id wire-identity.
func bankBidReq(parentID, actingEmployeeID uint64) *stockpb.OpenNegotiationRequest {
	return &stockpb.OpenNegotiationRequest{
		ParentOfferId:       parentID,
		BidderOwnerType:     "bank",
		BidderOwnerId:       0,
		BidderAccountId:     5001,
		Quantity:            "10",
		StrikePrice:         "150",
		Premium:             "20",
		SettlementDate:      "2026-07-01",
		ActingPrincipalType: "employee",
		ActingPrincipalId:   actingEmployeeID,
		ActingEmployeeId:    actingEmployeeID,
	}
}

func TestOpenNegotiation_RemoteClientBid_DispatchesAndRecordsMirror(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-xyz", publicStockResp: liveAAPLStock(t)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9 /* matches bidder client-9 */)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	resp, err := h.OpenNegotiation(context.Background(), openReq(parentID, 9, "client"))
	if err != nil {
		t.Fatalf("OpenNegotiation: %v", err)
	}

	// The freshness guard now runs for EVERY remote offer (even this preset-flagged
	// seed row), re-validating the seller's live /public-stock before dispatch.
	if len(dispatcher.publicStockCalls) != 1 {
		t.Errorf("PublicStock calls: got %d, want 1 (guard must run for every remote offer)", len(dispatcher.publicStockCalls))
	}
	// Dispatched to peer 222 with the composed offer.
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher called %d times, want 1", dispatcher.calls)
	}
	if dispatcher.gotPeerBankCode != "222" {
		t.Errorf("peer bank code: got %q, want 222", dispatcher.gotPeerBankCode)
	}
	// Composed offer carries buyer/seller ids, account number, and the
	// parentOfferId lot key.
	stock, _ := dispatcher.gotOffer["stock"].(map[string]any)
	if stock == nil || stock["ticker"] != "AAPL" {
		t.Errorf("offer stock.ticker missing/wrong: %v", dispatcher.gotOffer["stock"])
	}
	if dispatcher.gotOffer["buyerAccountNumber"] != "111-0000000001-22" {
		t.Errorf("offer buyerAccountNumber: got %v", dispatcher.gotOffer["buyerAccountNumber"])
	}
	buyer, _ := dispatcher.gotOffer["buyerId"].(map[string]any)
	if buyer == nil || buyer["id"] != "client-9" {
		t.Errorf("offer buyerId.id: got %v", dispatcher.gotOffer["buyerId"])
	}
	seller, _ := dispatcher.gotOffer["sellerId"].(map[string]any)
	if seller == nil || seller["id"] != "client-9" /* remote seller id from seed */ {
		// seed used RemoteSellerID="client-9"
		t.Errorf("offer sellerId.id: got %v", dispatcher.gotOffer["sellerId"])
	}
	parent, _ := dispatcher.gotOffer["parentOfferId"].(map[string]any)
	if parent == nil || parent["id"] != "peer-offer-1" {
		t.Errorf("offer parentOfferId.id: got %v", dispatcher.gotOffer["parentOfferId"])
	}

	// A remote OTCNegotiation mirror row was recorded (routing 222, native_id neg-xyz).
	mirror, gerr := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-xyz")
	if gerr != nil {
		t.Fatalf("expected remote mirror row: %v", gerr)
	}
	if mirror.Status != "ongoing" {
		t.Errorf("mirror status: got %q, want ongoing", mirror.Status)
	}
	if mirror.RemoteBuyerID == nil || *mirror.RemoteBuyerID != "client-9" {
		t.Errorf("mirror RemoteBuyerID: got %v", mirror.RemoteBuyerID)
	}
	if mirror.RemoteParentNativeID == nil || *mirror.RemoteParentNativeID != "peer-offer-1" {
		t.Errorf("mirror RemoteParentNativeID: got %v", mirror.RemoteParentNativeID)
	}
	// A client bid never carries an acting-employee wire identity.
	if mirror.ActingEmployeeID != nil {
		t.Errorf("client-bid mirror ActingEmployeeID: got %v, want nil", *mirror.ActingEmployeeID)
	}

	// The unified response is kind=remote and carries the mirror surrogate id.
	if resp.GetKind() != "remote" {
		t.Errorf("response kind: got %q, want remote", resp.GetKind())
	}
	if resp.GetId() != mirror.ID {
		t.Errorf("response id: got %d, want mirror id %d", resp.GetId(), mirror.ID)
	}
}

// TestOpenNegotiation_RemoteBid_CurrencyFromAccountNotListing proves the uniform
// termless-inventory behavior: the dispatched SI-TX offer carries the BIDDER
// ACCOUNT's currency, even when the remote offer ROW still has a different,
// non-nil preset currency (the seed sets USD). The bidder binds an RSD account,
// so the composed premium/pricePerUnit MUST be RSD — proving the handler no
// longer reads remoteOffer.PremiumCurrency/StrikeCurrency. There is no longer a
// currency-mismatch rejection (cross-bank SI-TX FX is the buyer's choice of
// account).
func TestOpenNegotiation_RemoteBid_CurrencyFromAccountNotListing(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-rsd", publicStockResp: liveAAPLStock(t)}
	rsd := usdAccount(9)
	rsd.CurrencyCode = "RSD" // differs from the listing row's preset USD — must win
	accounts := &fakeOTCAccountClient{acct: rsd}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db) // row has PremiumCurrency/StrikeCurrency = USD

	if _, err := h.OpenNegotiation(context.Background(), openReq(parentID, 9, "client")); err != nil {
		t.Fatalf("OpenNegotiation: %v", err)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher called %d times, want 1", dispatcher.calls)
	}
	// Both legs carry the bidder ACCOUNT's currency (RSD), NOT the row's USD.
	ppu, _ := dispatcher.gotOffer["pricePerUnit"].(map[string]any)
	if ppu == nil || ppu["currency"] != "RSD" {
		t.Errorf("pricePerUnit.currency = %v, want RSD (derived from bidder account, not the listing row's USD)", ppu)
	}
	prem, _ := dispatcher.gotOffer["premium"].(map[string]any)
	if prem == nil || prem["currency"] != "RSD" {
		t.Errorf("premium.currency = %v, want RSD (derived from bidder account, not the listing row's USD)", prem)
	}
}

// TestOpenNegotiation_RemoteBid_EmptyAccountCurrency rejects a bid whose bound
// account has no resolvable currency: since the currency is always derived from
// the account, an empty currency cannot produce a valid offer. The freshness
// guard passes (live listing seeded) so the rejection comes from the currency
// derivation, with no dispatch.
func TestOpenNegotiation_RemoteBid_EmptyAccountCurrency(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-xyz", publicStockResp: liveAAPLStock(t)}
	noCcy := usdAccount(9)
	noCcy.CurrencyCode = "" // unresolvable currency
	accounts := &fakeOTCAccountClient{acct: noCcy}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	_, err := h.OpenNegotiation(context.Background(), openReq(parentID, 9, "client"))
	if err == nil {
		t.Fatal("expected rejection for an account with no currency, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code: got %v, want FailedPrecondition", status.Code(err))
	}
	if dispatcher.calls != 0 {
		t.Errorf("dispatcher should NOT be called on an empty-currency account: %d", dispatcher.calls)
	}
}

// TestOpenNegotiation_RemoteBankBid_DispatchesAsEmployeeWireID is the SP-3
// Task 4 happy path: an employee acting AS the bank places a cross-bank bid.
// The SI-TX offer's buyerId is the stable wire identity "employee-<N>"; the
// bidder account is validated as a BANK account; and the persisted remote
// negotiation row carries ActingEmployeeID, RemoteBuyerID "employee-<N>", and
// bidder owner type bank.
func TestOpenNegotiation_RemoteBankBid_DispatchesAsEmployeeWireID(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-bank", publicStockResp: liveAAPLStock(t)}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	resp, err := h.OpenNegotiation(context.Background(), bankBidReq(parentID, 42))
	if err != nil {
		t.Fatalf("OpenNegotiation (bank bid): %v", err)
	}

	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher called %d times, want 1", dispatcher.calls)
	}
	// The wire buyer identity is the stable "employee-<actingEmployeeID>".
	buyer, _ := dispatcher.gotOffer["buyerId"].(map[string]any)
	if buyer == nil || buyer["id"] != "employee-42" {
		t.Errorf("offer buyerId.id: got %v, want employee-42", dispatcher.gotOffer["buyerId"])
	}
	lastBy, _ := dispatcher.gotOffer["lastModifiedBy"].(map[string]any)
	if lastBy == nil || lastBy["id"] != "employee-42" {
		t.Errorf("offer lastModifiedBy.id: got %v, want employee-42", dispatcher.gotOffer["lastModifiedBy"])
	}
	// The bank's account number was bound for settlement.
	if dispatcher.gotOffer["buyerAccountNumber"] != "111-BANK-USD-01" {
		t.Errorf("offer buyerAccountNumber: got %v, want 111-BANK-USD-01", dispatcher.gotOffer["buyerAccountNumber"])
	}

	// The persisted remote mirror carries the bank wire identity + acting employee.
	mirror, gerr := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-bank")
	if gerr != nil {
		t.Fatalf("expected remote mirror row: %v", gerr)
	}
	if mirror.BidderOwnerType != model.OwnerBank {
		t.Errorf("mirror BidderOwnerType: got %q, want bank", mirror.BidderOwnerType)
	}
	if mirror.ActingEmployeeID == nil || *mirror.ActingEmployeeID != 42 {
		t.Errorf("mirror ActingEmployeeID: got %v, want 42", mirror.ActingEmployeeID)
	}
	if mirror.RemoteBuyerID == nil || *mirror.RemoteBuyerID != "employee-42" {
		t.Errorf("mirror RemoteBuyerID: got %v, want employee-42", mirror.RemoteBuyerID)
	}
	// The persisted mirror's RemoteOfferJSON MUST retain the buyer's bound
	// account number. On a cross-bank accept the buyer's bank reads its own
	// mirror to compose the premium-DEBIT posting; if buyerAccountNumber is lost
	// it falls back to the participant id ("employee-42"), which the SI-TX
	// executor cannot resolve to a bank account → NO_SUCH_ACCOUNT (bank-buyer
	// accept rejected). Pin it so the accept debits the exact bound account.
	if mirror.RemoteOfferJSON == nil {
		t.Fatal("mirror RemoteOfferJSON is nil")
	}
	var mirrorOffer struct {
		BuyerAccountNumber string `json:"buyerAccountNumber"`
	}
	if jerr := json.Unmarshal([]byte(*mirror.RemoteOfferJSON), &mirrorOffer); jerr != nil {
		t.Fatalf("decode mirror RemoteOfferJSON: %v", jerr)
	}
	if mirrorOffer.BuyerAccountNumber != "111-BANK-USD-01" {
		t.Errorf("mirror RemoteOfferJSON.buyerAccountNumber: got %q, want 111-BANK-USD-01", mirrorOffer.BuyerAccountNumber)
	}
	if resp.GetKind() != "remote" || resp.GetId() != mirror.ID {
		t.Errorf("response: kind=%q id=%d, want remote id=%d", resp.GetKind(), resp.GetId(), mirror.ID)
	}
}

// TestOpenNegotiation_RemoteBankBid_NonBankAccount rejects a bank bid whose
// bound account is NOT a bank account (a bank bidder must settle against a bank
// account; the ownership assertion branches on bidder owner type).
func TestOpenNegotiation_RemoteBankBid_NonBankAccount(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-bank", publicStockResp: liveAAPLStock(t)}
	// A client-owned (non-bank) USD account masquerading as the bid account.
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	_, err := h.OpenNegotiation(context.Background(), bankBidReq(parentID, 42))
	if err == nil {
		t.Fatal("expected error for a non-bank bidder account, got nil")
	}
	if dispatcher.calls != 0 {
		t.Errorf("dispatcher should NOT be called for a non-bank account: %d", dispatcher.calls)
	}
}

// TestOpenNegotiation_RemoteBankBid_MissingActingEmployee rejects a bank bid
// without an acting_employee_id (no stable wire identity to publish as).
func TestOpenNegotiation_RemoteBankBid_MissingActingEmployee(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-bank", publicStockResp: liveAAPLStock(t)}
	accounts := &fakeOTCAccountClient{acct: usdBankAccount()}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	req := bankBidReq(parentID, 0) // acting_employee_id == 0
	_, err := h.OpenNegotiation(context.Background(), req)
	if err == nil {
		t.Fatal("expected InvalidArgument for missing acting_employee_id, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code: got %v, want InvalidArgument", status.Code(err))
	}
	if dispatcher.calls != 0 {
		t.Errorf("dispatcher should NOT be called without acting_employee_id: %d", dispatcher.calls)
	}
}

func TestOpenNegotiation_NonexistentParent_StillNotFound(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-xyz"}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, _ := newRemoteBidFixture(t, dispatcher, accounts)

	// parent id 999 is neither local nor remote → surface the original NotFound.
	_, err := h.OpenNegotiation(context.Background(), openReq(999, 9, "client"))
	if err == nil {
		t.Fatal("expected NotFound for an unknown parent, got nil")
	}
	if dispatcher.calls != 0 {
		t.Errorf("dispatcher should NOT be called for an unknown parent: %d", dispatcher.calls)
	}
}

// TestOfferComposition_AmountsAreJSONNumbers verifies that the SI-TX OtcOffer
// composed by openRemoteNegotiation serialises pricePerUnit.amount and
// premium.amount as bare JSON numbers (e.g. 150.5, not "150.5").
// SI-TX §2.5 / §2.8.1 require MonetaryValue.amount to be a number token; a
// strict cohort peer will reject a quoted string amount.
func TestOfferComposition_AmountsAreJSONNumbers(t *testing.T) {
	dispatcher := &fakePeerDispatcher{routing: 222, foreignID: "neg-wire", publicStockResp: liveAAPLStock(t)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	parentID := seedRemoteOffer(t, db)

	if _, err := h.OpenNegotiation(context.Background(), openReq(parentID, 9, "client")); err != nil {
		t.Fatalf("OpenNegotiation: %v", err)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("expected 1 dispatch call, got %d", dispatcher.calls)
	}

	// Marshal the offer the dispatcher received — this is exactly what would
	// be sent over the wire to the peer bank.
	raw, err := json.Marshal(dispatcher.gotOffer)
	if err != nil {
		t.Fatalf("marshal dispatched offer: %v", err)
	}
	wire := string(raw)

	// The wire MUST contain bare numbers (e.g. "amount":150), NOT quoted
	// strings (e.g. "amount":"150"). Check for both fields.
	if strings.Contains(wire, `"amount":"`) {
		t.Errorf("wire contains quoted amount (string), want bare number; wire: %s", wire)
	}

	// Unmarshal and verify the numeric values are correct.
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	ppu, _ := parsed["pricePerUnit"].(map[string]any)
	if ppu == nil {
		t.Fatalf("pricePerUnit missing from wire")
	}
	// json.Unmarshal produces float64 for JSON numbers.
	ppuAmt, ok := ppu["amount"].(float64)
	if !ok {
		t.Errorf("pricePerUnit.amount: want float64 (bare number), got %T = %v", ppu["amount"], ppu["amount"])
	} else if ppuAmt != 150 {
		t.Errorf("pricePerUnit.amount: got %v, want 150", ppuAmt)
	}

	prem, _ := parsed["premium"].(map[string]any)
	if prem == nil {
		t.Fatalf("premium missing from wire")
	}
	premAmt, ok := prem["amount"].(float64)
	if !ok {
		t.Errorf("premium.amount: want float64 (bare number), got %T = %v", prem["amount"], prem["amount"])
	} else if premAmt != 20 {
		t.Errorf("premium.amount: got %v, want 20", premAmt)
	}
}

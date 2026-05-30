package handler_test

import (
	"context"
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeHoldingReader struct {
	rows []model.Holding
	err  error
}

func (f *fakeHoldingReader) GetByOwnerAndTicker(ownerType model.OwnerType, ownerID *uint64, securityType, ticker string) (*model.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.rows {
		hd := f.rows[i]
		if hd.SecurityType != securityType || hd.Ticker != ticker || hd.OwnerType != ownerType {
			continue
		}
		if (ownerID == nil) != (hd.OwnerID == nil) {
			continue
		}
		if ownerID != nil && *ownerID != *hd.OwnerID {
			continue
		}
		return &hd, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeHoldingReader) ListPublic() ([]model.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

type fakePeerTxClient struct {
	gotReq *transactionpb.SiTxInitiateWithPostingsRequest
	resp   *transactionpb.SiTxInitiateResponse
	err    error
}

func (f *fakePeerTxClient) HandleNewTx(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakePeerTxClient) HandleCommitTx(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakePeerTxClient) HandleRollbackTx(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakePeerTxClient) InitiateOutboundTx(ctx context.Context, in *transactionpb.SiTxInitiateRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakePeerTxClient) GetTxStatus(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakePeerTxClient) InitiateOutboundTxWithPostings(ctx context.Context, in *transactionpb.SiTxInitiateWithPostingsRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	f.gotReq = in
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &transactionpb.SiTxInitiateResponse{TransactionId: "tx-1", Status: "initiated"}, nil
}

func newPeerOtcHandler(t *testing.T) (*handler.PeerOTCGRPCHandler, *gorm.DB, *fakePeerTxClient, *fakeHoldingReader) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerOtcNegotiation{}, &model.PeerOptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewPeerOtcNegotiationRepository(db)
	optRepo := repository.NewPeerOptionContractRepository(db)
	holdings := &fakeHoldingReader{}
	peerTx := &fakePeerTxClient{}
	return handler.NewPeerOTCGRPCHandler(repo, optRepo, holdings, peerTx, 111), db, peerTx, holdings
}

func TestPeerOTC_CreateAndGet(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	createResp, err := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 100,
			PricePerStock: "150.00", Currency: "USD",
			Premium: "10.00", PremiumCurrency: "USD",
			SettlementDate: "2026-12-31",
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-3"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if createResp.GetNegotiationId() == nil || createResp.GetNegotiationId().GetId() == "" {
		t.Fatalf("expected negotiation id, got %+v", createResp)
	}
	if createResp.GetNegotiationId().GetRoutingNumber() != 111 {
		t.Errorf("expected routing 111 (own), got %d", createResp.GetNegotiationId().GetRoutingNumber())
	}

	getResp, err := h.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if getResp.GetStatus() != "ongoing" {
		t.Errorf("expected ongoing, got %s", getResp.GetStatus())
	}
	if getResp.GetBuyerId().GetId() != "client-7" {
		t.Errorf("buyer mismatch: %+v", getResp.GetBuyerId())
	}
	if getResp.GetOffer().GetTicker() != "AAPL" {
		t.Errorf("ticker mismatch: %s", getResp.GetOffer().GetTicker())
	}
}

func TestPeerOTC_GetNotFound(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.GetNegotiation(context.Background(), &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "missing"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestPeerOTC_UpdateOffer(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	createResp, _ := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 100,
			PricePerStock: "150.00", Premium: "10.00",
			Currency: "USD", PremiumCurrency: "USD",
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "b"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "s"},
	})

	_, err := h.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 100,
			PricePerStock: "160.00", Premium: "12.50",
			Currency: "USD", PremiumCurrency: "USD",
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	getResp, _ := h.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if getResp.GetOffer().GetPricePerStock() != "160" && getResp.GetOffer().GetPricePerStock() != "160.00" {
		t.Errorf("expected updated price, got %s", getResp.GetOffer().GetPricePerStock())
	}
}

func TestPeerOTC_DeleteNegotiation(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	createResp, _ := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 1, PricePerStock: "1", Premium: "1",
			Currency: "USD", PremiumCurrency: "USD",
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "b"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "s"},
	})

	_, err := h.DeleteNegotiation(ctx, &stockpb.DeleteNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Per SI-TX §3.5, DELETE sets isOngoing to false; the negotiation
	// row remains so a subsequent GET still returns OtcNegotiation. The
	// status field flips to "cancelled" which the gateway maps to
	// isOngoing=false in its response.
	resp, gerr := h.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if gerr != nil {
		t.Fatalf("expected GET to succeed after soft-cancel, got %v", gerr)
	}
	if resp.GetStatus() != "cancelled" {
		t.Errorf("expected status=cancelled after delete, got %q", resp.GetStatus())
	}
}

func TestPeerOTC_AcceptNegotiation_DispatchesViaPeerTx(t *testing.T) {
	h, _, peerTx, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	createResp, _ := h.CreateNegotiation(ctx, &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 100,
			PricePerStock: "150.00", Currency: "USD",
			Premium: "10.00", PremiumCurrency: "USD",
			SettlementDate: "2026-12-31",
			// Pinned buyer account number (money leg) — DISTINCT from the buyer
			// participant id (option leg) so the test verifies each leg carries
			// the right identifier.
			BuyerAccountNumber: "111000000000000999",
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
	})

	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-99", Status: "initiated"}

	resp, err := h.AcceptNegotiation(ctx, &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if resp.GetTransactionId() != "tx-99" {
		t.Errorf("expected tx-99, got %s", resp.GetTransactionId())
	}
	if resp.GetStatus() != "initiated" {
		t.Errorf("expected initiated, got %s", resp.GetStatus())
	}

	if peerTx.gotReq == nil {
		t.Fatalf("InitiateOutboundTxWithPostings was not called")
	}
	if peerTx.gotReq.GetTxKind() != "otc-accept" {
		t.Errorf("expected tx_kind=otc-accept, got %s", peerTx.gotReq.GetTxKind())
	}
	if peerTx.gotReq.GetPeerBankCode() != "222" {
		t.Errorf("expected peer 222, got %s", peerTx.gotReq.GetPeerBankCode())
	}
	if got := len(peerTx.gotReq.GetPostings()); got != 4 {
		t.Fatalf("expected 4 postings, got %d", got)
	}

	postings := peerTx.gotReq.GetPostings()
	// Posting 0: buyer DEBIT premium currency — MONEY leg carries the pinned
	// buyer ACCOUNT NUMBER (so the executor debits the exact account).
	if postings[0].GetDirection() != "DEBIT" || postings[0].GetAccountId() != "111000000000000999" || postings[0].GetAssetId() != "USD" {
		t.Errorf("posting 0 mismatch: %+v", postings[0])
	}
	// Posting 1: seller CREDIT premium currency
	if postings[1].GetDirection() != "CREDIT" || postings[1].GetAccountId() != "client-9" || postings[1].GetAssetId() != "USD" {
		t.Errorf("posting 1 mismatch: %+v", postings[1])
	}
	// Posting 2: seller DEBIT 1× option — OPTION leg carries the seller PARTICIPANT id.
	if postings[2].GetDirection() != "DEBIT" || postings[2].GetAccountId() != "client-9" || postings[2].GetAmount() != "1" {
		t.Errorf("posting 2 mismatch: %+v", postings[2])
	}
	// Posting 3: buyer CREDIT 1× option — OPTION leg carries the buyer PARTICIPANT
	// id ("client-7"), NOT the account number. This is the buyer_id that the
	// contract stores and that the exercise share-credit + /me listing rely on.
	if postings[3].GetDirection() != "CREDIT" || postings[3].GetAccountId() != "client-7" || postings[3].GetAmount() != "1" {
		t.Errorf("posting 3 mismatch (option leg must carry buyer participant id, not account number): %+v", postings[3])
	}
	// Both option postings share the same asset_id (the JSON-encoded
	// OptionDescription), distinct from the premium asset.
	if postings[2].GetAssetId() != postings[3].GetAssetId() {
		t.Errorf("option asset_id mismatch between sides: %q vs %q", postings[2].GetAssetId(), postings[3].GetAssetId())
	}
	if postings[2].GetAssetId() == "USD" {
		t.Errorf("option asset_id should be JSON OptionDescription, not premium currency")
	}

	// Status flipped to accepted on the local mirror.
	getResp, _ := h.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if getResp.GetStatus() != "accepted" {
		t.Errorf("expected accepted, got %s", getResp.GetStatus())
	}

	// Concurrency guard: a SECOND accept of the same (now-accepted) negotiation
	// must be rejected, not dispatch a second option-formation TX (which would
	// double-charge the premium, double-reserve seller shares, and mint a
	// duplicate contract).
	peerTx.gotReq = nil
	_, err2 := h.AcceptNegotiation(ctx, &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if status.Code(err2) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition on second accept, got %v", err2)
	}
	if peerTx.gotReq != nil {
		t.Errorf("second accept must NOT dispatch a second option-formation TX")
	}
}

// TestValidatePeerOptionMoneyLeg_ForgedStrike is the receiver-side guard against
// the forged-strike theft: an exercise whose money differs from the stored
// contract's StrikePrice*Quantity must be denied, while the honest amount passes
// and accept-intent legs are not (yet) enforced.
func TestValidatePeerOptionMoneyLeg_ForgedStrike(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
	// Stored seller-side contract: 2 MA @ strike 250 RSD → honest exercise pays 500.
	if err := db.Create(&model.PeerOptionContract{
		CrossbankTxID: "seed:1", PostingIndex: 0,
		NegotiationRoutingNumber: 111, NegotiationID: "neg-1",
		BuyerRoutingNumber: 222, BuyerID: "client-9",
		SellerRoutingNumber: 111, SellerID: "client-1",
		Ticker: "MA", Quantity: 2, StrikePrice: decimal.NewFromInt(250),
		Currency: "RSD", SettlementDate: "2028-06-30T00:00:00Z",
		Direction: "DEBIT", Status: "active",
	}).Error; err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	base := func(money string) *stockpb.ValidatePeerOptionMoneyLegRequest {
		return &stockpb.ValidatePeerOptionMoneyLegRequest{
			NegotiationRouting: 111, NegotiationId: "neg-1", Direction: "DEBIT",
			Intent: "exercise", Ticker: "MA", Quantity: 2, StrikePrice: "250",
			MoneyAmount: money, Currency: "RSD",
		}
	}
	ctx := context.Background()

	// Forged low strike → DENY.
	if r, err := h.ValidatePeerOptionMoneyLeg(ctx, base("1")); err != nil || r.GetOk() {
		t.Errorf("forged strike (1 vs 500) must be denied; got ok=%v reason=%q err=%v", r.GetOk(), r.GetReason(), err)
	}
	// Honest strike 250*2=500 → OK.
	if r, err := h.ValidatePeerOptionMoneyLeg(ctx, base("500")); err != nil || !r.GetOk() {
		t.Errorf("honest strike (500) must pass; got ok=%v reason=%q err=%v", r.GetOk(), r.GetReason(), err)
	}
	// Quantity mismatch → DENY (claim 5 shares against a 2-share contract).
	q := base("500")
	q.Quantity = 5
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, q); r.GetOk() {
		t.Errorf("quantity mismatch must be denied; got ok with reason=%q", r.GetReason())
	}
	// No stored contract for this negotiation → DENY.
	nf := base("500")
	nf.NegotiationId = "does-not-exist"
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, nf); r.GetOk() {
		t.Errorf("missing contract must be denied; got ok")
	}
	// Replay defense: once the contract is exercised, even an honest-amount
	// exercise must be denied (a forged second exercise would double-charge).
	if err := db.Model(&model.PeerOptionContract{}).Where("negotiation_id = ?", "neg-1").Update("status", "exercised").Error; err != nil {
		t.Fatalf("mark exercised: %v", err)
	}
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, base("500")); r.GetOk() {
		t.Errorf("exercise of an already-exercised contract must be denied; got ok with reason=%q", r.GetReason())
	}
}

// TestValidatePeerOptionMoneyLeg_AcceptPremium guards the accept side: the
// premium money must equal the stored negotiation's premium (per-currency), and
// the option terms must match — a forged-low premium or forged terms is denied.
func TestValidatePeerOptionMoneyLeg_AcceptPremium(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting 111
	// Stored negotiation: 2 MA, strike 250, premium 35 RSD. peer_bank_code is the
	// counterparty's code ("222"); foreign_id is the negotiation UUID.
	offer := `{"ticker":"MA","amount":2,"pricePerStock":"250","currency":"RSD","premium":"35","premiumCurrency":"RSD","settlementDate":"2028-06-30T00:00:00Z"}`
	if err := db.Create(&model.PeerOtcNegotiation{
		PeerBankCode: "222", ForeignID: "neg-A", BuyerRoutingNumber: 111, BuyerID: "client-1",
		SellerRoutingNumber: 222, SellerID: "client-9", OfferJSON: offer, Status: "ongoing",
	}).Error; err != nil {
		t.Fatalf("seed negotiation: %v", err)
	}
	base := func(prem string) *stockpb.ValidatePeerOptionMoneyLegRequest {
		return &stockpb.ValidatePeerOptionMoneyLegRequest{
			NegotiationRouting: 222, NegotiationId: "neg-A", Direction: "CREDIT",
			Intent: "accept", Ticker: "MA", Quantity: 2, StrikePrice: "250",
			MoneyAmount: prem, Currency: "RSD", PeerBankCode: "222",
		}
	}
	ctx := context.Background()
	// Forged-low premium → DENY.
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, base("1")); r.GetOk() {
		t.Errorf("forged-low premium (1 vs 35) must be denied; got ok reason=%q", r.GetReason())
	}
	// Honest premium → OK.
	if r, err := h.ValidatePeerOptionMoneyLeg(ctx, base("35")); err != nil || !r.GetOk() {
		t.Errorf("honest premium (35) must pass; got ok=%v reason=%q err=%v", r.GetOk(), r.GetReason(), err)
	}
	// Forged quantity → DENY.
	q := base("35")
	q.Quantity = 99
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, q); r.GetOk() {
		t.Errorf("forged quantity must be denied; got ok reason=%q", r.GetReason())
	}
	// No negotiation → DENY.
	nf := base("35")
	nf.NegotiationId = "nope"
	if r, _ := h.ValidatePeerOptionMoneyLeg(ctx, nf); r.GetOk() {
		t.Errorf("missing negotiation must be denied; got ok")
	}
	// Cross-currency premium (money currency != premium currency): not amount-
	// checked, but a positive amount is accepted (documented residual).
	cc := base("9999")
	cc.Currency = "EUR"
	if r, err := h.ValidatePeerOptionMoneyLeg(ctx, cc); err != nil || !r.GetOk() {
		t.Errorf("cross-currency positive premium should pass (residual); got ok=%v err=%v", r.GetOk(), err)
	}
}

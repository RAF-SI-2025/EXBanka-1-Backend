package handler_test

import (
	"context"
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
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
	if err := db.AutoMigrate(&model.PeerOtcNegotiation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewPeerOtcNegotiationRepository(db)
	holdings := &fakeHoldingReader{}
	peerTx := &fakePeerTxClient{}
	return handler.NewPeerOTCGRPCHandler(repo, holdings, peerTx, 111), db, peerTx, holdings
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

	_, gerr := h.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	})
	if gerr == nil {
		t.Fatalf("expected NotFound after delete")
	}
	if status.Code(gerr) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(gerr))
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
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "buyer-acct"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "seller-acct"},
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
	// Posting 0: buyer DEBIT premium currency
	if postings[0].GetDirection() != "DEBIT" || postings[0].GetAccountId() != "buyer-acct" || postings[0].GetAssetId() != "USD" {
		t.Errorf("posting 0 mismatch: %+v", postings[0])
	}
	// Posting 1: seller CREDIT premium currency
	if postings[1].GetDirection() != "CREDIT" || postings[1].GetAccountId() != "seller-acct" || postings[1].GetAssetId() != "USD" {
		t.Errorf("posting 1 mismatch: %+v", postings[1])
	}
	// Posting 2: seller DEBIT 1× option
	if postings[2].GetDirection() != "DEBIT" || postings[2].GetAccountId() != "seller-acct" || postings[2].GetAmount() != "1" {
		t.Errorf("posting 2 mismatch: %+v", postings[2])
	}
	// Posting 3: buyer CREDIT 1× option
	if postings[3].GetDirection() != "CREDIT" || postings[3].GetAccountId() != "buyer-acct" || postings[3].GetAmount() != "1" {
		t.Errorf("posting 3 mismatch: %+v", postings[3])
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
}

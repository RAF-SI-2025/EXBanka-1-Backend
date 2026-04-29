package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubPeerOTCClient struct {
	stockpb.PeerOTCServiceClient
	getPublicStocksFn func(ctx context.Context, in *stockpb.GetPublicStocksRequest, opts ...grpc.CallOption) (*stockpb.GetPublicStocksResponse, error)
	createFn          func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error)
	updateFn          func(ctx context.Context, in *stockpb.UpdateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.UpdateNegotiationResponse, error)
	getFn             func(ctx context.Context, in *stockpb.GetNegotiationRequest, opts ...grpc.CallOption) (*stockpb.GetNegotiationResponse, error)
	deleteFn          func(ctx context.Context, in *stockpb.DeleteNegotiationRequest, opts ...grpc.CallOption) (*stockpb.DeleteNegotiationResponse, error)
	acceptFn          func(ctx context.Context, in *stockpb.AcceptNegotiationRequest, opts ...grpc.CallOption) (*stockpb.AcceptNegotiationResponse, error)
}

func (s *stubPeerOTCClient) GetPublicStocks(ctx context.Context, in *stockpb.GetPublicStocksRequest, opts ...grpc.CallOption) (*stockpb.GetPublicStocksResponse, error) {
	if s.getPublicStocksFn != nil {
		return s.getPublicStocksFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerOTCClient) CreateNegotiation(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerOTCClient) UpdateNegotiation(ctx context.Context, in *stockpb.UpdateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.UpdateNegotiationResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerOTCClient) GetNegotiation(ctx context.Context, in *stockpb.GetNegotiationRequest, opts ...grpc.CallOption) (*stockpb.GetNegotiationResponse, error) {
	if s.getFn != nil {
		return s.getFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerOTCClient) DeleteNegotiation(ctx context.Context, in *stockpb.DeleteNegotiationRequest, opts ...grpc.CallOption) (*stockpb.DeleteNegotiationResponse, error) {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerOTCClient) AcceptNegotiation(ctx context.Context, in *stockpb.AcceptNegotiationRequest, opts ...grpc.CallOption) (*stockpb.AcceptNegotiationResponse, error) {
	if s.acceptFn != nil {
		return s.acceptFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}

func setupOTCRouter(client stockpb.PeerOTCServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerOTCHandler(client)
	authMiddleware := func(c *gin.Context) {
		c.Set("peer_bank_code", "222")
		c.Next()
	}
	r.GET("/public-stock", authMiddleware, h.GetPublicStocks)
	r.POST("/negotiations", authMiddleware, h.CreateNegotiation)
	r.PUT("/negotiations/:rid/:id", authMiddleware, h.UpdateNegotiation)
	r.GET("/negotiations/:rid/:id", authMiddleware, h.GetNegotiation)
	r.DELETE("/negotiations/:rid/:id", authMiddleware, h.DeleteNegotiation)
	r.GET("/negotiations/:rid/:id/accept", authMiddleware, h.AcceptNegotiation)
	return r
}

func TestPeerOTC_GetPublicStocks(t *testing.T) {
	stub := &stubPeerOTCClient{
		getPublicStocksFn: func(ctx context.Context, in *stockpb.GetPublicStocksRequest, opts ...grpc.CallOption) (*stockpb.GetPublicStocksResponse, error) {
			return &stockpb.GetPublicStocksResponse{Stocks: []*stockpb.PeerPublicStock{
				{OwnerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"}, Ticker: "AAPL", Amount: 50, PricePerStock: "180.50", Currency: "USD"},
			}}, nil
		},
	}
	r := setupOTCRouter(stub)
	req := httptest.NewRequest(http.MethodGet, "/public-stock", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["stocks"] == nil {
		t.Errorf("missing stocks: %+v", got)
	}
}

func TestPeerOTC_CreateNegotiation(t *testing.T) {
	stub := &stubPeerOTCClient{
		createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
			return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-1"}}, nil
		},
	}
	r := setupOTCRouter(stub)
	body, _ := json.Marshal(map[string]any{
		"offer": map[string]any{
			"ticker": "AAPL", "amount": 50, "pricePerStock": "180.50", "currency": "USD",
			"premium": "700", "premiumCurrency": "USD", "settlementDate": "2026-12-31",
			"lastModifiedBy": map[string]any{"routingNumber": 222, "id": "user-1"},
		},
		"buyerId":  map[string]any{"routingNumber": 222, "id": "buyer-1"},
		"sellerId": map[string]any{"routingNumber": 111, "id": "seller-1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}

func TestPeerOTC_GetNegotiation(t *testing.T) {
	stub := &stubPeerOTCClient{
		getFn: func(ctx context.Context, in *stockpb.GetNegotiationRequest, opts ...grpc.CallOption) (*stockpb.GetNegotiationResponse, error) {
			return &stockpb.GetNegotiationResponse{
				Id:       &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "neg-7"},
				BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "b"},
				SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "s"},
				Offer: &stockpb.PeerOtcOffer{
					Ticker: "AAPL", Amount: 50, PricePerStock: "180", Currency: "USD",
					Premium: "700", PremiumCurrency: "USD", SettlementDate: "2026-12-31",
					LastModifiedBy: &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "u"},
				},
				Status:    "ongoing",
				UpdatedAt: "2026-04-29T12:00:00Z",
			}, nil
		},
	}
	r := setupOTCRouter(stub)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/negotiations/222/neg-7", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestPeerOTC_AcceptNegotiation_Dispatches(t *testing.T) {
	stub := &stubPeerOTCClient{
		acceptFn: func(ctx context.Context, in *stockpb.AcceptNegotiationRequest, opts ...grpc.CallOption) (*stockpb.AcceptNegotiationResponse, error) {
			return &stockpb.AcceptNegotiationResponse{TransactionId: "tx-otc-1", Status: "pending"}, nil
		},
	}
	r := setupOTCRouter(stub)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/negotiations/222/neg-1/accept", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["transactionId"] != "tx-otc-1" {
		t.Errorf("got %+v", got)
	}
}

func TestPeerOTC_DeleteNegotiation_Returns204(t *testing.T) {
	stub := &stubPeerOTCClient{
		deleteFn: func(ctx context.Context, in *stockpb.DeleteNegotiationRequest, opts ...grpc.CallOption) (*stockpb.DeleteNegotiationResponse, error) {
			return &stockpb.DeleteNegotiationResponse{}, nil
		},
	}
	r := setupOTCRouter(stub)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/negotiations/222/neg-1", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerOTC_GetNegotiation_BadRid_400(t *testing.T) {
	stub := &stubPeerOTCClient{}
	r := setupOTCRouter(stub)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/negotiations/notnumeric/neg-1", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: %d", w.Code)
	}
}

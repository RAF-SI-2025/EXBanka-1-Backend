package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// §3.1 bare-array response — top level must be a JSON array.
	var got []any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal bare array: %v (body=%s)", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(got), got)
	}
	// §3.1 inner shape: each entry groups a stock with its sellers.
	entry, ok := got[0].(map[string]any)
	if !ok {
		t.Fatalf("entry not an object: %+v", got[0])
	}
	stock, ok := entry["stock"].(map[string]any)
	if !ok {
		t.Fatalf("stock not an object: %+v", entry["stock"])
	}
	if stock["ticker"] != "AAPL" {
		t.Errorf("ticker: got %v, want AAPL", stock["ticker"])
	}
	sellers, ok := entry["sellers"].([]any)
	if !ok || len(sellers) == 0 {
		t.Fatalf("sellers not a non-empty array: %+v", entry["sellers"])
	}
	first, ok := sellers[0].(map[string]any)
	if !ok {
		t.Fatalf("seller entry not an object: %+v", sellers[0])
	}
	seller, ok := first["seller"].(map[string]any)
	if !ok {
		t.Fatalf("seller not an object: %+v", first["seller"])
	}
	if seller["routingNumber"].(float64) != 111 {
		t.Errorf("seller.routingNumber: got %v, want 111", seller["routingNumber"])
	}
	if seller["id"] != "client-7" {
		t.Errorf("seller.id: got %v, want client-7", seller["id"])
	}
	if first["amount"].(float64) != 50 {
		t.Errorf("amount: got %v, want 50", first["amount"])
	}
}

func TestPeerOTC_CreateNegotiation(t *testing.T) {
	stub := &stubPeerOTCClient{
		createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
			return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-1"}}, nil
		},
	}
	r := setupOTCRouter(stub)
	// Body matches the SI-TX OtcOffer wire shape (peerOtcOfferReq).
	// Updated 2026-05-16 to pass Fix #6 validation: real ISO currency
	// codes + client-<N>/employee-<N> form for buyerId / sellerId.
	body, _ := json.Marshal(map[string]any{
		"stock":          map[string]any{"ticker": "AAPL"},
		"settlementDate": "2026-12-31",
		"pricePerUnit":   map[string]any{"amount": "180.50", "currency": "USD"},
		"premium":        map[string]any{"amount": "700", "currency": "USD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": "client-1"},
		"sellerId":       map[string]any{"routingNumber": 111, "id": "client-3"},
		"amount":         50,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": "client-1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}

// TestPeerOTC_CreateNegotiation_ForwardsParentOfferId asserts the inbound
// parser carries the SI-TX OtcOffer's parentOfferId (the cross-bank
// cascade-cancel grouping key) through to the gRPC CreateNegotiation request.
// Without it the seller's bank stores remote_parent_* as NULL and can neither
// surface the inbound chain on a bank-owned listing nor cascade-cancel siblings
// on accept.
func TestPeerOTC_CreateNegotiation_ForwardsParentOfferId(t *testing.T) {
	var gotParent *stockpb.PeerForeignBankId
	stub := &stubPeerOTCClient{
		createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
			gotParent = in.GetOffer().GetParentOfferId()
			return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-parent"}}, nil
		},
	}
	r := setupOTCRouter(stub)
	body, _ := json.Marshal(map[string]any{
		"stock":          map[string]any{"ticker": "AAPL"},
		"settlementDate": "2026-12-31",
		"pricePerUnit":   map[string]any{"amount": "180.50", "currency": "USD"},
		"premium":        map[string]any{"amount": "700", "currency": "USD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": "client-1"},
		"sellerId":       map[string]any{"routingNumber": 111, "id": "client-3"},
		"amount":         50,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": "client-1"},
		"parentOfferId":  map[string]any{"routingNumber": 111, "id": "70"},
	})
	req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if gotParent == nil {
		t.Fatal("parent_offer_id was dropped — gRPC request carried no ParentOfferId")
	}
	if gotParent.GetRoutingNumber() != 111 || gotParent.GetId() != "70" {
		t.Errorf("parent_offer_id = {%d,%q}, want {111,\"70\"}", gotParent.GetRoutingNumber(), gotParent.GetId())
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
					Ticker: "AAPL", Amount: 50, PricePerStock: "150.5", Currency: "USD",
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
	// SI-TX §2.5 — monetary amounts MUST serialize as JSON numbers, not
	// quoted strings. Assert on the raw bytes so we catch the quoting
	// regression that a map[string]any decode would silently hide.
	raw := w.Body.String()
	if !strings.Contains(raw, `"pricePerUnit":{"amount":150.5,`) && !strings.Contains(raw, `"amount":150.5,"currency":"USD"`) {
		t.Errorf("pricePerUnit.amount must be unquoted number 150.5; body=%s", raw)
	}
	if strings.Contains(raw, `"amount":"150.5"`) || strings.Contains(raw, `"amount":"700"`) {
		t.Errorf("monetary amount must NOT be quoted; body=%s", raw)
	}
	if !strings.Contains(raw, `"amount":700`) {
		t.Errorf("premium.amount must be unquoted number 700; body=%s", raw)
	}
	// Decode-level sanity: amounts parse back as JSON numbers.
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ppu, _ := got["pricePerUnit"].(map[string]any)
	if _, ok := ppu["amount"].(float64); !ok {
		t.Errorf("pricePerUnit.amount not a JSON number: %#v", ppu["amount"])
	}
	prem, _ := got["premium"].(map[string]any)
	if _, ok := prem["amount"].(float64); !ok {
		t.Errorf("premium.amount not a JSON number: %#v", prem["amount"])
	}
}

// TestPeerOTC_CreateNegotiation_NumericAmount asserts the inbound parser
// accepts a SI-TX §2.5 numeric `amount` (JSON number, not quoted).
func TestPeerOTC_CreateNegotiation_NumericAmount(t *testing.T) {
	stub := &stubPeerOTCClient{
		createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
			// The decimal-string proto field must carry the parsed value.
			if in.GetOffer().GetPricePerStock() != "180.5" {
				t.Errorf("pricePerStock: got %q, want 180.5", in.GetOffer().GetPricePerStock())
			}
			if in.GetOffer().GetPremium() != "700" {
				t.Errorf("premium: got %q, want 700", in.GetOffer().GetPremium())
			}
			return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-2"}}, nil
		},
	}
	r := setupOTCRouter(stub)
	// amount fields are bare JSON numbers (not quoted) per §2.5.
	body := []byte(`{
		"stock":{"ticker":"AAPL"},
		"settlementDate":"2026-12-31",
		"pricePerUnit":{"amount":180.5,"currency":"USD"},
		"premium":{"amount":700,"currency":"USD"},
		"buyerId":{"routingNumber":222,"id":"client-1"},
		"sellerId":{"routingNumber":111,"id":"client-3"},
		"amount":50,
		"lastModifiedBy":{"routingNumber":222,"id":"client-1"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("numeric amount must parse: status=%d body=%s", w.Code, w.Body.String())
	}
}

// otcOfferBody builds a spec-shaped OtcOffer (peerOtcOfferReq) body with the
// given buyer/seller ids, for the inbound-validation tests below.
func otcOfferBody(buyerID, sellerID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"stock":          map[string]any{"ticker": "AAPL"},
		"settlementDate": "2026-12-31",
		"pricePerUnit":   map[string]any{"amount": "180.50", "currency": "USD"},
		"premium":        map[string]any{"amount": "700", "currency": "USD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": buyerID},
		"sellerId":       map[string]any{"routingNumber": 111, "id": sellerID},
		"amount":         50,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": buyerID},
	})
	return b
}

// TestPeerOTC_CreateNegotiation_OpaqueBuyerId asserts that, per SI-TX §2.3,
// the gateway does NOT interpret the PEER's opaque buyerId.id. A buyer id that
// is NOT in our local "client-<N>"/"employee-<N>" form (a UUID, "acc-42", …)
// must be accepted and stored verbatim, so long as it is non-empty and ≤64
// bytes. Rejecting it was a spec violation (we were format-checking another
// bank's opaque id) that broke interop with peers using a different id scheme.
func TestPeerOTC_CreateNegotiation_OpaqueBuyerId(t *testing.T) {
	cases := []struct {
		name    string
		buyerID string
	}{
		{"uuid buyer id", "550e8400-e29b-41d4-a716-446655440000"},
		{"acc-prefixed buyer id", "acc-42"},
		{"opaque scheme buyer id", "BANK222::user::99"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBuyer string
			stub := &stubPeerOTCClient{
				createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
					gotBuyer = in.GetBuyerId().GetId()
					return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-op"}}, nil
				},
			}
			r := setupOTCRouter(stub)
			// seller is OURS (client-3 resolves downstream); buyer is the peer's opaque id.
			req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(otcOfferBody(tc.buyerID, "client-3")))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				t.Fatalf("opaque buyer id %q must be accepted (no interpretation): status=%d body=%s", tc.buyerID, w.Code, w.Body.String())
			}
			if gotBuyer != tc.buyerID {
				t.Errorf("buyer id stored = %q, want verbatim %q", gotBuyer, tc.buyerID)
			}
		})
	}
}

// TestPeerOTC_CreateNegotiation_SellerBankForm asserts our-side seller id
// "bank" is accepted (parseSellerOwner resolves it downstream) — the old rigid
// ^(client|employee)-\d+$ regex wrongly rejected the literal "bank".
func TestPeerOTC_CreateNegotiation_SellerBankForm(t *testing.T) {
	stub := &stubPeerOTCClient{
		createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
			return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-bank"}}, nil
		},
	}
	r := setupOTCRouter(stub)
	req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(otcOfferBody("client-1", "bank")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf(`seller id "bank" must be accepted: status=%d body=%s`, w.Code, w.Body.String())
	}
}

// TestPeerOTC_CreateNegotiation_RejectsOverlongOrEmptyId asserts the §2.3 max
// 64-byte / non-empty bound is still enforced on participant ids (the real
// invariant, replacing the format regex).
func TestPeerOTC_CreateNegotiation_RejectsOverlongOrEmptyId(t *testing.T) {
	over := strings.Repeat("x", 65) // 65 bytes > §2.3 max of 64
	cases := []struct {
		name              string
		buyerID, sellerID string
	}{
		{"empty buyer id", "", "client-3"},
		{"empty seller id", "client-1", ""},
		{"overlong buyer id", over, "client-3"},
		{"overlong seller id", "client-1", over},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubPeerOTCClient{
				createFn: func(ctx context.Context, in *stockpb.CreateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
					t.Fatal("gRPC must not be called for an invalid id")
					return nil, nil
				},
			}
			r := setupOTCRouter(stub)
			req := httptest.NewRequest(http.MethodPost, "/negotiations", bytes.NewReader(otcOfferBody(tc.buyerID, tc.sellerID)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
		})
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

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubPeerTxClient struct {
	transactionpb.PeerTxServiceClient
	newTxFn    func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error)
	commitFn   func(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error)
	rollbackFn func(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error)
	initiateFn func(ctx context.Context, in *transactionpb.SiTxInitiateRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error)
}

func (s *stubPeerTxClient) HandleNewTx(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
	if s.newTxFn != nil {
		return s.newTxFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerTxClient) HandleCommitTx(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	if s.commitFn != nil {
		return s.commitFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerTxClient) HandleRollbackTx(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	if s.rollbackFn != nil {
		return s.rollbackFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerTxClient) InitiateOutboundTx(ctx context.Context, in *transactionpb.SiTxInitiateRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	if s.initiateFn != nil {
		return s.initiateFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}

func setupPeerTxRouter(client transactionpb.PeerTxServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerTxHandler(client)
	r.POST("/interbank", func(c *gin.Context) {
		c.Set("peer_bank_code", "222")
		c.Set("peer_routing_number", int64(222))
		h.PostInterbank(c)
	})
	return r
}

func TestPeerTxHandler_NewTx_Unimplemented_Returns501(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})

	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 (Unimplemented passthrough), got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPeerTxHandler_NewTx_YesPassthrough(t *testing.T) {
	client := &stubPeerTxClient{
		newTxFn: func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
			return &transactionpb.SiTxVoteResponse{Type: "YES", TransactionId: "tx-1"}, nil
		},
	}
	r := setupPeerTxRouter(client)

	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["type"] != "YES" {
		t.Errorf("unexpected response: %v", got)
	}
}

func TestPeerTxHandler_UnknownMessageType_Returns400(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "UNKNOWN",
		"message":        map[string]any{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: %d", w.Code)
	}
}

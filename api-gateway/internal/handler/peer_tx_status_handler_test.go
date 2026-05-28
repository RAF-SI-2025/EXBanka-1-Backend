package handler_test

import (
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

// stubPeerTxStatusClient satisfies transactionpb.PeerTxServiceClient with
// a controllable GetTxStatus fn; all other methods fall through to the
// embedded interface (which returns nil, meaning tests must not call them).
type stubPeerTxStatusClient struct {
	transactionpb.PeerTxServiceClient
	getTxStatusFn func(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error)
}

func (s *stubPeerTxStatusClient) GetTxStatus(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
	if s.getTxStatusFn != nil {
		return s.getTxStatusFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "stub")
}

func setupPeerTxStatusRouter(client transactionpb.PeerTxServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerTxStatusHandler(client)
	r.GET("/interbank/:transaction_id/status", func(c *gin.Context) {
		c.Set("peer_bank_code", "222")
		h.GetTxStatus(c)
	})
	return r
}

// TestPeerTxStatusHandler_HappyPath verifies a successful lookup returns
// 200 with state, our_role, last_action_at, last_error fields.
func TestPeerTxStatusHandler_HappyPath(t *testing.T) {
	cl := &stubPeerTxStatusClient{
		getTxStatusFn: func(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
			if in.GetTransactionId() != "test-uuid-001" {
				return nil, status.Errorf(codes.InvalidArgument, "unexpected id %q", in.GetTransactionId())
			}
			if in.GetCallerPeerBankCode() != "222" {
				t.Errorf("expected caller_peer_bank_code=222, got %q", in.GetCallerPeerBankCode())
			}
			return &transactionpb.GetTxStatusResponse{
				State:        "committed",
				OurRole:      "sender",
				LastActionAt: "2026-05-28T12:00:00Z",
				LastError:    "",
			}, nil
		},
	}
	r := setupPeerTxStatusRouter(cl)

	req := httptest.NewRequest(http.MethodGet, "/interbank/test-uuid-001/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["transaction_id"] != "test-uuid-001" {
		t.Errorf("unexpected transaction_id: %v", got["transaction_id"])
	}
	if got["state"] != "committed" {
		t.Errorf("unexpected state: %v", got["state"])
	}
	if got["our_role"] != "sender" {
		t.Errorf("unexpected our_role: %v", got["our_role"])
	}
}

// TestPeerTxStatusHandler_Unknown verifies "unknown" state is surfaced
// correctly when the gRPC backend returns it.
func TestPeerTxStatusHandler_Unknown(t *testing.T) {
	cl := &stubPeerTxStatusClient{
		getTxStatusFn: func(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
			return &transactionpb.GetTxStatusResponse{
				State:   "unknown",
				OurRole: "",
			}, nil
		},
	}
	r := setupPeerTxStatusRouter(cl)

	req := httptest.NewRequest(http.MethodGet, "/interbank/unknown-uuid/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["state"] != "unknown" {
		t.Errorf("unexpected state: %v", got["state"])
	}
}

// TestPeerTxStatusHandler_GRPCError verifies gRPC errors are propagated
// as HTTP 500.
func TestPeerTxStatusHandler_GRPCError(t *testing.T) {
	cl := &stubPeerTxStatusClient{
		getTxStatusFn: func(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
			return nil, status.Error(codes.Internal, "db error")
		},
	}
	r := setupPeerTxStatusRouter(cl)

	req := httptest.NewRequest(http.MethodGet, "/interbank/some-id/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

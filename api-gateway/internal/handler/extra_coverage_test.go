// api-gateway/internal/handler/extra_coverage_test.go
//
// Additional table-driven tests for handler functions that were below 60%
// coverage prior to this batch: role-permission grants, peer-OTC update,
// transaction recipient update, peer-bank-admin happy paths, and a few
// auth_handler.Client branches.
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
)

// ---------------------------------------------------------------------------
// RoleHandler — AssignPermissionToRole / RevokePermissionFromRole
// ---------------------------------------------------------------------------

func rolePermsRouter(h *handler.RoleHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(1)) }
	r.POST("/roles/:id/permissions", withCtx, h.AssignPermissionToRole)
	r.DELETE("/roles/:id/permissions/:permission", withCtx, h.RevokePermissionFromRole)
	return r
}

func TestRole_AssignPermissionToRole_Success(t *testing.T) {
	user := &stubUserClient{
		assignRolePermFn: func(req *userpb.AssignPermissionToRoleRequest) (*userpb.AssignPermissionToRoleResponse, error) {
			require.Equal(t, uint64(7), req.RoleId)
			require.Equal(t, "clients.read.all", req.Permission)
			return &userpb.AssignPermissionToRoleResponse{}, nil
		},
	}
	r := rolePermsRouter(handler.NewRoleHandler(user))
	body := `{"permission":"clients.read.all"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/roles/7/permissions", strings.NewReader(body)))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRole_AssignPermissionToRole_BadID(t *testing.T) {
	r := rolePermsRouter(handler.NewRoleHandler(&stubUserClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/roles/abc/permissions", strings.NewReader(`{"permission":"x"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_AssignPermissionToRole_BadBody(t *testing.T) {
	r := rolePermsRouter(handler.NewRoleHandler(&stubUserClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/roles/7/permissions", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_AssignPermissionToRole_GRPCError(t *testing.T) {
	user := &stubUserClient{
		assignRolePermFn: func(*userpb.AssignPermissionToRoleRequest) (*userpb.AssignPermissionToRoleResponse, error) {
			return nil, status.Error(codes.NotFound, "role missing")
		},
	}
	r := rolePermsRouter(handler.NewRoleHandler(user))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/roles/7/permissions", strings.NewReader(`{"permission":"x"}`)))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRole_RevokePermissionFromRole_Success(t *testing.T) {
	user := &stubUserClient{
		revokeRolePermFn: func(req *userpb.RevokePermissionFromRoleRequest) (*userpb.RevokePermissionFromRoleResponse, error) {
			require.Equal(t, uint64(7), req.RoleId)
			require.Equal(t, "clients.read.all", req.Permission)
			return &userpb.RevokePermissionFromRoleResponse{}, nil
		},
	}
	r := rolePermsRouter(handler.NewRoleHandler(user))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/roles/7/permissions/clients.read.all", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRole_RevokePermissionFromRole_BadID(t *testing.T) {
	r := rolePermsRouter(handler.NewRoleHandler(&stubUserClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/roles/abc/permissions/x", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_RevokePermissionFromRole_GRPCError(t *testing.T) {
	user := &stubUserClient{
		revokeRolePermFn: func(*userpb.RevokePermissionFromRoleRequest) (*userpb.RevokePermissionFromRoleResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	r := rolePermsRouter(handler.NewRoleHandler(user))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/roles/7/permissions/x", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// PeerOTCHandler.UpdateNegotiation
// ---------------------------------------------------------------------------

func TestPeerOTC_UpdateNegotiation_Success(t *testing.T) {
	stub := &stubPeerOTCClient{
		updateFn: func(ctx context.Context, in *stockpb.UpdateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.UpdateNegotiationResponse, error) {
			require.Equal(t, int64(222), in.NegotiationId.RoutingNumber)
			require.Equal(t, "neg-1", in.NegotiationId.Id)
			require.Equal(t, "AAPL", in.Offer.Ticker)
			return &stockpb.UpdateNegotiationResponse{}, nil
		},
	}
	r := setupOTCRouter(stub)
	body, _ := json.Marshal(map[string]any{
		"stock":          map[string]any{"ticker": "AAPL"},
		"settlementDate": "2026-12-31",
		"pricePerUnit":   map[string]any{"amount": "200", "currency": "USD"},
		"premium":        map[string]any{"amount": "10", "currency": "USD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": "client-1"},
		"sellerId":       map[string]any{"routingNumber": 111, "id": "client-3"},
		"amount":         50,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": "client-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/negotiations/222/neg-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPeerOTC_UpdateNegotiation_BadRid(t *testing.T) {
	stub := &stubPeerOTCClient{}
	r := setupOTCRouter(stub)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/negotiations/abc/neg-1", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerOTC_UpdateNegotiation_BadBody(t *testing.T) {
	stub := &stubPeerOTCClient{}
	r := setupOTCRouter(stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/negotiations/222/neg-1", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerOTC_UpdateNegotiation_GRPCError(t *testing.T) {
	stub := &stubPeerOTCClient{
		updateFn: func(ctx context.Context, in *stockpb.UpdateNegotiationRequest, opts ...grpc.CallOption) (*stockpb.UpdateNegotiationResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	r := setupOTCRouter(stub)
	body, _ := json.Marshal(map[string]any{
		"stock":          map[string]any{"ticker": "AAPL"},
		"settlementDate": "2026-12-31",
		"pricePerUnit":   map[string]any{"amount": "200", "currency": "USD"},
		"premium":        map[string]any{"amount": "10", "currency": "USD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": "client-1"},
		"sellerId":       map[string]any{"routingNumber": 111, "id": "client-3"},
		"amount":         50,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": "client-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/negotiations/222/neg-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------------------------------------------------------------------------
// TransactionHandler.UpdatePaymentRecipient
// ---------------------------------------------------------------------------

func TestTx_UpdatePaymentRecipient_Success(t *testing.T) {
	new1 := "Jane Doe"
	new2 := "265-12-22"
	tx := &stubTransactionClient{
		getRecipFn: func(in *transactionpb.GetPaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			return &transactionpb.PaymentRecipientResponse{Id: in.Id, ClientId: 7}, nil
		},
		updateRecipFn: func(in *transactionpb.UpdatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			require.Equal(t, uint64(3), in.Id)
			require.NotNil(t, in.RecipientName)
			require.Equal(t, new1, *in.RecipientName)
			require.NotNil(t, in.AccountNumber)
			require.Equal(t, new2, *in.AccountNumber)
			return &transactionpb.PaymentRecipientResponse{Id: in.Id, RecipientName: new1, AccountNumber: new2}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	gin.SetMode(gin.TestMode)
	r2 := gin.New()
	r2.PUT("/payment-recipients/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.UpdatePaymentRecipient(c)
	})

	body := `{"recipient_name":"Jane Doe","account_number":"265-12-22"}`
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest("PUT", "/payment-recipients/3", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	_ = r // suppress
}

func TestTx_UpdatePaymentRecipient_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/payment-recipients/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.UpdatePaymentRecipient(c)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/payment-recipients/abc", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_UpdatePaymentRecipient_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/payment-recipients/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.UpdatePaymentRecipient(c)
	})
	req := httptest.NewRequest("PUT", "/payment-recipients/3", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_UpdatePaymentRecipient_OwnershipMismatch(t *testing.T) {
	tx := &stubTransactionClient{
		getRecipFn: func(in *transactionpb.GetPaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			return &transactionpb.PaymentRecipientResponse{Id: in.Id, ClientId: 999}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/payment-recipients/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.UpdatePaymentRecipient(c)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/payment-recipients/3", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------------------------------------------------------------------------
// PeerBankAdminHandler — error paths not yet covered
// ---------------------------------------------------------------------------

func TestPeerBankAdmin_Get_BadID(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/peer-banks/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerBankAdmin_Get_NotFound(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/peer-banks/9999", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPeerBankAdmin_Create_BadBody(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/peer-banks", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerBankAdmin_Create_MissingFields(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	body := `{"bank_code":"222"}` // missing routing_number, base_url, api_token
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/peer-banks", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerBankAdmin_Update_BadID(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/peer-banks/abc", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerBankAdmin_Update_BadBody(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/peer-banks/1", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerBankAdmin_Update_AllFields(t *testing.T) {
	client := &stubPeerBankAdminClient{}
	_, _ = client.CreatePeerBank(context.Background(), &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "t", Active: true,
	})
	r := setupAdminRouter(client)
	body, _ := json.Marshal(map[string]any{
		"base_url":          "http://y",
		"api_token":         "new-tok",
		"hmac_inbound_key":  "in",
		"hmac_outbound_key": "out",
		"active":            false,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/peer-banks/1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPeerBankAdmin_Delete_BadID(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/peer-banks/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---------------------------------------------------------------------------
// PeerTxHandler — additional message types and error paths
// ---------------------------------------------------------------------------

func TestPeerTxHandler_CommitTx_NoContent(t *testing.T) {
	client := &stubPeerTxClient{
		commitFn: func(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
			require.Equal(t, "tx-1", in.TransactionId)
			return &transactionpb.SiTxAckResponse{}, nil
		},
	}
	r := setupPeerTxRouter(client)
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "COMMIT_TX",
		"message":        map[string]any{"transactionId": "tx-1"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPeerTxHandler_RollbackTx_NoContent(t *testing.T) {
	client := &stubPeerTxClient{
		rollbackFn: func(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
			require.Equal(t, "tx-1", in.TransactionId)
			return &transactionpb.SiTxAckResponse{}, nil
		},
	}
	r := setupPeerTxRouter(client)
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "ROLLBACK_TX",
		"message":        map[string]any{"transactionId": "tx-1"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPeerTxHandler_BadJSON_Returns400(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})
	req := httptest.NewRequest("POST", "/interbank", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPeerTxHandler_MissingMessageType_Returns400(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPeerTxHandler_MissingIdempotenceKey_Returns400(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": ""},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPeerTxHandler_NewTx_NoVotes(t *testing.T) {
	idx := int32(0)
	idxSet := true
	client := &stubPeerTxClient{
		newTxFn: func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
			return &transactionpb.SiTxVoteResponse{
				Type: "NO",
				NoVotes: []*transactionpb.SiTxNoVote{
					{Reason: "INSUFFICIENT_ASSET", PostingIndex: idx, PostingIndexSet: idxSet},
					{Reason: "NO_SUCH_ACCOUNT"},
				},
			}, nil
		},
	}
	r := setupPeerTxRouter(client)
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "INSUFFICIENT_ASSET")
}

func TestPeerTxHandler_NewTx_InvalidArg_Returns400(t *testing.T) {
	client := &stubPeerTxClient{
		newTxFn: func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "bad")
		},
	}
	r := setupPeerTxRouter(client)
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPeerTxHandler_NewTx_OtherError_Returns500(t *testing.T) {
	client := &stubPeerTxClient{
		newTxFn: func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	r := setupPeerTxRouter(client)
	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/interbank", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// ---------------------------------------------------------------------------
// AuthHandler.Client — accessor
// ---------------------------------------------------------------------------

func TestAuthHandler_Client_ReturnsUnderlying(t *testing.T) {
	stub := &stubAuthClient{}
	h := handler.NewAuthHandler(stub)
	require.Equal(t, stub, h.Client())
}

// keeps tests from being placed before TestTx_UpdatePaymentRecipient_GRPCError when patches reorder
func TestTx_UpdatePaymentRecipient_GRPCError(t *testing.T) {
	tx := &stubTransactionClient{
		getRecipFn: func(in *transactionpb.GetPaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			return &transactionpb.PaymentRecipientResponse{Id: in.Id, ClientId: 7}, nil
		},
		updateRecipFn: func(*transactionpb.UpdatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/payment-recipients/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.UpdatePaymentRecipient(c)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/payment-recipients/3", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

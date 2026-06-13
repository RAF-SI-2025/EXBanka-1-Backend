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
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakePeerTxClient implements the full transactionpb.PeerTxServiceClient
// interface. It captures the last NEW_TX request and returns a configurable
// vote type so tests can assert the translated, enriched gRPC request.
type fakePeerTxClient struct {
	voteType  string
	pending   bool
	noVotes   []*transactionpb.SiTxNoVote
	lastNewTx *transactionpb.SiTxNewTxRequest
}

func (f *fakePeerTxClient) HandleNewTx(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error) {
	f.lastNewTx = in
	return &transactionpb.SiTxVoteResponse{Type: f.voteType, Pending: f.pending, NoVotes: f.noVotes}, nil
}

func (f *fakePeerTxClient) HandleCommitTx(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	return &transactionpb.SiTxAckResponse{}, nil
}

func (f *fakePeerTxClient) HandleRollbackTx(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error) {
	return &transactionpb.SiTxAckResponse{}, nil
}

func (f *fakePeerTxClient) InitiateOutboundTx(ctx context.Context, in *transactionpb.SiTxInitiateRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	return nil, nil
}

func (f *fakePeerTxClient) InitiateOutboundTxWithPostings(ctx context.Context, in *transactionpb.SiTxInitiateWithPostingsRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	return nil, nil
}

func (f *fakePeerTxClient) GetTxStatus(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
	return nil, nil
}

// stubPeerTxClient implements the full transactionpb.PeerTxServiceClient
// interface with optional per-method hooks; unhooked methods return
// Unimplemented so the handler's 501 passthrough can be exercised.
type stubPeerTxClient struct {
	newTxFn       func(ctx context.Context, in *transactionpb.SiTxNewTxRequest, opts ...grpc.CallOption) (*transactionpb.SiTxVoteResponse, error)
	commitFn      func(ctx context.Context, in *transactionpb.SiTxCommitRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error)
	rollbackFn    func(ctx context.Context, in *transactionpb.SiTxRollbackRequest, opts ...grpc.CallOption) (*transactionpb.SiTxAckResponse, error)
	initiateFn    func(ctx context.Context, in *transactionpb.SiTxInitiateRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error)
	getTxStatusFn func(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error)
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
func (s *stubPeerTxClient) InitiateOutboundTxWithPostings(ctx context.Context, in *transactionpb.SiTxInitiateWithPostingsRequest, opts ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "stub")
}
func (s *stubPeerTxClient) GetTxStatus(ctx context.Context, in *transactionpb.GetTxStatusRequest, opts ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
	if s.getTxStatusFn != nil {
		return s.getTxStatusFn(ctx, in, opts...)
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

func TestPostInterbank_NewTx_SpecShape(t *testing.T) {
	fake := &fakePeerTxClient{voteType: "YES"}
	h := handler.NewPeerTxHandler(fake)

	body := `{"idempotenceKey":{"routingNumber":222,"locallyGeneratedKey":"k1"},"messageType":"NEW_TX","message":{"postings":[{"account":{"type":"ACCOUNT","num":"444000100182503611"},"amount":-260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}},{"account":{"type":"ACCOUNT","num":"111000141215476411"},"amount":260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}}],"transactionId":{"routingNumber":222,"id":"k1"},"message":"coffee","paymentCode":"289","paymentPurpose":"debt"}}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("peer_bank_code", "222")
	c.Request = httptest.NewRequest(http.MethodPost, "/interbank", strings.NewReader(body))

	h.PostInterbank(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"vote":"YES"}` {
		t.Fatalf("vote body: %s", w.Body.String())
	}
	if len(fake.lastNewTx.GetPostings()) != 2 {
		t.Fatalf("postings not forwarded")
	}
	if fake.lastNewTx.GetPostings()[0].GetDirection() != "DEBIT" { // -260 → DEBIT
		t.Fatalf("inversion wrong: %s", fake.lastNewTx.GetPostings()[0].GetDirection())
	}
	if fake.lastNewTx.GetPostings()[1].GetDirection() != "CREDIT" { // +260 → CREDIT
		t.Fatalf("inversion wrong (positive leg): %s", fake.lastNewTx.GetPostings()[1].GetDirection())
	}
	if fake.lastNewTx.GetPostings()[1].GetAmount() != "260" {
		t.Fatalf("positive-leg amount not forwarded: %s", fake.lastNewTx.GetPostings()[1].GetAmount())
	}
	if fake.lastNewTx.GetPostings()[0].GetAccountType() != "ACCOUNT" || fake.lastNewTx.GetPostings()[0].GetAssetType() != "MONAS" {
		t.Fatalf("type tags not forwarded: %+v", fake.lastNewTx.GetPostings()[0])
	}
	if fake.lastNewTx.GetTransactionId().GetId() != "k1" || fake.lastNewTx.GetMessage() != "coffee" || fake.lastNewTx.GetPaymentCode() != "289" {
		t.Fatalf("metadata/tx-id not forwarded: %+v", fake.lastNewTx)
	}
}

func TestPostInterbank_NewTx_Pending_Returns202(t *testing.T) {
	fake := &fakePeerTxClient{pending: true}
	h := handler.NewPeerTxHandler(fake)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("peer_bank_code", "222")
	c.Request = httptest.NewRequest(http.MethodPost, "/interbank",
		strings.NewReader(`{"idempotenceKey":{"routingNumber":222,"locallyGeneratedKey":"k1"},"messageType":"NEW_TX","message":{"postings":[],"transactionId":{"routingNumber":222,"id":"k1"},"message":"","paymentCode":"","paymentPurpose":""}}`))
	h.PostInterbank(c)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("202 must have empty body, got %s", w.Body.String())
	}
}

func TestPostInterbank_NewTx_NoVote_ReattachesPosting(t *testing.T) {
	fake := &fakePeerTxClient{
		voteType: "NO",
		noVotes: []*transactionpb.SiTxNoVote{
			{Reason: "INSUFFICIENT_ASSET", PostingIndex: 1, PostingIndexSet: true},
		},
	}
	h := handler.NewPeerTxHandler(fake)

	body := `{"idempotenceKey":{"routingNumber":222,"locallyGeneratedKey":"k1"},"messageType":"NEW_TX","message":{"postings":[{"account":{"type":"ACCOUNT","num":"444000100182503611"},"amount":-260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}},{"account":{"type":"ACCOUNT","num":"111000141215476411"},"amount":260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}}],"transactionId":{"routingNumber":222,"id":"k1"},"message":"coffee","paymentCode":"289","paymentPurpose":"debt"}}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("peer_bank_code", "222")
	c.Request = httptest.NewRequest(http.MethodPost, "/interbank", strings.NewReader(body))

	h.PostInterbank(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}

	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v body=%s", err, w.Body.String())
	}
	if got["vote"] != "NO" {
		t.Fatalf("expected NO vote, got: %v", got["vote"])
	}
	reasons, ok := got["reasons"].([]interface{})
	if !ok || len(reasons) != 1 {
		t.Fatalf("expected 1 reason, got: %v", got["reasons"])
	}
	reason0, ok := reasons[0].(map[string]interface{})
	if !ok {
		t.Fatalf("reason[0] not an object: %v", reasons[0])
	}
	if reason0["reason"] != "INSUFFICIENT_ASSET" {
		t.Fatalf("reason[0].reason: %v", reason0["reason"])
	}
	posting, ok := reason0["posting"].(map[string]interface{})
	if !ok {
		t.Fatalf("reason[0].posting missing/not an object: %v", reason0["posting"])
	}
	// FULL posting at index 1 (the +260 leg) must be re-attached.
	account, ok := posting["account"].(map[string]interface{})
	if !ok || account["num"] != "111000141215476411" {
		t.Fatalf("reason[0].posting.account.num: %v", posting["account"])
	}
	if amt, ok := posting["amount"].(float64); !ok || amt != 260 {
		t.Fatalf("reason[0].posting.amount: %v (want JSON number 260)", posting["amount"])
	}
	asset, ok := posting["asset"].(map[string]interface{})
	if !ok {
		t.Fatalf("reason[0].posting.asset missing: %v", posting["asset"])
	}
	innerAsset, ok := asset["asset"].(map[string]interface{})
	if !ok || innerAsset["currency"] != "RSD" {
		t.Fatalf("reason[0].posting.asset.asset.currency: %v", asset["asset"])
	}
}

func TestPeerTxHandler_NewTx_Unimplemented_Returns501(t *testing.T) {
	r := setupPeerTxRouter(&stubPeerTxClient{})

	body := map[string]any{
		"idempotenceKey": map[string]any{"routingNumber": 333, "locallyGeneratedKey": "k1"},
		"messageType":    "NEW_TX",
		"message":        map[string]any{"postings": []any{}, "transactionId": map[string]any{"routingNumber": 333, "id": "k1"}},
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
		"message":        map[string]any{"postings": []any{}, "transactionId": map[string]any{"routingNumber": 333, "id": "k1"}},
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
	if got["vote"] != "YES" {
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

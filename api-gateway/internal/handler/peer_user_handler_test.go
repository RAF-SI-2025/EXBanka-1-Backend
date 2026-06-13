package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// testBankDisplayName is the display name interbank-service returns; the gateway
// echoes it through verbatim as bankDisplayName (§9).
const testBankDisplayName = "EXBanka Test"

// stubPeerUserClient is a fake interbank PeerUserService client. It embeds the
// interface (nil) so it satisfies the type; only ResolvePeerUser is overridden.
type stubPeerUserClient struct {
	transactionpb.PeerUserServiceClient
	resp   *transactionpb.ResolvePeerUserResponse
	err    error
	gotReq *transactionpb.ResolvePeerUserRequest
}

func (s *stubPeerUserClient) ResolvePeerUser(_ context.Context, in *transactionpb.ResolvePeerUserRequest, _ ...grpc.CallOption) (*transactionpb.ResolvePeerUserResponse, error) {
	s.gotReq = in
	return s.resp, s.err
}

func setupPeerUserRouter(c transactionpb.PeerUserServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerUserHandler(c)
	r.GET("/user/:rid/:id", h.GetUser)
	return r
}

// found ⇒ 200 with {bankDisplayName, displayName}; rid+id are forwarded to interbank.
func TestPeerUser_Found(t *testing.T) {
	stub := &stubPeerUserClient{resp: &transactionpb.ResolvePeerUserResponse{
		Found: true, BankDisplayName: testBankDisplayName, DisplayName: "Marko Marković",
	}}
	r := setupPeerUserRouter(stub)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-7", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if stub.gotReq.GetRoutingNumber() != 111 || stub.gotReq.GetId() != "client-7" {
		t.Errorf("forwarded req = %+v, want rid=111 id=client-7", stub.gotReq)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["bankDisplayName"] != testBankDisplayName || got["displayName"] != "Marko Marković" {
		t.Errorf("body = %+v", got)
	}
}

// interbank reports found=false (unknown user OR foreign routing) ⇒ 404.
func TestPeerUser_NotFound_404(t *testing.T) {
	r := setupPeerUserRouter(&stubPeerUserClient{resp: &transactionpb.ResolvePeerUserResponse{Found: false}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-999", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerUser_BadRid_400(t *testing.T) {
	r := setupPeerUserRouter(&stubPeerUserClient{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/notnumeric/client-7", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: %d", w.Code)
	}
}

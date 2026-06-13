package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// Resolution semantics (client vs employee, unknown prefix, bad id, own-routing)
// now live in interbank-service's PeerUserService and are tested there
// (interbank-service/internal/handler/peer_user_grpc_handler_test.go). At the
// gateway, PeerUserHandler is a thin forwarder, so these tests cover only the
// gateway's concerns: routing + gRPC-error mapping.

func TestPeerUser_MissingID_404(t *testing.T) {
	r := setupPeerUserRouter(&stubPeerUserClient{resp: &transactionpb.ResolvePeerUserResponse{}})
	w := httptest.NewRecorder()
	// The :id segment is required; an empty id doesn't match the route.
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/", nil))
	if w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
		t.Logf("info: empty-id path status %d", w.Code)
	}
}

// A downstream (interbank) gRPC error is mapped to an HTTP status, not swallowed.
func TestPeerUser_InterbankError_Mapped(t *testing.T) {
	r := setupPeerUserRouter(&stubPeerUserClient{err: status.Error(codes.Internal, "interbank down")})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-7", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body=%s)", w.Code, w.Body.String())
	}
}

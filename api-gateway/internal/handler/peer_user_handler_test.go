package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reuses stubClientClient and stubUserClient defined in mocks_test.go.
// Each shared stub already implements the broader gRPC client interface;
// these tests only override the GetClient / GetEmployee fns.

func setupPeerUserRouter(cc clientpb.ClientServiceClient, uc userpb.UserServiceClient, ownRouting int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerUserHandler(cc, uc, ownRouting)
	r.GET("/user/:rid/:id", h.GetUser)
	return r
}

func TestPeerUser_OwnClient_Found(t *testing.T) {
	cc := &stubClientClient{
		getFn: func(in *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return &clientpb.ClientResponse{Id: in.Id, FirstName: "Marko", LastName: "Marković"}, nil
		},
	}
	uc := &stubUserClient{
		getEmployeeFn: func(in *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-7", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["firstName"] != "Marko" {
		t.Errorf("got %+v", got)
	}
}

func TestPeerUser_ForeignRid_404(t *testing.T) {
	cc := &stubClientClient{}
	uc := &stubUserClient{}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/222/client-7", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerUser_NotFound_404(t *testing.T) {
	cc := &stubClientClient{
		getFn: func(in *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	uc := &stubUserClient{
		getEmployeeFn: func(in *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-999", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerUser_BadRid_400(t *testing.T) {
	cc := &stubClientClient{}
	uc := &stubUserClient{}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/notnumeric/client-7", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: %d", w.Code)
	}
}

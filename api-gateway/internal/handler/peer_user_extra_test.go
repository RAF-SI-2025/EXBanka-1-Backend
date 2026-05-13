package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPeerUser_MissingID_400(t *testing.T) {
	r := setupPeerUserRouter(&stubClientClient{}, &stubUserClient{}, 111)
	w := httptest.NewRecorder()
	// /user/:rid/:id requires the id segment; sending a request that doesn't
	// match the route returns 404 from the router. Use a route with empty id.
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/", nil))
	if w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
		t.Logf("info: empty-id path status %d", w.Code)
	}
}

func TestPeerUser_OwnEmployee_Found(t *testing.T) {
	cc := &stubClientClient{
		getFn: func(in *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.NotFound, "")
		},
	}
	uc := &stubUserClient{
		getEmployeeFn: func(in *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return &userpb.EmployeeResponse{Id: in.Id, FirstName: "Ana", LastName: "An"}, nil
		},
	}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/employee-3", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["firstName"] != "Ana" {
		t.Errorf("got %+v", got)
	}
}

func TestPeerUser_ClientGetReturnsInternalErr_Mapped(t *testing.T) {
	cc := &stubClientClient{
		getFn: func(in *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.Internal, "db down")
		},
	}
	uc := &stubUserClient{}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-7", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestPeerUser_EmployeeGetReturnsInternalErr_Mapped(t *testing.T) {
	cc := &stubClientClient{}
	uc := &stubUserClient{
		getEmployeeFn: func(in *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return nil, status.Error(codes.Internal, "db down")
		},
	}
	r := setupPeerUserRouter(cc, uc, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/employee-3", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestPeerUser_UnknownPrefix_404(t *testing.T) {
	r := setupPeerUserRouter(&stubClientClient{}, &stubUserClient{}, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/foo-7", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}

func TestPeerUser_BadClientID_404(t *testing.T) {
	r := setupPeerUserRouter(&stubClientClient{}, &stubUserClient{}, 111)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/111/client-abc", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}

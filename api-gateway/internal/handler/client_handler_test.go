// api-gateway/internal/handler/client_handler_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func clientRouter(h *handler.ClientHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("user_id", int64(1)) }
	r.POST("/clients", withCtx, h.CreateClient)
	r.GET("/clients", withCtx, h.ListClients)
	r.GET("/clients/:id", withCtx, h.GetClient)
	r.PUT("/clients/:id", withCtx, h.UpdateClient)
	r.GET("/clients/me", withCtx, h.GetCurrentClient)
	return r
}

func TestClient_CreateClient_Success(t *testing.T) {
	cl := &stubClientClient{
		createFn: func(req *clientpb.CreateClientRequest) (*clientpb.ClientResponse, error) {
			require.Equal(t, "John", req.FirstName)
			return &clientpb.ClientResponse{Id: 1, FirstName: req.FirstName}, nil
		},
	}
	h := handler.NewClientHandler(cl, &stubAuthClient{})
	r := clientRouter(h)
	body := `{"first_name":"John","last_name":"Doe","date_of_birth":946684800,"email":"j@d.com","jmbg":"1234567890123"}`
	req := httptest.NewRequest("POST", "/clients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"first_name":"John"`)
}

func TestClient_CreateClient_BadEmail(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	r := clientRouter(h)
	body := `{"first_name":"x","last_name":"y","date_of_birth":1,"email":"not-an-email","jmbg":"1234567890123"}`
	req := httptest.NewRequest("POST", "/clients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClient_CreateClient_GRPCError(t *testing.T) {
	cl := &stubClientClient{
		createFn: func(_ *clientpb.CreateClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "duplicate")
		},
	}
	h := handler.NewClientHandler(cl, &stubAuthClient{})
	r := clientRouter(h)
	body := `{"first_name":"x","last_name":"y","date_of_birth":1,"email":"e@e.com","jmbg":"1234567890123"}`
	req := httptest.NewRequest("POST", "/clients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestClient_ListClients_Success(t *testing.T) {
	cl := &stubClientClient{
		listFn: func(_ *clientpb.ListClientsRequest) (*clientpb.ListClientsResponse, error) {
			return &clientpb.ListClientsResponse{
				Clients: []*clientpb.ClientResponse{{Id: 1}, {Id: 2}},
				Total:   2,
			}, nil
		},
	}
	auth := &stubAuthClient{
		getStatusBatchFn: func(req *authpb.GetAccountStatusBatchRequest) (*authpb.GetAccountStatusBatchResponse, error) {
			require.Equal(t, "client", req.PrincipalType)
			return &authpb.GetAccountStatusBatchResponse{
				Entries: []*authpb.AccountStatusEntry{{PrincipalId: 1, Active: true}},
			}, nil
		},
	}
	h := handler.NewClientHandler(cl, auth)
	r := clientRouter(h)
	req := httptest.NewRequest("GET", "/clients", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":2`)
}

func TestClient_GetClient_Success(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	r := clientRouter(h)
	req := httptest.NewRequest("GET", "/clients/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestClient_GetClient_BadID(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	r := clientRouter(h)
	req := httptest.NewRequest("GET", "/clients/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClient_GetClient_NotFound(t *testing.T) {
	cl := &stubClientClient{
		getFn: func(_ *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewClientHandler(cl, &stubAuthClient{})
	r := clientRouter(h)
	req := httptest.NewRequest("GET", "/clients/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestClient_UpdateClient_Success(t *testing.T) {
	cl := &stubClientClient{
		updateFn: func(req *clientpb.UpdateClientRequest) (*clientpb.ClientResponse, error) {
			require.NotNil(t, req.LastName)
			require.Equal(t, "X", *req.LastName)
			return &clientpb.ClientResponse{Id: req.Id, LastName: *req.LastName}, nil
		},
	}
	h := handler.NewClientHandler(cl, &stubAuthClient{})
	r := clientRouter(h)
	body := `{"last_name":"X"}`
	req := httptest.NewRequest("PUT", "/clients/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestClient_UpdateClient_BadID(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	r := clientRouter(h)
	req := httptest.NewRequest("PUT", "/clients/abc", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClient_UpdateClient_WithActiveStatus(t *testing.T) {
	auth := &stubAuthClient{
		setAccountStatusFn: func(req *authpb.SetAccountStatusRequest) (*authpb.SetAccountStatusResponse, error) {
			require.Equal(t, true, req.Active)
			return &authpb.SetAccountStatusResponse{}, nil
		},
	}
	h := handler.NewClientHandler(&stubClientClient{}, auth)
	r := clientRouter(h)
	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/clients/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestClient_GetCurrentClient_Success(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	r := clientRouter(h)
	req := httptest.NewRequest("GET", "/clients/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestClient_GetCurrentClient_NoAuth(t *testing.T) {
	h := handler.NewClientHandler(&stubClientClient{}, &stubAuthClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clients/me", h.GetCurrentClient)
	req := httptest.NewRequest("GET", "/clients/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

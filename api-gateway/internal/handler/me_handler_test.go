package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)

func meRouter(h *handler.MeHandler, sysType string, uid int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me", func(c *gin.Context) {
		c.Set("system_type", sysType)
		c.Set("user_id", uid)
		h.GetMe(c)
	})
	return r
}

func TestMe_GetMe_Client_Success(t *testing.T) {
	cli := &stubClientClient{
		getFn: func(req *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			require.Equal(t, uint64(7), req.Id)
			return &clientpb.ClientResponse{Id: 7, Email: "x@y"}, nil
		},
	}
	auth := &stubAuthClient{
		getAccountStatusFn: func(req *authpb.GetAccountStatusRequest) (*authpb.GetAccountStatusResponse, error) {
			require.Equal(t, "client", req.PrincipalType)
			return &authpb.GetAccountStatusResponse{Active: true}, nil
		},
	}
	h := handler.NewMeHandler(cli, &stubUserClient{}, auth)
	r := meRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"x@y"`)
}

func TestMe_GetMe_Employee_Success(t *testing.T) {
	usr := &stubUserClient{
		getEmployeeFn: func(req *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			require.Equal(t, int64(11), req.Id)
			return &userpb.EmployeeResponse{Id: 11, Email: "e@y"}, nil
		},
	}
	auth := &stubAuthClient{
		getAccountStatusFn: func(req *authpb.GetAccountStatusRequest) (*authpb.GetAccountStatusResponse, error) {
			require.Equal(t, "employee", req.PrincipalType)
			return &authpb.GetAccountStatusResponse{Active: true}, nil
		},
	}
	h := handler.NewMeHandler(&stubClientClient{}, usr, auth)
	r := meRouter(h, "employee", 11)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"e@y"`)
}

func TestMe_GetMe_UnknownSystemType_Returns403(t *testing.T) {
	h := handler.NewMeHandler(&stubClientClient{}, &stubUserClient{}, &stubAuthClient{})
	r := meRouter(h, "alien", 7)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "unknown system type")
}

func TestMe_GetMe_MissingUserID_Returns401(t *testing.T) {
	h := handler.NewMeHandler(&stubClientClient{}, &stubUserClient{}, &stubAuthClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me", func(c *gin.Context) {
		c.Set("system_type", "client")
		// don't set user_id (string instead of int64)
		c.Set("user_id", "not-an-int")
		h.GetMe(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid token claims")
}

func TestMe_GetMe_Client_GRPCError(t *testing.T) {
	cli := &stubClientClient{
		getFn: func(*clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewMeHandler(cli, &stubUserClient{}, &stubAuthClient{})
	r := meRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMe_GetMe_Employee_GRPCError(t *testing.T) {
	usr := &stubUserClient{
		getEmployeeFn: func(*userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewMeHandler(&stubClientClient{}, usr, &stubAuthClient{})
	r := meRouter(h, "employee", 11)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Even when fetching account status fails, the profile must still come back
// (just with active=false logged via WARN).
func TestMe_GetMe_Client_StatusFetchFailure_StillReturns200(t *testing.T) {
	cli := &stubClientClient{
		getFn: func(req *clientpb.GetClientRequest) (*clientpb.ClientResponse, error) {
			return &clientpb.ClientResponse{Id: req.Id}, nil
		},
	}
	auth := &stubAuthClient{
		getAccountStatusFn: func(*authpb.GetAccountStatusRequest) (*authpb.GetAccountStatusResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	h := handler.NewMeHandler(cli, &stubUserClient{}, auth)
	r := meRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

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

	"github.com/exbanka/api-gateway/internal/handler"
	userpb "github.com/exbanka/contract/userpb"
)

func actuaryRouter(h *handler.ActuaryHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "employee")
	}
	r.GET("/api/v3/actuaries", withCtx, h.ListActuaries)
	r.PUT("/api/v3/actuaries/:id/limit", withCtx, h.SetActuaryLimit)
	r.POST("/api/v3/actuaries/:id/reset-limit", withCtx, h.ResetActuaryLimit)
	r.POST("/api/v3/actuaries/:id/require-approval", withCtx, h.RequireApproval)
	r.POST("/api/v3/actuaries/:id/skip-approval", withCtx, h.SkipApproval)
	return r
}

func TestActuary_ListActuaries_Defaults(t *testing.T) {
	called := false
	st := &stubActuaryClient{
		listFn: func(req *userpb.ListActuariesRequest) (*userpb.ListActuariesResponse, error) {
			called = true
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(10), req.PageSize)
			return &userpb.ListActuariesResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	req := httptest.NewRequest("GET", "/api/v3/actuaries", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
	require.Contains(t, rec.Body.String(), `"actuaries":[]`)
}

func TestActuary_ListActuaries_GRPCError(t *testing.T) {
	st := &stubActuaryClient{
		listFn: func(*userpb.ListActuariesRequest) (*userpb.ListActuariesResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	req := httptest.NewRequest("GET", "/api/v3/actuaries", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestActuary_SetActuaryLimit_Success(t *testing.T) {
	st := &stubActuaryClient{
		setLimitFn: func(req *userpb.SetActuaryLimitRequest) (*userpb.ActuaryInfo, error) {
			require.Equal(t, uint64(7), req.Id)
			require.Equal(t, "1000", req.Limit)
			return &userpb.ActuaryInfo{}, nil
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	body := `{"limit":"1000"}`
	req := httptest.NewRequest("PUT", "/api/v3/actuaries/7/limit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestActuary_SetActuaryLimit_BadID(t *testing.T) {
	h := handler.NewActuaryHandler(&stubActuaryClient{})
	r := actuaryRouter(h)
	body := `{"limit":"1000"}`
	req := httptest.NewRequest("PUT", "/api/v3/actuaries/x/limit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid actuary id")
}

func TestActuary_SetActuaryLimit_MissingLimit(t *testing.T) {
	h := handler.NewActuaryHandler(&stubActuaryClient{})
	r := actuaryRouter(h)
	body := `{}`
	req := httptest.NewRequest("PUT", "/api/v3/actuaries/7/limit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "limit is required")
}

func TestActuary_ResetActuaryLimit_Success(t *testing.T) {
	st := &stubActuaryClient{
		resetUsedFn: func(req *userpb.ResetActuaryUsedLimitRequest) (*userpb.ActuaryInfo, error) {
			require.Equal(t, uint64(7), req.Id)
			return &userpb.ActuaryInfo{}, nil
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	req := httptest.NewRequest("POST", "/api/v3/actuaries/7/reset-limit", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestActuary_ResetActuaryLimit_BadID(t *testing.T) {
	h := handler.NewActuaryHandler(&stubActuaryClient{})
	r := actuaryRouter(h)
	req := httptest.NewRequest("POST", "/api/v3/actuaries/x/reset-limit", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestActuary_RequireApproval_Success(t *testing.T) {
	st := &stubActuaryClient{
		setApprFn: func(req *userpb.SetNeedApprovalRequest) (*userpb.ActuaryInfo, error) {
			require.Equal(t, uint64(7), req.Id)
			require.True(t, req.NeedApproval)
			return &userpb.ActuaryInfo{}, nil
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	req := httptest.NewRequest("POST", "/api/v3/actuaries/7/require-approval", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestActuary_SkipApproval_Success(t *testing.T) {
	st := &stubActuaryClient{
		setApprFn: func(req *userpb.SetNeedApprovalRequest) (*userpb.ActuaryInfo, error) {
			require.Equal(t, uint64(7), req.Id)
			require.False(t, req.NeedApproval)
			return &userpb.ActuaryInfo{}, nil
		},
	}
	h := handler.NewActuaryHandler(st)
	r := actuaryRouter(h)
	req := httptest.NewRequest("POST", "/api/v3/actuaries/7/skip-approval", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestActuary_RequireApproval_BadID(t *testing.T) {
	h := handler.NewActuaryHandler(&stubActuaryClient{})
	r := actuaryRouter(h)
	req := httptest.NewRequest("POST", "/api/v3/actuaries/x/require-approval", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

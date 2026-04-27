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
	stockpb "github.com/exbanka/contract/stockpb"
)

func taxRouter(h *handler.TaxHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(42))
		c.Set("principal_type", "client")
	}
	r.GET("/api/v2/tax", withCtx, h.ListTaxRecords)
	r.GET("/api/v2/me/tax", withCtx, h.ListMyTaxRecords)
	r.POST("/api/v2/tax/collect", withCtx, h.CollectTax)
	return r
}

func TestTax_ListTaxRecords_Default(t *testing.T) {
	st := &stubTaxClient{
		listFn: func(req *stockpb.ListTaxRecordsRequest) (*stockpb.ListTaxRecordsResponse, error) {
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(10), req.PageSize)
			return &stockpb.ListTaxRecordsResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/tax", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"tax_records":[]`)
}

func TestTax_ListTaxRecords_BadUserType(t *testing.T) {
	h := handler.NewTaxHandler(&stubTaxClient{})
	r := taxRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/tax?user_type=astronaut", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "user_type must be one of")
}

func TestTax_ListTaxRecords_FilterByUserType(t *testing.T) {
	st := &stubTaxClient{
		listFn: func(req *stockpb.ListTaxRecordsRequest) (*stockpb.ListTaxRecordsResponse, error) {
			require.Equal(t, "client", req.UserType)
			return &stockpb.ListTaxRecordsResponse{}, nil
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/tax?user_type=client", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTax_ListTaxRecords_GRPCError(t *testing.T) {
	st := &stubTaxClient{
		listFn: func(*stockpb.ListTaxRecordsRequest) (*stockpb.ListTaxRecordsResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "")
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/tax", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestTax_ListMyTaxRecords_Success(t *testing.T) {
	st := &stubTaxClient{
		listUserFn: func(req *stockpb.ListUserTaxRecordsRequest) (*stockpb.ListUserTaxRecordsResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, "client", req.SystemType)
			return &stockpb.ListUserTaxRecordsResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/tax", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"records":[]`)
}

func TestTax_ListMyTaxRecords_MissingSystemType(t *testing.T) {
	st := &stubTaxClient{}
	h := handler.NewTaxHandler(st)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/tax", func(c *gin.Context) {
		c.Set("principal_id", int64(42))
		// deliberately omit system_type
		h.ListMyTaxRecords(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/tax", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTax_CollectTax_Success(t *testing.T) {
	st := &stubTaxClient{
		collectFn: func(*stockpb.CollectTaxRequest) (*stockpb.CollectTaxResponse, error) {
			return &stockpb.CollectTaxResponse{
				CollectedCount:    7,
				TotalCollectedRsd: "12345.67",
				FailedCount:       1,
			}, nil
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/tax/collect", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"collected_count":7`)
	require.Contains(t, rec.Body.String(), `"failed_count":1`)
}

func TestTax_CollectTax_GRPCError(t *testing.T) {
	st := &stubTaxClient{
		collectFn: func(*stockpb.CollectTaxRequest) (*stockpb.CollectTaxResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewTaxHandler(st)
	r := taxRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/tax/collect", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

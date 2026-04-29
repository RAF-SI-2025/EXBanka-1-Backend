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
	stockpb "github.com/exbanka/contract/stockpb"
)

func stockExchangeRouter(h *handler.StockExchangeHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/stock-exchanges", h.ListExchanges)
	r.GET("/api/v2/stock-exchanges/:id", h.GetExchange)
	r.POST("/api/v2/stock-exchanges/testing-mode", h.SetTestingMode)
	r.GET("/api/v2/stock-exchanges/testing-mode", h.GetTestingMode)
	return r
}

func TestStockExchange_List_Defaults(t *testing.T) {
	st := &stubStockExchangeClient{
		listFn: func(req *stockpb.ListExchangesRequest) (*stockpb.ListExchangesResponse, error) {
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(10), req.PageSize)
			return &stockpb.ListExchangesResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"exchanges":[]`)
}

func TestStockExchange_List_GRPCError(t *testing.T) {
	st := &stubStockExchangeClient{
		listFn: func(*stockpb.ListExchangesRequest) (*stockpb.ListExchangesResponse, error) {
			return nil, status.Error(codes.Unavailable, "")
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestStockExchange_Get_Success(t *testing.T) {
	st := &stubStockExchangeClient{
		getFn: func(req *stockpb.GetExchangeRequest) (*stockpb.Exchange, error) {
			require.Equal(t, uint64(7), req.Id)
			return &stockpb.Exchange{Id: 7, Acronym: "NYSE"}, nil
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"NYSE"`)
}

func TestStockExchange_Get_BadID(t *testing.T) {
	h := handler.NewStockExchangeHandler(&stubStockExchangeClient{})
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid exchange id")
}

func TestStockExchange_Get_NotFound(t *testing.T) {
	st := &stubStockExchangeClient{
		getFn: func(*stockpb.GetExchangeRequest) (*stockpb.Exchange, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStockExchange_SetTestingMode_Success(t *testing.T) {
	st := &stubStockExchangeClient{
		setTestModeFn: func(req *stockpb.SetTestingModeRequest) (*stockpb.SetTestingModeResponse, error) {
			require.True(t, req.Enabled)
			return &stockpb.SetTestingModeResponse{TestingMode: true}, nil
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	body := `{"enabled":true}`
	req := httptest.NewRequest("POST", "/api/v2/stock-exchanges/testing-mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"testing_mode":true`)
}

func TestStockExchange_SetTestingMode_BadBody(t *testing.T) {
	h := handler.NewStockExchangeHandler(&stubStockExchangeClient{})
	r := stockExchangeRouter(h)
	body := `not json`
	req := httptest.NewRequest("POST", "/api/v2/stock-exchanges/testing-mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid request body")
}

func TestStockExchange_GetTestingMode_Success(t *testing.T) {
	st := &stubStockExchangeClient{
		getTestModeFn: func(*stockpb.GetTestingModeRequest) (*stockpb.GetTestingModeResponse, error) {
			return &stockpb.GetTestingModeResponse{TestingMode: false}, nil
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges/testing-mode", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"testing_mode":false`)
}

func TestStockExchange_GetTestingMode_GRPCError(t *testing.T) {
	st := &stubStockExchangeClient{
		getTestModeFn: func(*stockpb.GetTestingModeRequest) (*stockpb.GetTestingModeResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewStockExchangeHandler(st)
	r := stockExchangeRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/stock-exchanges/testing-mode", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

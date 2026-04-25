package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

// Extend stubSecurityClient (defined in options_v2_handler_test.go) with
// additional function fields by wrapping it in a richer test helper.
//
// To keep the existing package-level stub intact, we use a dedicated
// secStub type that satisfies stockpb.SecurityGRPCServiceClient and exposes
// per-method function fields needed by the securities-handler tests.

type secStub struct {
	listStocksFn        func(*stockpb.ListStocksRequest) (*stockpb.ListStocksResponse, error)
	getStockFn          func(*stockpb.GetStockRequest) (*stockpb.StockDetail, error)
	stockHistoryFn      func(*stockpb.GetPriceHistoryRequest) (*stockpb.PriceHistoryResponse, error)
	listFuturesFn       func(*stockpb.ListFuturesRequest) (*stockpb.ListFuturesResponse, error)
	getFuturesFn        func(*stockpb.GetFuturesRequest) (*stockpb.FuturesDetail, error)
	futuresHistoryFn    func(*stockpb.GetPriceHistoryRequest) (*stockpb.PriceHistoryResponse, error)
	listForexFn         func(*stockpb.ListForexPairsRequest) (*stockpb.ListForexPairsResponse, error)
	getForexFn          func(*stockpb.GetForexPairRequest) (*stockpb.ForexPairDetail, error)
	forexHistoryFn      func(*stockpb.GetPriceHistoryRequest) (*stockpb.PriceHistoryResponse, error)
	listOptionsFn       func(*stockpb.ListOptionsRequest) (*stockpb.ListOptionsResponse, error)
	getOptionFn         func(*stockpb.GetOptionRequest) (*stockpb.OptionDetail, error)
	getCandlesFn        func(*stockpb.GetCandlesRequest) (*stockpb.GetCandlesResponse, error)
}

func (s *secStub) ListStocks(_ context.Context, in *stockpb.ListStocksRequest, _ ...grpc.CallOption) (*stockpb.ListStocksResponse, error) {
	if s.listStocksFn != nil {
		return s.listStocksFn(in)
	}
	return &stockpb.ListStocksResponse{}, nil
}
func (s *secStub) GetStock(_ context.Context, in *stockpb.GetStockRequest, _ ...grpc.CallOption) (*stockpb.StockDetail, error) {
	if s.getStockFn != nil {
		return s.getStockFn(in)
	}
	return &stockpb.StockDetail{Id: in.Id}, nil
}
func (s *secStub) GetStockHistory(_ context.Context, in *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	if s.stockHistoryFn != nil {
		return s.stockHistoryFn(in)
	}
	return &stockpb.PriceHistoryResponse{}, nil
}
func (s *secStub) ListFutures(_ context.Context, in *stockpb.ListFuturesRequest, _ ...grpc.CallOption) (*stockpb.ListFuturesResponse, error) {
	if s.listFuturesFn != nil {
		return s.listFuturesFn(in)
	}
	return &stockpb.ListFuturesResponse{}, nil
}
func (s *secStub) GetFutures(_ context.Context, in *stockpb.GetFuturesRequest, _ ...grpc.CallOption) (*stockpb.FuturesDetail, error) {
	if s.getFuturesFn != nil {
		return s.getFuturesFn(in)
	}
	return &stockpb.FuturesDetail{Id: in.Id}, nil
}
func (s *secStub) GetFuturesHistory(_ context.Context, in *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	if s.futuresHistoryFn != nil {
		return s.futuresHistoryFn(in)
	}
	return &stockpb.PriceHistoryResponse{}, nil
}
func (s *secStub) ListForexPairs(_ context.Context, in *stockpb.ListForexPairsRequest, _ ...grpc.CallOption) (*stockpb.ListForexPairsResponse, error) {
	if s.listForexFn != nil {
		return s.listForexFn(in)
	}
	return &stockpb.ListForexPairsResponse{}, nil
}
func (s *secStub) GetForexPair(_ context.Context, in *stockpb.GetForexPairRequest, _ ...grpc.CallOption) (*stockpb.ForexPairDetail, error) {
	if s.getForexFn != nil {
		return s.getForexFn(in)
	}
	return &stockpb.ForexPairDetail{Id: in.Id}, nil
}
func (s *secStub) GetForexPairHistory(_ context.Context, in *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	if s.forexHistoryFn != nil {
		return s.forexHistoryFn(in)
	}
	return &stockpb.PriceHistoryResponse{}, nil
}
func (s *secStub) ListOptions(_ context.Context, in *stockpb.ListOptionsRequest, _ ...grpc.CallOption) (*stockpb.ListOptionsResponse, error) {
	if s.listOptionsFn != nil {
		return s.listOptionsFn(in)
	}
	return &stockpb.ListOptionsResponse{}, nil
}
func (s *secStub) GetOption(_ context.Context, in *stockpb.GetOptionRequest, _ ...grpc.CallOption) (*stockpb.OptionDetail, error) {
	if s.getOptionFn != nil {
		return s.getOptionFn(in)
	}
	return &stockpb.OptionDetail{Id: in.Id}, nil
}
func (s *secStub) GetCandles(_ context.Context, in *stockpb.GetCandlesRequest, _ ...grpc.CallOption) (*stockpb.GetCandlesResponse, error) {
	if s.getCandlesFn != nil {
		return s.getCandlesFn(in)
	}
	return &stockpb.GetCandlesResponse{}, nil
}

func securitiesRouter(h *handler.SecuritiesHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/securities/stocks", h.ListStocks)
	r.GET("/api/v2/securities/stocks/:id", h.GetStock)
	r.GET("/api/v2/securities/stocks/:id/history", h.GetStockHistory)
	r.GET("/api/v2/securities/futures", h.ListFutures)
	r.GET("/api/v2/securities/futures/:id", h.GetFutures)
	r.GET("/api/v2/securities/futures/:id/history", h.GetFuturesHistory)
	r.GET("/api/v2/securities/forex-pairs", h.ListForexPairs)
	r.GET("/api/v2/securities/forex-pairs/:id", h.GetForexPair)
	r.GET("/api/v2/securities/forex-pairs/:id/history", h.GetForexPairHistory)
	r.GET("/api/v2/securities/options", h.ListOptions)
	r.GET("/api/v2/securities/options/:id", h.GetOption)
	r.GET("/api/v2/securities/candles", h.GetCandles)
	return r
}

func TestSecurities_ListStocks_Defaults(t *testing.T) {
	called := false
	st := &secStub{
		listStocksFn: func(req *stockpb.ListStocksRequest) (*stockpb.ListStocksResponse, error) {
			called = true
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(10), req.PageSize)
			require.Equal(t, "asc", req.SortOrder)
			return &stockpb.ListStocksResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
	require.Contains(t, rec.Body.String(), `"stocks":[]`)
}

func TestSecurities_ListStocks_BadSortBy(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks?sort_by=foo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "sort_by must be one of")
}

func TestSecurities_ListStocks_BadSortOrder(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks?sort_order=sideways", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "sort_order must be one of")
}

func TestSecurities_ListStocks_GRPCError(t *testing.T) {
	st := &secStub{
		listStocksFn: func(*stockpb.ListStocksRequest) (*stockpb.ListStocksResponse, error) {
			return nil, status.Error(codes.Internal, "kaboom")
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSecurities_GetStock_Success(t *testing.T) {
	st := &secStub{
		getStockFn: func(req *stockpb.GetStockRequest) (*stockpb.StockDetail, error) {
			require.Equal(t, uint64(42), req.Id)
			return &stockpb.StockDetail{Id: 42, Ticker: "AAPL"}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"AAPL"`)
}

func TestSecurities_GetStock_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid stock id")
}

func TestSecurities_GetStock_NotFound(t *testing.T) {
	st := &secStub{
		getStockFn: func(*stockpb.GetStockRequest) (*stockpb.StockDetail, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSecurities_GetStockHistory_Default(t *testing.T) {
	st := &secStub{
		stockHistoryFn: func(req *stockpb.GetPriceHistoryRequest) (*stockpb.PriceHistoryResponse, error) {
			require.Equal(t, "month", req.Period)
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(30), req.PageSize)
			return &stockpb.PriceHistoryResponse{TotalCount: 5}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/42/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetStockHistory_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/x/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSecurities_GetStockHistory_BadPeriod(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/stocks/42/history?period=decade", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "period must be one of")
}

func TestSecurities_ListFutures_Defaults(t *testing.T) {
	called := false
	st := &secStub{
		listFuturesFn: func(req *stockpb.ListFuturesRequest) (*stockpb.ListFuturesResponse, error) {
			called = true
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, "asc", req.SortOrder)
			return &stockpb.ListFuturesResponse{}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/futures", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestSecurities_GetFutures_Success(t *testing.T) {
	st := &secStub{
		getFuturesFn: func(req *stockpb.GetFuturesRequest) (*stockpb.FuturesDetail, error) {
			require.Equal(t, uint64(7), req.Id)
			return &stockpb.FuturesDetail{Id: 7}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/futures/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetFutures_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/futures/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSecurities_GetFuturesHistory_Default(t *testing.T) {
	st := &secStub{}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/futures/7/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetFuturesHistory_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/futures/x/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSecurities_ListForexPairs_Defaults(t *testing.T) {
	st := &secStub{
		listForexFn: func(req *stockpb.ListForexPairsRequest) (*stockpb.ListForexPairsResponse, error) {
			require.Equal(t, "asc", req.SortOrder)
			return &stockpb.ListForexPairsResponse{}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_ListForexPairs_BadLiquidity(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs?liquidity=tiny", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "liquidity must be one of")
}

func TestSecurities_GetForexPair_Success(t *testing.T) {
	st := &secStub{
		getForexFn: func(req *stockpb.GetForexPairRequest) (*stockpb.ForexPairDetail, error) {
			return &stockpb.ForexPairDetail{Id: req.Id}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs/9", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetForexPair_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSecurities_GetForexPairHistory(t *testing.T) {
	st := &secStub{}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs/9/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetForexPairHistory_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/forex-pairs/x/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSecurities_ListOptions_MissingStockID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "stock_id query parameter is required")
}

func TestSecurities_ListOptions_BadStockID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options?stock_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid stock_id")
}

func TestSecurities_ListOptions_BadOptionType(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options?stock_id=1&option_type=straddle", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "option_type must be one of")
}

func TestSecurities_ListOptions_Success(t *testing.T) {
	st := &secStub{
		listOptionsFn: func(req *stockpb.ListOptionsRequest) (*stockpb.ListOptionsResponse, error) {
			require.Equal(t, uint64(5), req.StockId)
			require.Equal(t, "call", req.OptionType)
			return &stockpb.ListOptionsResponse{TotalCount: 1}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options?stock_id=5&option_type=call", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total_count":1`)
}

func TestSecurities_GetOption_Success(t *testing.T) {
	st := &secStub{
		getOptionFn: func(req *stockpb.GetOptionRequest) (*stockpb.OptionDetail, error) {
			return &stockpb.OptionDetail{Id: req.Id}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options/3", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurities_GetOption_BadID(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/options/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid option id")
}

func TestSecurities_GetCandles_MissingListing(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/candles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_id query parameter is required")
}

func TestSecurities_GetCandles_BadListing(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/candles?listing_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid listing_id")
}

func TestSecurities_GetCandles_BadInterval(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/candles?listing_id=1&interval=99h", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "interval must be one of")
}

func TestSecurities_GetCandles_MissingFrom(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/candles?listing_id=1&interval=1h", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "from query parameter is required")
}

func TestSecurities_GetCandles_MissingTo(t *testing.T) {
	h := handler.NewSecuritiesHandler(&secStub{})
	r := securitiesRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/securities/candles?listing_id=1&interval=1h&from=2024-01-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "to query parameter is required")
}

func TestSecurities_GetCandles_Success(t *testing.T) {
	st := &secStub{
		getCandlesFn: func(req *stockpb.GetCandlesRequest) (*stockpb.GetCandlesResponse, error) {
			require.Equal(t, uint64(5), req.ListingId)
			require.Equal(t, "1h", req.Interval)
			require.Equal(t, "2024-01-01T00:00:00Z", req.From)
			require.Equal(t, "2024-01-02T00:00:00Z", req.To)
			return &stockpb.GetCandlesResponse{Count: 24}, nil
		},
	}
	h := handler.NewSecuritiesHandler(st)
	r := securitiesRouter(h)
	url := "/api/v2/securities/candles?listing_id=5&interval=1h&from=2024-01-01T00:00:00Z&to=2024-01-02T00:00:00Z"
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"count":24`)
}

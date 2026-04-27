package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

// --- stub SecurityGRPCServiceClient ---
// For new tests needing additional method overrides, prefer secStub from
// securities_handler_test.go which uses the function-field pattern.

type stubSecurityClient struct {
	getOptionFn func(req *stockpb.GetOptionRequest) *stockpb.OptionDetail
}

func (s *stubSecurityClient) ListStocks(ctx context.Context, in *stockpb.ListStocksRequest, opts ...grpc.CallOption) (*stockpb.ListStocksResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetStock(ctx context.Context, in *stockpb.GetStockRequest, opts ...grpc.CallOption) (*stockpb.StockDetail, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetStockHistory(ctx context.Context, in *stockpb.GetPriceHistoryRequest, opts ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) ListFutures(ctx context.Context, in *stockpb.ListFuturesRequest, opts ...grpc.CallOption) (*stockpb.ListFuturesResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetFutures(ctx context.Context, in *stockpb.GetFuturesRequest, opts ...grpc.CallOption) (*stockpb.FuturesDetail, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetFuturesHistory(ctx context.Context, in *stockpb.GetPriceHistoryRequest, opts ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) ListForexPairs(ctx context.Context, in *stockpb.ListForexPairsRequest, opts ...grpc.CallOption) (*stockpb.ListForexPairsResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetForexPair(ctx context.Context, in *stockpb.GetForexPairRequest, opts ...grpc.CallOption) (*stockpb.ForexPairDetail, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetForexPairHistory(ctx context.Context, in *stockpb.GetPriceHistoryRequest, opts ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) ListOptions(ctx context.Context, in *stockpb.ListOptionsRequest, opts ...grpc.CallOption) (*stockpb.ListOptionsResponse, error) {
	return nil, nil
}
func (s *stubSecurityClient) GetOption(ctx context.Context, in *stockpb.GetOptionRequest, opts ...grpc.CallOption) (*stockpb.OptionDetail, error) {
	if s.getOptionFn != nil {
		return s.getOptionFn(in), nil
	}
	return nil, nil
}
func (s *stubSecurityClient) GetCandles(ctx context.Context, in *stockpb.GetCandlesRequest, opts ...grpc.CallOption) (*stockpb.GetCandlesResponse, error) {
	return nil, nil
}

// --- stub OrderGRPCServiceClient ---

type stubOrderClient struct {
	createFn      func(req *stockpb.CreateOrderRequest) *stockpb.Order
	getOrderFn    func(req *stockpb.GetOrderRequest) *stockpb.OrderDetail
	listMyFn      func(req *stockpb.ListMyOrdersRequest) *stockpb.ListOrdersResponse
	cancelFn      func(req *stockpb.CancelOrderRequest) *stockpb.Order
	lastCreateReq *stockpb.CreateOrderRequest
	lastGetReq    *stockpb.GetOrderRequest
	lastListReq   *stockpb.ListMyOrdersRequest
	lastCancelReq *stockpb.CancelOrderRequest
}

func (s *stubOrderClient) CreateOrder(ctx context.Context, in *stockpb.CreateOrderRequest, opts ...grpc.CallOption) (*stockpb.Order, error) {
	s.lastCreateReq = in
	if s.createFn != nil {
		return s.createFn(in), nil
	}
	return &stockpb.Order{Id: 1, Status: "approved"}, nil
}
func (s *stubOrderClient) GetOrder(ctx context.Context, in *stockpb.GetOrderRequest, opts ...grpc.CallOption) (*stockpb.OrderDetail, error) {
	s.lastGetReq = in
	if s.getOrderFn != nil {
		return s.getOrderFn(in), nil
	}
	return &stockpb.OrderDetail{Order: &stockpb.Order{Id: in.Id}}, nil
}
func (s *stubOrderClient) ListMyOrders(ctx context.Context, in *stockpb.ListMyOrdersRequest, opts ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	s.lastListReq = in
	if s.listMyFn != nil {
		return s.listMyFn(in), nil
	}
	return &stockpb.ListOrdersResponse{}, nil
}
func (s *stubOrderClient) CancelOrder(ctx context.Context, in *stockpb.CancelOrderRequest, opts ...grpc.CallOption) (*stockpb.Order, error) {
	s.lastCancelReq = in
	if s.cancelFn != nil {
		return s.cancelFn(in), nil
	}
	return &stockpb.Order{Id: in.Id}, nil
}
func (s *stubOrderClient) ListOrders(ctx context.Context, in *stockpb.ListOrdersRequest, opts ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	return nil, nil
}
func (s *stubOrderClient) ApproveOrder(ctx context.Context, in *stockpb.ApproveOrderRequest, opts ...grpc.CallOption) (*stockpb.Order, error) {
	return nil, nil
}
func (s *stubOrderClient) DeclineOrder(ctx context.Context, in *stockpb.DeclineOrderRequest, opts ...grpc.CallOption) (*stockpb.Order, error) {
	return nil, nil
}

// --- stub PortfolioGRPCServiceClient ---
// For new tests needing additional method overrides, prefer portfolioStub from
// portfolio_handler_test.go which uses the function-field pattern.

type stubPortfolioClient struct {
	exerciseByOptionIDFn func(req *stockpb.ExerciseOptionByOptionIDRequest) *stockpb.ExerciseResult
}

func (s *stubPortfolioClient) ListHoldings(ctx context.Context, in *stockpb.ListHoldingsRequest, opts ...grpc.CallOption) (*stockpb.ListHoldingsResponse, error) {
	return nil, nil
}
func (s *stubPortfolioClient) GetPortfolioSummary(ctx context.Context, in *stockpb.GetPortfolioSummaryRequest, opts ...grpc.CallOption) (*stockpb.PortfolioSummary, error) {
	return nil, nil
}
func (s *stubPortfolioClient) MakePublic(ctx context.Context, in *stockpb.MakePublicRequest, opts ...grpc.CallOption) (*stockpb.Holding, error) {
	return nil, nil
}
func (s *stubPortfolioClient) ExerciseOption(ctx context.Context, in *stockpb.ExerciseOptionRequest, opts ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	return nil, nil
}
func (s *stubPortfolioClient) ListHoldingTransactions(ctx context.Context, in *stockpb.ListHoldingTransactionsRequest, opts ...grpc.CallOption) (*stockpb.ListHoldingTransactionsResponse, error) {
	return nil, nil
}
func (s *stubPortfolioClient) ExerciseOptionByOptionID(ctx context.Context, in *stockpb.ExerciseOptionByOptionIDRequest, opts ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	if s.exerciseByOptionIDFn != nil {
		return s.exerciseByOptionIDFn(in), nil
	}
	return nil, nil
}

// --- helper ---

func makeOptionsV2Router(h *handler.OptionsV2Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v2/options/:option_id/orders", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "client")
		h.CreateOrder(c)
	})
	return router
}

// --- tests ---

func TestOptionsV2_CreateOrder_Valid(t *testing.T) {
	sec := &stubSecurityClient{
		getOptionFn: func(req *stockpb.GetOptionRequest) *stockpb.OptionDetail {
			lid := uint64(77)
			return &stockpb.OptionDetail{Id: req.Id, Ticker: "AAPL260116C00200000", ListingId: &lid}
		},
	}
	ord := &stubOrderClient{
		createFn: func(req *stockpb.CreateOrderRequest) *stockpb.Order {
			require.Equal(t, uint64(77), req.ListingId)
			require.Equal(t, "buy", req.Direction)
			return &stockpb.Order{Id: 999, ListingId: 77}
		},
	}

	h := handler.NewOptionsV2Handler(sec, ord, &stubPortfolioClient{})
	router := makeOptionsV2Router(h)

	body := `{"direction":"buy","order_type":"market","quantity":1,"account_id":42}`
	req := httptest.NewRequest("POST", "/api/v2/options/5/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOptionsV2_CreateOrder_RejectsOptionWithoutListing(t *testing.T) {
	sec := &stubSecurityClient{
		getOptionFn: func(req *stockpb.GetOptionRequest) *stockpb.OptionDetail {
			return &stockpb.OptionDetail{Id: req.Id, Ticker: "X"}
		},
	}
	ord := &stubOrderClient{}
	h := handler.NewOptionsV2Handler(sec, ord, &stubPortfolioClient{})
	router := makeOptionsV2Router(h)

	body := `{"direction":"buy","order_type":"market","quantity":1,"account_id":42}`
	req := httptest.NewRequest("POST", "/api/v2/options/5/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestOptionsV2_CreateOrder_RejectsInvalidDirection(t *testing.T) {
	h := handler.NewOptionsV2Handler(&stubSecurityClient{}, &stubOrderClient{}, &stubPortfolioClient{})
	router := makeOptionsV2Router(h)

	body := `{"direction":"sideways","order_type":"market","quantity":1,"account_id":42}`
	req := httptest.NewRequest("POST", "/api/v2/options/5/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOptionsV2_Exercise_HappyPath(t *testing.T) {
	port := &stubPortfolioClient{
		exerciseByOptionIDFn: func(req *stockpb.ExerciseOptionByOptionIDRequest) *stockpb.ExerciseResult {
			require.Equal(t, uint64(5), req.OptionId)
			require.Equal(t, uint64(1), req.UserId)
			return &stockpb.ExerciseResult{
				Id:                1,
				OptionTicker:      "AAPL260116C00200000",
				ExercisedQuantity: 1,
				SharesAffected:    100,
				Profit:            "42.00",
			}
		},
	}

	h := handler.NewOptionsV2Handler(&stubSecurityClient{}, &stubOrderClient{}, port)

	router := gin.New()
	router.POST("/api/v2/options/:option_id/exercise", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "client")
		h.Exercise(c)
	})

	body := `{"holding_id":0}`
	req := httptest.NewRequest("POST", "/api/v2/options/5/exercise", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOptionsV2_Exercise_InvalidOptionID(t *testing.T) {
	h := handler.NewOptionsV2Handler(&stubSecurityClient{}, &stubOrderClient{}, &stubPortfolioClient{})
	router := gin.New()
	router.POST("/api/v2/options/:option_id/exercise", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "client")
		h.Exercise(c)
	})
	req := httptest.NewRequest("POST", "/api/v2/options/0/exercise", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

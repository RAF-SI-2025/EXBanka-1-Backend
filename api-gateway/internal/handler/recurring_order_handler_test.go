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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

type stubRecurringOrderClient struct {
	createFn func(*stockpb.CreateRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error)
	getFn    func(*stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error)
	pauseFn  func(*stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error)
	resumeFn func(*stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error)
	cancelFn func(*stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error)
	listMyFn func(*stockpb.ListMyRecurringOrdersRequest) (*stockpb.ListMyRecurringOrdersResponse, error)
}

func (s *stubRecurringOrderClient) CreateOrder(_ context.Context, in *stockpb.CreateRecurringOrderRequest, _ ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.RecurringOrderResponse{}, nil
}
func (s *stubRecurringOrderClient) GetOrder(_ context.Context, in *stockpb.GetRecurringOrderRequest, _ ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.RecurringOrderResponse{}, nil
}
func (s *stubRecurringOrderClient) PauseOrder(_ context.Context, in *stockpb.GetRecurringOrderRequest, _ ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error) {
	if s.pauseFn != nil {
		return s.pauseFn(in)
	}
	return &stockpb.RecurringOrderResponse{}, nil
}
func (s *stubRecurringOrderClient) ResumeOrder(_ context.Context, in *stockpb.GetRecurringOrderRequest, _ ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error) {
	if s.resumeFn != nil {
		return s.resumeFn(in)
	}
	return &stockpb.RecurringOrderResponse{}, nil
}
func (s *stubRecurringOrderClient) CancelOrder(_ context.Context, in *stockpb.GetRecurringOrderRequest, _ ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error) {
	if s.cancelFn != nil {
		return s.cancelFn(in)
	}
	return &stockpb.RecurringOrderResponse{}, nil
}
func (s *stubRecurringOrderClient) ListMy(_ context.Context, in *stockpb.ListMyRecurringOrdersRequest, _ ...grpc.CallOption) (*stockpb.ListMyRecurringOrdersResponse, error) {
	if s.listMyFn != nil {
		return s.listMyFn(in)
	}
	return &stockpb.ListMyRecurringOrdersResponse{}, nil
}

var _ stockpb.RecurringOrderServiceClient = (*stubRecurringOrderClient)(nil)

func recurringOrderRouter(h *handler.RecurringOrderHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/api/v3/me/recurring-orders", withCli, h.Create)
	r.GET("/api/v3/me/recurring-orders/:id", withCli, h.Get)
	r.POST("/api/v3/me/recurring-orders/:id/pause", withCli, h.Pause)
	r.POST("/api/v3/me/recurring-orders/:id/resume", withCli, h.Resume)
	r.POST("/api/v3/me/recurring-orders/:id/cancel", withCli, h.Cancel)
	r.GET("/api/v3/me/recurring-orders", withCli, h.ListMy)
	return r
}

func roDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestRecurringOrder_Create_WeeklySuccess(t *testing.T) {
	var captured *stockpb.CreateRecurringOrderRequest
	cl := &stubRecurringOrderClient{createFn: func(in *stockpb.CreateRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		captured = in
		return &stockpb.RecurringOrderResponse{Id: 1}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	body := `{"listing_id":5,"side":"buy","quantity":10,"account_id":3,"interval":"weekly","day_of_week":2}`
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "buy", captured.Side)
	require.Equal(t, "weekly", captured.Interval)
	require.Equal(t, int32(2), captured.DayOfWeek)
	require.Equal(t, "client", captured.OwnerType)
}

func TestRecurringOrder_Create_MonthlySuccess(t *testing.T) {
	var captured *stockpb.CreateRecurringOrderRequest
	cl := &stubRecurringOrderClient{createFn: func(in *stockpb.CreateRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		captured = in
		return &stockpb.RecurringOrderResponse{Id: 1}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	body := `{"listing_id":5,"side":"sell","quantity":10,"account_id":3,"interval":"monthly","day_of_month":15}`
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, int32(15), captured.DayOfMonth)
}

func TestRecurringOrder_Create_BadBody(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `nope`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecurringOrder_Create_BadSide(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `{"side":"hold","interval":"weekly","quantity":1,"listing_id":1,"account_id":1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "side must be one of")
}

func TestRecurringOrder_Create_BadInterval(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `{"side":"buy","interval":"daily","quantity":1,"listing_id":1,"account_id":1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "interval must be one of")
}

func TestRecurringOrder_Create_MissingNumerics(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `{"side":"buy","interval":"weekly","quantity":0,"listing_id":1,"account_id":1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "quantity, listing_id, account_id are required")
}

func TestRecurringOrder_Create_BadDayOfWeek(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `{"side":"buy","interval":"weekly","quantity":1,"listing_id":1,"account_id":1,"day_of_week":9}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "day_of_week must be 0..6")
}

func TestRecurringOrder_Create_BadDayOfMonth(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders", `{"side":"buy","interval":"monthly","quantity":1,"listing_id":1,"account_id":1,"day_of_month":31}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "day_of_month must be 1..28")
}

func TestRecurringOrder_Get_Success(t *testing.T) {
	var captured *stockpb.GetRecurringOrderRequest
	cl := &stubRecurringOrderClient{getFn: func(in *stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		captured = in
		return &stockpb.RecurringOrderResponse{Id: in.Id}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	rec := roDo(r, "GET", "/api/v3/me/recurring-orders/7", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(7), captured.Id)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestRecurringOrder_Get_BadID(t *testing.T) {
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(&stubRecurringOrderClient{}))
	rec := roDo(r, "GET", "/api/v3/me/recurring-orders/xx", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}

func TestRecurringOrder_Pause_Success(t *testing.T) {
	called := false
	cl := &stubRecurringOrderClient{pauseFn: func(in *stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		called = true
		require.Equal(t, uint64(7), in.Id)
		return &stockpb.RecurringOrderResponse{Id: in.Id}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders/7/pause", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRecurringOrder_Resume_Success(t *testing.T) {
	cl := &stubRecurringOrderClient{resumeFn: func(in *stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		return &stockpb.RecurringOrderResponse{Id: in.Id}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders/7/resume", "")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRecurringOrder_Cancel_GRPCError(t *testing.T) {
	cl := &stubRecurringOrderClient{cancelFn: func(*stockpb.GetRecurringOrderRequest) (*stockpb.RecurringOrderResponse, error) {
		return nil, status.Error(codes.NotFound, "no order")
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	rec := roDo(r, "POST", "/api/v3/me/recurring-orders/7/cancel", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRecurringOrder_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyRecurringOrdersRequest
	cl := &stubRecurringOrderClient{listMyFn: func(in *stockpb.ListMyRecurringOrdersRequest) (*stockpb.ListMyRecurringOrdersResponse, error) {
		captured = in
		return &stockpb.ListMyRecurringOrdersResponse{}, nil
	}}
	r := recurringOrderRouter(handler.NewRecurringOrderHandler(cl))
	rec := roDo(r, "GET", "/api/v3/me/recurring-orders", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Contains(t, rec.Body.String(), `"recurring_orders"`)
}

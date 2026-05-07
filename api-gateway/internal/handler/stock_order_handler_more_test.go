// api-gateway/internal/handler/stock_order_handler_more_test.go
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
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// fullOrderClient is an OrderGRPCServiceClient stub that records additional
// methods (ListOrders, ApproveOrder, DeclineOrder) needed by the supervisor
// review tests. Built as a fresh stub rather than extending stubOrderClient
// to keep its compile-time interface compliance.
type fullOrderClient struct {
	createFn  func(*stockpb.CreateOrderRequest) (*stockpb.Order, error)
	getFn     func(*stockpb.GetOrderRequest) (*stockpb.OrderDetail, error)
	listMyFn  func(*stockpb.ListMyOrdersRequest) (*stockpb.ListOrdersResponse, error)
	cancelFn  func(*stockpb.CancelOrderRequest) (*stockpb.Order, error)
	listFn    func(*stockpb.ListOrdersRequest) (*stockpb.ListOrdersResponse, error)
	approveFn func(*stockpb.ApproveOrderRequest) (*stockpb.Order, error)
	declineFn func(*stockpb.DeclineOrderRequest) (*stockpb.Order, error)
}

func (s *fullOrderClient) CreateOrder(_ context.Context, in *stockpb.CreateOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.Order{Id: 1}, nil
}
func (s *fullOrderClient) GetOrder(_ context.Context, in *stockpb.GetOrderRequest, _ ...grpc.CallOption) (*stockpb.OrderDetail, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.OrderDetail{Order: &stockpb.Order{Id: in.Id}}, nil
}
func (s *fullOrderClient) ListMyOrders(_ context.Context, in *stockpb.ListMyOrdersRequest, _ ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	if s.listMyFn != nil {
		return s.listMyFn(in)
	}
	return &stockpb.ListOrdersResponse{}, nil
}
func (s *fullOrderClient) CancelOrder(_ context.Context, in *stockpb.CancelOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	if s.cancelFn != nil {
		return s.cancelFn(in)
	}
	return &stockpb.Order{Id: in.Id}, nil
}
func (s *fullOrderClient) ListOrders(_ context.Context, in *stockpb.ListOrdersRequest, _ ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListOrdersResponse{}, nil
}
func (s *fullOrderClient) ApproveOrder(_ context.Context, in *stockpb.ApproveOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	if s.approveFn != nil {
		return s.approveFn(in)
	}
	return &stockpb.Order{Id: in.Id, Status: "approved"}, nil
}
func (s *fullOrderClient) DeclineOrder(_ context.Context, in *stockpb.DeclineOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	if s.declineFn != nil {
		return s.declineFn(in)
	}
	return &stockpb.Order{Id: in.Id, Status: "declined"}, nil
}

var _ stockpb.OrderGRPCServiceClient = (*fullOrderClient)(nil)

func adminOrdersRouter(h *handler.StockOrderHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/orders", employeeIdentity(11), h.CreateOrderOnBehalf)
	r.GET("/orders", h.ListOrders)
	r.POST("/orders/:id/approve", employeeIdentity(11), h.ApproveOrder)
	r.POST("/orders/:id/reject", employeeIdentity(11), h.RejectOrder)
	return r
}

// --- ListOrders ---

func TestListOrders_Success(t *testing.T) {
	ord := &fullOrderClient{
		listFn: func(in *stockpb.ListOrdersRequest) (*stockpb.ListOrdersResponse, error) {
			require.Equal(t, "pending", in.Status)
			require.Equal(t, "buy", in.Direction)
			require.Equal(t, int32(2), in.Page)
			require.Equal(t, int32(50), in.PageSize)
			return &stockpb.ListOrdersResponse{TotalCount: 0}, nil
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/orders?status=pending&direction=buy&page=2&page_size=50", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestListOrders_GRPCError(t *testing.T) {
	ord := &fullOrderClient{
		listFn: func(*stockpb.ListOrdersRequest) (*stockpb.ListOrdersResponse, error) {
			return nil, status.Error(codes.Internal, "fail")
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/orders", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- ApproveOrder ---

func TestApproveOrder_Success(t *testing.T) {
	ord := &fullOrderClient{
		approveFn: func(in *stockpb.ApproveOrderRequest) (*stockpb.Order, error) {
			require.Equal(t, uint64(7), in.Id)
			require.Equal(t, uint64(11), in.SupervisorId)
			return &stockpb.Order{Id: in.Id, Status: "approved"}, nil
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/7/approve", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveOrder_BadID(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/abc/approve", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveOrder_GRPCError(t *testing.T) {
	ord := &fullOrderClient{
		approveFn: func(*stockpb.ApproveOrderRequest) (*stockpb.Order, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/7/approve", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- RejectOrder ---

func TestRejectOrder_Success(t *testing.T) {
	ord := &fullOrderClient{
		declineFn: func(in *stockpb.DeclineOrderRequest) (*stockpb.Order, error) {
			require.Equal(t, uint64(7), in.Id)
			require.Equal(t, uint64(11), in.SupervisorId)
			return &stockpb.Order{Id: in.Id, Status: "declined"}, nil
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/7/reject", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRejectOrder_BadID(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/abc/reject", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRejectOrder_GRPCError(t *testing.T) {
	ord := &fullOrderClient{
		declineFn: func(*stockpb.DeclineOrderRequest) (*stockpb.Order, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders/7/reject", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// --- CreateOrderOnBehalf ---

func TestCreateOrderOnBehalf_Success(t *testing.T) {
	acct := &stubAccountClient{
		getAccountFn: func(in *accountpb.GetAccountRequest) *accountpb.AccountResponse {
			return &accountpb.AccountResponse{Id: in.Id, OwnerId: 5}
		},
	}
	ord := &fullOrderClient{
		createFn: func(in *stockpb.CreateOrderRequest) (*stockpb.Order, error) {
			require.Equal(t, uint64(5), in.UserId)
			require.Equal(t, "employee", in.SystemType)
			require.Equal(t, uint64(11), in.ActingEmployeeId)
			require.Equal(t, uint64(5), in.OnBehalfOfClientId)
			return &stockpb.Order{Id: 1}, nil
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, acct))

	body := `{"client_id":5,"account_id":42,"listing_id":7,"direction":"buy","order_type":"market","quantity":10}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
}

func TestCreateOrderOnBehalf_BadBody(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader("nope")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOrderOnBehalf_MissingClientID(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":0,"account_id":1,"listing_id":1,"direction":"buy","order_type":"market","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "client_id")
}

func TestCreateOrderOnBehalf_BadDirection(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"bogus","order_type":"market","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOrderOnBehalf_BadOrderType(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"buy","order_type":"weird","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOrderOnBehalf_BadSecurityType(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"buy","order_type":"market","quantity":1,"security_type":"banana"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOrderOnBehalf_NegativeQuantity(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"buy","order_type":"market","quantity":0}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "quantity")
}

func TestCreateOrderOnBehalf_MissingListingID(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":0,"direction":"buy","order_type":"market","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_id")
}

func TestCreateOrderOnBehalf_BuyMissingAccount(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"listing_id":1,"direction":"buy","order_type":"market","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "account_id is required")
}

func TestCreateOrderOnBehalf_LimitMissingValue(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"buy","order_type":"limit","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "limit_value")
}

func TestCreateOrderOnBehalf_StopMissingValue(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":1,"listing_id":1,"direction":"buy","order_type":"stop","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "stop_value")
}

func TestCreateOrderOnBehalf_AccountOwnerMismatch(t *testing.T) {
	acct := &stubAccountClient{
		getAccountFn: func(in *accountpb.GetAccountRequest) *accountpb.AccountResponse {
			return &accountpb.AccountResponse{Id: in.Id, OwnerId: 999}
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, acct))
	body := `{"client_id":5,"account_id":42,"listing_id":1,"direction":"buy","order_type":"market","quantity":1}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateOrderOnBehalf_ForexValid(t *testing.T) {
	acct := &stubAccountClient{
		getAccountFn: func(in *accountpb.GetAccountRequest) *accountpb.AccountResponse {
			return &accountpb.AccountResponse{Id: in.Id, OwnerId: 5}
		},
	}
	ord := &fullOrderClient{
		createFn: func(in *stockpb.CreateOrderRequest) (*stockpb.Order, error) {
			require.NotNil(t, in.BaseAccountId)
			require.Equal(t, uint64(99), *in.BaseAccountId)
			return &stockpb.Order{Id: 1}, nil
		},
	}
	r := adminOrdersRouter(handler.NewStockOrderHandler(ord, acct))
	body := `{"client_id":5,"account_id":42,"listing_id":1,"direction":"buy","order_type":"market","quantity":1,"security_type":"forex","base_account_id":99}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
}

func TestCreateOrderOnBehalf_ForexSellRejected(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":42,"listing_id":1,"direction":"sell","order_type":"market","quantity":1,"security_type":"forex"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOrderOnBehalf_ForexBaseEqAccount(t *testing.T) {
	r := adminOrdersRouter(handler.NewStockOrderHandler(&fullOrderClient{}, &stubAccountClient{}))
	body := `{"client_id":5,"account_id":42,"listing_id":1,"direction":"buy","order_type":"market","quantity":1,"security_type":"forex","base_account_id":42}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}


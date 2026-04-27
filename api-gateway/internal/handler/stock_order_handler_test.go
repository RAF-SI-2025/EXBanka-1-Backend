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

	accountpb "github.com/exbanka/contract/accountpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

// --- stub AccountServiceClient (minimal, only GetAccount used by these tests) ---
// For new tests that need to override additional methods, prefer accountFullStub
// from account_handler_test.go which uses the function-field pattern.

type stubAccountClient struct {
	getAccountFn func(req *accountpb.GetAccountRequest) *accountpb.AccountResponse
}

func (s *stubAccountClient) CreateAccount(ctx context.Context, in *accountpb.CreateAccountRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) GetAccount(ctx context.Context, in *accountpb.GetAccountRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getAccountFn != nil {
		return s.getAccountFn(in), nil
	}
	return &accountpb.AccountResponse{Id: in.Id, OwnerId: 1}, nil
}
func (s *stubAccountClient) GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ListAccountsByClient(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ListAllAccounts(ctx context.Context, in *accountpb.ListAllAccountsRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) UpdateAccountName(ctx context.Context, in *accountpb.UpdateAccountNameRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) UpdateAccountLimits(ctx context.Context, in *accountpb.UpdateAccountLimitsRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) UpdateAccountStatus(ctx context.Context, in *accountpb.UpdateAccountStatusRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) CreateCompany(ctx context.Context, in *accountpb.CreateCompanyRequest, opts ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) GetCompany(ctx context.Context, in *accountpb.GetCompanyRequest, opts ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) UpdateCompany(ctx context.Context, in *accountpb.UpdateCompanyRequest, opts ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ListCurrencies(ctx context.Context, in *accountpb.ListCurrenciesRequest, opts ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) GetCurrency(ctx context.Context, in *accountpb.GetCurrencyRequest, opts ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) GetLedgerEntries(ctx context.Context, in *accountpb.GetLedgerEntriesRequest, opts ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ReserveFunds(ctx context.Context, in *accountpb.ReserveFundsRequest, opts ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ReleaseReservation(ctx context.Context, in *accountpb.ReleaseReservationRequest, opts ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) PartialSettleReservation(ctx context.Context, in *accountpb.PartialSettleReservationRequest, opts ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) GetReservation(ctx context.Context, in *accountpb.GetReservationRequest, opts ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) CommitIncoming(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return nil, nil
}
func (s *stubAccountClient) ReleaseIncoming(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	return nil, nil
}

// --- helper ---

func makeMyOrdersRouter(h *handler.StockOrderHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		// use "employee" to bypass enforceOwnership on buy paths in these tests
		c.Set("principal_type", "employee")
		h.CreateOrder(c)
	})
	return router
}

// --- tests ---

func TestCreateOrder_Forex_SellRejected(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}

	h := handler.NewStockOrderHandler(ord, acct)
	router := makeMyOrdersRouter(h)

	body := `{"security_type":"forex","direction":"sell","order_type":"market","quantity":1,"listing_id":5,"account_id":42,"holding_id":7}`
	req := httptest.NewRequest("POST", "/api/v1/me/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "forex orders must be direction=buy")
}

func TestCreateOrder_Forex_MissingBaseAccount_Rejected(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}

	h := handler.NewStockOrderHandler(ord, acct)
	router := makeMyOrdersRouter(h)

	body := `{"security_type":"forex","direction":"buy","order_type":"market","quantity":1,"listing_id":5,"account_id":42}`
	req := httptest.NewRequest("POST", "/api/v1/me/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "forex orders require base_account_id")
}

func TestCreateOrder_BaseAccountEqualsAccount_Rejected(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}

	h := handler.NewStockOrderHandler(ord, acct)
	router := makeMyOrdersRouter(h)

	// security_type=forex with matching base_account_id == account_id
	body := `{"security_type":"forex","direction":"buy","order_type":"market","quantity":1,"listing_id":5,"account_id":42,"base_account_id":42}`
	req := httptest.NewRequest("POST", "/api/v1/me/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "base_account_id must differ from account_id")
}

// --- /me/* system_type forwarding (Task 5) ---

func TestGetMyOrder_ForwardsSystemType(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/me/orders/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.GetMyOrder(c)
	})

	req := httptest.NewRequest("GET", "/api/v1/me/orders/42", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastGetReq)
	require.Equal(t, uint64(42), ord.lastGetReq.Id)
	require.Equal(t, uint64(7), ord.lastGetReq.UserId)
	require.Equal(t, "client", ord.lastGetReq.SystemType)
}

func TestGetMyOrder_MissingSystemType_Returns401(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/me/orders/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		// deliberately omit principal_type
		h.GetMyOrder(c)
	})

	req := httptest.NewRequest("GET", "/api/v1/me/orders/42", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Nil(t, ord.lastGetReq, "RPC must not be called when principal_type is missing")
	require.Contains(t, rec.Body.String(), "missing principal_type")
}

func TestCancelOrder_ForwardsSystemType(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders/:id/cancel", func(c *gin.Context) {
		c.Set("principal_id", int64(11))
		c.Set("principal_type", "employee")
		h.CancelOrder(c)
	})

	req := httptest.NewRequest("POST", "/api/v1/me/orders/9/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastCancelReq)
	require.Equal(t, uint64(9), ord.lastCancelReq.Id)
	// Phase 3: employee /me/* ops act on the bank's portfolio, so the
	// underlying RPC carries the bank sentinel user_id and system_type=bank,
	// not the employee's own user_id.
	require.Equal(t, handler.BankSentinelUserID, ord.lastCancelReq.UserId)
	require.Equal(t, handler.BankSystemType, ord.lastCancelReq.SystemType)
}

// TestCreateOrder_Employee_PassesActingEmployeeID is the regression
// guard for the Phase 3 limit-bypass bug. Phase 3's mePortfolioIdentity
// rewrites an employee's identity to (BankSentinel, "bank") on
// /me/orders so the bank's portfolio surfaces — but stock-service still
// needs to know which employee acted, otherwise the per-actuary
// EmployeeLimit gate can't fire.
//
// Assertion: when an employee posts to /me/orders, the outgoing
// CreateOrderRequest carries:
//   - UserId = BankSentinelUserID (the swap from mePortfolioIdentity)
//   - SystemType = "bank"          (ditto)
//   - ActingEmployeeId = JWT user_id (the gate keys on this)
func TestCreateOrder_Employee_PassesActingEmployeeID(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{
		getAccountFn: func(_ *accountpb.GetAccountRequest) *accountpb.AccountResponse {
			// Bank account owned by the bank sentinel — ownership check
			// passes when the swapped identity matches.
			return &accountpb.AccountResponse{Id: 42, OwnerId: handler.BankSentinelUserID}
		},
	}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders", func(c *gin.Context) {
		c.Set("principal_id", int64(11))
		c.Set("principal_type", "employee")
		h.CreateOrder(c)
	})

	body := `{"listing_id":5,"direction":"buy","order_type":"limit","quantity":1,"limit_value":"100","account_id":42}`
	req := httptest.NewRequest("POST", "/api/v1/me/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.NotNil(t, ord.lastCreateReq, "stock-service must be called")
	require.Equal(t, handler.BankSentinelUserID, ord.lastCreateReq.UserId,
		"Phase 3: employee /me/orders should swap to bank sentinel")
	require.Equal(t, handler.BankSystemType, ord.lastCreateReq.SystemType,
		"Phase 3: employee /me/orders should swap system_type to bank")
	require.Equal(t, uint64(11), ord.lastCreateReq.ActingEmployeeId,
		"per-actuary EmployeeLimit gate keys on this — must be the JWT employee id")
}

func TestListMyOrders_ForwardsSystemType(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/me/orders", func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		h.ListMyOrders(c)
	})

	req := httptest.NewRequest("GET", "/api/v1/me/orders", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastListReq)
	require.Equal(t, uint64(7), ord.lastListReq.UserId)
	require.Equal(t, "client", ord.lastListReq.SystemType)
}

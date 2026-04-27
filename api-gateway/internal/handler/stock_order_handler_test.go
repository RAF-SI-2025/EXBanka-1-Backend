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
	"github.com/exbanka/api-gateway/internal/middleware"
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

// employeeIdentity mimics middleware.ResolveIdentity(OwnerIsBankIfEmployee)
// for an employee principal: owner becomes "bank" with nil OwnerID, and
// ActingEmployeeID carries the JWT principal_id.
func employeeIdentity(empID uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := empID
		c.Set("principal_id", int64(empID))
		c.Set("principal_type", "employee")
		c.Set("identity", &middleware.ResolvedIdentity{
			PrincipalType:    "employee",
			PrincipalID:      empID,
			OwnerType:        "bank",
			OwnerID:          nil,
			ActingEmployeeID: &id,
		})
		c.Next()
	}
}

// clientIdentity mimics middleware.ResolveIdentity(OwnerIsBankIfEmployee)
// for a client principal: owner == principal.
func clientIdentity(uid uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uid
		c.Set("principal_id", int64(uid))
		c.Set("principal_type", "client")
		c.Set("identity", &middleware.ResolvedIdentity{
			PrincipalType: "client",
			PrincipalID:   uid,
			OwnerType:     "client",
			OwnerID:       &id,
		})
		c.Next()
	}
}

func makeMyOrdersRouter(h *handler.StockOrderHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// use "employee" identity to bypass enforceOwnership on buy paths in these tests
	router.POST("/api/v1/me/orders", employeeIdentity(1), h.CreateOrder)
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

// --- /me/* identity forwarding (post Spec C Task 7) ---

// TestGetMyOrder_ForwardsClientIdentity locks the contract that a client
// principal's GET /me/orders/:id forwards (UserId=client_id, SystemType=client)
// to stock-service. The legacy bank-sentinel swap only fires for employees.
func TestGetMyOrder_ForwardsClientIdentity(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/me/orders/:id", clientIdentity(7), h.GetMyOrder)

	req := httptest.NewRequest("GET", "/api/v1/me/orders/42", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastGetReq)
	require.Equal(t, uint64(42), ord.lastGetReq.Id)
	require.Equal(t, uint64(7), ord.lastGetReq.UserId)
	require.Equal(t, "client", ord.lastGetReq.SystemType)
}

// TestCancelOrder_EmployeeForwardsBankOwner asserts that an employee
// principal hitting /me/orders/:id/cancel surfaces the bank's order
// (OwnerType="bank", OwnerID=nil → UserId=0, SystemType="bank") to the
// underlying RPC. The legacy bank-sentinel uint64 (1_000_000_000) is
// gone — the bank is now represented by the absence of an OwnerID.
func TestCancelOrder_EmployeeForwardsBankOwner(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders/:id/cancel", employeeIdentity(11), h.CancelOrder)

	req := httptest.NewRequest("POST", "/api/v1/me/orders/9/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastCancelReq)
	require.Equal(t, uint64(9), ord.lastCancelReq.Id)
	require.Equal(t, uint64(0), ord.lastCancelReq.UserId,
		"bank owner has nil OwnerID; ownerToLegacyUserID(nil)==0")
	require.Equal(t, "bank", ord.lastCancelReq.SystemType)
}

// TestCreateOrder_Employee_PassesActingEmployeeID is the regression
// guard for the Phase 3 limit-bypass bug. ResolveIdentity rewrites an
// employee's owner to ("bank", nil) on /me/orders so the bank's portfolio
// surfaces, AND populates ActingEmployeeID with the JWT principal so
// stock-service's per-actuary EmployeeLimit gate can fire.
//
// Assertion: when an employee posts to /me/orders, the outgoing
// CreateOrderRequest carries:
//   - UserId = 0          (bank owner — nil OwnerID flattens to 0)
//   - SystemType = "bank"
//   - ActingEmployeeId = JWT principal_id (the gate keys on this)
func TestCreateOrder_Employee_PassesActingEmployeeID(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{
		getAccountFn: func(_ *accountpb.GetAccountRequest) *accountpb.AccountResponse {
			// Bank account; ownership check is bypassed for employees by
			// enforceOwnership(), so the OwnerId value is irrelevant — the
			// employee identity unconditionally passes.
			return &accountpb.AccountResponse{Id: 42, OwnerId: 0}
		},
	}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders", employeeIdentity(11), h.CreateOrder)

	body := `{"listing_id":5,"direction":"buy","order_type":"limit","quantity":1,"limit_value":"100","account_id":42}`
	req := httptest.NewRequest("POST", "/api/v1/me/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.NotNil(t, ord.lastCreateReq, "stock-service must be called")
	require.Equal(t, uint64(0), ord.lastCreateReq.UserId,
		"employee /me/orders resolves to bank owner; nil OwnerID → 0")
	require.Equal(t, "bank", ord.lastCreateReq.SystemType,
		"employee /me/orders resolves to OwnerType=bank")
	require.Equal(t, uint64(11), ord.lastCreateReq.ActingEmployeeId,
		"per-actuary EmployeeLimit gate keys on this — must be the JWT employee id")
}

func TestListMyOrders_ForwardsClientIdentity(t *testing.T) {
	ord := &stubOrderClient{}
	acct := &stubAccountClient{}
	h := handler.NewStockOrderHandler(ord, acct)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/me/orders", clientIdentity(7), h.ListMyOrders)

	req := httptest.NewRequest("GET", "/api/v1/me/orders", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, ord.lastListReq)
	require.Equal(t, uint64(7), ord.lastListReq.UserId)
	require.Equal(t, "client", ord.lastListReq.SystemType)
}

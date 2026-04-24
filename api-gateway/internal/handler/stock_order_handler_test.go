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

// --- helper ---

func makeMyOrdersRouter(h *handler.StockOrderHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/me/orders", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		// use "employee" to bypass enforceOwnership on buy paths in these tests
		c.Set("system_type", "employee")
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

	body := `{"security_type":"forex","direction":"sell","order_type":"market","quantity":1,"account_id":42,"holding_id":7}`
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

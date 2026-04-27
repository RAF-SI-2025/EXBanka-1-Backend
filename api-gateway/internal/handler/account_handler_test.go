// api-gateway/internal/handler/account_handler_test.go
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

	accountpb "github.com/exbanka/contract/accountpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

// accountFullStub is a comprehensive AccountServiceClient stub with function
// fields per RPC method. (The minimal stubAccountClient in
// stock_order_handler_test.go forwards GetAccount only.)

type accountFullStub struct {
	createFn      func(*accountpb.CreateAccountRequest) (*accountpb.AccountResponse, error)
	getFn         func(*accountpb.GetAccountRequest) (*accountpb.AccountResponse, error)
	getByNumFn    func(*accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error)
	listByClient  func(*accountpb.ListAccountsByClientRequest) (*accountpb.ListAccountsResponse, error)
	listAll       func(*accountpb.ListAllAccountsRequest) (*accountpb.ListAccountsResponse, error)
	updName       func(*accountpb.UpdateAccountNameRequest) (*accountpb.AccountResponse, error)
	updLimits     func(*accountpb.UpdateAccountLimitsRequest) (*accountpb.AccountResponse, error)
	updStatus     func(*accountpb.UpdateAccountStatusRequest) (*accountpb.AccountResponse, error)
	createCompany func(*accountpb.CreateCompanyRequest) (*accountpb.CompanyResponse, error)
	listCurr      func(*accountpb.ListCurrenciesRequest) (*accountpb.ListCurrenciesResponse, error)
	getLedger     func(*accountpb.GetLedgerEntriesRequest) (*accountpb.GetLedgerEntriesResponse, error)
}

func (s *accountFullStub) CreateAccount(_ context.Context, in *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &accountpb.AccountResponse{Id: 1, OwnerId: in.OwnerId, AccountNumber: "265-1-00"}, nil
}
func (s *accountFullStub) GetAccount(_ context.Context, in *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &accountpb.AccountResponse{Id: in.Id, OwnerId: 1, AccountNumber: "265-1-00", CurrencyCode: "RSD"}, nil
}
func (s *accountFullStub) GetAccountByNumber(_ context.Context, in *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getByNumFn != nil {
		return s.getByNumFn(in)
	}
	return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, OwnerId: 1}, nil
}
func (s *accountFullStub) ListAccountsByClient(_ context.Context, in *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	if s.listByClient != nil {
		return s.listByClient(in)
	}
	return &accountpb.ListAccountsResponse{}, nil
}
func (s *accountFullStub) ListAllAccounts(_ context.Context, in *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	if s.listAll != nil {
		return s.listAll(in)
	}
	return &accountpb.ListAccountsResponse{}, nil
}
func (s *accountFullStub) UpdateAccountName(_ context.Context, in *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.updName != nil {
		return s.updName(in)
	}
	return &accountpb.AccountResponse{Id: in.Id, AccountName: in.NewName}, nil
}
func (s *accountFullStub) UpdateAccountLimits(_ context.Context, in *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.updLimits != nil {
		return s.updLimits(in)
	}
	return &accountpb.AccountResponse{Id: in.Id}, nil
}
func (s *accountFullStub) UpdateAccountStatus(_ context.Context, in *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.updStatus != nil {
		return s.updStatus(in)
	}
	return &accountpb.AccountResponse{Id: in.Id, Status: in.Status}, nil
}
func (s *accountFullStub) UpdateBalance(_ context.Context, _ *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (s *accountFullStub) CreateCompany(_ context.Context, in *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	if s.createCompany != nil {
		return s.createCompany(in)
	}
	return &accountpb.CompanyResponse{Id: 1, CompanyName: in.CompanyName}, nil
}
func (s *accountFullStub) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return &accountpb.CompanyResponse{}, nil
}
func (s *accountFullStub) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return &accountpb.CompanyResponse{}, nil
}
func (s *accountFullStub) ListCurrencies(_ context.Context, in *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	if s.listCurr != nil {
		return s.listCurr(in)
	}
	return &accountpb.ListCurrenciesResponse{}, nil
}
func (s *accountFullStub) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return &accountpb.CurrencyResponse{}, nil
}
func (s *accountFullStub) GetLedgerEntries(_ context.Context, in *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	if s.getLedger != nil {
		return s.getLedger(in)
	}
	return &accountpb.GetLedgerEntriesResponse{}, nil
}
func (s *accountFullStub) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return &accountpb.ReserveFundsResponse{}, nil
}
func (s *accountFullStub) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return &accountpb.ReleaseReservationResponse{}, nil
}
func (s *accountFullStub) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return &accountpb.PartialSettleReservationResponse{}, nil
}
func (s *accountFullStub) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return &accountpb.GetReservationResponse{}, nil
}
func (s *accountFullStub) ReserveIncoming(_ context.Context, _ *accountpb.ReserveIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return &accountpb.ReserveIncomingResponse{}, nil
}
func (s *accountFullStub) CommitIncoming(_ context.Context, _ *accountpb.CommitIncomingRequest, _ ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return &accountpb.CommitIncomingResponse{}, nil
}
func (s *accountFullStub) ReleaseIncoming(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	return &accountpb.ReleaseIncomingResponse{}, nil
}

// Account handler tests
func accountRouter(h *handler.AccountHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "employee")
	}
	r.POST("/accounts", withCtx, h.CreateAccount)
	r.GET("/accounts", withCtx, h.ListAllAccounts)
	r.GET("/accounts/:id", withCtx, h.GetAccount)
	r.GET("/accounts/by-number/:account_number", withCtx, h.GetAccountByNumber)
	r.GET("/accounts/client/:client_id", withCtx, h.ListAccountsByClient)
	r.PUT("/accounts/:id/name", withCtx, h.UpdateAccountName)
	r.PUT("/accounts/:id/limits", withCtx, h.UpdateAccountLimits)
	r.PUT("/accounts/:id/status", withCtx, h.UpdateAccountStatus)
	r.GET("/currencies", withCtx, h.ListCurrencies)
	r.POST("/companies", withCtx, h.CreateCompany)
	r.GET("/bank-accounts", withCtx, h.ListBankAccounts)
	r.POST("/bank-accounts", withCtx, h.CreateBankAccount)
	r.DELETE("/bank-accounts/:id", withCtx, h.DeleteBankAccount)
	r.GET("/me/accounts", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		h.ListMyAccounts(c)
	})
	r.GET("/me/accounts/:id", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		h.GetMyAccount(c)
	})
	r.GET("/me/accounts/:id/activity", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		h.GetMyAccountActivity(c)
	})
	return r
}

func TestAccount_CreateAccount_Success(t *testing.T) {
	acc := &accountFullStub{}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"owner_id":1,"account_kind":"current","account_type":"personal","currency_code":"RSD","initial_balance":100}`
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestAccount_CreateAccount_BadKind(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"owner_id":1,"account_kind":"savings","account_type":"personal","currency_code":"RSD"}`
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "account_kind")
}

func TestAccount_CreateAccount_BadCategory(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"owner_id":1,"account_kind":"current","account_type":"personal","account_category":"savings","currency_code":"RSD"}`
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_CreateAccount_NegativeBalance(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"owner_id":1,"account_kind":"current","account_type":"personal","currency_code":"RSD","initial_balance":-1}`
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "initial_balance")
}

func TestAccount_CreateAccount_GRPCFailure(t *testing.T) {
	acc := &accountFullStub{
		createFn: func(_ *accountpb.CreateAccountRequest) (*accountpb.AccountResponse, error) {
			return nil, status.Error(codes.Internal, "down")
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"owner_id":1,"account_kind":"current","account_type":"personal","currency_code":"RSD"}`
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAccount_ListAllAccounts_Default(t *testing.T) {
	acc := &accountFullStub{
		listAll: func(req *accountpb.ListAllAccountsRequest) (*accountpb.ListAccountsResponse, error) {
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(20), req.PageSize)
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{Id: 7}}, Total: 1}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":1`)
}

func TestAccount_ListAllAccounts_FilterByClient(t *testing.T) {
	acc := &accountFullStub{
		listByClient: func(req *accountpb.ListAccountsByClientRequest) (*accountpb.ListAccountsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{Id: 100}}, Total: 1}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts?client_id=7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":1`)
}

func TestAccount_ListAllAccounts_BadClientID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts?client_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_GetAccount_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"id":42`)
}

func TestAccount_GetAccount_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_GetAccount_NotFound(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return nil, status.Error(codes.NotFound, "no such account")
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAccount_GetAccountByNumber(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/by-number/265-1-00", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"account_number":"265-1-00"`)
}

func TestAccount_ListAccountsByClient_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/client/5", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_ListAccountsByClient_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/accounts/client/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_UpdateAccountName_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"new_name":"Updated Name","client_id":1}`
	req := httptest.NewRequest("PUT", "/accounts/1/name", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_UpdateAccountName_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("PUT", "/accounts/x/name", strings.NewReader(`{"new_name":"x"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_UpdateAccountLimits_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"daily_limit":1000,"monthly_limit":5000}`
	req := httptest.NewRequest("PUT", "/accounts/1/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_UpdateAccountLimits_NegativeDaily(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"daily_limit":-1}`
	req := httptest.NewRequest("PUT", "/accounts/1/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "daily_limit")
}

func TestAccount_UpdateAccountLimits_NegativeMonthly(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"monthly_limit":-1}`
	req := httptest.NewRequest("PUT", "/accounts/1/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_UpdateAccountStatus_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"status":"active"}`
	req := httptest.NewRequest("PUT", "/accounts/1/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_UpdateAccountStatus_BadStatus(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"status":"weird"}`
	req := httptest.NewRequest("PUT", "/accounts/1/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_ListCurrencies(t *testing.T) {
	acc := &accountFullStub{
		listCurr: func(_ *accountpb.ListCurrenciesRequest) (*accountpb.ListCurrenciesResponse, error) {
			return &accountpb.ListCurrenciesResponse{Currencies: []*accountpb.CurrencyResponse{{Code: "RSD", Name: "Dinar", Symbol: "din"}}}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/currencies", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"RSD"`)
}

func TestAccount_CreateCompany_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"company_name":"ACME","registration_number":"R1","activity_code":"10.1","owner_id":1}`
	req := httptest.NewRequest("POST", "/companies", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"company_name":"ACME"`)
}

func TestAccount_CreateCompany_BadActivityCode(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"company_name":"X","registration_number":"R1","activity_code":"NOTACODE","owner_id":1}`
	req := httptest.NewRequest("POST", "/companies", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "activity code")
}

func TestAccount_ListBankAccounts(t *testing.T) {
	bank := &stubBankAccountClient{
		listFn: func(_ *accountpb.ListBankAccountsRequest) (*accountpb.ListBankAccountsResponse, error) {
			return &accountpb.ListBankAccountsResponse{}, nil
		},
	}
	h := handler.NewAccountHandler(&accountFullStub{}, bank, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/bank-accounts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_CreateBankAccount_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"currency_code":"RSD","account_kind":"current","account_name":"Bank RSD"}`
	req := httptest.NewRequest("POST", "/bank-accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestAccount_CreateBankAccount_BadKind(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	body := `{"currency_code":"RSD","account_kind":"savings"}`
	req := httptest.NewRequest("POST", "/bank-accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_DeleteBankAccount_Success(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("DELETE", "/bank-accounts/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_DeleteBankAccount_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("DELETE", "/bank-accounts/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// /me/* tests

func TestAccount_ListMyAccounts(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_GetMyAccount_Owner(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: 7, OwnerId: 1}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAccount_GetMyAccount_NotOwner(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: 7, OwnerId: 999}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAccount_GetMyAccount_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_GetMyAccountActivity_Owner(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: 7, OwnerId: 1, AccountNumber: "265-1-00", CurrencyCode: "RSD"}, nil
		},
		getLedger: func(_ *accountpb.GetLedgerEntriesRequest) (*accountpb.GetLedgerEntriesResponse, error) {
			return &accountpb.GetLedgerEntriesResponse{
				Entries:    []*accountpb.LedgerEntryResponse{{Id: 1, EntryType: "credit", Amount: "100"}},
				TotalCount: 1,
			}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/7/activity?page=1&page_size=10", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total_count":1`)
}

func TestAccount_GetMyAccountActivity_NotOwner(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: 7, OwnerId: 999}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/7/activity", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAccount_GetMyAccountActivity_PageSizeCap(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(_ *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: 7, OwnerId: 1, AccountNumber: "265-1-00"}, nil
		},
		getLedger: func(req *accountpb.GetLedgerEntriesRequest) (*accountpb.GetLedgerEntriesResponse, error) {
			require.Equal(t, int32(200), req.PageSize, "page_size > 200 should be capped to 200")
			return &accountpb.GetLedgerEntriesResponse{}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	req := httptest.NewRequest("GET", "/me/accounts/7/activity?page_size=500", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// api-gateway/internal/handler/credit_handler_test.go
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

	creditpb "github.com/exbanka/contract/creditpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func creditRouter(h *handler.CreditHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "client")
	}
	withEmployee := func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		c.Set("principal_type", "employee")
	}
	r.POST("/loans/requests", withCtx, h.CreateLoanRequest)
	r.GET("/loans/requests/:id", withCtx, h.GetLoanRequest)
	r.GET("/loans/requests", withEmployee, h.ListLoanRequests)
	r.PUT("/loans/requests/:id/approve", withEmployee, h.ApproveLoanRequest)
	r.PUT("/loans/requests/:id/reject", withEmployee, h.RejectLoanRequest)
	r.GET("/loans/:id", withEmployee, h.GetLoan)
	r.GET("/loans/client/:client_id", withEmployee, h.ListLoansByClient)
	// Phase B: path-scoped /clients/:id/loans replaces /loans?client_id=X
	r.GET("/api/v3/clients/:id/loans", withEmployee, h.ListLoansByClientPath)
	r.GET("/loans", withEmployee, h.ListAllLoans)
	r.GET("/loans/:id/installments", withEmployee, h.GetInstallmentsByLoan)
	r.GET("/loans/requests/client/:client_id", withEmployee, h.ListLoanRequestsByClient)
	r.GET("/interest-rate-tiers", withEmployee, h.ListInterestRateTiers)
	r.POST("/interest-rate-tiers", withEmployee, h.CreateInterestRateTier)
	r.PUT("/interest-rate-tiers/:id", withEmployee, h.UpdateInterestRateTier)
	r.DELETE("/interest-rate-tiers/:id", withEmployee, h.DeleteInterestRateTier)
	r.GET("/bank-margins", withEmployee, h.ListBankMargins)
	r.PUT("/bank-margins/:id", withEmployee, h.UpdateBankMargin)
	r.POST("/loans/apply-variable-rate-update/:id", withEmployee, h.ApplyVariableRateUpdate)
	return r
}

func TestCredit_CreateLoanRequest_Success(t *testing.T) {
	cc := &stubCreditClient{
		createReqFn: func(req *creditpb.CreateLoanRequestReq) (*creditpb.LoanRequestResponse, error) {
			require.Equal(t, "cash", req.LoanType)
			require.Equal(t, "fixed", req.InterestType)
			require.Equal(t, uint64(1), req.ClientId)
			return &creditpb.LoanRequestResponse{Id: 1}, nil
		},
	}
	h := handler.NewCreditHandler(cc)
	r := creditRouter(h)
	body := `{"loan_type":"cash","interest_type":"fixed","amount":1000,"currency_code":"RSD","repayment_period":24,"account_number":"265-1-00"}`
	req := httptest.NewRequest("POST", "/loans/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCredit_CreateLoanRequest_BadLoanType(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"loan_type":"weird","interest_type":"fixed","amount":1000,"currency_code":"RSD","repayment_period":24,"account_number":"a"}`
	req := httptest.NewRequest("POST", "/loans/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateLoanRequest_BadInterestType(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"loan_type":"cash","interest_type":"weird","amount":1000,"currency_code":"RSD","repayment_period":24,"account_number":"a"}`
	req := httptest.NewRequest("POST", "/loans/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateLoanRequest_NegativeAmount(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"loan_type":"cash","interest_type":"fixed","amount":-1,"currency_code":"RSD","repayment_period":24,"account_number":"a"}`
	req := httptest.NewRequest("POST", "/loans/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateLoanRequest_BadRepaymentPeriod(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"loan_type":"cash","interest_type":"fixed","amount":1000,"currency_code":"RSD","repayment_period":15,"account_number":"a"}`
	req := httptest.NewRequest("POST", "/loans/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "not allowed")
}

func TestCredit_GetLoanRequest_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_GetLoanRequest_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListLoanRequests_Default(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ListLoanRequests_FilterByClient(t *testing.T) {
	cc := &stubCreditClient{
		listReqFn: func(req *creditpb.ListLoanRequestsReq) (*creditpb.ListLoanRequestsResponse, error) {
			require.Equal(t, uint64(7), req.ClientIdFilter)
			return &creditpb.ListLoanRequestsResponse{}, nil
		},
	}
	h := handler.NewCreditHandler(cc)
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests?client_id=7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ListLoanRequests_BadClientID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests?client_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ApproveLoanRequest_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/loans/requests/1/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ApproveLoanRequest_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/loans/requests/abc/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ApproveLoanRequest_GRPCError(t *testing.T) {
	cc := &stubCreditClient{
		approveReqFn: func(_ *creditpb.ApproveLoanRequestReq) (*creditpb.LoanResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "limit exceeded")
		},
	}
	h := handler.NewCreditHandler(cc)
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/loans/requests/1/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCredit_RejectLoanRequest_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/loans/requests/1/reject", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_GetLoan_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_GetLoan_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListLoansByClient_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/client/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ListLoansByClient_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/client/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListAllLoans_Default(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// Phase B: /loans?client_id=X dispatch removed — see /clients/:id/loans.

func TestCredit_ListLoansByClientPath_Success(t *testing.T) {
	cc := &stubCreditClient{
		listLoansByClientFn: func(req *creditpb.ListLoansByClientReq) (*creditpb.ListLoansResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &creditpb.ListLoansResponse{Total: 0}, nil
		},
	}
	h := handler.NewCreditHandler(cc)
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/api/v3/clients/7/loans", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ListLoansByClientPath_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/api/v3/clients/abc/loans", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_GetInstallmentsByLoan_Success(t *testing.T) {
	cc := &stubCreditClient{
		getInstallmentsFn: func(req *creditpb.GetInstallmentsByLoanReq) (*creditpb.ListInstallmentsResponse, error) {
			require.Equal(t, uint64(7), req.LoanId)
			return &creditpb.ListInstallmentsResponse{Installments: []*creditpb.InstallmentResponse{{Id: 1, LoanId: 7, Amount: "100"}}}, nil
		},
	}
	h := handler.NewCreditHandler(cc)
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/7/installments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_GetInstallmentsByLoan_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/abc/installments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListLoanRequestsByClient_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests/client/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ListLoanRequestsByClient_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/loans/requests/client/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListInterestRateTiers(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/interest-rate-tiers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_CreateInterestRateTier_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1000,"fixed_rate":5,"variable_base":3}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCredit_CreateInterestRateTier_NegativeAmount(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":-1,"amount_to":1000,"fixed_rate":5,"variable_base":3}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1000,"fixed_rate":5,"variable_base":3}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_UpdateInterestRateTier_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/abc", strings.NewReader(`{"fixed_rate":1,"variable_base":1}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_DeleteInterestRateTier_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("DELETE", "/interest-rate-tiers/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_DeleteInterestRateTier_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("DELETE", "/interest-rate-tiers/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ListBankMargins(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/bank-margins", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_UpdateBankMargin_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"margin":3.5}`
	req := httptest.NewRequest("PUT", "/bank-margins/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_UpdateBankMargin_NegativeMargin(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"margin":-1}`
	req := httptest.NewRequest("PUT", "/bank-margins/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_ApplyVariableRateUpdate_Success(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("POST", "/loans/apply-variable-rate-update/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCredit_ApplyVariableRateUpdate_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("POST", "/loans/apply-variable-rate-update/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

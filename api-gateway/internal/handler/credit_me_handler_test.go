// /me/* endpoints exposed by CreditHandler.
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	creditpb "github.com/exbanka/contract/creditpb"
)

func creditMeRouter(h *handler.CreditHandler, sysType string, uid int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", uid)
		c.Set("system_type", sysType)
	}
	r.GET("/api/v2/me/loans", withCtx, h.ListMyLoans)
	r.GET("/api/v2/me/loans/:id", withCtx, h.GetMyLoan)
	r.GET("/api/v2/me/loans/:id/installments", withCtx, h.GetMyInstallments)
	r.GET("/api/v2/me/loan-requests", withCtx, h.ListMyLoanRequests)
	return r
}

func TestCreditMe_ListMyLoans_Success(t *testing.T) {
	st := &stubCreditClient{
		listLoansByClientFn: func(req *creditpb.ListLoansByClientReq) (*creditpb.ListLoansResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &creditpb.ListLoansResponse{
				Loans: []*creditpb.LoanResponse{{Id: 1, ClientId: 7}},
				Total: 1,
			}, nil
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":1`)
}

func TestCreditMe_ListMyLoans_BadUserContext(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/loans", func(c *gin.Context) {
		c.Set("user_id", "wrong")
		h.ListMyLoans(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/loans", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreditMe_GetMyLoan_Success(t *testing.T) {
	st := &stubCreditClient{
		getLoanFn: func(req *creditpb.GetLoanReq) (*creditpb.LoanResponse, error) {
			require.Equal(t, uint64(99), req.Id)
			return &creditpb.LoanResponse{Id: 99, ClientId: 7}, nil
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"id":99`)
}

func TestCreditMe_GetMyLoan_OwnershipMismatch_Returns404(t *testing.T) {
	st := &stubCreditClient{
		getLoanFn: func(*creditpb.GetLoanReq) (*creditpb.LoanResponse, error) {
			// Resource belongs to client 99, not the caller (7)
			return &creditpb.LoanResponse{Id: 1, ClientId: 99}, nil
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreditMe_GetMyLoan_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreditMe_GetMyLoan_NotFound(t *testing.T) {
	st := &stubCreditClient{
		getLoanFn: func(*creditpb.GetLoanReq) (*creditpb.LoanResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreditMe_GetMyInstallments_Success(t *testing.T) {
	st := &stubCreditClient{
		getInstallmentsFn: func(req *creditpb.GetInstallmentsByLoanReq) (*creditpb.ListInstallmentsResponse, error) {
			require.Equal(t, uint64(99), req.LoanId)
			return &creditpb.ListInstallmentsResponse{
				Installments: []*creditpb.InstallmentResponse{{Id: 1, LoanId: 99}},
			}, nil
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/99/installments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"loan_id":99`)
}

func TestCreditMe_GetMyInstallments_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loans/x/installments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreditMe_ListMyLoanRequests_Success(t *testing.T) {
	st := &stubCreditClient{
		listReqFn: func(req *creditpb.ListLoanRequestsReq) (*creditpb.ListLoanRequestsResponse, error) {
			require.Equal(t, uint64(7), req.ClientIdFilter)
			return &creditpb.ListLoanRequestsResponse{Total: 0}, nil
		},
	}
	h := handler.NewCreditHandler(st)
	r := creditMeRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/loan-requests", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":0`)
}

func TestCreditMe_ListMyLoanRequests_BadUserContext(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/loan-requests", func(c *gin.Context) {
		c.Set("user_id", "wrong")
		h.ListMyLoanRequests(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/loan-requests", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

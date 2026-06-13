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
	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

type stubRecurringFundClient struct {
	createFn func(*stockpb.CreateRecurringFundRequest) (*stockpb.RecurringFundResponse, error)
	getFn    func(*stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error)
	pauseFn  func(*stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error)
	resumeFn func(*stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error)
	cancelFn func(*stockpb.GetRecurringFundRequest) (*stockpb.CancelRecurringFundResponse, error)
	listMyFn func(*stockpb.ListMyRecurringFundsRequest) (*stockpb.ListMyRecurringFundsResponse, error)
}

func (s *stubRecurringFundClient) Create(_ context.Context, in *stockpb.CreateRecurringFundRequest, _ ...grpc.CallOption) (*stockpb.RecurringFundResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.RecurringFundResponse{}, nil
}
func (s *stubRecurringFundClient) Get(_ context.Context, in *stockpb.GetRecurringFundRequest, _ ...grpc.CallOption) (*stockpb.RecurringFundResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.RecurringFundResponse{}, nil
}
func (s *stubRecurringFundClient) Pause(_ context.Context, in *stockpb.GetRecurringFundRequest, _ ...grpc.CallOption) (*stockpb.RecurringFundResponse, error) {
	if s.pauseFn != nil {
		return s.pauseFn(in)
	}
	return &stockpb.RecurringFundResponse{}, nil
}
func (s *stubRecurringFundClient) Resume(_ context.Context, in *stockpb.GetRecurringFundRequest, _ ...grpc.CallOption) (*stockpb.RecurringFundResponse, error) {
	if s.resumeFn != nil {
		return s.resumeFn(in)
	}
	return &stockpb.RecurringFundResponse{}, nil
}
func (s *stubRecurringFundClient) Cancel(_ context.Context, in *stockpb.GetRecurringFundRequest, _ ...grpc.CallOption) (*stockpb.CancelRecurringFundResponse, error) {
	if s.cancelFn != nil {
		return s.cancelFn(in)
	}
	return &stockpb.CancelRecurringFundResponse{}, nil
}
func (s *stubRecurringFundClient) ListMy(_ context.Context, in *stockpb.ListMyRecurringFundsRequest, _ ...grpc.CallOption) (*stockpb.ListMyRecurringFundsResponse, error) {
	if s.listMyFn != nil {
		return s.listMyFn(in)
	}
	return &stockpb.ListMyRecurringFundsResponse{}, nil
}

var _ stockpb.RecurringFundServiceClient = (*stubRecurringFundClient)(nil)

func recurringFundRouter(h *handler.RecurringFundHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/api/v3/me/recurring-funds", withCli, h.Create)
	r.GET("/api/v3/me/recurring-funds/:id", withCli, h.Get)
	r.POST("/api/v3/me/recurring-funds/:id/pause", withCli, h.Pause)
	r.POST("/api/v3/me/recurring-funds/:id/resume", withCli, h.Resume)
	r.DELETE("/api/v3/me/recurring-funds/:id", withCli, h.Cancel)
	r.GET("/api/v3/me/recurring-funds", withCli, h.ListMy)
	return r
}

// recurringFundEmployeeRouter wires the Create route with a bank-owner
// identity and no client principal, so callerClientID resolves to 0 and the
// "only clients" guard fires.
func recurringFundEmployeeRouter(h *handler.RecurringFundHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	bankNoPrincipal := func(c *gin.Context) {
		c.Set("identity", &middleware.ResolvedIdentity{
			PrincipalType: "employee",
			OwnerType:     "bank",
			OwnerID:       nil,
		})
		c.Set("principal_id", int64(0))
		c.Next()
	}
	r.POST("/api/v3/me/recurring-funds", bankNoPrincipal, h.Create)
	return r
}

func rfDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestRecurringFund_Create_Success(t *testing.T) {
	var captured *stockpb.CreateRecurringFundRequest
	cl := &stubRecurringFundClient{createFn: func(in *stockpb.CreateRecurringFundRequest) (*stockpb.RecurringFundResponse, error) {
		captured = in
		return &stockpb.RecurringFundResponse{Id: 1}, nil
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	body := `{"fund_id":3,"amount_rsd":"5000","source_account_id":9,"day_of_month":10}`
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, uint64(42), captured.ClientId)
	require.Equal(t, uint64(3), captured.FundId)
	require.Equal(t, "5000", captured.AmountRsd)
	require.Equal(t, int32(10), captured.DayOfMonth)
}

func TestRecurringFund_Create_BadBody(t *testing.T) {
	r := recurringFundRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds", `nope`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecurringFund_Create_MissingFields(t *testing.T) {
	r := recurringFundRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds", `{"fund_id":0,"amount_rsd":"","source_account_id":0,"day_of_month":5}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "fund_id, source_account_id, amount_rsd are required")
}

func TestRecurringFund_Create_BadDayOfMonth(t *testing.T) {
	r := recurringFundRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds", `{"fund_id":3,"amount_rsd":"5000","source_account_id":9,"day_of_month":31}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "day_of_month must be 1..28")
}

func TestRecurringFund_Create_EmployeeForbidden(t *testing.T) {
	r := recurringFundEmployeeRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds", `{"fund_id":3,"amount_rsd":"5000","source_account_id":9,"day_of_month":10}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "only clients can create recurring fund investments")
}

func TestRecurringFund_Get_Success(t *testing.T) {
	var captured *stockpb.GetRecurringFundRequest
	cl := &stubRecurringFundClient{getFn: func(in *stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error) {
		captured = in
		return &stockpb.RecurringFundResponse{Id: in.Id}, nil
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	rec := rfDo(r, "GET", "/api/v3/me/recurring-funds/4", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(4), captured.Id)
	require.Equal(t, uint64(42), captured.ClientId)
}

func TestRecurringFund_Get_BadID(t *testing.T) {
	r := recurringFundRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "GET", "/api/v3/me/recurring-funds/zz", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}

func TestRecurringFund_Pause_Success(t *testing.T) {
	cl := &stubRecurringFundClient{pauseFn: func(in *stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error) {
		return &stockpb.RecurringFundResponse{Id: in.Id}, nil
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds/4/pause", "")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRecurringFund_Resume_GRPCError(t *testing.T) {
	cl := &stubRecurringFundClient{resumeFn: func(*stockpb.GetRecurringFundRequest) (*stockpb.RecurringFundResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "not paused")
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	rec := rfDo(r, "POST", "/api/v3/me/recurring-funds/4/resume", "")
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestRecurringFund_Cancel_Success(t *testing.T) {
	var captured *stockpb.GetRecurringFundRequest
	cl := &stubRecurringFundClient{cancelFn: func(in *stockpb.GetRecurringFundRequest) (*stockpb.CancelRecurringFundResponse, error) {
		captured = in
		return &stockpb.CancelRecurringFundResponse{}, nil
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	rec := rfDo(r, "DELETE", "/api/v3/me/recurring-funds/4", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(4), captured.Id)
}

func TestRecurringFund_Cancel_BadID(t *testing.T) {
	r := recurringFundRouter(handler.NewRecurringFundHandler(&stubRecurringFundClient{}))
	rec := rfDo(r, "DELETE", "/api/v3/me/recurring-funds/zz", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecurringFund_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyRecurringFundsRequest
	cl := &stubRecurringFundClient{listMyFn: func(in *stockpb.ListMyRecurringFundsRequest) (*stockpb.ListMyRecurringFundsResponse, error) {
		captured = in
		return &stockpb.ListMyRecurringFundsResponse{}, nil
	}}
	r := recurringFundRouter(handler.NewRecurringFundHandler(cl))
	rec := rfDo(r, "GET", "/api/v3/me/recurring-funds", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(42), captured.ClientId)
	require.Contains(t, rec.Body.String(), `"recurring_funds"`)
}

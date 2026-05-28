// api-gateway/internal/handler/investment_fund_handler_test.go
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

// stubInvestmentFundClient implements stockpb.InvestmentFundServiceClient with
// per-method function fields.
type stubInvestmentFundClient struct {
	createFn       func(*stockpb.CreateFundRequest) (*stockpb.FundResponse, error)
	listFn         func(*stockpb.ListFundsRequest) (*stockpb.ListFundsResponse, error)
	getFn          func(*stockpb.GetFundRequest) (*stockpb.FundDetailResponse, error)
	updateFn       func(*stockpb.UpdateFundRequest) (*stockpb.FundResponse, error)
	investFn       func(*stockpb.InvestInFundRequest) (*stockpb.ContributionResponse, error)
	redeemFn       func(*stockpb.RedeemFromFundRequest) (*stockpb.ContributionResponse, error)
	myPositionsFn  func(*stockpb.ListMyPositionsRequest) (*stockpb.ListPositionsResponse, error)
	bnkPositionsFn func(*stockpb.ListBankPositionsRequest) (*stockpb.ListPositionsResponse, error)
	actuaryFn      func(*stockpb.GetActuaryPerformanceRequest) (*stockpb.GetActuaryPerformanceResponse, error)
}

func (s *stubInvestmentFundClient) CreateFund(_ context.Context, in *stockpb.CreateFundRequest, _ ...grpc.CallOption) (*stockpb.FundResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.FundResponse{Id: 1, Name: in.Name}, nil
}
func (s *stubInvestmentFundClient) ListFunds(_ context.Context, in *stockpb.ListFundsRequest, _ ...grpc.CallOption) (*stockpb.ListFundsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListFundsResponse{}, nil
}
func (s *stubInvestmentFundClient) GetFund(_ context.Context, in *stockpb.GetFundRequest, _ ...grpc.CallOption) (*stockpb.FundDetailResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.FundDetailResponse{}, nil
}
func (s *stubInvestmentFundClient) UpdateFund(_ context.Context, in *stockpb.UpdateFundRequest, _ ...grpc.CallOption) (*stockpb.FundResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	return &stockpb.FundResponse{Id: in.FundId}, nil
}
func (s *stubInvestmentFundClient) InvestInFund(_ context.Context, in *stockpb.InvestInFundRequest, _ ...grpc.CallOption) (*stockpb.ContributionResponse, error) {
	if s.investFn != nil {
		return s.investFn(in)
	}
	return &stockpb.ContributionResponse{}, nil
}
func (s *stubInvestmentFundClient) RedeemFromFund(_ context.Context, in *stockpb.RedeemFromFundRequest, _ ...grpc.CallOption) (*stockpb.ContributionResponse, error) {
	if s.redeemFn != nil {
		return s.redeemFn(in)
	}
	return &stockpb.ContributionResponse{}, nil
}
func (s *stubInvestmentFundClient) ListMyPositions(_ context.Context, in *stockpb.ListMyPositionsRequest, _ ...grpc.CallOption) (*stockpb.ListPositionsResponse, error) {
	if s.myPositionsFn != nil {
		return s.myPositionsFn(in)
	}
	return &stockpb.ListPositionsResponse{}, nil
}
func (s *stubInvestmentFundClient) ListBankPositions(_ context.Context, in *stockpb.ListBankPositionsRequest, _ ...grpc.CallOption) (*stockpb.ListPositionsResponse, error) {
	if s.bnkPositionsFn != nil {
		return s.bnkPositionsFn(in)
	}
	return &stockpb.ListPositionsResponse{}, nil
}
func (s *stubInvestmentFundClient) GetActuaryPerformance(_ context.Context, in *stockpb.GetActuaryPerformanceRequest, _ ...grpc.CallOption) (*stockpb.GetActuaryPerformanceResponse, error) {
	if s.actuaryFn != nil {
		return s.actuaryFn(in)
	}
	return &stockpb.GetActuaryPerformanceResponse{}, nil
}

// E4 dividend stubs — return empty responses so existing tests continue to
// compile and pass without being affected by the new RPC additions.
func (s *stubInvestmentFundClient) DeclareDividend(_ context.Context, _ *stockpb.DeclareDividendRequest, _ ...grpc.CallOption) (*stockpb.DividendPaymentResponse, error) {
	return &stockpb.DividendPaymentResponse{}, nil
}
func (s *stubInvestmentFundClient) PayoutDividend(_ context.Context, _ *stockpb.PayoutDividendRequest, _ ...grpc.CallOption) (*stockpb.PayoutDividendResponse, error) {
	return &stockpb.PayoutDividendResponse{}, nil
}
func (s *stubInvestmentFundClient) ListMyDividends(_ context.Context, _ *stockpb.ListMyDividendsRequest, _ ...grpc.CallOption) (*stockpb.ListDividendPayoutsResponse, error) {
	return &stockpb.ListDividendPayoutsResponse{}, nil
}
func (s *stubInvestmentFundClient) ListFundDividends(_ context.Context, _ *stockpb.ListFundDividendsRequest, _ ...grpc.CallOption) (*stockpb.ListFundDividendPaymentsResponse, error) {
	return &stockpb.ListFundDividendPaymentsResponse{}, nil
}

var _ stockpb.InvestmentFundServiceClient = (*stubInvestmentFundClient)(nil)

func investmentFundRouter(h *handler.InvestmentFundHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/funds", setEmployeeBankIdentity(7), h.CreateFund)
	r.GET("/funds", h.ListFunds)
	r.GET("/funds/:id", h.GetFund)
	r.PUT("/funds/:id", setEmployeeBankIdentity(7), h.UpdateFund)
	r.POST("/funds/:id/invest", setClientIdentity(42), h.Invest)
	r.POST("/funds/:id/redeem", setClientIdentity(42), h.Redeem)
	r.GET("/me/positions", setClientIdentity(42), h.ListMyPositions)
	r.GET("/positions", h.ListBankPositions)
	r.GET("/actuaries/performance", h.ActuaryPerformance)
	return r
}

func TestFund_CreateFund_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		createFn: func(in *stockpb.CreateFundRequest) (*stockpb.FundResponse, error) {
			require.Equal(t, "Growth", in.Name)
			require.Equal(t, int64(7), in.ActorEmployeeId)
			return &stockpb.FundResponse{Id: 99, Name: in.Name}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"name":"Growth","description":"x","minimum_contribution_rsd":"100"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestFund_CreateFund_BadBody(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_CreateFund_MissingName(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds", strings.NewReader(`{"description":"x"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "name is required")
}

func TestFund_CreateFund_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		createFn: func(*stockpb.CreateFundRequest) (*stockpb.FundResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds", strings.NewReader(`{"name":"X"}`)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFund_ListFunds_DefaultsAndOptions(t *testing.T) {
	var captured *stockpb.ListFundsRequest
	cl := &stubInvestmentFundClient{
		listFn: func(in *stockpb.ListFundsRequest) (*stockpb.ListFundsResponse, error) {
			captured = in
			return &stockpb.ListFundsResponse{Total: 0}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/funds?page=2&page_size=5&search=Tech&active_only=true", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, int32(2), captured.Page)
	require.Equal(t, int32(5), captured.PageSize)
	require.Equal(t, "Tech", captured.Search)
	require.True(t, captured.ActiveOnly)
}

func TestFund_ListFunds_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		listFn: func(*stockpb.ListFundsRequest) (*stockpb.ListFundsResponse, error) {
			return nil, status.Error(codes.Internal, "x")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/funds", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestFund_GetFund_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		getFn: func(in *stockpb.GetFundRequest) (*stockpb.FundDetailResponse, error) {
			require.Equal(t, uint64(11), in.FundId)
			return &stockpb.FundDetailResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/funds/11", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestFund_GetFund_BadID(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/funds/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_UpdateFund_AllFields(t *testing.T) {
	cl := &stubInvestmentFundClient{
		updateFn: func(in *stockpb.UpdateFundRequest) (*stockpb.FundResponse, error) {
			require.Equal(t, "newname", in.Name)
			require.Equal(t, "newdesc", in.Description)
			require.Equal(t, "100", in.MinimumContributionRsd)
			require.True(t, in.ActiveSet)
			require.False(t, in.Active)
			require.Equal(t, int64(7), in.ActorEmployeeId)
			return &stockpb.FundResponse{Id: in.FundId}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"name":"newname","description":"newdesc","minimum_contribution_rsd":"100","active":false}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/funds/5", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestFund_UpdateFund_BadBody(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/funds/5", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_UpdateFund_BadID(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/funds/x", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Invest_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		investFn: func(in *stockpb.InvestInFundRequest) (*stockpb.ContributionResponse, error) {
			require.Equal(t, uint64(8), in.FundId)
			require.Equal(t, "100", in.Amount)
			require.Equal(t, "RSD", in.Currency)
			require.Equal(t, "self", in.OnBehalfOf.GetType())
			return &stockpb.ContributionResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"source_account_id":1,"amount":"100","currency":"RSD"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/invest", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestFund_Invest_MissingFields(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/invest", strings.NewReader(`{"source_account_id":1}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Invest_BadID(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/abc/invest", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Invest_BadBody(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/invest", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Redeem_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		redeemFn: func(in *stockpb.RedeemFromFundRequest) (*stockpb.ContributionResponse, error) {
			require.Equal(t, "200", in.AmountRsd)
			require.Equal(t, uint64(3), in.TargetAccountId)
			require.Equal(t, "self", in.OnBehalfOf.GetType())
			return &stockpb.ContributionResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"amount_rsd":"200","target_account_id":3}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/redeem", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestFund_Redeem_MissingFields(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/redeem", strings.NewReader(`{"amount_rsd":""}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Redeem_BadID(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/x/redeem", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_Redeem_BadBody(t *testing.T) {
	r := investmentFundRouter(handler.NewInvestmentFundHandler(&stubInvestmentFundClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/redeem", strings.NewReader("nope")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFund_ListMyPositions_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		myPositionsFn: func(in *stockpb.ListMyPositionsRequest) (*stockpb.ListPositionsResponse, error) {
			require.Equal(t, uint64(42), in.ActorUserId)
			require.Equal(t, "client", in.ActorSystemType)
			return &stockpb.ListPositionsResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/positions", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestFund_ListBankPositions_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		bnkPositionsFn: func(*stockpb.ListBankPositionsRequest) (*stockpb.ListPositionsResponse, error) {
			return &stockpb.ListPositionsResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/positions", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestFund_ListBankPositions_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		bnkPositionsFn: func(*stockpb.ListBankPositionsRequest) (*stockpb.ListPositionsResponse, error) {
			return nil, status.Error(codes.Internal, "x")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/positions", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestFund_ActuaryPerformance_Success(t *testing.T) {
	cl := &stubInvestmentFundClient{
		actuaryFn: func(*stockpb.GetActuaryPerformanceRequest) (*stockpb.GetActuaryPerformanceResponse, error) {
			return &stockpb.GetActuaryPerformanceResponse{}, nil
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/actuaries/performance", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestFund_ActuaryPerformance_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		actuaryFn: func(*stockpb.GetActuaryPerformanceRequest) (*stockpb.GetActuaryPerformanceResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/actuaries/performance", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestFund_ListMyPositions_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		myPositionsFn: func(*stockpb.ListMyPositionsRequest) (*stockpb.ListPositionsResponse, error) {
			return nil, status.Error(codes.Internal, "x")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/positions", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestFund_Invest_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		investFn: func(*stockpb.InvestInFundRequest) (*stockpb.ContributionResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "below min")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"source_account_id":1,"amount":"100","currency":"RSD"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/invest", strings.NewReader(body)))
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestFund_Redeem_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{
		redeemFn: func(*stockpb.RedeemFromFundRequest) (*stockpb.ContributionResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	r := investmentFundRouter(handler.NewInvestmentFundHandler(cl))
	body := `{"amount_rsd":"200","target_account_id":3}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/funds/8/redeem", strings.NewReader(body)))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

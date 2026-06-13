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

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

func dividendRouter(h *handler.DividendHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	emp := func(c *gin.Context) { c.Set("principal_id", int64(7)) }
	r.POST("/api/v3/admin/dividends", emp, h.DeclareDividend)
	r.POST("/api/v3/admin/dividends/:id/payout", emp, h.PayoutDividend)
	r.GET("/api/v3/me/dividends", setClientIdentity(42), h.ListMyDividends)
	r.GET("/api/v3/investment-funds/:id/dividends", setClientIdentity(42), h.ListFundDividends)
	return r
}

func divDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestDividend_Declare_Success(t *testing.T) {
	var captured *stockpb.DeclareDividendRequest
	cl := &stubInvestmentFundClient{declareDivFn: func(in *stockpb.DeclareDividendRequest) (*stockpb.DividendPaymentResponse, error) {
		captured = in
		return &stockpb.DividendPaymentResponse{Id: 1}, nil
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	body := `{"security_id":3,"ticker":"AAPL","amount_per_share_rsd":"12.5","payment_date":"2026-06-15"}`
	rec := divDo(r, "POST", "/api/v3/admin/dividends", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, int64(7), captured.DeclaredByEmployeeId)
	require.Equal(t, uint64(3), captured.SecurityId)
	require.Equal(t, "AAPL", captured.Ticker)
	require.Equal(t, "2026-06-15", captured.PaymentDate)
}

func TestDividend_Declare_BadBody(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends", `nope`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid request body")
}

func TestDividend_Declare_MissingSecurityID(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends", `{"security_id":0,"ticker":"AAPL","amount_per_share_rsd":"1","payment_date":"2026-06-15"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "security_id is required")
}

func TestDividend_Declare_MissingTicker(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends", `{"security_id":3,"ticker":"","amount_per_share_rsd":"1","payment_date":"2026-06-15"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "ticker is required")
}

func TestDividend_Declare_MissingAmount(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends", `{"security_id":3,"ticker":"AAPL","amount_per_share_rsd":"","payment_date":"2026-06-15"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "amount_per_share_rsd is required")
}

func TestDividend_Declare_MissingDate(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends", `{"security_id":3,"ticker":"AAPL","amount_per_share_rsd":"1","payment_date":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "payment_date is required")
}

func TestDividend_Declare_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{declareDivFn: func(*stockpb.DeclareDividendRequest) (*stockpb.DividendPaymentResponse, error) {
		return nil, status.Error(codes.AlreadyExists, "dup")
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	body := `{"security_id":3,"ticker":"AAPL","amount_per_share_rsd":"1","payment_date":"2026-06-15"}`
	rec := divDo(r, "POST", "/api/v3/admin/dividends", body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestDividend_Payout_Success(t *testing.T) {
	var captured *stockpb.PayoutDividendRequest
	cl := &stubInvestmentFundClient{payoutDivFn: func(in *stockpb.PayoutDividendRequest) (*stockpb.PayoutDividendResponse, error) {
		captured = in
		return &stockpb.PayoutDividendResponse{PayoutsCreated: 5, FundPayouts: 1, TotalAmountRsd: "999"}, nil
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	rec := divDo(r, "POST", "/api/v3/admin/dividends/8/payout", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(8), captured.DividendPaymentId)
	require.Contains(t, rec.Body.String(), `"payouts_created":5`)
	require.Contains(t, rec.Body.String(), `"total_amount_rsd":"999"`)
}

func TestDividend_Payout_BadID(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "POST", "/api/v3/admin/dividends/xx/payout", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}

func TestDividend_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyDividendsRequest
	cl := &stubInvestmentFundClient{myDivFn: func(in *stockpb.ListMyDividendsRequest) (*stockpb.ListDividendPayoutsResponse, error) {
		captured = in
		return &stockpb.ListDividendPayoutsResponse{Total: 0}, nil
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	rec := divDo(r, "GET", "/api/v3/me/dividends?page=2&page_size=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Equal(t, int32(2), captured.Page)
	require.Equal(t, int32(10), captured.PageSize)
	require.Contains(t, rec.Body.String(), `"payouts"`)
}

func TestDividend_ListMy_GRPCError(t *testing.T) {
	cl := &stubInvestmentFundClient{myDivFn: func(*stockpb.ListMyDividendsRequest) (*stockpb.ListDividendPayoutsResponse, error) {
		return nil, status.Error(codes.Internal, "boom")
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	rec := divDo(r, "GET", "/api/v3/me/dividends", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestDividend_ListFund_Success(t *testing.T) {
	var captured *stockpb.ListFundDividendsRequest
	cl := &stubInvestmentFundClient{fundDivFn: func(in *stockpb.ListFundDividendsRequest) (*stockpb.ListFundDividendPaymentsResponse, error) {
		captured = in
		return &stockpb.ListFundDividendPaymentsResponse{Total: 0}, nil
	}}
	r := dividendRouter(handler.NewDividendHandler(cl))
	rec := divDo(r, "GET", "/api/v3/investment-funds/6/dividends", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(6), captured.FundId)
	require.Contains(t, rec.Body.String(), `"payments"`)
}

func TestDividend_ListFund_BadID(t *testing.T) {
	r := dividendRouter(handler.NewDividendHandler(&stubInvestmentFundClient{}))
	rec := divDo(r, "GET", "/api/v3/investment-funds/zz/dividends", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid fund id")
}

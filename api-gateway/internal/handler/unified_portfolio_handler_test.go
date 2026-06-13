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
	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

// setEmployeeWithPerms installs an employee/bank identity plus a permission set
// in the gin context (the same shape AuthMiddleware writes), so the unified
// portfolio access checks that read GetCallerPermissions can be exercised.
func setEmployeeWithPerms(empID uint64, perms ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := empID
		c.Set("principal_id", int64(empID))
		c.Set("principal_type", "employee")
		c.Set("permissions", perms)
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

func unifiedClientRouter(h *handler.UnifiedPortfolioHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/me/portfolio", setClientIdentity(42), h.GetMy)
	r.GET("/api/v3/portfolio/:portfolio_id", setClientIdentity(42), h.GetByPortfolioID)
	return r
}

func TestUnifiedPortfolio_GetMy_Client(t *testing.T) {
	var captured *stockpb.GetUnifiedPortfolioRequest
	st := &portfolioStub{getUnifiedFn: func(in *stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
		captured = in
		return &stockpb.UnifiedPortfolioResponse{}, nil
	}}
	r := unifiedClientRouter(handler.NewUnifiedPortfolioHandler(st))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/me/portfolio", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestUnifiedPortfolio_GetByPortfolioID_OwnClient(t *testing.T) {
	st := &portfolioStub{}
	r := unifiedClientRouter(handler.NewUnifiedPortfolioHandler(st))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client-42", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUnifiedPortfolio_GetByPortfolioID_OtherClientForbidden(t *testing.T) {
	st := &portfolioStub{}
	r := unifiedClientRouter(handler.NewUnifiedPortfolioHandler(st))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client-99", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUnifiedPortfolio_GetByPortfolioID_BadID(t *testing.T) {
	st := &portfolioStub{}
	r := unifiedClientRouter(handler.NewUnifiedPortfolioHandler(st))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/garbage", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnifiedPortfolio_GetByPortfolioID_GRPCError(t *testing.T) {
	st := &portfolioStub{getUnifiedFn: func(*stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
		return nil, status.Error(codes.Internal, "boom")
	}}
	r := unifiedClientRouter(handler.NewUnifiedPortfolioHandler(st))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client-42", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnifiedPortfolio_GetByClientID_EmployeeWithPerm(t *testing.T) {
	var captured *stockpb.GetUnifiedPortfolioRequest
	st := &portfolioStub{getUnifiedFn: func(in *stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
		captured = in
		return &stockpb.UnifiedPortfolioResponse{}, nil
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/client/:client_id",
		setEmployeeWithPerms(7, "portfolio.view.client"),
		handler.NewUnifiedPortfolioHandler(st).GetByClientID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client/55", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(55), captured.OwnerId)
}

func TestUnifiedPortfolio_GetByClientID_EmployeeMissingPerm(t *testing.T) {
	st := &portfolioStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/client/:client_id",
		setEmployeeWithPerms(7),
		handler.NewUnifiedPortfolioHandler(st).GetByClientID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client/55", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUnifiedPortfolio_GetByClientID_BadID(t *testing.T) {
	st := &portfolioStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/client/:client_id",
		setEmployeeWithPerms(7, "portfolio.view.client"),
		handler.NewUnifiedPortfolioHandler(st).GetByClientID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/client/0", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnifiedPortfolio_GetBank_Employee(t *testing.T) {
	var captured *stockpb.GetUnifiedPortfolioRequest
	st := &portfolioStub{getUnifiedFn: func(in *stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
		captured = in
		return &stockpb.UnifiedPortfolioResponse{}, nil
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/bank",
		setEmployeeWithPerms(7),
		handler.NewUnifiedPortfolioHandler(st).GetBank)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/bank", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "bank", captured.OwnerType)
}

func TestUnifiedPortfolio_GetByFundID_EmployeeWithPerm(t *testing.T) {
	var captured *stockpb.GetUnifiedPortfolioRequest
	st := &portfolioStub{getUnifiedFn: func(in *stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
		captured = in
		return &stockpb.UnifiedPortfolioResponse{}, nil
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/investment-fund/:fund_id",
		setEmployeeWithPerms(7, "portfolio.view.fund"),
		handler.NewUnifiedPortfolioHandler(st).GetByFundID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/investment-fund/12", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "investment_fund", captured.OwnerType)
	require.Equal(t, uint64(12), captured.OwnerId)
}

func TestUnifiedPortfolio_GetByFundID_BadID(t *testing.T) {
	st := &portfolioStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/investment-fund/:fund_id",
		setEmployeeWithPerms(7, "portfolio.view.fund"),
		handler.NewUnifiedPortfolioHandler(st).GetByFundID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/investment-fund/0", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnifiedPortfolio_GetByFundID_EmployeeMissingPerm(t *testing.T) {
	st := &portfolioStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/portfolio/investment-fund/:fund_id",
		setEmployeeWithPerms(7),
		handler.NewUnifiedPortfolioHandler(st).GetByFundID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/portfolio/investment-fund/12", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

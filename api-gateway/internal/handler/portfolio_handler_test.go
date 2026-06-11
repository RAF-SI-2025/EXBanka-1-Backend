package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// portfolioStub satisfies stockpb.PortfolioGRPCServiceClient with per-method
// function fields. Keeps existing stubPortfolioClient (in
// options_v2_handler_test.go) untouched.
type portfolioStub struct {
	listFn         func(*stockpb.ListHoldingsRequest) (*stockpb.ListHoldingsResponse, error)
	summaryFn      func(*stockpb.GetPortfolioSummaryRequest) (*stockpb.PortfolioSummary, error)
	exerciseFn     func(*stockpb.ExerciseOptionRequest) (*stockpb.ExerciseResult, error)
	listTxFn       func(*stockpb.ListHoldingTransactionsRequest) (*stockpb.ListHoldingTransactionsResponse, error)
	exerciseByIDFn func(*stockpb.ExerciseOptionByOptionIDRequest) (*stockpb.ExerciseResult, error)
	getHoldingFn   func(*stockpb.GetHoldingRequest) (*stockpb.HoldingWithOwner, error)
}

func (s *portfolioStub) ListHoldings(_ context.Context, in *stockpb.ListHoldingsRequest, _ ...grpc.CallOption) (*stockpb.ListHoldingsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListHoldingsResponse{}, nil
}
func (s *portfolioStub) GetPortfolioSummary(_ context.Context, in *stockpb.GetPortfolioSummaryRequest, _ ...grpc.CallOption) (*stockpb.PortfolioSummary, error) {
	if s.summaryFn != nil {
		return s.summaryFn(in)
	}
	return &stockpb.PortfolioSummary{}, nil
}
func (s *portfolioStub) ExerciseOption(_ context.Context, in *stockpb.ExerciseOptionRequest, _ ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	if s.exerciseFn != nil {
		return s.exerciseFn(in)
	}
	return &stockpb.ExerciseResult{}, nil
}
func (s *portfolioStub) ListHoldingTransactions(_ context.Context, in *stockpb.ListHoldingTransactionsRequest, _ ...grpc.CallOption) (*stockpb.ListHoldingTransactionsResponse, error) {
	if s.listTxFn != nil {
		return s.listTxFn(in)
	}
	return &stockpb.ListHoldingTransactionsResponse{}, nil
}
func (s *portfolioStub) ExerciseOptionByOptionID(_ context.Context, in *stockpb.ExerciseOptionByOptionIDRequest, _ ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	if s.exerciseByIDFn != nil {
		return s.exerciseByIDFn(in)
	}
	return &stockpb.ExerciseResult{}, nil
}

func (s *portfolioStub) GetUnifiedPortfolio(_ context.Context, _ *stockpb.GetUnifiedPortfolioRequest, _ ...grpc.CallOption) (*stockpb.UnifiedPortfolioResponse, error) {
	return &stockpb.UnifiedPortfolioResponse{}, nil
}

// GetHolding stub: per-test fn or a default "owned by client 42" row so
// the new R5 ownership pre-check in ExerciseOption tests passes without
// per-test wiring. Tests that need to exercise the 404 path can set
// getHoldingFn to return a different owner_id.
func (s *portfolioStub) GetHolding(_ context.Context, in *stockpb.GetHoldingRequest, _ ...grpc.CallOption) (*stockpb.HoldingWithOwner, error) {
	if s.getHoldingFn != nil {
		return s.getHoldingFn(in)
	}
	return &stockpb.HoldingWithOwner{
		Holding:   &stockpb.Holding{Id: in.GetId(), SecurityType: "option", Ticker: "OPT-test"},
		OwnerType: "client",
		OwnerId:   42,
	}, nil
}

// setClientIdentity mimics what AuthMiddleware + ResolveIdentity(OwnerIsBankIfEmployee)
// install for a logged-in client (post-Spec-C-Task-7 schema). It writes both
// principal_* keys (read by helpers that still consume them) AND the
// "identity" key that handlers now read directly.
func setClientIdentity(uid uint64) gin.HandlerFunc {
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

// setEmployeeBankIdentity mimics ResolveIdentity(OwnerIsBankIfEmployee) for an
// employee principal — owner becomes "bank" with nil OwnerID, and
// ActingEmployeeID carries the JWT principal_id.
func setEmployeeBankIdentity(empID uint64) gin.HandlerFunc {
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

func portfolioRouter(h *handler.PortfolioHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := setClientIdentity(42)
	r.GET("/api/v2/me/holdings", withCtx, h.ListHoldings)
	r.GET("/api/v2/me/portfolio/summary", withCtx, h.GetPortfolioSummary)
	r.GET("/api/v2/me/holdings/:id/transactions", withCtx, h.ListHoldingTransactions)
	r.POST("/api/v2/me/holdings/:id/exercise", withCtx, h.ExerciseOption)
	r.GET("/api/v3/otc/options", withCtx, h.ListOTCOptions)
	r.GET("/api/v3/me/otc/options", withCtx, h.ListMyOTCOptions)
	return r
}

func TestPortfolio_ListHoldings_Success(t *testing.T) {
	st := &portfolioStub{
		listFn: func(req *stockpb.ListHoldingsRequest) (*stockpb.ListHoldingsResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, "client", req.SystemType)
			return &stockpb.ListHoldingsResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"holdings":[]`)
}

func TestPortfolio_ListHoldings_BadSecurityType(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings?security_type=foo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "security_type must be one of")
}

func TestPortfolio_ListHoldings_GRPCError(t *testing.T) {
	st := &portfolioStub{
		listFn: func(*stockpb.ListHoldingsRequest) (*stockpb.ListHoldingsResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortfolio_GetPortfolioSummary_Success(t *testing.T) {
	st := &portfolioStub{
		summaryFn: func(req *stockpb.GetPortfolioSummaryRequest) (*stockpb.PortfolioSummary, error) {
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, "client", req.SystemType)
			return &stockpb.PortfolioSummary{TotalProfitRsd: "1000"}, nil
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/portfolio/summary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPortfolio_ListHoldingTransactions_BadID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings/x/transactions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_ListHoldingTransactions_BadDirection(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings/9/transactions?direction=neutral", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "direction must be one of")
}

func TestPortfolio_ListHoldingTransactions_Success(t *testing.T) {
	st := &portfolioStub{
		listTxFn: func(req *stockpb.ListHoldingTransactionsRequest) (*stockpb.ListHoldingTransactionsResponse, error) {
			require.Equal(t, uint64(9), req.HoldingId)
			require.Equal(t, "buy", req.Direction)
			return &stockpb.ListHoldingTransactionsResponse{TotalCount: 0}, nil
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/holdings/9/transactions?direction=buy", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"transactions":[]`)
}

func TestPortfolio_ExerciseOption_BadID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/x/exercise", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_ExerciseOption_Success(t *testing.T) {
	st := &portfolioStub{
		exerciseFn: func(req *stockpb.ExerciseOptionRequest) (*stockpb.ExerciseResult, error) {
			require.Equal(t, uint64(9), req.HoldingId)
			require.Equal(t, "client", req.SystemType)
			return &stockpb.ExerciseResult{}, nil
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/9/exercise", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// ListMyOTCOptions reshape: the marketplace endpoint, scoped to the
// caller. Must populate owner_only_seller_id with the SI-TX seller id
// derived from the caller's identity.
func TestPortfolio_ListMyOTCOptions_PassesOwnerFilter(t *testing.T) {
	var captured *stockpb.ListUnifiedOptionOffersRequest
	otc := &stubOTCClient{
		listUnifiedOptionFn: func(in *stockpb.ListUnifiedOptionOffersRequest) (*stockpb.ListUnifiedOptionOffersResponse, error) {
			captured = in
			return &stockpb.ListUnifiedOptionOffersResponse{}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, &accountFullStub{})
	r := portfolioRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/me/otc/options", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	// Caller is client principal 42 (setClientIdentity), so the SI-TX
	// seller_id form is "client-42".
	require.Equal(t, "client-42", captured.OwnerOnlySellerId)
}

// The public marketplace endpoint must NOT set the owner filter — it
// returns everyone's open listings.
func TestPortfolio_ListOTCOptions_NoOwnerFilter(t *testing.T) {
	var captured *stockpb.ListUnifiedOptionOffersRequest
	otc := &stubOTCClient{
		listUnifiedOptionFn: func(in *stockpb.ListUnifiedOptionOffersRequest) (*stockpb.ListUnifiedOptionOffersResponse, error) {
			captured = in
			return &stockpb.ListUnifiedOptionOffersResponse{}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, &accountFullStub{})
	r := portfolioRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/otc/options", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, "", captured.OwnerOnlySellerId)
}

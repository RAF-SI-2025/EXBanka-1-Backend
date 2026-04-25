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
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// portfolioStub satisfies stockpb.PortfolioGRPCServiceClient with per-method
// function fields. Keeps existing stubPortfolioClient (in
// options_v2_handler_test.go) untouched.
type portfolioStub struct {
	listFn          func(*stockpb.ListHoldingsRequest) (*stockpb.ListHoldingsResponse, error)
	summaryFn       func(*stockpb.GetPortfolioSummaryRequest) (*stockpb.PortfolioSummary, error)
	makePublicFn    func(*stockpb.MakePublicRequest) (*stockpb.Holding, error)
	exerciseFn      func(*stockpb.ExerciseOptionRequest) (*stockpb.ExerciseResult, error)
	listTxFn        func(*stockpb.ListHoldingTransactionsRequest) (*stockpb.ListHoldingTransactionsResponse, error)
	exerciseByIDFn  func(*stockpb.ExerciseOptionByOptionIDRequest) (*stockpb.ExerciseResult, error)
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
func (s *portfolioStub) MakePublic(_ context.Context, in *stockpb.MakePublicRequest, _ ...grpc.CallOption) (*stockpb.Holding, error) {
	if s.makePublicFn != nil {
		return s.makePublicFn(in)
	}
	return &stockpb.Holding{Id: in.HoldingId}, nil
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

func portfolioRouter(h *handler.PortfolioHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Set("system_type", "client")
	}
	r.GET("/api/v2/me/holdings", withCtx, h.ListHoldings)
	r.GET("/api/v2/me/portfolio/summary", withCtx, h.GetPortfolioSummary)
	r.POST("/api/v2/me/holdings/:id/make-public", withCtx, h.MakePublic)
	r.GET("/api/v2/me/holdings/:id/transactions", withCtx, h.ListHoldingTransactions)
	r.POST("/api/v2/me/holdings/:id/exercise", withCtx, h.ExerciseOption)
	r.GET("/api/v2/otc/offers", withCtx, h.ListOTCOffers)
	r.POST("/api/v2/otc/offers/:id/buy", withCtx, h.BuyOTCOffer)
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

func TestPortfolio_MakePublic_BadID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":1}`
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/x/make-public", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid holding id")
}

func TestPortfolio_MakePublic_NegativeQuantity(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":-1}`
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/9/make-public", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "quantity must be positive")
}

func TestPortfolio_MakePublic_BadBody(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `not json`
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/9/make-public", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_MakePublic_Success(t *testing.T) {
	st := &portfolioStub{
		makePublicFn: func(req *stockpb.MakePublicRequest) (*stockpb.Holding, error) {
			require.Equal(t, uint64(9), req.HoldingId)
			require.Equal(t, int64(2), req.Quantity)
			return &stockpb.Holding{Id: req.HoldingId}, nil
		},
	}
	h := handler.NewPortfolioHandler(st, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":2}`
	req := httptest.NewRequest("POST", "/api/v2/me/holdings/9/make-public", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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

func TestPortfolio_ListOTCOffers_Default(t *testing.T) {
	otc := &stubOTCClient{
		listFn: func(req *stockpb.ListOTCOffersRequest) (*stockpb.ListOTCOffersResponse, error) {
			require.Equal(t, int32(1), req.Page)
			return &stockpb.ListOTCOffersResponse{}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/otc/offers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"offers":[]`)
}

func TestPortfolio_ListOTCOffers_BadSecurityType(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/otc/offers?security_type=option", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "security_type must be one of")
}

func TestPortfolio_BuyOTCOffer_BadID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":1,"account_id":1}`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/x/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_BuyOTCOffer_BadBody(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `not json`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_BuyOTCOffer_NegativeQuantity(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":-1,"account_id":1}`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "quantity must be positive")
}

func TestPortfolio_BuyOTCOffer_MissingAccountID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	r := portfolioRouter(h)
	body := `{"quantity":1}`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "account_id is required")
}

func TestPortfolio_BuyOTCOffer_OwnershipMismatch_Returns404(t *testing.T) {
	// Caller is client user_id=42; account owner is 99 → client must see 404.
	acct := &accountFullStub{
		getFn: func(req *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: req.Id, OwnerId: 99}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, acct)
	r := portfolioRouter(h)
	body := `{"quantity":1,"account_id":1}`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortfolio_BuyOTCOffer_Success(t *testing.T) {
	acct := &accountFullStub{
		getFn: func(req *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: req.Id, OwnerId: 42}, nil
		},
	}
	otc := &stubOTCClient{
		buyFn: func(req *stockpb.BuyOTCOfferRequest) (*stockpb.OTCTransaction, error) {
			require.Equal(t, uint64(3), req.OfferId)
			require.Equal(t, uint64(42), req.BuyerId)
			require.Equal(t, "client", req.SystemType)
			return &stockpb.OTCTransaction{Id: 1}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, acct)
	r := portfolioRouter(h)
	body := `{"quantity":1,"account_id":7}`
	req := httptest.NewRequest("POST", "/api/v2/otc/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPortfolio_BuyOTCOfferOnBehalf_BadID(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v2/otc/admin/offers/:id/buy", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "employee")
		h.BuyOTCOfferOnBehalf(c)
	})
	body := `{"client_id":7,"account_id":3,"quantity":2}`
	req := httptest.NewRequest("POST", "/api/v2/otc/admin/offers/x/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPortfolio_BuyOTCOfferOnBehalf_MissingFields(t *testing.T) {
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, &accountFullStub{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v2/otc/admin/offers/:id/buy", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "employee")
		h.BuyOTCOfferOnBehalf(c)
	})
	body := `{"quantity":2}`
	req := httptest.NewRequest("POST", "/api/v2/otc/admin/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "client_id and account_id are required")
}

func TestPortfolio_BuyOTCOfferOnBehalf_AccountClientMismatch(t *testing.T) {
	acct := &accountFullStub{
		getFn: func(req *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: req.Id, OwnerId: 99}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, &stubOTCClient{}, acct)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v2/otc/admin/offers/:id/buy", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "employee")
		h.BuyOTCOfferOnBehalf(c)
	})
	body := `{"client_id":7,"account_id":3,"quantity":2}`
	req := httptest.NewRequest("POST", "/api/v2/otc/admin/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "account does not belong to client")
}

func TestPortfolio_BuyOTCOfferOnBehalf_Success(t *testing.T) {
	acct := &accountFullStub{
		getFn: func(req *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: req.Id, OwnerId: 7}, nil
		},
	}
	otc := &stubOTCClient{
		buyFn: func(req *stockpb.BuyOTCOfferRequest) (*stockpb.OTCTransaction, error) {
			require.Equal(t, uint64(7), req.OnBehalfOfClientId)
			require.Equal(t, uint64(1), req.ActingEmployeeId)
			require.Equal(t, "employee", req.SystemType)
			return &stockpb.OTCTransaction{Id: 99}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, acct)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v2/otc/admin/offers/:id/buy", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "employee")
		h.BuyOTCOfferOnBehalf(c)
	})
	body := `{"client_id":7,"account_id":3,"quantity":2}`
	req := httptest.NewRequest("POST", "/api/v2/otc/admin/offers/3/buy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

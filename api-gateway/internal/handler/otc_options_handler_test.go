// api-gateway/internal/handler/otc_options_handler_test.go
package handler_test

import (
	"context"
	"encoding/json"
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

// otcStubSecurityClient implements stockpb.SecurityGRPCServiceClient; only
// GetStockByTicker is exercised by the OTC handler.
type otcStubSecurityClient struct {
	stockpb.SecurityGRPCServiceClient
	byTickerFn func(*stockpb.GetStockByTickerRequest) (*stockpb.StockDetail, error)
}

func (s *otcStubSecurityClient) GetStockByTicker(_ context.Context, in *stockpb.GetStockByTickerRequest, _ ...grpc.CallOption) (*stockpb.StockDetail, error) {
	if s.byTickerFn != nil {
		return s.byTickerFn(in)
	}
	return &stockpb.StockDetail{Id: 11}, nil
}

// otcStubAccountClient implements accountpb.AccountServiceClient; only
// GetAccount is exercised by the ownership checks.
type otcStubAccountClient struct {
	accountpb.AccountServiceClient
	getFn      func(*accountpb.GetAccountRequest) (*accountpb.AccountResponse, error)
	getByNumFn func(*accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error)
}

func (s *otcStubAccountClient) GetAccount(_ context.Context, in *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	// Default: account owned by the test client principal (42), non-bank.
	return &accountpb.AccountResponse{Id: in.Id, OwnerId: 42, AccountKind: "current"}, nil
}

func (s *otcStubAccountClient) GetAccountByNumber(_ context.Context, in *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getByNumFn != nil {
		return s.getByNumFn(in)
	}
	// Default: account owned by the test client principal (42), non-bank.
	return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, OwnerId: 42, AccountKind: "current"}, nil
}

// otcHandler builds an OTCOptionsHandler with permissive default security +
// account stubs (ticker resolves to stock 11; accounts are owned by client
// principal 42). Tests needing other behaviour construct the handler directly.
func otcHandler(cl *stubOTCOptionsClient) *handler.OTCOptionsHandler {
	return handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, &otcStubAccountClient{})
}

// stubOTCOptionsClient implements stockpb.OTCOptionsServiceClient.
type stubOTCOptionsClient struct {
	createFn                 func(*stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error)
	listMyOffersFn           func(*stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error)
	getOfferFn               func(*stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error)
	listContractsFn          func(*stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error)
	getContractFn            func(*stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error)
	exerciseFn               func(*stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error)
	listNegotiationHistoryFn func(*stockpb.ListNegotiationHistoryRequest) (*stockpb.ListMyOTCOffersResponse, error)
	submitRatingFn           func(*stockpb.SubmitOTCRatingRequest) (*stockpb.OTCRatingResponse, error)
	getTraderProfileFn       func(*stockpb.GetTraderProfileRequest) (*stockpb.TraderProfileResponse, error)
	listReceivedRatingsFn    func(*stockpb.ListReceivedRatingsRequest) (*stockpb.ListOTCRatingsResponse, error)
	cancelListingFn          func(*stockpb.CancelListingRequest) (*stockpb.CancelListingResponse, error)
	listRevisionsFn          func(*stockpb.ListNegotiationRevisionsRequest) (*stockpb.ListNegotiationRevisionsResponse, error)
	listByListingFn          func(*stockpb.ListNegotiationsByListingRequest) (*stockpb.ListNegotiationsResponse, error)
	getTimelineFn            func(*stockpb.GetOfferTimelineRequest) (*stockpb.GetOfferTimelineResponse, error)
	openNegotiationFn        func(*stockpb.OpenNegotiationRequest) (*stockpb.OTCNegotiationResponse, error)
	updateQuantityFn         func(*stockpb.UpdateOTCOfferQuantityRequest) (*stockpb.OTCOfferResponse, error)
}

func (s *stubOTCOptionsClient) CreateOffer(_ context.Context, in *stockpb.CreateOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCOfferResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.OTCOfferResponse{}, nil
}
func (s *stubOTCOptionsClient) ListMyOffers(_ context.Context, in *stockpb.ListMyOTCOffersRequest, _ ...grpc.CallOption) (*stockpb.ListMyOTCOffersResponse, error) {
	if s.listMyOffersFn != nil {
		return s.listMyOffersFn(in)
	}
	return &stockpb.ListMyOTCOffersResponse{}, nil
}
func (s *stubOTCOptionsClient) GetOffer(_ context.Context, in *stockpb.GetOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCOfferDetailResponse, error) {
	if s.getOfferFn != nil {
		return s.getOfferFn(in)
	}
	return &stockpb.OTCOfferDetailResponse{}, nil
}
func (s *stubOTCOptionsClient) UpdateOTCOfferQuantity(_ context.Context, in *stockpb.UpdateOTCOfferQuantityRequest, _ ...grpc.CallOption) (*stockpb.OTCOfferResponse, error) {
	if s.updateQuantityFn != nil {
		return s.updateQuantityFn(in)
	}
	return &stockpb.OTCOfferResponse{}, nil
}
func (s *stubOTCOptionsClient) ListMyContracts(_ context.Context, in *stockpb.ListMyContractsRequest, _ ...grpc.CallOption) (*stockpb.ListContractsResponse, error) {
	if s.listContractsFn != nil {
		return s.listContractsFn(in)
	}
	return &stockpb.ListContractsResponse{}, nil
}
func (s *stubOTCOptionsClient) GetContract(_ context.Context, in *stockpb.GetContractRequest, _ ...grpc.CallOption) (*stockpb.OptionContractResponse, error) {
	if s.getContractFn != nil {
		return s.getContractFn(in)
	}
	return &stockpb.OptionContractResponse{}, nil
}
func (s *stubOTCOptionsClient) ExerciseContract(_ context.Context, in *stockpb.ExerciseContractRequest, _ ...grpc.CallOption) (*stockpb.ExerciseResponse, error) {
	if s.exerciseFn != nil {
		return s.exerciseFn(in)
	}
	return &stockpb.ExerciseResponse{}, nil
}
func (s *stubOTCOptionsClient) ListNegotiationHistory(_ context.Context, in *stockpb.ListNegotiationHistoryRequest, _ ...grpc.CallOption) (*stockpb.ListMyOTCOffersResponse, error) {
	if s.listNegotiationHistoryFn != nil {
		return s.listNegotiationHistoryFn(in)
	}
	return &stockpb.ListMyOTCOffersResponse{}, nil
}
func (s *stubOTCOptionsClient) SubmitRating(_ context.Context, in *stockpb.SubmitOTCRatingRequest, _ ...grpc.CallOption) (*stockpb.OTCRatingResponse, error) {
	if s.submitRatingFn != nil {
		return s.submitRatingFn(in)
	}
	return &stockpb.OTCRatingResponse{}, nil
}
func (s *stubOTCOptionsClient) GetTraderProfile(_ context.Context, in *stockpb.GetTraderProfileRequest, _ ...grpc.CallOption) (*stockpb.TraderProfileResponse, error) {
	if s.getTraderProfileFn != nil {
		return s.getTraderProfileFn(in)
	}
	return &stockpb.TraderProfileResponse{}, nil
}
func (s *stubOTCOptionsClient) ListReceivedRatings(_ context.Context, in *stockpb.ListReceivedRatingsRequest, _ ...grpc.CallOption) (*stockpb.ListOTCRatingsResponse, error) {
	if s.listReceivedRatingsFn != nil {
		return s.listReceivedRatingsFn(in)
	}
	return &stockpb.ListOTCRatingsResponse{}, nil
}

// Phase-2 marketplace RPCs — added by the OTC options refactor. Tests
// don't exercise these directly (covered by otc_negotiation_handler_test.go
// and stock-service tests), so the stub returns zero-value responses.
func (s *stubOTCOptionsClient) OpenNegotiation(_ context.Context, in *stockpb.OpenNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCNegotiationResponse, error) {
	if s.openNegotiationFn != nil {
		return s.openNegotiationFn(in)
	}
	return &stockpb.OTCNegotiationResponse{}, nil
}
func (s *stubOTCOptionsClient) CounterNegotiation(_ context.Context, _ *stockpb.CounterNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCNegotiationResponse, error) {
	return &stockpb.OTCNegotiationResponse{}, nil
}
func (s *stubOTCOptionsClient) AcceptNegotiationChain(_ context.Context, _ *stockpb.OTCAcceptNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCAcceptNegotiationResponse, error) {
	return &stockpb.OTCAcceptNegotiationResponse{}, nil
}
func (s *stubOTCOptionsClient) RejectNegotiation(_ context.Context, _ *stockpb.RejectNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCNegotiationResponse, error) {
	return &stockpb.OTCNegotiationResponse{}, nil
}
func (s *stubOTCOptionsClient) CancelNegotiation(_ context.Context, _ *stockpb.CancelNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCNegotiationResponse, error) {
	return &stockpb.OTCNegotiationResponse{}, nil
}
func (s *stubOTCOptionsClient) CancelListing(_ context.Context, in *stockpb.CancelListingRequest, _ ...grpc.CallOption) (*stockpb.CancelListingResponse, error) {
	if s.cancelListingFn != nil {
		return s.cancelListingFn(in)
	}
	return &stockpb.CancelListingResponse{OfferId: in.GetOfferId(), Status: "cancelled"}, nil
}
func (s *stubOTCOptionsClient) ListMyNegotiations(_ context.Context, _ *stockpb.ListMyNegotiationsRequest, _ ...grpc.CallOption) (*stockpb.ListNegotiationsResponse, error) {
	return &stockpb.ListNegotiationsResponse{}, nil
}
func (s *stubOTCOptionsClient) ListNegotiationsByListing(_ context.Context, in *stockpb.ListNegotiationsByListingRequest, _ ...grpc.CallOption) (*stockpb.ListNegotiationsResponse, error) {
	if s.listByListingFn != nil {
		return s.listByListingFn(in)
	}
	return &stockpb.ListNegotiationsResponse{}, nil
}
func (s *stubOTCOptionsClient) GetOfferTimeline(_ context.Context, in *stockpb.GetOfferTimelineRequest, _ ...grpc.CallOption) (*stockpb.GetOfferTimelineResponse, error) {
	if s.getTimelineFn != nil {
		return s.getTimelineFn(in)
	}
	return &stockpb.GetOfferTimelineResponse{}, nil
}
func (s *stubOTCOptionsClient) ListNegotiationRevisions(_ context.Context, in *stockpb.ListNegotiationRevisionsRequest, _ ...grpc.CallOption) (*stockpb.ListNegotiationRevisionsResponse, error) {
	if s.listRevisionsFn != nil {
		return s.listRevisionsFn(in)
	}
	return &stockpb.ListNegotiationRevisionsResponse{}, nil
}

var _ stockpb.OTCOptionsServiceClient = (*stubOTCOptionsClient)(nil)

func otcOptionsRouter(h *handler.OTCOptionsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/otc/offers", withCli, h.CreateOffer)
	r.GET("/me/otc/offers", withCli, h.ListMyOffers)
	r.GET("/otc/offers/:id", withCli, h.GetOffer)
	r.GET("/me/otc/contracts", withCli, h.ListMyContracts)
	r.GET("/otc/contracts/:id", withCli, h.GetContract)
	r.POST("/otc/contracts/:id/exercise", withCli, h.ExerciseContract)
	r.GET("/me/otc/options/posted", withCli, h.ListMyPostedOffers)
	r.PUT("/me/otc/options/:id", withCli, h.UpdateMyOption)
	r.DELETE("/me/otc/options/:id", withCli, h.CancelMyListing)
	r.POST("/otc/options/:id/bid", withCli, h.OpenNegotiationChain)
	r.POST("/me/otc/options/:id/negotiations/:nid/counter", withCli, h.CounterMyNegotiation)
	return r
}

func TestOTCOpt_CreateOffer_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		createFn: func(in *stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			require.Equal(t, "sell_initiated", in.Direction)
			require.Equal(t, uint64(11), in.StockId)
			require.Equal(t, int64(42), in.ActorUserId)
			require.Equal(t, "client", in.ActorSystemType)
			return &stockpb.OTCOfferResponse{Id: 1}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	// Terms (strike_price/premium/settlement_date) are no longer part of the
	// create body — offers are posted open and terms are negotiated later.
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

// TestOTCOpt_CreateOffer_Duplicate_Conflict asserts that a duplicate open
// offer per (owner, ticker, direction) — surfaced by the service as a gRPC
// AlreadyExists — is mapped to HTTP 409 with an apiError body whose code is
// "conflict".
func TestOTCOpt_CreateOffer_Duplicate_Conflict(t *testing.T) {
	cl := &stubOTCOptionsClient{
		createFn: func(*stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "an open offer for this ticker and direction already exists")
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusConflict, rec.Code)
	var body2 struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body2))
	require.Equal(t, "conflict", body2.Error.Code)
}

func TestOTCOpt_CreateOffer_EmployeeBank_ForwardsActingEmployee(t *testing.T) {
	cl := &stubOTCOptionsClient{
		createFn: func(in *stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			// Employee acting as the bank: owner resolves to bank (actor_user_id
			// 0, actor_system_type "bank") and the originating employee is
			// forwarded separately so stock-service can capture it.
			require.Equal(t, int64(0), in.ActorUserId)
			require.Equal(t, "bank", in.ActorSystemType)
			require.Equal(t, uint64(17), in.ActingEmployeeId)
			return &stockpb.OTCOfferResponse{Id: 1}, nil
		},
	}
	// Employees acting as the bank may only bind a bank-owned account.
	bankAcct := &otcStubAccountClient{getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{Id: in.Id, AccountKind: "bank"}, nil
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, bankAcct)
	// Route with the employee-bank identity instead of the default client one.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/otc/offers", setEmployeeBankIdentity(17), h.CreateOffer)
	body := `{"direction":"buy_initiated","ticker":"AAPL","quantity":"100","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_CreateOffer_UnknownTicker(t *testing.T) {
	sec := &otcStubSecurityClient{byTickerFn: func(*stockpb.GetStockByTickerRequest) (*stockpb.StockDetail, error) {
		return nil, status.Error(codes.NotFound, "no stock")
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, sec, &otcStubAccountClient{})
	r := otcOptionsRouter(h)
	body := `{"direction":"sell_initiated","ticker":"NOPE","quantity":"1","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_AccountNotOwned(t *testing.T) {
	acct := &otcStubAccountClient{getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{Id: in.Id, OwnerId: 999, AccountKind: "current"}, nil
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &otcStubSecurityClient{}, acct)
	r := otcOptionsRouter(h)
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"1","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOTCOpt_CreateOffer_BadDirection(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	body := `{"direction":"weird","stock_id":1,"quantity":"100"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_MissingFields(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	body := `{"direction":"sell_initiated","stock_id":0}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_BadBody(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader("xxx")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_WithCounterparty(t *testing.T) {
	cl := &stubOTCOptionsClient{
		createFn: func(in *stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			require.NotNil(t, in.Counterparty)
			require.Equal(t, int64(7), in.Counterparty.UserId)
			require.Equal(t, "client", in.Counterparty.SystemType)
			return &stockpb.OTCOfferResponse{Id: 1}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	body := `{"direction":"buy_initiated","ticker":"AAPL","quantity":"100","counterparty_user_id":7,"account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_CreateOffer_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{
		createFn: func(*stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOTCOpt_ListMyOffers_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		listMyOffersFn: func(in *stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error) {
			require.Equal(t, "initiator", in.Role)
			require.Equal(t, int32(2), in.Page)
			require.Equal(t, int32(50), in.PageSize)
			return &stockpb.ListMyOTCOffersResponse{Total: 0}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/offers?role=initiator&page=2&page_size=50", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetOffer_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			require.Equal(t, uint64(15), in.OfferId)
			return &stockpb.OTCOfferDetailResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/15", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetOffer_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// SP-1 (passthrough): GetOffer passes identity fields down to the service,
// which now resolves local vs remote internally and returns kind + me_owner.
// Assert 200 and that the service response is passed through unchanged.
func TestGetOffer_PassthroughLocal(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			require.Equal(t, uint64(15), in.OfferId)
			require.Equal(t, "client", in.ActingOwnerType)
			require.Equal(t, uint64(42), in.ActingOwnerId)
			return &stockpb.OTCOfferDetailResponse{Offer: &stockpb.OTCOfferResponse{
				Id:      15,
				Kind:    "local",
				MeOwner: true,
			}}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/15", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// Service response is passed through: offer wrapper is present.
	require.Contains(t, body, "offer")
	offer, _ := body["offer"].(map[string]any)
	require.Equal(t, "local", offer["kind"])
	require.Equal(t, true, offer["me_owner"])
}

// SP-1 (passthrough): NotFound from the service propagates as HTTP 404.
func TestGetOffer_NotFound(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(*stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			return nil, status.Error(codes.NotFound, "offer not found")
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/99", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestOTCOpt_OpenNegotiation_RejectsNegativePremium asserts the gateway rejects
// a bid with a negative premium (or strike/quantity) with 400 BEFORE forwarding
// to stock-service. A negative amount is a money-safety violation per the API
// Gateway Input Validation Requirement; without the check it reached the service
// and minted an "ongoing" negotiation with premium=-5.
func TestOTCOpt_OpenNegotiation_RejectsNegativePremium(t *testing.T) {
	called := false
	cl := &stubOTCOptionsClient{
		openNegotiationFn: func(*stockpb.OpenNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
			called = true
			return &stockpb.OTCNegotiationResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	body := `{"bidder_account_id":50,"quantity":"1","strike_price":"40","premium":"-5","settlement_date":"2026-12-31"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/options/1/bid", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, called, "stock-service must NOT be called for a negative-premium bid")
}

// TestOTCOpt_OpenNegotiation_RejectsNonPositiveQuantity asserts a non-positive
// quantity is rejected with 400.
func TestOTCOpt_OpenNegotiation_RejectsNonPositiveQuantity(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	body := `{"bidder_account_id":50,"quantity":"0","strike_price":"40","premium":"5","settlement_date":"2026-12-31"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/options/1/bid", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestOTCOpt_CounterNegotiation_RejectsNegativePremium mirrors the bid check on
// the counter path.
func TestOTCOpt_CounterNegotiation_RejectsNegativePremium(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	body := `{"quantity":"1","strike_price":"40","premium":"-5","settlement_date":"2026-12-31"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/options/1/negotiations/2/counter", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ListMyContracts_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		listContractsFn: func(in *stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error) {
			require.Equal(t, "either", in.Role)
			return &stockpb.ListContractsResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/contracts", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetContract_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getContractFn: func(in *stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error) {
			require.Equal(t, uint64(8), in.ContractId)
			return &stockpb.OptionContractResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/contracts/8", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetContract_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/contracts/x", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ExerciseContract_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		exerciseFn: func(in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			require.Equal(t, uint64(8), in.ContractId)
			return &stockpb.ExerciseResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_ExerciseContract_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/x/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// SP-2b Task 5: the unified ExerciseContract passes buyer_account_number through
// to the gRPC ExerciseContract (stock-service decides local vs cross-bank). The
// gateway validates the caller owns the settlement account first.
func TestOTCOpt_ExerciseContract_CrossBankPassesBuyerAccount(t *testing.T) {
	cl := &stubOTCOptionsClient{
		exerciseFn: func(in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			require.Equal(t, uint64(8), in.ContractId)
			require.Equal(t, "265-12-13", in.BuyerAccountNumber)
			return &stockpb.ExerciseResponse{ContractId: 8, Status: "pending", SagaId: "tx-cb"}, nil
		},
	}
	// Account owned by the caller (42) by default.
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{"buyer_account_number":"265-12-13"}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), "tx-cb")
}

// The exercise theft vector: a client must NOT pay the strike from an account
// they don't own. The settlement account's owner (999) differs from the caller
// (42) → 403, and the gRPC ExerciseContract must NOT be invoked (no money moves).
func TestOTCOpt_ExerciseContract_CrossBankStrikeAccountNotOwned(t *testing.T) {
	dispatched := false
	cl := &stubOTCOptionsClient{
		exerciseFn: func(*stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			dispatched = true
			return &stockpb.ExerciseResponse{}, nil
		},
	}
	acct := &otcStubAccountClient{getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, OwnerId: 999, AccountKind: "current"}, nil // not the caller (42)
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, acct)
	r := otcOptionsRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{"buyer_account_number":"111000130146666611"}`)))
	require.Equal(t, http.StatusForbidden, rec.Code, "expected 403 for strike paid from a non-owned account; body=%s", rec.Body.String())
	require.False(t, dispatched, "exercise must NOT dispatch when the strike account is not owned by the caller (theft vector)")
}

// SP-3 Task 5 gateway gate: a BANK-acting EMPLOYEE exercising cross-bank must
// bind a BANK account for the strike — binding a CLIENT's account (the verified
// money-path gap that enforceOwnership left open for non-client principals) →
// 403, no dispatch. This is the core fix: enforceOwnership returned nil for any
// non-client caller, leaving the employee ungated.
func TestOTCOpt_ExerciseContract_EmployeeBankBindingClientAccountForbidden(t *testing.T) {
	dispatched := false
	cl := &stubOTCOptionsClient{
		exerciseFn: func(*stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			dispatched = true
			return &stockpb.ExerciseResponse{}, nil
		},
	}
	// The bound account is a CLIENT account (owner 7, non-bank).
	acct := &otcStubAccountClient{getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, OwnerId: 7, AccountKind: "current"}, nil
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, acct)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/otc/contracts/:id/exercise", setEmployeeBankIdentity(5), h.ExerciseContract)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{"buyer_account_number":"111000130146666611"}`)))
	require.Equal(t, http.StatusForbidden, rec.Code, "bank employee binding a client account must be 403; body=%s", rec.Body.String())
	require.False(t, dispatched, "THEFT VECTOR: exercise dispatched the strike against a client's account at the bank's routing")
}

// SP-3 Task 5 gateway gate: a BANK-acting EMPLOYEE exercising cross-bank with a
// BANK account is allowed → forwarded to the gRPC ExerciseContract.
func TestOTCOpt_ExerciseContract_EmployeeBankBindingBankAccountForwards(t *testing.T) {
	var captured *stockpb.ExerciseContractRequest
	cl := &stubOTCOptionsClient{
		exerciseFn: func(in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			captured = in
			return &stockpb.ExerciseResponse{ContractId: 8, Status: "pending", SagaId: "tx-cb"}, nil
		},
	}
	// A BANK account (account_kind == "bank").
	acct := &otcStubAccountClient{getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, OwnerId: 1_000_000_000, AccountKind: "bank"}, nil
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, acct)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/otc/contracts/:id/exercise", setEmployeeBankIdentity(5), h.ExerciseContract)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{"buyer_account_number":"111-BANK-USD-01"}`)))
	require.Equal(t, http.StatusCreated, rec.Code, "bank employee binding a bank account must forward; body=%s", rec.Body.String())
	require.NotNil(t, captured)
	require.Equal(t, "111-BANK-USD-01", captured.BuyerAccountNumber)
}

// SP-3 Task 5 gateway gate: a LOCAL exercise (no buyer_account_number) by a
// bank-acting employee is NOT account-gated — the account lookup must NOT fire,
// and the request forwards with an empty buyer_account_number.
func TestOTCOpt_ExerciseContract_EmployeeLocalNoAccountGate(t *testing.T) {
	cl := &stubOTCOptionsClient{
		exerciseFn: func(in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			require.Empty(t, in.BuyerAccountNumber)
			return &stockpb.ExerciseResponse{ContractId: 8, Status: "exercised"}, nil
		},
	}
	acct := &otcStubAccountClient{getByNumFn: func(*accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
		t.Fatalf("account lookup must NOT happen on the local path (no buyer_account_number)")
		return nil, nil
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, acct)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/otc/contracts/:id/exercise", setEmployeeBankIdentity(5), h.ExerciseContract)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

// A LOCAL exercise (no buyer_account_number) skips the ownership gate entirely —
// accounts come from the persisted contract — and forwards an empty
// buyer_account_number.
func TestOTCOpt_ExerciseContract_LocalNoAccountGate(t *testing.T) {
	cl := &stubOTCOptionsClient{
		exerciseFn: func(in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
			require.Equal(t, uint64(8), in.ContractId)
			require.Empty(t, in.BuyerAccountNumber)
			return &stockpb.ExerciseResponse{ContractId: 8, Status: "exercised"}, nil
		},
	}
	// An account client that would 404 if it were ever consulted — proves the
	// gate is skipped when no settlement account is supplied.
	acct := &otcStubAccountClient{getByNumFn: func(*accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
		t.Fatalf("account lookup must NOT happen on the local path")
		return nil, nil
	}}
	h := handler.NewOTCOptionsHandler(cl, &otcStubSecurityClient{}, acct)
	r := otcOptionsRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

// ListMyPostedOffers: caller's posted listings, role hardcoded to initiator.
func TestOTCOpt_ListMyPostedOffers_HardcodesInitiator(t *testing.T) {
	var captured *stockpb.ListMyOTCOffersRequest
	cl := &stubOTCOptionsClient{
		listMyOffersFn: func(in *stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error) {
			captured = in
			return &stockpb.ListMyOTCOffersResponse{Total: 0}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/options/posted?statuses=open,cancelled", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, "initiator", captured.Role)
	require.Equal(t, int64(42), captured.ActorUserId)
	require.Equal(t, []string{"open", "cancelled"}, captured.Statuses)
}

// CancelMyListing: 204 when caller is the initiator.
func TestOTCOpt_CancelMyListing_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			return &stockpb.OTCOfferDetailResponse{Offer: &stockpb.OTCOfferResponse{
				Id:        in.OfferId,
				Initiator: &stockpb.PartyRef{UserId: 42, SystemType: "client"},
			}}, nil
		},
		cancelListingFn: func(in *stockpb.CancelListingRequest) (*stockpb.CancelListingResponse, error) {
			require.Equal(t, uint64(6), in.OfferId)
			require.Equal(t, "client", in.CallerOwnerType)
			require.Equal(t, uint64(42), in.CallerOwnerId)
			return &stockpb.CancelListingResponse{OfferId: 6, Status: "cancelled"}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/6", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// CancelMyListing: 403 when caller is NOT the initiator (e.g. they're the counterparty or a stranger).
func TestOTCOpt_CancelMyListing_NotInitiator(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			return &stockpb.OTCOfferDetailResponse{Offer: &stockpb.OTCOfferResponse{
				Id:        in.OfferId,
				Initiator: &stockpb.PartyRef{UserId: 99, SystemType: "client"},
			}}, nil
		},
		cancelListingFn: func(*stockpb.CancelListingRequest) (*stockpb.CancelListingResponse, error) {
			t.Fatalf("CancelListing should not be called when caller is not the initiator")
			return nil, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/6", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// CancelMyListing: 404 when the offer doesn't exist or isn't visible to caller (GetOffer returns NotFound).
func TestOTCOpt_CancelMyListing_NotFound(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getOfferFn: func(*stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
			return nil, status.Error(codes.NotFound, "offer not found")
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/6", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// CancelMyListing: bad id format yields 400.
func TestOTCOpt_CancelMyListing_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- UpdateMyOption (PUT /me/otc/options/:id) gateway handler tests ----

// Happy path: PUT sets the total quantity; the new quantity + acting identity
// are forwarded to the gRPC UpdateOTCOfferQuantity and 200 is returned.
func TestOTCOpt_UpdateMyOption_SetsQuantity(t *testing.T) {
	var captured *stockpb.UpdateOTCOfferQuantityRequest
	cl := &stubOTCOptionsClient{
		updateQuantityFn: func(in *stockpb.UpdateOTCOfferQuantityRequest) (*stockpb.OTCOfferResponse, error) {
			captured = in
			return &stockpb.OTCOfferResponse{Id: in.GetOfferId(), Quantity: in.GetQuantity()}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/me/otc/options/6", strings.NewReader(`{"quantity":"200"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, uint64(6), captured.GetOfferId())
	require.Equal(t, "200", captured.GetQuantity())
	require.Equal(t, "client", captured.GetActingOwnerType())
	require.Equal(t, uint64(42), captured.GetActingOwnerId())
	require.Contains(t, rec.Body.String(), `"offer"`)
}

// Non-positive quantity is rejected with 400 BEFORE the gRPC call.
func TestOTCOpt_UpdateMyOption_RejectsNonPositive(t *testing.T) {
	called := false
	cl := &stubOTCOptionsClient{
		updateQuantityFn: func(*stockpb.UpdateOTCOfferQuantityRequest) (*stockpb.OTCOfferResponse, error) {
			called = true
			return &stockpb.OTCOfferResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/me/otc/options/6", strings.NewReader(`{"quantity":"0"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, called, "stock-service must NOT be called for a non-positive quantity")
}

// Bad id format yields 400.
func TestOTCOpt_UpdateMyOption_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/me/otc/options/abc", strings.NewReader(`{"quantity":"5"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// A PermissionDenied from the service (caller is not the owner) maps to 403.
func TestOTCOpt_UpdateMyOption_ServiceForbidden(t *testing.T) {
	cl := &stubOTCOptionsClient{
		updateQuantityFn: func(*stockpb.UpdateOTCOfferQuantityRequest) (*stockpb.OTCOfferResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "only the offer's owner can edit it")
		},
	}
	r := otcOptionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/me/otc/options/6", strings.NewReader(`{"quantity":"5"}`)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---- ListMyNegotiationRevisions gateway handler tests ----

func otcRevisionsRouter(h *handler.OTCOptionsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.GET("/me/otc/options/negotiations/:nid/revisions", withCli, h.ListMyNegotiationRevisions)
	return r
}

// TestOTCOpt_ListRevisions_Success verifies the happy path: stub returns an
// empty revision list and the handler responds 200 with a "revisions" key.
func TestOTCOpt_ListRevisions_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{}
	r := otcRevisionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/options/negotiations/5/revisions", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"revisions"`)
}

// TestOTCOpt_ListRevisions_BadNID verifies that a non-numeric :nid yields 400.
func TestOTCOpt_ListRevisions_BadNID(t *testing.T) {
	r := otcRevisionsRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/options/negotiations/abc/revisions", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestOTCOpt_ListRevisions_GRPCError verifies that gRPC errors from the
// stock-service are mapped to appropriate HTTP status codes by handleGRPCError.
func TestOTCOpt_ListRevisions_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{}
	cl.listRevisionsFn = func(_ *stockpb.ListNegotiationRevisionsRequest) (*stockpb.ListNegotiationRevisionsResponse, error) {
		return nil, status.Error(codes.PermissionDenied, "not a party")
	}
	r := otcRevisionsRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/me/otc/options/negotiations/5/revisions", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---- ListNegotiationsOnListing + GetOfferTimeline gateway handler tests ----

func otcListingRouter(h *handler.OTCOptionsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.GET("/otc/options/:id/negotiations", withCli, h.ListNegotiationsOnListing)
	r.GET("/otc/options/:id/timeline", withCli, h.GetOfferTimeline)
	return r
}

// The listing handler must forward the caller's resolved identity to the
// gRPC request so the service can run the poster/employee audience check.
func TestOTCOpt_ListNegotiationsOnListing_ForwardsIdentity(t *testing.T) {
	var got *stockpb.ListNegotiationsByListingRequest
	cl := &stubOTCOptionsClient{
		listByListingFn: func(in *stockpb.ListNegotiationsByListingRequest) (*stockpb.ListNegotiationsResponse, error) {
			got = in
			return &stockpb.ListNegotiationsResponse{}, nil
		},
	}
	r := otcListingRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/options/42/negotiations", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	require.Equal(t, uint64(42), got.GetParentOfferId())
	require.Equal(t, "client", got.GetCallerOwnerType())
	require.Equal(t, uint64(42), got.GetCallerOwnerId())
}

// A 403 from the service (competing bidder) propagates as HTTP 403.
func TestOTCOpt_ListNegotiationsOnListing_Forbidden(t *testing.T) {
	cl := &stubOTCOptionsClient{
		listByListingFn: func(*stockpb.ListNegotiationsByListingRequest) (*stockpb.ListNegotiationsResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "not the poster")
		},
	}
	r := otcListingRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/options/42/negotiations", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// Timeline happy path: 200 with "offer" + "timeline" keys, identity forwarded.
func TestOTCOpt_GetOfferTimeline_Success(t *testing.T) {
	var got *stockpb.GetOfferTimelineRequest
	cl := &stubOTCOptionsClient{
		getTimelineFn: func(in *stockpb.GetOfferTimelineRequest) (*stockpb.GetOfferTimelineResponse, error) {
			got = in
			return &stockpb.GetOfferTimelineResponse{
				Offer:    &stockpb.OTCOfferResponse{Id: in.GetParentOfferId()},
				Timeline: []*stockpb.OTCTimelineEntry{{NegotiationId: 100, Action: "BID"}},
			}, nil
		},
	}
	r := otcListingRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/options/42/timeline", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"timeline"`)
	require.Contains(t, rec.Body.String(), `"offer"`)
	require.NotNil(t, got)
	require.Equal(t, "client", got.GetCallerOwnerType())
	require.Equal(t, uint64(42), got.GetCallerOwnerId())
}

// Timeline: non-numeric id yields 400.
func TestOTCOpt_GetOfferTimeline_BadID(t *testing.T) {
	r := otcListingRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/options/abc/timeline", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Timeline: a 403 from the service propagates as HTTP 403.
func TestOTCOpt_GetOfferTimeline_Forbidden(t *testing.T) {
	cl := &stubOTCOptionsClient{
		getTimelineFn: func(*stockpb.GetOfferTimelineRequest) (*stockpb.GetOfferTimelineResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "not the poster")
		},
	}
	r := otcListingRouter(otcHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/options/42/timeline", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

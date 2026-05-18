// api-gateway/internal/handler/otc_options_handler_test.go
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
	getFn func(*accountpb.GetAccountRequest) (*accountpb.AccountResponse, error)
}

func (s *otcStubAccountClient) GetAccount(_ context.Context, in *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	// Default: account owned by the test client principal (42), non-bank.
	return &accountpb.AccountResponse{Id: in.Id, OwnerId: 42, AccountKind: "current"}, nil
}

// otcHandler builds an OTCOptionsHandler with permissive default security +
// account stubs (ticker resolves to stock 11; accounts are owned by client
// principal 42). Tests needing other behaviour construct the handler directly.
func otcHandler(cl *stubOTCOptionsClient, peer *stubPeerOTCExerciseClient) *handler.OTCOptionsHandler {
	return handler.NewOTCOptionsHandler(cl, peer, &otcStubSecurityClient{}, &otcStubAccountClient{})
}

// stubOTCOptionsClient implements stockpb.OTCOptionsServiceClient.
type stubOTCOptionsClient struct {
	createFn                 func(*stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error)
	listMyOffersFn           func(*stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error)
	getOfferFn               func(*stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error)
	counterFn                func(*stockpb.CounterOTCOfferRequest) (*stockpb.OTCOfferResponse, error)
	acceptFn                 func(*stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error)
	rejectFn                 func(*stockpb.RejectOTCOfferRequest) (*stockpb.OTCOfferResponse, error)
	listContractsFn          func(*stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error)
	getContractFn            func(*stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error)
	exerciseFn               func(*stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error)
	listNegotiationHistoryFn func(*stockpb.ListNegotiationHistoryRequest) (*stockpb.ListMyOTCOffersResponse, error)
	submitRatingFn           func(*stockpb.SubmitOTCRatingRequest) (*stockpb.OTCRatingResponse, error)
	getTraderProfileFn       func(*stockpb.GetTraderProfileRequest) (*stockpb.TraderProfileResponse, error)
	listReceivedRatingsFn    func(*stockpb.ListReceivedRatingsRequest) (*stockpb.ListOTCRatingsResponse, error)
	cancelListingFn          func(*stockpb.CancelListingRequest) (*stockpb.CancelListingResponse, error)
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
func (s *stubOTCOptionsClient) CounterOffer(_ context.Context, in *stockpb.CounterOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCOfferResponse, error) {
	if s.counterFn != nil {
		return s.counterFn(in)
	}
	return &stockpb.OTCOfferResponse{}, nil
}
func (s *stubOTCOptionsClient) AcceptOffer(_ context.Context, in *stockpb.AcceptOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.AcceptOfferResponse, error) {
	if s.acceptFn != nil {
		return s.acceptFn(in)
	}
	return &stockpb.AcceptOfferResponse{}, nil
}
func (s *stubOTCOptionsClient) RejectOffer(_ context.Context, in *stockpb.RejectOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCOfferResponse, error) {
	if s.rejectFn != nil {
		return s.rejectFn(in)
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
func (s *stubOTCOptionsClient) OpenNegotiation(_ context.Context, _ *stockpb.OpenNegotiationRequest, _ ...grpc.CallOption) (*stockpb.OTCNegotiationResponse, error) {
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
func (s *stubOTCOptionsClient) ListNegotiationsByListing(_ context.Context, _ *stockpb.ListNegotiationsByListingRequest, _ ...grpc.CallOption) (*stockpb.ListNegotiationsResponse, error) {
	return &stockpb.ListNegotiationsResponse{}, nil
}

var _ stockpb.OTCOptionsServiceClient = (*stubOTCOptionsClient)(nil)

// stubPeerOTCExerciseClient is a minimal PeerOTCServiceClient that only
// implements InitiateOptionExercise. Other methods return Unimplemented.
type stubPeerOTCExerciseClient struct {
	stockpb.PeerOTCServiceClient
	initiateFn func(*stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error)
}

func (s *stubPeerOTCExerciseClient) InitiateOptionExercise(_ context.Context, in *stockpb.InitiateOptionExerciseRequest, _ ...grpc.CallOption) (*stockpb.InitiateOptionExerciseResponse, error) {
	if s.initiateFn != nil {
		return s.initiateFn(in)
	}
	return &stockpb.InitiateOptionExerciseResponse{}, nil
}

func otcOptionsRouter(h *handler.OTCOptionsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/otc/offers", withCli, h.CreateOffer)
	r.GET("/me/otc/offers", withCli, h.ListMyOffers)
	r.GET("/otc/offers/:id", withCli, h.GetOffer)
	r.POST("/otc/offers/:id/counter", withCli, h.CounterOffer)
	r.POST("/otc/offers/:id/accept", withCli, h.AcceptOffer)
	r.POST("/otc/offers/:id/reject", withCli, h.RejectOffer)
	r.GET("/me/otc/contracts", withCli, h.ListMyContracts)
	r.GET("/otc/contracts/:id", withCli, h.GetContract)
	r.POST("/otc/contracts/:id/exercise", withCli, h.ExerciseContract)
	r.POST("/me/otc/contracts/peer/:id/exercise", withCli, h.ExercisePeerContract)
	r.GET("/me/otc/options/posted", withCli, h.ListMyPostedOffers)
	r.DELETE("/me/otc/options/:id", withCli, h.CancelMyListing)
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","strike_price":"5","premium":"1","settlement_date":"2026-12-31","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_CreateOffer_UnknownTicker(t *testing.T) {
	sec := &otcStubSecurityClient{byTickerFn: func(*stockpb.GetStockByTickerRequest) (*stockpb.StockDetail, error) {
		return nil, status.Error(codes.NotFound, "no stock")
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}, sec, &otcStubAccountClient{})
	r := otcOptionsRouter(h)
	body := `{"direction":"sell_initiated","ticker":"NOPE","quantity":"1","strike_price":"5","premium":"1","settlement_date":"2026-12-31","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_AccountNotOwned(t *testing.T) {
	acct := &otcStubAccountClient{getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{Id: in.Id, OwnerId: 999, AccountKind: "current"}, nil
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}, &otcStubSecurityClient{}, acct)
	r := otcOptionsRouter(h)
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"1","strike_price":"5","premium":"1","settlement_date":"2026-12-31","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOTCOpt_CreateOffer_BadDirection(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	body := `{"direction":"weird","stock_id":1,"quantity":"100","strike_price":"5","settlement_date":"2026-12-31"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_MissingFields(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	body := `{"direction":"sell_initiated","stock_id":0}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CreateOffer_BadBody(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	body := `{"direction":"buy_initiated","ticker":"AAPL","quantity":"100","strike_price":"5","premium":"1","settlement_date":"2026-12-31","counterparty_user_id":7,"account_id":50}`
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	body := `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","strike_price":"5","premium":"1","settlement_date":"2026-12-31","account_id":50}`
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/15", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetOffer_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/offers/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CounterOffer_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		counterFn: func(in *stockpb.CounterOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			require.Equal(t, uint64(3), in.OfferId)
			return &stockpb.OTCOfferResponse{Id: in.OfferId}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	body := `{"quantity":"100","strike_price":"7","premium":"2","settlement_date":"2026-12-31"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/counter", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_CounterOffer_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/abc/counter", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_CounterOffer_BadBody(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/1/counter", strings.NewReader("nope")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_AcceptOffer_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		acceptFn: func(in *stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error) {
			require.Equal(t, uint64(3), in.OfferId)
			require.Equal(t, uint64(10), in.AccountId)
			return &stockpb.AcceptOfferResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	body := `{"account_id":10}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/accept", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_AcceptOffer_MissingFields(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/accept", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_AcceptOffer_AccountNotOwned(t *testing.T) {
	acct := &otcStubAccountClient{getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{Id: in.Id, OwnerId: 999, AccountKind: "current"}, nil
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}, &otcStubSecurityClient{}, acct)
	r := otcOptionsRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/1/accept", strings.NewReader(`{"account_id":50}`)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOTCOpt_AcceptOffer_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/x/accept", strings.NewReader(`{"buyer_account_id":1,"seller_account_id":2}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_AcceptOffer_BadBody(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/accept", strings.NewReader("xxx")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_RejectOffer_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		rejectFn: func(in *stockpb.RejectOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			require.Equal(t, uint64(3), in.OfferId)
			return &stockpb.OTCOfferResponse{Id: in.OfferId}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/reject", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_RejectOffer_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/x/reject", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_RejectOffer_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{
		rejectFn: func(*stockpb.RejectOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/3/reject", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestOTCOpt_ListMyContracts_Success(t *testing.T) {
	cl := &stubOTCOptionsClient{
		listContractsFn: func(in *stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error) {
			require.Equal(t, "either", in.Role)
			return &stockpb.ListContractsResponse{}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/otc/contracts/8", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOTCOpt_GetContract_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/8/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestOTCOpt_ExerciseContract_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/contracts/x/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ExercisePeerContract_Success(t *testing.T) {
	peer := &stubPeerOTCExerciseClient{
		initiateFn: func(in *stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error) {
			require.Equal(t, uint64(8), in.PeerOptionContractId)
			require.Equal(t, "265-12-13", in.BuyerAccountNumber)
			return &stockpb.InitiateOptionExerciseResponse{TransactionId: "tx-1", Status: "pending"}, nil
		},
	}
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, peer))
	body := `{"buyer_account_number":"265-12-13"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/contracts/peer/8/exercise", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "tx-1")
}

func TestOTCOpt_ExercisePeerContract_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/contracts/peer/x/exercise", strings.NewReader(`{"buyer_account_number":"a"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ExercisePeerContract_BadBody(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/contracts/peer/8/exercise", strings.NewReader("nope")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ExercisePeerContract_MissingAccount(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/contracts/peer/8/exercise", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCOpt_ExercisePeerContract_GRPCError(t *testing.T) {
	peer := &stubPeerOTCExerciseClient{
		initiateFn: func(*stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "expired")
		},
	}
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, peer))
	body := `{"buyer_account_number":"265-12-13"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/otc/contracts/peer/8/exercise", strings.NewReader(body)))
	require.Equal(t, http.StatusConflict, rec.Code)
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
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
	r := otcOptionsRouter(otcHandler(cl, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/6", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// CancelMyListing: bad id format yields 400.
func TestOTCOpt_CancelMyListing_BadID(t *testing.T) {
	r := otcOptionsRouter(otcHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/me/otc/options/abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

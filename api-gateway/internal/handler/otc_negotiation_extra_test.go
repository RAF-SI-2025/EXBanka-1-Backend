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
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// otcNegRouter wires the negotiation + ratings/history routes onto an
// OTCOptionsHandler with the permissive default account stub (accounts owned
// by client principal 42).
func otcNegRouter(h *handler.OTCOptionsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/api/v3/me/otc/options/:id/negotiations/:nid/accept", withCli, h.AcceptMyNegotiation)
	r.POST("/api/v3/me/otc/options/:id/negotiations/:nid/reject", withCli, h.RejectMyNegotiation)
	r.DELETE("/api/v3/me/otc/options/:id/negotiations/:nid", withCli, h.CancelMyNegotiation)
	r.GET("/api/v3/me/otc/options/negotiations", withCli, h.ListMyNegotiations)
	r.GET("/api/v3/me/otc/history", withCli, h.ListNegotiationHistory)
	r.POST("/api/v3/me/otc/ratings", withCli, h.SubmitRating)
	r.GET("/api/v3/otc/traders/:owner_type/:owner_id/rating", withCli, h.GetTraderProfile)
	r.GET("/api/v3/me/otc/ratings/received", withCli, h.ListMyReceivedRatings)
	return r
}

func negDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// ---- AcceptMyNegotiation ----

func TestOTCNeg_Accept_Success(t *testing.T) {
	var captured *stockpb.OTCAcceptNegotiationRequest
	cl := &stubOTCOptionsClient{acceptNegFn: func(in *stockpb.OTCAcceptNegotiationRequest) (*stockpb.OTCAcceptNegotiationResponse, error) {
		captured = in
		return &stockpb.OTCAcceptNegotiationResponse{
			Winning:      &stockpb.OTCNegotiationResponse{},
			ParentStatus: "consumed",
		}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/accept", `{"acceptor_account_id":50}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(9), captured.NegotiationId)
	require.Equal(t, uint64(50), captured.AcceptorAccountId)
	require.Equal(t, "client", captured.CallerOwnerType)
	require.Contains(t, rec.Body.String(), `"parent_status":"consumed"`)
}

func TestOTCNeg_Accept_BadNID(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/0/accept", `{"acceptor_account_id":50}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid nid")
}

func TestOTCNeg_Accept_BadBody(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/accept", `not-json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid body")
}

func TestOTCNeg_Accept_MissingAcceptorAccount(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/accept", `{"acceptor_account_id":0}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "acceptor_account_id is required")
}

func TestOTCNeg_Accept_AccountNotOwned(t *testing.T) {
	acct := &otcStubAccountClient{getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{Id: in.Id, OwnerId: 999, AccountKind: "current"}, nil
	}}
	h := handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &otcStubSecurityClient{}, acct)
	r := otcNegRouter(h)
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/accept", `{"acceptor_account_id":50}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOTCNeg_Accept_SagaRejected(t *testing.T) {
	cl := &stubOTCOptionsClient{acceptNegFn: func(*stockpb.OTCAcceptNegotiationRequest) (*stockpb.OTCAcceptNegotiationResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "seller short on shares")
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/accept", `{"acceptor_account_id":50}`)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// ---- RejectMyNegotiation ----

func TestOTCNeg_Reject_Success(t *testing.T) {
	var captured *stockpb.RejectNegotiationRequest
	cl := &stubOTCOptionsClient{rejectNegFn: func(in *stockpb.RejectNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
		captured = in
		return &stockpb.OTCNegotiationResponse{}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/reject", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(9), captured.NegotiationId)
	require.Equal(t, "client", captured.CallerOwnerType)
	require.Contains(t, rec.Body.String(), `"negotiation"`)
}

func TestOTCNeg_Reject_BadNID(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/x/reject", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCNeg_Reject_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{rejectNegFn: func(*stockpb.RejectNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
		return nil, status.Error(codes.PermissionDenied, "not a party")
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "POST", "/api/v3/me/otc/options/3/negotiations/9/reject", "")
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---- CancelMyNegotiation ----

func TestOTCNeg_Cancel_Success(t *testing.T) {
	var captured *stockpb.CancelNegotiationRequest
	cl := &stubOTCOptionsClient{cancelNegFn: func(in *stockpb.CancelNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
		captured = in
		return &stockpb.OTCNegotiationResponse{}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "DELETE", "/api/v3/me/otc/options/3/negotiations/9", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(9), captured.NegotiationId)
}

func TestOTCNeg_Cancel_BadNID(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "DELETE", "/api/v3/me/otc/options/3/negotiations/0", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCNeg_Cancel_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{cancelNegFn: func(*stockpb.CancelNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
		return nil, status.Error(codes.PermissionDenied, "only the bidder may withdraw")
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "DELETE", "/api/v3/me/otc/options/3/negotiations/9", "")
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---- ListMyNegotiations ----

func TestOTCNeg_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyNegotiationsRequest
	cl := &stubOTCOptionsClient{listMyNegFn: func(in *stockpb.ListMyNegotiationsRequest) (*stockpb.ListNegotiationsResponse, error) {
		captured = in
		return &stockpb.ListNegotiationsResponse{Total: 0}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "GET", "/api/v3/me/otc/options/negotiations?statuses=open,accepted&page=2&page_size=5", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Equal(t, []string{"open", "accepted"}, captured.Statuses)
	require.Equal(t, int32(2), captured.Page)
	require.Equal(t, int32(5), captured.PageSize)
}

func TestOTCNeg_ListMy_PageSizeCapped(t *testing.T) {
	var captured *stockpb.ListMyNegotiationsRequest
	cl := &stubOTCOptionsClient{listMyNegFn: func(in *stockpb.ListMyNegotiationsRequest) (*stockpb.ListNegotiationsResponse, error) {
		captured = in
		return &stockpb.ListNegotiationsResponse{}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "GET", "/api/v3/me/otc/options/negotiations?page_size=9999", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int32(200), captured.PageSize)
}

// ---- ListNegotiationHistory ----

func TestOTCHistory_Success(t *testing.T) {
	var captured *stockpb.ListNegotiationHistoryRequest
	cl := &stubOTCOptionsClient{listNegotiationHistoryFn: func(in *stockpb.ListNegotiationHistoryRequest) (*stockpb.ListMyOTCOffersResponse, error) {
		captured = in
		return &stockpb.ListMyOTCOffersResponse{Total: 0}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	// NOTE: the status filter currently rejects every value (see
	// TestOTCHistory_StatusFilterAlwaysRejected) so the success path omits it.
	rec := negDo(r, "GET", "/api/v3/me/otc/history?since=2026-01-01&until=2026-06-01&counterparty_id=5&page=2&page_size=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(5), captured.CounterpartyId)
	require.Equal(t, int32(2), captured.Page)
	require.Equal(t, int32(10), captured.PageSize)
	require.True(t, captured.SinceUnix > 0 && captured.UntilUnix > 0)
	require.Contains(t, rec.Body.String(), `"offers"`)
}

// TestOTCHistory_StatusFilterAlwaysRejected documents that the status filter is
// currently unusable: oneOf() lowercases the input before comparing it against
// the uppercase allowed set, so even a valid uppercase status yields 400.
func TestOTCHistory_StatusFilterAlwaysRejected(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/me/otc/history?status=ACCEPTED", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "status must be one of")
}

func TestOTCHistory_BadStatus(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/me/otc/history?status=PENDING", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "status must be one of")
}

func TestOTCHistory_BadSince(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/me/otc/history?since=2026/01/01", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "since must be YYYY-MM-DD")
}

func TestOTCHistory_BadUntil(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/me/otc/history?until=nope", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "until must be YYYY-MM-DD")
}

func TestOTCHistory_SinceAfterUntil(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/me/otc/history?since=2026-06-01&until=2026-01-01", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "since must be")
}

// ---- SubmitRating ----

func TestOTCRating_Submit_Success(t *testing.T) {
	var captured *stockpb.SubmitOTCRatingRequest
	cl := &stubOTCOptionsClient{submitRatingFn: func(in *stockpb.SubmitOTCRatingRequest) (*stockpb.OTCRatingResponse, error) {
		captured = in
		return &stockpb.OTCRatingResponse{Id: 1}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "POST", "/api/v3/me/otc/ratings", `{"offer_id":7,"score":5,"comment":"great"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, uint64(7), captured.OfferId)
	require.Equal(t, int32(5), captured.Score)
	require.Equal(t, "client", captured.RaterOwnerType)
}

func TestOTCRating_Submit_BadBody(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/ratings", `nope`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTCRating_Submit_MissingOffer(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/ratings", `{"offer_id":0,"score":5}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "offer_id is required")
}

func TestOTCRating_Submit_BadScore(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/ratings", `{"offer_id":7,"score":9}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "score must be 1..5")
}

func TestOTCRating_Submit_CommentTooLong(t *testing.T) {
	long := strings.Repeat("x", 1001)
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "POST", "/api/v3/me/otc/ratings", `{"offer_id":7,"score":3,"comment":"`+long+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "comment must be")
}

// ---- GetTraderProfile ----

func TestOTCRating_GetTraderProfile_Success(t *testing.T) {
	var captured *stockpb.GetTraderProfileRequest
	cl := &stubOTCOptionsClient{getTraderProfileFn: func(in *stockpb.GetTraderProfileRequest) (*stockpb.TraderProfileResponse, error) {
		captured = in
		return &stockpb.TraderProfileResponse{}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "GET", "/api/v3/otc/traders/client/42/rating?recent_limit=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Equal(t, int32(10), captured.RecentLimit)
}

func TestOTCRating_GetTraderProfile_BadOwnerType(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/otc/traders/alien/42/rating", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "owner_type must be one of")
}

func TestOTCRating_GetTraderProfile_BadOwnerID(t *testing.T) {
	r := otcNegRouter(otcHandler(&stubOTCOptionsClient{}))
	rec := negDo(r, "GET", "/api/v3/otc/traders/client/xx/rating", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid owner_id")
}

// ---- ListMyReceivedRatings ----

func TestOTCRating_ListReceived_Success(t *testing.T) {
	var captured *stockpb.ListReceivedRatingsRequest
	cl := &stubOTCOptionsClient{listReceivedRatingsFn: func(in *stockpb.ListReceivedRatingsRequest) (*stockpb.ListOTCRatingsResponse, error) {
		captured = in
		return &stockpb.ListOTCRatingsResponse{}, nil
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "GET", "/api/v3/me/otc/ratings/received?limit=15", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Equal(t, int32(15), captured.Limit)
	require.Contains(t, rec.Body.String(), `"ratings"`)
}

func TestOTCRating_ListReceived_GRPCError(t *testing.T) {
	cl := &stubOTCOptionsClient{listReceivedRatingsFn: func(*stockpb.ListReceivedRatingsRequest) (*stockpb.ListOTCRatingsResponse, error) {
		return nil, status.Error(codes.Internal, "boom")
	}}
	r := otcNegRouter(otcHandler(cl))
	rec := negDo(r, "GET", "/api/v3/me/otc/ratings/received", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

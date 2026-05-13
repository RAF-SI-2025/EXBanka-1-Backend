// api-gateway/internal/handler/extra_coverage2_test.go
//
// Second pass of additional tests targeting error paths in credit/card/account
// handlers that were already partially covered. Drives coverage by exercising
// branches missed by the existing happy-path tests.
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	cardpb "github.com/exbanka/contract/cardpb"
	creditpb "github.com/exbanka/contract/creditpb"
)

// ---------------------------------------------------------------------------
// CreditHandler — tier error paths
// ---------------------------------------------------------------------------

func TestCredit_CreateInterestRateTier_BadBody(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader("nope"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateInterestRateTier_NegativeAmountTo(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":-1,"fixed_rate":1,"variable_base":1}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateInterestRateTier_NegativeFixedRate(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1,"fixed_rate":-1,"variable_base":1}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateInterestRateTier_NegativeVariableBase(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1,"fixed_rate":1,"variable_base":-1}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_CreateInterestRateTier_GRPCError(t *testing.T) {
	credit := &stubCreditClient{
		createTierFn: func(*creditpb.CreateInterestRateTierRequest) (*creditpb.InterestRateTierResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "exists")
		},
	}
	h := handler.NewCreditHandler(credit)
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1000,"fixed_rate":5,"variable_base":3}`
	req := httptest.NewRequest("POST", "/interest-rate-tiers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestCredit_UpdateInterestRateTier_BadBody(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader("nope"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_NegativeAmountFrom(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":-1,"amount_to":1,"fixed_rate":1,"variable_base":1}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_NegativeAmountTo(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":-1,"fixed_rate":1,"variable_base":1}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_NegativeFixedRate(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1,"fixed_rate":-1,"variable_base":1}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_NegativeVariableBase(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1,"fixed_rate":1,"variable_base":-1}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_UpdateInterestRateTier_GRPCError(t *testing.T) {
	credit := &stubCreditClient{
		updateTierFn: func(*creditpb.UpdateInterestRateTierRequest) (*creditpb.InterestRateTierResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewCreditHandler(credit)
	r := creditRouter(h)
	body := `{"amount_from":0,"amount_to":1,"fixed_rate":1,"variable_base":1}`
	req := httptest.NewRequest("PUT", "/interest-rate-tiers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------------------------------------------------------------------------
// CreditHandler — RejectLoanRequest
// ---------------------------------------------------------------------------

func TestCredit_RejectLoanRequest_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("PUT", "/loans/requests/abc/reject", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCredit_RejectLoanRequest_GRPCError(t *testing.T) {
	credit := &stubCreditClient{
		rejectReqFn: func(*creditpb.RejectLoanRequestReq) (*creditpb.LoanRequestResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewCreditHandler(credit)
	r := creditRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/loans/requests/1/reject", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------------------------------------------------------------------------
// CardHandler — Unblock/Deactivate/CreateAuthorizedPerson error paths
// ---------------------------------------------------------------------------

func TestCard_UnblockCard_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/cards/abc/unblock", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_UnblockCard_GRPCError(t *testing.T) {
	card := &stubCardClient{
		unblockFn: func(*cardpb.UnblockCardRequest) (*cardpb.CardResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewCardHandler(card, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/cards/1/unblock", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCard_DeactivateCard_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/cards/abc/deactivate", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_DeactivateCard_GRPCError(t *testing.T) {
	card := &stubCardClient{
		deactivateFn: func(*cardpb.DeactivateCardRequest) (*cardpb.CardResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "no")
		},
	}
	h := handler.NewCardHandler(card, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/cards/1/deactivate", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCard_CreateAuthorizedPerson_BadBody(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/cards/authorized-persons", strings.NewReader("nope")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateAuthorizedPerson_MissingFields(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"first_name":"A"}` // missing last_name and account_id
	req := httptest.NewRequest("POST", "/cards/authorized-persons", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateAuthorizedPerson_GRPCError(t *testing.T) {
	card := &stubCardClient{
		createAuthPersFn: func(*cardpb.CreateAuthorizedPersonRequest) (*cardpb.AuthorizedPersonResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	h := handler.NewCardHandler(card, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"first_name":"A","last_name":"B","account_id":1}`
	req := httptest.NewRequest("POST", "/cards/authorized-persons", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

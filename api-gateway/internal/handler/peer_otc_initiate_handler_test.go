// api-gateway/internal/handler/peer_otc_initiate_handler_test.go
package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// stubPeerAdminResolver embeds the existing stub but lets per-test code wire
// resolve behavior. The base stub returns Unimplemented for these methods.
type stubPeerAdminResolver struct {
	stubPeerBankAdminClient
	resolveByCodeFn func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error)
}

func (s *stubPeerAdminResolver) ResolvePeerByBankCode(_ context.Context, in *transactionpb.ResolvePeerByBankCodeRequest, _ ...grpc.CallOption) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
	if s.resolveByCodeFn != nil {
		return s.resolveByCodeFn(in)
	}
	return &transactionpb.ResolvePeerByBankCodeResponse{Found: false}, nil
}

// peerOTCInitiateRouter wires the handler with a configurable JWT principal.
func peerOTCInitiateRouter(h *handler.PeerOTCInitiateHandler, principal int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/me/peer-otc/negotiations", func(c *gin.Context) {
		if principal != 0 {
			c.Set("principal_id", principal)
			c.Set("principal_type", "client")
		}
		c.Next()
	}, h.CreatePeerNegotiation)
	return r
}

func validInitiateBody(sellerCode string) string {
	return `{
        "seller_bank_code":"` + sellerCode + `",
        "seller_id":"client-3",
        "stock":{"ticker":"AAPL"},
        "settlement_date":"2026-12-31",
        "price_per_unit":{"amount":"180","currency":"USD"},
        "premium":{"amount":"700","currency":"USD"},
        "amount":50
    }`
}

func TestPeerOTCInitiate_Success(t *testing.T) {
	// Stand up a fake peer that returns a ForeignBankId on POST /negotiations.
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/negotiations", r.URL.Path)
		require.Equal(t, "tok-222", r.Header.Get("X-Api-Key"))
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "AAPL", got["stock"].(map[string]any)["ticker"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"routingNumber":222,"id":"neg-7"}`))
	}))
	defer peerSrv.Close()

	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(in *transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			require.Equal(t, "222", in.BankCode)
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode:          "222",
					RoutingNumber:     222,
					BaseUrl:           peerSrv.URL,
					ApiTokenPlaintext: "tok-222",
					Active:            true,
				},
			}, nil
		},
	}

	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "neg-7")
	require.Contains(t, rec.Body.String(), "222")
}

func TestPeerOTCInitiate_Success_WithHMAC(t *testing.T) {
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HMAC headers must all be present when an outbound key is configured.
		require.NotEmpty(t, r.Header.Get("X-Bank-Code"))
		require.NotEmpty(t, r.Header.Get("X-Bank-Signature"))
		require.NotEmpty(t, r.Header.Get("X-Timestamp"))
		require.NotEmpty(t, r.Header.Get("X-Nonce"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"routingNumber":222,"id":"neg-9"}`))
	}))
	defer peerSrv.Close()

	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode:          "222",
					RoutingNumber:     222,
					BaseUrl:           peerSrv.URL,
					ApiTokenPlaintext: "tok",
					HmacOutboundKey:   "secret-outbound-key",
					Active:            true,
				},
			}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestPeerOTCInitiate_BadBody(t *testing.T) {
	h := handler.NewPeerOTCInitiateHandler(&stubPeerAdminResolver{}, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader("not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerOTCInitiate_MissingFields(t *testing.T) {
	h := handler.NewPeerOTCInitiateHandler(&stubPeerAdminResolver{}, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	body := `{"seller_bank_code":"222","seller_id":""}`
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPeerOTCInitiate_RejectsOwnBankCode(t *testing.T) {
	h := handler.NewPeerOTCInitiateHandler(&stubPeerAdminResolver{}, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("111"))))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "must be a peer bank")
}

func TestPeerOTCInitiate_MissingPrincipal(t *testing.T) {
	h := handler.NewPeerOTCInitiateHandler(&stubPeerAdminResolver{}, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 0)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPeerOTCInitiate_PeerNotRegistered(t *testing.T) {
	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{Found: false}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPeerOTCInitiate_PeerInactive(t *testing.T) {
	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode: "222", BaseUrl: "http://x", Active: false,
				},
			}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusFailedDependency, rec.Code)
}

func TestPeerOTCInitiate_ResolveError(t *testing.T) {
	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return nil, context.DeadlineExceeded
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPeerOTCInitiate_PeerRejects(t *testing.T) {
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer peerSrv.Close()

	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode: "222", RoutingNumber: 222, BaseUrl: peerSrv.URL, ApiTokenPlaintext: "tok", Active: true,
				},
			}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestPeerOTCInitiate_PeerDispatchFails(t *testing.T) {
	// BaseUrl points to a black-hole — connection is refused, dispatch fails.
	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode: "222", RoutingNumber: 222, BaseUrl: "http://127.0.0.1:1", ApiTokenPlaintext: "tok", Active: true,
				},
			}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestPeerOTCInitiate_PeerReturnsInvalidJSON(t *testing.T) {
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer peerSrv.Close()

	resolver := &stubPeerAdminResolver{
		resolveByCodeFn: func(*transactionpb.ResolvePeerByBankCodeRequest) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
			return &transactionpb.ResolvePeerByBankCodeResponse{
				Found: true,
				PeerBank: &transactionpb.PeerBankFull{
					BankCode: "222", RoutingNumber: 222, BaseUrl: peerSrv.URL, ApiTokenPlaintext: "tok", Active: true,
				},
			}, nil
		},
	}
	h := handler.NewPeerOTCInitiateHandler(resolver, nil, 111, "111")
	r := peerOTCInitiateRouter(h, 42)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/me/peer-otc/negotiations", strings.NewReader(validInitiateBody("222"))))
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

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

	"github.com/exbanka/api-gateway/internal/handler"
	accountpb "github.com/exbanka/contract/accountpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// peerTxDispatcherRouter wires a fresh gin router with a PeerTxDispatcherHandler
// (now serving /api/v3/me/payments) that embeds the supplied stubs. ownBankCode
// is "111" to match the tests' intra-bank vs foreign-prefix expectations. acct
// may be nil (a default stub is used).
func peerTxDispatcherRouter(t *testing.T, tx *stubTransactionClient, peerTx transactionpb.PeerTxServiceClient, ownBankCode string, acct *accountFullStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	if acct == nil {
		acct = &accountFullStub{}
	}
	txHandler := handler.NewTransactionHandler(tx, &stubFeeClient{}, acct, &stubExchangeClient{})
	pd := handler.NewPeerTxDispatcherHandler(txHandler, peerTx, ownBankCode)

	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		c.Set("email", "x@y.com")
	}
	r.POST("/api/v3/me/payments", withCtx, pd.CreatePayment)
	r.GET("/api/v3/me/payments/:id", withCtx, pd.GetPaymentByID)
	r.GET("/api/v3/me/payments/:id/status", withCtx, pd.GetPaymentStatusByID)
	return r
}

// decodeAPIError unmarshals the standard apiError body into a struct.
func decodeAPIError(t *testing.T, body []byte) (code, message string) {
	t.Helper()
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Error.Code, resp.Error.Message
}

func TestPeerTxDispatcher_CreatePayment(t *testing.T) {
	// Standard inter-bank stub: returns a deterministic SiTxInitiateResponse so
	// tests can assert on transaction_id + poll_url. Tests that delegate to
	// the intra-bank handler don't reach this stub.
	stubPeerTx := &stubPeerTxClient{
		initiateFn: func(_ context.Context, _ *transactionpb.SiTxInitiateRequest, _ ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
			return &transactionpb.SiTxInitiateResponse{
				TransactionId: "tx-test-1",
				PollUrl:       "/api/v3/me/payments/tx-test-1",
				Status:        "pending",
			}, nil
		},
	}

	tests := []struct {
		name            string
		body            string
		expectStatus    int
		expectDelegated bool
		expectErrorCode string
		expectAccepted  bool
	}{
		{
			name:            "intra-bank to_account_number delegates",
			body:            `{"from_account_number":"111000000000000001","to_account_number":"111000000000000002","amount":100}`,
			expectStatus:    http.StatusCreated,
			expectDelegated: true,
		},
		{
			name:            "foreign 3-digit prefix dispatches to PeerTxService and returns 202",
			body:            `{"from_account_number":"111000000000000001","to_account_number":"222999999999999999","amount":"100","currency":"RSD"}`,
			expectStatus:    http.StatusAccepted,
			expectDelegated: false,
			expectAccepted:  true,
		},
		{
			name:            "foreign without currency dispatches (currency auto-resolved from sender account)",
			body:            `{"from_account_number":"111000000000000001","to_account_number":"222999999999999999","amount":"100"}`,
			expectStatus:    http.StatusAccepted,
			expectDelegated: false,
			expectAccepted:  true,
		},
		{
			name:            "foreign receiverAccount field dispatches to PeerTxService",
			body:            `{"senderAccount":"111000000000000001","receiverAccount":"333111111111111111","amount":"100","currency":"RSD"}`,
			expectStatus:    http.StatusAccepted,
			expectDelegated: false,
			expectAccepted:  true,
		},
		{
			name:            "malformed JSON returns 400 validation_error",
			body:            `{not valid`,
			expectStatus:    http.StatusBadRequest,
			expectDelegated: false,
			expectErrorCode: "validation_error",
		},
		{
			name:            "body missing both fields falls through",
			body:            `{"amount":100}`,
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name:            "account number shorter than 3 chars falls through, not dispatched",
			body:            `{"from_account_number":"12","to_account_number":"34","amount":100}`,
			expectStatus:    http.StatusCreated,
			expectDelegated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			delegated := false
			tx := &stubTransactionClient{
				createPaymentFn: func(*transactionpb.CreatePaymentRequest) (*transactionpb.PaymentResponse, error) {
					delegated = true
					return &transactionpb.PaymentResponse{Id: 1}, nil
				},
			}
			// Account stub returns a currency so the auto-resolve (no-currency)
			// foreign case can stamp the SI-TX posting currency.
			acct := &accountFullStub{getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", OwnerId: 1}, nil
			}}

			r := peerTxDispatcherRouter(t, tx, stubPeerTx, "111", acct)
			req := httptest.NewRequest("POST", "/api/v3/me/payments", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			require.Equal(t, tc.expectStatus, rec.Code, "body=%s response=%s", tc.body, rec.Body.String())

			if tc.expectErrorCode != "" {
				code, _ := decodeAPIError(t, rec.Body.Bytes())
				require.Equal(t, tc.expectErrorCode, code)
			}

			if tc.expectAccepted {
				var resp struct {
					TransactionID string `json:"transaction_id"`
					PollURL       string `json:"poll_url"`
					Status        string `json:"status"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Equal(t, "tx-test-1", resp.TransactionID)
				require.Equal(t, "/api/v3/me/payments/tx-test-1", resp.PollURL)
				require.Equal(t, "pending", resp.Status)
			}

			if tc.expectDelegated {
				if !delegated {
					require.NotEqual(t, http.StatusAccepted, rec.Code,
						"expected fall-through to tx.CreatePayment, got 202")
				}
			} else {
				require.False(t, delegated, "expected NOT to delegate to tx.CreatePayment")
			}
		})
	}
}

func TestPeerTxDispatcher_GetPaymentByID_Delegates(t *testing.T) {
	delegated := false
	tx := &stubTransactionClient{
		getPaymentFn: func(req *transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error) {
			delegated = true
			require.Equal(t, uint64(42), req.Id)
			// ClientId must match the principal_id set by peerTxDispatcherRouter
			// (7), otherwise GetMyPayment's ownership check returns 404.
			return &transactionpb.PaymentResponse{Id: 42, ClientId: 7}, nil
		},
	}

	r := peerTxDispatcherRouter(t, tx, &stubPeerTxClient{}, "111", nil)
	req := httptest.NewRequest("GET", "/api/v3/me/payments/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, delegated, "expected GetPaymentByID to delegate to tx.GetMyPayment")
}

// A UUID-style id (cross-bank SI-TX poll url) resolves via GetTxStatus instead
// of the intra-bank payment lookup — for both the by-id and the /status routes.
func TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	for _, route := range []string{"/api/v3/me/payments/" + uuid, "/api/v3/me/payments/" + uuid + "/status"} {
		t.Run(route, func(t *testing.T) {
			called := false
			peerTx := &stubPeerTxClient{
				getTxStatusFn: func(_ context.Context, in *transactionpb.GetTxStatusRequest, _ ...grpc.CallOption) (*transactionpb.GetTxStatusResponse, error) {
					called = true
					require.Equal(t, uuid, in.GetTransactionId())
					return &transactionpb.GetTxStatusResponse{State: "committed", OurRole: "sender", LastActionAt: "2026-05-30T00:00:00Z"}, nil
				},
			}
			// tx delegate must NOT be reached for a UUID id.
			tx := &stubTransactionClient{
				getPaymentFn: func(*transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error) {
					t.Fatal("intra-bank GetPayment must not be called for a UUID id")
					return nil, nil
				},
			}
			r := peerTxDispatcherRouter(t, tx, peerTx, "111", nil)
			req := httptest.NewRequest("GET", route, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.True(t, called, "expected GetTxStatus to be called")
			var resp map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, uuid, resp["transaction_id"])
			require.Equal(t, "committed", resp["status"])
			require.Equal(t, "sender", resp["role"])
		})
	}
}

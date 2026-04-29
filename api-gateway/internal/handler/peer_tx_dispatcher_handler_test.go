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
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// peerTxDispatcherRouter wires a fresh gin router with a PeerTxDispatcherHandler
// that embeds the supplied stubs. ownBankCode is "111" to match the tests'
// intra-bank vs foreign-prefix expectations.
func peerTxDispatcherRouter(t *testing.T, tx *stubTransactionClient, peerTx transactionpb.PeerTxServiceClient, ownBankCode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	txHandler := handler.NewTransactionHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	pd := handler.NewPeerTxDispatcherHandler(txHandler, peerTx, ownBankCode)

	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(7))
		c.Set("principal_type", "client")
		c.Set("email", "x@y.com")
	}
	r.POST("/api/v3/me/transfers", withCtx, pd.CreateTransfer)
	r.GET("/api/v3/me/transfers/:id", withCtx, pd.GetTransferByID)
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

func TestPeerTxDispatcher_CreateTransfer(t *testing.T) {
	// Standard inter-bank stub: returns a deterministic SiTxInitiateResponse so
	// tests can assert on transaction_id + poll_url. Tests that delegate to
	// the intra-bank handler don't reach this stub.
	stubPeerTx := &stubPeerTxClient{
		initiateFn: func(_ context.Context, _ *transactionpb.SiTxInitiateRequest, _ ...grpc.CallOption) (*transactionpb.SiTxInitiateResponse, error) {
			return &transactionpb.SiTxInitiateResponse{
				TransactionId: "tx-test-1",
				PollUrl:       "/api/v3/me/transfers/tx-test-1",
				Status:        "pending",
			}, nil
		},
	}

	tests := []struct {
		name              string
		body              string
		expectStatus      int
		expectDelegated   bool
		expectErrorCode   string
		expectErrorSubstr string
		// expectAccepted asserts the 202-with-poll-url body shape from the
		// inter-bank dispatch path.
		expectAccepted bool
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
			name: "intra-bank receiverAccount field falls through (peek detects own prefix)",
			body: `{"senderAccount":"111000000000000001","receiverAccount":"111000000000000002","amount":100}`,
			// receiverAccount field is recognised by the dispatcher (own
			// prefix detected), so we fall through to tx.CreateTransfer. The
			// downstream handler binds the snake_case shape only, so it
			// returns a 400 validation_error — but importantly NOT a 202.
			// This proves the dispatcher supports both body shapes.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name:            "foreign receiverAccount field dispatches to PeerTxService",
			body:            `{"senderAccount":"111000000000000001","receiverAccount":"333111111111111111","amount":"100","currency":"RSD"}`,
			expectStatus:    http.StatusAccepted,
			expectDelegated: false,
			expectAccepted:  true,
		},
		{
			name: "malformed JSON returns 400 validation_error",
			body: `{not valid`,
			// The dispatcher itself json.Unmarshals the body for receiver
			// detection, so malformed JSON is rejected at the dispatcher
			// layer with a 400.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: false,
			expectErrorCode: "validation_error",
		},
		{
			name: "body missing both fields falls through",
			body: `{"amount":100}`,
			// receiver=="", len<3, so we fall through to tx.CreateTransfer.
			// The downstream handler requires from/to account numbers via
			// binding tags; it'll respond with 400.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name: "account number shorter than 3 chars falls through, not dispatched",
			body: `{"from_account_number":"12","to_account_number":"34","amount":100}`,
			// receiver="34" (len<3), so we cannot identify a foreign-bank
			// prefix. Routing must fall through to tx.CreateTransfer so the
			// stub gRPC client is reached (it returns 201 here). Crucially
			// we are NOT dispatching to PeerTxService.
			expectStatus:    http.StatusCreated,
			expectDelegated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			delegated := false
			tx := &stubTransactionClient{
				createTransferFn: func(*transactionpb.CreateTransferRequest) (*transactionpb.TransferResponse, error) {
					delegated = true
					return &transactionpb.TransferResponse{Id: 1}, nil
				},
			}

			r := peerTxDispatcherRouter(t, tx, stubPeerTx, "111")
			req := httptest.NewRequest("POST", "/api/v3/me/transfers", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			require.Equal(t, tc.expectStatus, rec.Code, "body=%s response=%s", tc.body, rec.Body.String())

			if tc.expectErrorCode != "" {
				code, message := decodeAPIError(t, rec.Body.Bytes())
				require.Equal(t, tc.expectErrorCode, code)
				if tc.expectErrorSubstr != "" {
					require.Contains(t, message, tc.expectErrorSubstr)
				}
			}

			if tc.expectAccepted {
				var resp struct {
					TransactionID string `json:"transaction_id"`
					PollURL       string `json:"poll_url"`
					Status        string `json:"status"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Equal(t, "tx-test-1", resp.TransactionID)
				require.Equal(t, "/api/v3/me/transfers/tx-test-1", resp.PollURL)
				require.Equal(t, "pending", resp.Status)
			}

			if tc.expectDelegated {
				// Either the gRPC stub was called (success path) or
				// CreateTransfer ran far enough to fail validation
				// itself (not the inter-bank branch). Both signal delegation.
				if !delegated {
					// validation-error path: assert it's a 4xx from
					// the downstream handler (not a 202 dispatch).
					require.NotEqual(t, http.StatusAccepted, rec.Code,
						"expected fall-through to tx.CreateTransfer, got 202")
				}
			} else {
				require.False(t, delegated, "expected NOT to delegate to tx.CreateTransfer")
			}
		})
	}
}

func TestPeerTxDispatcher_GetTransferByID_Delegates(t *testing.T) {
	delegated := false
	tx := &stubTransactionClient{
		getTransferFn: func(req *transactionpb.GetTransferRequest) (*transactionpb.TransferResponse, error) {
			delegated = true
			require.Equal(t, uint64(42), req.Id)
			// ClientId must match the principal_id set by peerTxDispatcherRouter
			// (7), otherwise GetMyTransfer's enforceOwnership check returns
			// 404. The router below sets principal_type=client.
			return &transactionpb.TransferResponse{Id: 42, ClientId: 7}, nil
		},
	}

	r := peerTxDispatcherRouter(t, tx, &stubPeerTxClient{}, "111")
	req := httptest.NewRequest("GET", "/api/v3/me/transfers/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, delegated, "expected GetTransferByID to delegate to tx.GetMyTransfer")
}

package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// peerDisabledRouter wires a fresh gin router with a PeerDisabledHandler that
// embeds the supplied stubs. ownBankCode is "111" to match the tests'
// intra-bank vs foreign-prefix expectations.
func peerDisabledRouter(t *testing.T, tx *stubTransactionClient, ownBankCode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	txHandler := handler.NewTransactionHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	pd := handler.NewPeerDisabledHandler(txHandler, ownBankCode)

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

func TestPeerDisabled_CreateTransfer(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		expectStatus      int
		expectDelegated   bool
		expectErrorCode   string
		expectErrorSubstr string
	}{
		{
			name:            "intra-bank to_account_number delegates",
			body:            `{"from_account_number":"111000000000000001","to_account_number":"111000000000000002","amount":100}`,
			expectStatus:    http.StatusCreated,
			expectDelegated: true,
		},
		{
			name:              "foreign 3-digit prefix returns 501",
			body:              `{"from_account_number":"111000000000000001","to_account_number":"222999999999999999","amount":100}`,
			expectStatus:      http.StatusNotImplemented,
			expectDelegated:   false,
			expectErrorCode:   "not_implemented",
			expectErrorSubstr: "inter-bank transfers are temporarily disabled",
		},
		{
			name: "intra-bank receiverAccount field falls through (peek detects own prefix)",
			body: `{"senderAccount":"111000000000000001","receiverAccount":"111000000000000002","amount":100}`,
			// receiverAccount field is recognised by peek (own prefix
			// detected), so we fall through to tx.CreateTransfer. The
			// downstream handler binds the snake_case shape only, so it
			// returns a 400 validation_error — but importantly NOT 501.
			// This proves the peek supports both body shapes.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name:            "foreign receiverAccount field returns 501",
			body:            `{"senderAccount":"111000000000000001","receiverAccount":"333111111111111111","amount":100}`,
			expectStatus:    http.StatusNotImplemented,
			expectDelegated: false,
			expectErrorCode: "not_implemented",
		},
		{
			name: "malformed JSON falls through to tx.CreateTransfer",
			body: `{not valid`,
			// tx.CreateTransfer's ShouldBindJSON returns a 400 validation_error.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name: "body missing both fields falls through",
			body: `{"amount":100}`,
			// tx.CreateTransfer requires from/to account numbers via binding tags;
			// it'll respond with 400.
			expectStatus:    http.StatusBadRequest,
			expectDelegated: true,
		},
		{
			name: "account number shorter than 3 chars falls through, not 501",
			body: `{"from_account_number":"12","to_account_number":"34","amount":100}`,
			// receiver="34" (len<3), so we cannot identify a foreign-bank
			// prefix. Routing must fall through to tx.CreateTransfer so the
			// stub gRPC client is reached (it returns 201 here). Crucially
			// we are NOT 501ing — the test asserts on `expectDelegated`
			// rather than a specific downstream-validation code, since the
			// downstream stub accepts the request without binding rejection.
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

			r := peerDisabledRouter(t, tx, "111")
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

			if tc.expectDelegated {
				// Either the gRPC stub was called (success path) or
				// CreateTransfer ran far enough to fail validation
				// itself (not the 501 branch). Both signal delegation.
				if !delegated {
					// validation-error path: assert it's a 4xx from
					// the downstream handler (not 501).
					require.NotEqual(t, http.StatusNotImplemented, rec.Code,
						"expected fall-through to tx.CreateTransfer, got 501")
				}
			} else {
				require.False(t, delegated, "expected NOT to delegate to tx.CreateTransfer")
			}
		})
	}
}

func TestPeerDisabled_GetTransferByID_Delegates(t *testing.T) {
	delegated := false
	tx := &stubTransactionClient{
		getTransferFn: func(req *transactionpb.GetTransferRequest) (*transactionpb.TransferResponse, error) {
			delegated = true
			require.Equal(t, uint64(42), req.Id)
			// ClientId must match the principal_id set by peerDisabledRouter
			// (7), otherwise GetMyTransfer's enforceOwnership check returns
			// 404. The router below sets principal_type=client.
			return &transactionpb.TransferResponse{Id: 42, ClientId: 7}, nil
		},
	}

	r := peerDisabledRouter(t, tx, "111")
	req := httptest.NewRequest("GET", "/api/v3/me/transfers/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, delegated, "expected GetTransferByID to delegate to tx.GetMyTransfer")
}

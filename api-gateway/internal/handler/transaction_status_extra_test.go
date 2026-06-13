package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

func transferStatusRouter(h *handler.TransactionHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(7)); c.Set("principal_type", "client") }
	r.GET("/api/v3/me/transfers/:id/status", withCtx, h.GetMyTransferStatus)
	return r
}

func TestTx_GetMyTransferStatus_Success(t *testing.T) {
	var captured *transactionpb.GetTransferRequest
	tx := &stubTransactionClient{getTransferStatusFn: func(in *transactionpb.GetTransferRequest) (*transactionpb.TransferStatusResponse, error) {
		captured = in
		return &transactionpb.TransferStatusResponse{
			TransferId:      12,
			Status:          "COMPLETED",
			InternalStatus:  "completed",
			LastChangedUnix: 1234,
		}, nil
	}}
	h := handler.NewTransactionHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transferStatusRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/me/transfers/12/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(12), captured.Id)
	require.Contains(t, rec.Body.String(), `"status":"COMPLETED"`)
	require.Contains(t, rec.Body.String(), `"internal_status":"completed"`)
}

func TestTx_GetMyTransferStatus_BadID(t *testing.T) {
	h := handler.NewTransactionHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transferStatusRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/me/transfers/xx/status", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}

func TestTx_GetMyTransferStatus_NotFound(t *testing.T) {
	tx := &stubTransactionClient{getTransferStatusFn: func(*transactionpb.GetTransferRequest) (*transactionpb.TransferStatusResponse, error) {
		return nil, status.Error(codes.NotFound, "no such transfer")
	}}
	h := handler.NewTransactionHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transferStatusRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v3/me/transfers/99/status", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerTxStatusHandler serves GET /api/v3/interbank/:transaction_id/status.
// Allows a peer bank to query the status of a cross-bank transaction by
// its transactionId so both sides can resume a stuck saga (Celina-5
// CHECK_STATUS mechanism, §"Mehanizam za Retry").
type PeerTxStatusHandler struct {
	client transactionpb.PeerTxServiceClient
}

func NewPeerTxStatusHandler(c transactionpb.PeerTxServiceClient) *PeerTxStatusHandler {
	return &PeerTxStatusHandler{client: c}
}

// GetTxStatus godoc
// @Summary      Peer-to-peer: query cross-bank transaction status (CHECK_STATUS)
// @Description  Allows a peer bank to query the state of a cross-bank SI-TX transaction by its transactionId. Returns the state and our role (sender|receiver). Used by the Celina-5 CHECK_STATUS retry mechanism so stuck sagas can be resolved by either side.
// @Tags         PeerOTC
// @Produce      json
// @Param        transaction_id  path      string  true  "SI-TX transaction UUID (idempotence key)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/interbank/{transaction_id}/status [get]
func (h *PeerTxStatusHandler) GetTxStatus(c *gin.Context) {
	txID := c.Param("transaction_id")
	if txID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transaction_id required"})
		return
	}

	peerBankCode, _ := c.Get("peer_bank_code")
	callerCode, _ := peerBankCode.(string)

	resp, err := h.client.GetTxStatus(c.Request.Context(), &transactionpb.GetTxStatusRequest{
		TransactionId:      txID,
		CallerPeerBankCode: callerCode,
	})
	if err != nil {
		renderPeerGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transaction_id":  txID,
		"state":           resp.GetState(),
		"our_role":        resp.GetOurRole(),
		"last_action_at":  resp.GetLastActionAt(),
		"last_error":      resp.GetLastError(),
	})
}

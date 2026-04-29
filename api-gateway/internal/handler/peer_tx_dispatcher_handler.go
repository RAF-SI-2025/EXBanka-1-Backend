package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerTxDispatcherHandler serves /api/v3/me/transfers and /api/v3/me/transfers/:id.
// For intra-bank receivers (own 3-digit prefix) it delegates to the
// existing TransactionHandler. For foreign-prefix receivers it dispatches
// to PeerTxService.InitiateOutboundTx via gRPC, returning 202 Accepted
// with a poll URL.
type PeerTxDispatcherHandler struct {
	tx          *TransactionHandler
	peerTx      transactionpb.PeerTxServiceClient
	ownBankCode string
}

// NewPeerTxDispatcherHandler constructs the dispatcher. ownBankCode is the
// 3-digit prefix of this bank (matches OWN_BANK_CODE env var; default "111").
func NewPeerTxDispatcherHandler(tx *TransactionHandler, peerTx transactionpb.PeerTxServiceClient, ownBankCode string) *PeerTxDispatcherHandler {
	return &PeerTxDispatcherHandler{tx: tx, peerTx: peerTx, ownBankCode: ownBankCode}
}

// CreateTransfer routes to the intra-bank handler when the receiver
// account number's 3-digit prefix matches ownBankCode. Foreign-prefix
// receivers dispatch to PeerTxService.InitiateOutboundTx.
//
// CreateTransfer godoc
// @Summary      Create a transfer (dispatches intra-bank or inter-bank SI-TX)
// @Description  Intra-bank receivers (own 3-digit prefix) delegate to the standard transfer flow and return 201. Foreign-prefix receivers dispatch to PeerTxService.InitiateOutboundTx and return 202 Accepted with {transaction_id, poll_url, status}.
// @Tags         transfers
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body interface{} true "Transfer request — see TransactionHandler.CreateTransfer"
// @Success      201 {object} map[string]interface{}
// @Success      202 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      500 {object} map[string]interface{}
// @Router       /api/v3/me/transfers [post]
func (h *PeerTxDispatcherHandler) CreateTransfer(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "unreadable body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// Tolerant unmarshal: amount may arrive as a JSON number (legacy
	// intra-bank shape) or as a string (SI-TX shape). RawMessage lets us
	// decide based on the routing branch which form to forward.
	var req struct {
		FromAccountNumber string          `json:"from_account_number"`
		ToAccountNumber   string          `json:"to_account_number"`
		ReceiverAccount   string          `json:"receiverAccount,omitempty"`
		Amount            json.RawMessage `json:"amount"`
		Currency          string          `json:"currency,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	receiver := req.ToAccountNumber
	if receiver == "" {
		receiver = req.ReceiverAccount
	}

	if len(receiver) >= 3 && receiver[:3] != h.ownBankCode {
		// Inter-bank dispatch via PeerTxService. Stringify amount so it
		// matches the SiTxInitiateRequest.amount string contract.
		amount := strings.Trim(string(req.Amount), `"`)
		resp, err := h.peerTx.InitiateOutboundTx(c.Request.Context(), &transactionpb.SiTxInitiateRequest{
			FromAccountNumber: req.FromAccountNumber,
			ToAccountNumber:   receiver,
			Amount:            amount,
			Currency:          req.Currency,
		})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{
			"transaction_id": resp.GetTransactionId(),
			"poll_url":       resp.GetPollUrl(),
			"status":         resp.GetStatus(),
		})
		return
	}
	// Intra-bank.
	h.tx.CreateTransfer(c)
}

// GetTransferByID — intra-bank lookup only. UUID-style transactionIds
// (which the deleted InterBankPublicHandler used to recognise) now
// return 404 via the underlying handler's int parse. Phase 3+ will
// extend this to fall through to outbound_peer_txs lookup if the id
// parses as a UUID.
//
// GetTransferByID godoc
// @Summary      Get a transfer by ID
// @Description  Delegates to the intra-bank GetMyTransfer handler. UUID-style transaction IDs return 404 since the inter-bank lookup path is not yet wired.
// @Tags         transfers
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Transfer ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/transfers/{id} [get]
func (h *PeerTxDispatcherHandler) GetTransferByID(c *gin.Context) {
	h.tx.GetMyTransfer(c)
}

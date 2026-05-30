package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerTxDispatcherHandler serves /api/v3/me/payments. For intra-bank receivers
// (own 3-digit prefix) it delegates to the existing TransactionHandler payment
// flow. For foreign-prefix receivers it dispatches to
// PeerTxService.InitiateOutboundTx via gRPC, returning 202 Accepted with a poll
// URL. (Cross-bank money sends are payments — to another person at another
// bank; transfers are intra-bank/same-client only.)
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

// CreatePayment routes to the intra-bank payment handler when the receiver
// account number's 3-digit prefix matches ownBankCode. Foreign-prefix receivers
// dispatch to PeerTxService.InitiateOutboundTx, which rejects an unregistered
// peer bank with 404 "peer bank XXX not registered" before any funds move.
//
// Currency: the SI-TX posting uses a single currency for both legs. The
// sender's account is always local, so the currency is resolved from it
// (account-service) unless the caller supplies an explicit "currency" override.
//
// CreatePayment godoc
// @Summary      Create a payment (dispatches intra-bank or inter-bank SI-TX)
// @Description  Intra-bank receivers (own 3-digit prefix) run the standard payment flow and return 201. Foreign-prefix receivers dispatch to PeerTxService.InitiateOutboundTx and return 202 Accepted with {transaction_id, poll_url, status}. An unregistered destination bank code returns 404 before any debit. Currency is resolved from the sender's account unless an explicit "currency" is supplied.
// @Tags         payments
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body interface{} true "Payment request — see TransactionHandler.CreatePayment; optional 'currency' override for cross-bank"
// @Success      201 {object} map[string]interface{}
// @Success      202 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      500 {object} map[string]interface{}
// @Router       /api/v3/me/payments [post]
func (h *PeerTxDispatcherHandler) CreatePayment(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "unreadable body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// Tolerant unmarshal: amount may arrive as a JSON number (intra-bank shape)
	// or as a string (SI-TX shape). RawMessage lets us forward the right form
	// per branch. "currency" is an optional cross-bank override.
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
		// Cross-bank payment dispatched via SI-TX.
		amount := strings.Trim(string(req.Amount), `"`)
		currency := req.Currency
		if currency == "" {
			// Resolve the sender's account currency — the SI-TX posting currency.
			cur, cerr := h.tx.AccountCurrency(c.Request.Context(), req.FromAccountNumber)
			if cerr != nil {
				handleGRPCError(c, cerr)
				return
			}
			currency = cur
		}
		resp, err := h.peerTx.InitiateOutboundTx(c.Request.Context(), &transactionpb.SiTxInitiateRequest{
			FromAccountNumber: req.FromAccountNumber,
			ToAccountNumber:   receiver,
			Amount:            amount,
			Currency:          currency,
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
	// Intra-bank payment.
	h.tx.CreatePayment(c)
}

// crossBankStatus resolves a cross-bank SI-TX transaction id (the UUID returned
// by a cross-bank payment's 202 `poll_url`). A numeric id is a normal intra-bank
// payment — it returns false so the caller delegates to the intra-bank handler.
// A non-numeric id is treated as a SI-TX transaction id and resolved via
// PeerTxService.GetTxStatus; the UUID is unguessable and is only ever handed to
// the initiator, so knowing it authorizes reading its status.
func (h *PeerTxDispatcherHandler) crossBankStatus(c *gin.Context, id string) bool {
	if _, err := strconv.ParseUint(id, 10, 64); err == nil {
		return false // numeric → intra-bank payment id
	}
	resp, err := h.peerTx.GetTxStatus(c.Request.Context(), &transactionpb.GetTxStatusRequest{TransactionId: id})
	if err != nil {
		handleGRPCError(c, err)
		return true
	}
	c.JSON(http.StatusOK, gin.H{
		"transaction_id": id,
		"status":         resp.GetState(),
		"role":           resp.GetOurRole(),
		"last_action_at": resp.GetLastActionAt(),
		"last_error":     resp.GetLastError(),
	})
	return true
}

// GetPaymentByID resolves the id in the cross-bank poll URL. A UUID-style id
// (cross-bank SI-TX send) is resolved via PeerTxService.GetTxStatus; a numeric
// id delegates to the intra-bank GetMyPayment handler.
//
// GetPaymentByID godoc
// @Summary      Get a payment by ID (or cross-bank SI-TX status by UUID)
// @Description  Numeric id → intra-bank payment. UUID id (from a cross-bank payment's poll_url) → SI-TX status {transaction_id, status, role, last_action_at, last_error}.
// @Tags         payments
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Payment ID (numeric) or SI-TX transaction id (UUID)"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/payments/{id} [get]
func (h *PeerTxDispatcherHandler) GetPaymentByID(c *gin.Context) {
	if h.crossBankStatus(c, c.Param("id")) {
		return
	}
	h.tx.GetMyPayment(c)
}

// GetPaymentStatusByID is the status route's UUID-aware variant: a cross-bank
// SI-TX id resolves via GetTxStatus, a numeric id delegates to the intra-bank
// GetMyPaymentStatus.
//
// GetPaymentStatusByID godoc
// @Summary      Get a payment's status (or cross-bank SI-TX status by UUID)
// @Description  Numeric id → intra-bank payment status. UUID id (cross-bank) → SI-TX status.
// @Tags         payments
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Payment ID (numeric) or SI-TX transaction id (UUID)"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/payments/{id}/status [get]
func (h *PeerTxDispatcherHandler) GetPaymentStatusByID(c *gin.Context) {
	if h.crossBankStatus(c, c.Param("id")) {
		return
	}
	h.tx.GetMyPaymentStatus(c)
}

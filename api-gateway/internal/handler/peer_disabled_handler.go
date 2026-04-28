package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// PeerDisabledHandler serves /api/v3/me/transfers and /api/v3/me/transfers/:id
// while the SI-TX implementation is being built. For intra-bank receivers it
// delegates to the existing TransactionHandler. For foreign-prefix receivers
// it returns 501 with a clear error.
//
// Phase 1 of the SI-TX refactor (docs/superpowers/specs/2026-04-29-celina5-
// sitx-refactor-design.md): this temporary wrapper lets the gateway compile
// without the InterBankPublicHandler while inter-bank functionality is
// pending.
type PeerDisabledHandler struct {
	tx          *TransactionHandler
	ownBankCode string
}

// NewPeerDisabledHandler constructs the wrapper. ownBankCode is the 3-digit
// prefix of this bank (matches OWN_BANK_CODE env var; default "111").
func NewPeerDisabledHandler(tx *TransactionHandler, ownBankCode string) *PeerDisabledHandler {
	return &PeerDisabledHandler{tx: tx, ownBankCode: ownBankCode}
}

// CreateTransfer routes to the intra-bank handler when the receiver account
// number's 3-digit prefix matches ownBankCode. Foreign-prefix receivers get
// 501 not_implemented (SI-TX implementation pending).
//
// CreateTransfer godoc
// @Summary      Create a transfer (intra-bank only during SI-TX refactor)
// @Description  Foreign-prefix receivers return 501 not_implemented while the SI-TX implementation is being built. Intra-bank receivers (own 3-digit prefix) delegate to the standard transfer flow.
// @Tags         transfers
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body interface{} true "Transfer request — see TransactionHandler.CreateTransfer"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      501 {object} map[string]interface{}
// @Router       /api/v3/me/transfers [post]
func (h *PeerDisabledHandler) CreateTransfer(c *gin.Context) {
	receiver := h.peekReceiverAccountNumber(c)
	// Only return 501 when we can confidently identify a foreign-bank prefix
	// (receiver has at least 3 chars and the prefix is not ours). Receivers
	// shorter than 3 chars (or unreadable bodies) fall through so the
	// downstream handler can surface the appropriate validation error.
	if len(receiver) >= 3 && !h.isOwnPrefix(receiver) {
		apiError(c, http.StatusNotImplemented, "not_implemented",
			"inter-bank transfers are temporarily disabled (SI-TX implementation in progress)")
		return
	}
	h.tx.CreateTransfer(c)
}

// GetTransferByID — intra-bank lookup only. UUID-style transactionIds (which
// the deleted InterBankPublicHandler used to recognise) now return 404 via
// the underlying handler's int parse.
//
// GetTransferByID godoc
// @Summary      Get a transfer by ID (intra-bank only during SI-TX refactor)
// @Description  Delegates to the intra-bank GetMyTransfer handler. UUID-style transaction IDs return 404 since the inter-bank lookup path is temporarily removed.
// @Tags         transfers
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Transfer ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/transfers/{id} [get]
func (h *PeerDisabledHandler) GetTransferByID(c *gin.Context) {
	h.tx.GetMyTransfer(c)
}

// peekReceiverAccountNumber pulls the to_account_number / receiverAccount
// field from the request body without consuming it. We read the raw bytes,
// restore the body so the downstream handler can re-bind, then JSON-decode
// into a map for the peek. On any error we return "" and fall through to
// the intra-bank handler.
func (h *PeerDisabledHandler) peekReceiverAccountNumber(c *gin.Context) string {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	// Restore so the downstream handler can ShouldBindJSON normally.
	c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	if v, ok := body["to_account_number"].(string); ok && v != "" {
		return v
	}
	if v, ok := body["receiverAccount"].(string); ok && v != "" {
		return v
	}
	return ""
}

func (h *PeerDisabledHandler) isOwnPrefix(accountNumber string) bool {
	return len(accountNumber) >= 3 && accountNumber[:3] == h.ownBankCode
}

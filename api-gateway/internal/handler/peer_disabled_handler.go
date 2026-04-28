package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
func (h *PeerDisabledHandler) CreateTransfer(c *gin.Context) {
	receiver := h.peekReceiverAccountNumber(c)
	if receiver != "" && !h.isOwnPrefix(receiver) {
		apiError(c, http.StatusNotImplemented, "not_implemented",
			"inter-bank transfers are temporarily disabled (SI-TX implementation in progress)")
		return
	}
	h.tx.CreateTransfer(c)
}

// GetTransferByID — intra-bank lookup only. UUID-style transactionIds (which
// the deleted InterBankPublicHandler used to recognise) now return 404 via
// the underlying handler's int parse.
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
	if len(accountNumber) < 3 {
		return false
	}
	prefix := accountNumber[:3]
	if strings.Contains(prefix, "-") {
		// Account numbers with separator: prefix is everything before first '-'
		idx := strings.Index(accountNumber, "-")
		if idx <= 0 {
			return false
		}
		prefix = accountNumber[:idx]
	}
	return prefix == h.ownBankCode
}

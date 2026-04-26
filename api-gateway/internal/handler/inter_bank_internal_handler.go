package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// Action enum strings on the wire (Spec 3 §6.1, mirror of
// transaction-service/internal/messaging/inter_bank_envelope.go).
const (
	actionPrepare     = "Prepare"
	actionReady       = "Ready"
	actionNotReady    = "NotReady"
	actionCommit      = "Commit"
	actionCommitted   = "Committed"
	actionCheckStatus = "CheckStatus"
	actionStatus      = "Status"
)

// InterBankInternalHandler bridges the HMAC-authenticated internal HTTP
// routes to the InterBankService gRPC handlers. The gateway is a thin
// translator: decode the envelope, forward to gRPC, encode the response.
type InterBankInternalHandler struct {
	client transactionpb.InterBankServiceClient
}

// NewInterBankInternalHandler constructs the handler.
func NewInterBankInternalHandler(client transactionpb.InterBankServiceClient) *InterBankInternalHandler {
	return &InterBankInternalHandler{client: client}
}

type rawEnvelope struct {
	TransactionID    string          `json:"transactionId"`
	Action           string          `json:"action"`
	SenderBankCode   string          `json:"senderBankCode"`
	ReceiverBankCode string          `json:"receiverBankCode"`
	Timestamp        string          `json:"timestamp"`
	ProtocolVersion  string          `json:"protocolVersion,omitempty"`
	Body             json.RawMessage `json:"body"`
}

type prepareInner struct {
	SenderAccount   string `json:"senderAccount"`
	ReceiverAccount string `json:"receiverAccount"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	Memo            string `json:"memo,omitempty"`
}

type readyInner struct {
	Status           string `json:"status"`
	OriginalAmount   string `json:"originalAmount"`
	OriginalCurrency string `json:"originalCurrency"`
	FinalAmount      string `json:"finalAmount"`
	FinalCurrency    string `json:"finalCurrency"`
	FxRate           string `json:"fxRate"`
	Fees             string `json:"fees"`
	ValidUntil       string `json:"validUntil"`
}

type notReadyInner struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type commitInner struct {
	FinalAmount   string `json:"finalAmount"`
	FinalCurrency string `json:"finalCurrency"`
	FxRate        string `json:"fxRate"`
	Fees          string `json:"fees"`
}

type committedInner struct {
	CreditedAt       string `json:"creditedAt"`
	CreditedAmount   string `json:"creditedAmount"`
	CreditedCurrency string `json:"creditedCurrency"`
}

type statusInner struct {
	Role          string `json:"role"`
	Status        string `json:"status"`
	FinalAmount   string `json:"finalAmount,omitempty"`
	FinalCurrency string `json:"finalCurrency,omitempty"`
	UpdatedAt     string `json:"updatedAt"`
}

func decodeEnvelope(c *gin.Context) (*rawEnvelope, bool) {
	var env rawEnvelope
	if err := c.BindJSON(&env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_envelope", "message": err.Error()}})
		return nil, false
	}
	if env.TransactionID == "" || env.Action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_envelope", "message": "transactionId and action are required"}})
		return nil, false
	}
	return &env, true
}

func writeEnvelope(c *gin.Context, status int, env rawEnvelope) {
	if env.Timestamp == "" {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	c.JSON(status, env)
}

// Prepare handles POST /internal/inter-bank/transfer/prepare.
func (h *InterBankInternalHandler) Prepare(c *gin.Context) {
	env, ok := decodeEnvelope(c)
	if !ok {
		return
	}
	if env.Action != actionPrepare {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "wrong_action", "message": "expected Prepare"}})
		return
	}
	var inner prepareInner
	if err := json.Unmarshal(env.Body, &inner); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_body", "message": err.Error()}})
		return
	}
	resp, err := h.client.HandlePrepare(c.Request.Context(), &transactionpb.InterBankPrepareRequest{
		TransactionId:    env.TransactionID,
		SenderBankCode:   env.SenderBankCode,
		ReceiverBankCode: env.ReceiverBankCode,
		SenderAccount:    inner.SenderAccount,
		ReceiverAccount:  inner.ReceiverAccount,
		Amount:           inner.Amount,
		Currency:         inner.Currency,
		Memo:             inner.Memo,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "downstream_error", "message": err.Error()}})
		return
	}
	if resp.Ready {
		body, _ := json.Marshal(readyInner{
			Status:           "Ready",
			OriginalAmount:   inner.Amount,
			OriginalCurrency: inner.Currency,
			FinalAmount:      resp.FinalAmount,
			FinalCurrency:    resp.FinalCurrency,
			FxRate:           resp.FxRate,
			Fees:             resp.Fees,
			ValidUntil:       resp.ValidUntil,
		})
		writeEnvelope(c, http.StatusOK, rawEnvelope{
			TransactionID:    env.TransactionID,
			Action:           actionReady,
			SenderBankCode:   env.ReceiverBankCode,
			ReceiverBankCode: env.SenderBankCode,
			Body:             body,
		})
		return
	}
	body, _ := json.Marshal(notReadyInner{Status: "NotReady", Reason: resp.Reason})
	writeEnvelope(c, http.StatusOK, rawEnvelope{
		TransactionID:    env.TransactionID,
		Action:           actionNotReady,
		SenderBankCode:   env.ReceiverBankCode,
		ReceiverBankCode: env.SenderBankCode,
		Body:             body,
	})
}

// Commit handles POST /internal/inter-bank/transfer/commit.
func (h *InterBankInternalHandler) Commit(c *gin.Context) {
	env, ok := decodeEnvelope(c)
	if !ok {
		return
	}
	if env.Action != actionCommit {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "wrong_action", "message": "expected Commit"}})
		return
	}
	var inner commitInner
	if err := json.Unmarshal(env.Body, &inner); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_body", "message": err.Error()}})
		return
	}
	resp, err := h.client.HandleCommit(c.Request.Context(), &transactionpb.InterBankCommitRequest{
		TransactionId: env.TransactionID,
		FinalAmount:   inner.FinalAmount,
		FinalCurrency: inner.FinalCurrency,
		FxRate:        inner.FxRate,
		Fees:          inner.Fees,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "downstream_error", "message": err.Error()}})
		return
	}
	if resp.NotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "unknown transactionId"}})
		return
	}
	if !resp.Committed {
		body, _ := json.Marshal(notReadyInner{Status: "CommitMismatch", Reason: resp.MismatchReason})
		writeEnvelope(c, http.StatusConflict, rawEnvelope{
			TransactionID:    env.TransactionID,
			Action:           actionNotReady,
			SenderBankCode:   env.ReceiverBankCode,
			ReceiverBankCode: env.SenderBankCode,
			Body:             body,
		})
		return
	}
	body, _ := json.Marshal(committedInner{
		CreditedAt:       resp.CreditedAt,
		CreditedAmount:   resp.CreditedAmount,
		CreditedCurrency: resp.CreditedCurrency,
	})
	writeEnvelope(c, http.StatusOK, rawEnvelope{
		TransactionID:    env.TransactionID,
		Action:           actionCommitted,
		SenderBankCode:   env.ReceiverBankCode,
		ReceiverBankCode: env.SenderBankCode,
		Body:             body,
	})
}

// CheckStatus handles POST /internal/inter-bank/check-status.
func (h *InterBankInternalHandler) CheckStatus(c *gin.Context) {
	env, ok := decodeEnvelope(c)
	if !ok {
		return
	}
	if env.Action != actionCheckStatus {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "wrong_action", "message": "expected CheckStatus"}})
		return
	}
	resp, err := h.client.HandleCheckStatus(c.Request.Context(), &transactionpb.InterBankCheckStatusRequest{
		TransactionId: env.TransactionID,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "downstream_error", "message": err.Error()}})
		return
	}
	if resp.NotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "unknown transactionId"}})
		return
	}
	body, _ := json.Marshal(statusInner{
		Role:          resp.Role,
		Status:        resp.Status,
		FinalAmount:   resp.FinalAmount,
		FinalCurrency: resp.FinalCurrency,
		UpdatedAt:     resp.UpdatedAt,
	})
	writeEnvelope(c, http.StatusOK, rawEnvelope{
		TransactionID:    env.TransactionID,
		Action:           actionStatus,
		SenderBankCode:   env.ReceiverBankCode,
		ReceiverBankCode: env.SenderBankCode,
		Body:             body,
	})
}

// ensure imports are used
var _ = errors.New

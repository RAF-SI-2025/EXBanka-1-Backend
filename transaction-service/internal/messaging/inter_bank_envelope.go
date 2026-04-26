// Package messaging holds wire-protocol envelope marshalling for inter-bank
// 2PC transfers (SI-TX-PROTO 2024/25 — see docs/superpowers/refs/si-tx-proto-mapping.md).
//
// The envelope is the constant outermost shape of every cross-bank message:
// transactionId, action, senderBankCode, receiverBankCode, timestamp, body.
// The body is action-specific JSON, kept as RawMessage so callers can decode
// only when they need to.
package messaging

import (
	"encoding/json"
	"time"
)

// Action enum values defined by Spec 3 §6.1. Case-sensitive on the wire.
const (
	ActionPrepare     = "Prepare"
	ActionReady       = "Ready"
	ActionNotReady    = "NotReady"
	ActionCommit      = "Commit"
	ActionCommitted   = "Committed"
	ActionCheckStatus = "CheckStatus"
	ActionStatus      = "Status"
)

// Envelope is the outer JSON shape exchanged between banks.
type Envelope struct {
	TransactionID    string          `json:"transactionId"`
	Action           string          `json:"action"`
	SenderBankCode   string          `json:"senderBankCode"`
	ReceiverBankCode string          `json:"receiverBankCode"`
	Timestamp        string          `json:"timestamp"`
	ProtocolVersion  string          `json:"protocolVersion,omitempty"`
	Body             json.RawMessage `json:"body"`
}

// PrepareBody — sender → receiver (Spec 3 §6.2).
type PrepareBody struct {
	SenderAccount   string `json:"senderAccount"`
	ReceiverAccount string `json:"receiverAccount"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	Memo            string `json:"memo,omitempty"`
}

// ReadyBody — receiver → sender (Spec 3 §6.3).
type ReadyBody struct {
	Status           string `json:"status"`
	OriginalAmount   string `json:"originalAmount"`
	OriginalCurrency string `json:"originalCurrency"`
	FinalAmount      string `json:"finalAmount"`
	FinalCurrency    string `json:"finalCurrency"`
	FxRate           string `json:"fxRate"`
	Fees             string `json:"fees"`
	ValidUntil       string `json:"validUntil"`
}

// NotReadyBody — receiver → sender (Spec 3 §6.4).
type NotReadyBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// CommitBody — sender → receiver (Spec 3 §6.5).
type CommitBody struct {
	FinalAmount   string `json:"finalAmount"`
	FinalCurrency string `json:"finalCurrency"`
	FxRate        string `json:"fxRate"`
	Fees          string `json:"fees"`
}

// CommittedBody — receiver → sender (Spec 3 §6.6).
type CommittedBody struct {
	CreditedAt        string `json:"creditedAt"`
	CreditedAmount    string `json:"creditedAmount"`
	CreditedCurrency  string `json:"creditedCurrency"`
}

// CheckStatusBody — either direction (Spec 3 §6.7 request).
type CheckStatusBody struct{}

// StatusBody — either direction (Spec 3 §6.7 response).
type StatusBody struct {
	Role          string `json:"role"`
	Status        string `json:"status"`
	FinalAmount   string `json:"finalAmount,omitempty"`
	FinalCurrency string `json:"finalCurrency,omitempty"`
	UpdatedAt     string `json:"updatedAt"`
}

// NowRFC3339 returns the current UTC time formatted for the envelope's
// `timestamp` field.
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

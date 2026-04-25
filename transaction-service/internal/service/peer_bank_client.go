package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/exbanka/transaction-service/internal/messaging"
)

// ErrPeerNotFound is returned when the peer responds 404 to CheckStatus or
// Commit — signals the peer has no record of this transactionId.
var ErrPeerNotFound = errors.New("peer reports unknown transaction")

// ErrPeerCommitMismatch is returned when the peer responds 409 to a Commit —
// signals our final terms don't match its prior Ready.
var ErrPeerCommitMismatch = errors.New("peer reports commit mismatch")

// PeerBankClient sends Prepare/Commit/CheckStatus to a single peer bank,
// signing each request with the outbound HMAC key for that peer. Use one
// instance per peer (held in a map by code).
type PeerBankClient struct {
	ownBankCode      string
	receiverBankCode string
	outboundKey      []byte
	baseURL          string
	httpClient       *http.Client
}

// NewPeerBankClient constructs a client targeted at one peer bank. baseURL
// must be the full prefix to which `/transfer/prepare`, `/transfer/commit`,
// and `/check-status` will be appended (e.g.
// "https://peer-222/internal/inter-bank").
func NewPeerBankClient(ownBankCode, receiverBankCode, outboundKey, baseURL string, timeout time.Duration) *PeerBankClient {
	return &PeerBankClient{
		ownBankCode:      ownBankCode,
		receiverBankCode: receiverBankCode,
		outboundKey:      []byte(outboundKey),
		baseURL:          baseURL,
		httpClient:       &http.Client{Timeout: timeout},
	}
}

// PrepareRequest is the input shape callers pass to SendPrepare.
type PrepareRequest struct {
	TransactionID   string
	SenderAccount   string
	ReceiverAccount string
	Amount          string
	Currency        string
	Memo            string
}

// PrepareResponse is the parsed output of a Prepare round-trip.
type PrepareResponse struct {
	Ready         bool
	FinalAmount   string
	FinalCurrency string
	FxRate        string
	Fees          string
	ValidUntil    string
	Reason        string
}

// SendPrepare sends a Prepare envelope and parses the Ready/NotReady reply.
func (c *PeerBankClient) SendPrepare(ctx context.Context, req PrepareRequest) (*PrepareResponse, error) {
	bodyJSON, err := json.Marshal(messaging.PrepareBody{
		SenderAccount:   req.SenderAccount,
		ReceiverAccount: req.ReceiverAccount,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Memo:            req.Memo,
	})
	if err != nil {
		return nil, err
	}
	respEnv, err := c.do(ctx, "/transfer/prepare", messaging.Envelope{
		TransactionID:    req.TransactionID,
		Action:           messaging.ActionPrepare,
		SenderBankCode:   c.ownBankCode,
		ReceiverBankCode: c.receiverBankCode,
		Timestamp:        messaging.NowRFC3339(),
		Body:             bodyJSON,
	})
	if err != nil {
		return nil, err
	}
	switch respEnv.Action {
	case messaging.ActionReady:
		var rb messaging.ReadyBody
		if err := json.Unmarshal(respEnv.Body, &rb); err != nil {
			return nil, fmt.Errorf("parse Ready body: %w", err)
		}
		return &PrepareResponse{
			Ready:         true,
			FinalAmount:   rb.FinalAmount,
			FinalCurrency: rb.FinalCurrency,
			FxRate:        rb.FxRate,
			Fees:          rb.Fees,
			ValidUntil:    rb.ValidUntil,
		}, nil
	case messaging.ActionNotReady:
		var nb messaging.NotReadyBody
		if err := json.Unmarshal(respEnv.Body, &nb); err != nil {
			return nil, fmt.Errorf("parse NotReady body: %w", err)
		}
		return &PrepareResponse{Ready: false, Reason: nb.Reason}, nil
	default:
		return nil, fmt.Errorf("unexpected action in Prepare response: %q", respEnv.Action)
	}
}

// CommitOutboundRequest is the input shape for SendCommit.
type CommitOutboundRequest struct {
	TransactionID string
	FinalAmount   string
	FinalCurrency string
	FxRate        string
	Fees          string
}

// CommitOutboundResponse is the parsed output of a Commit round-trip.
type CommitOutboundResponse struct {
	CreditedAt       string
	CreditedAmount   string
	CreditedCurrency string
}

// SendCommit sends a Commit envelope. Returns ErrPeerCommitMismatch on HTTP
// 409 (the peer's stored final terms don't match), ErrPeerNotFound on 404.
func (c *PeerBankClient) SendCommit(ctx context.Context, req CommitOutboundRequest) (*CommitOutboundResponse, error) {
	bodyJSON, err := json.Marshal(messaging.CommitBody{
		FinalAmount:   req.FinalAmount,
		FinalCurrency: req.FinalCurrency,
		FxRate:        req.FxRate,
		Fees:          req.Fees,
	})
	if err != nil {
		return nil, err
	}
	respEnv, err := c.do(ctx, "/transfer/commit", messaging.Envelope{
		TransactionID:    req.TransactionID,
		Action:           messaging.ActionCommit,
		SenderBankCode:   c.ownBankCode,
		ReceiverBankCode: c.receiverBankCode,
		Timestamp:        messaging.NowRFC3339(),
		Body:             bodyJSON,
	})
	if err != nil {
		return nil, err
	}
	if respEnv.Action != messaging.ActionCommitted {
		return nil, fmt.Errorf("unexpected action in Commit response: %q", respEnv.Action)
	}
	var cb messaging.CommittedBody
	if err := json.Unmarshal(respEnv.Body, &cb); err != nil {
		return nil, fmt.Errorf("parse Committed body: %w", err)
	}
	return &CommitOutboundResponse{
		CreditedAt:       cb.CreditedAt,
		CreditedAmount:   cb.CreditedAmount,
		CreditedCurrency: cb.CreditedCurrency,
	}, nil
}

// CheckStatusResponse is the parsed output of a CheckStatus round-trip.
type CheckStatusResponse struct {
	Role          string
	Status        string
	FinalAmount   string
	FinalCurrency string
	UpdatedAt     string
}

// SendCheckStatus probes the peer for the current state of a transaction.
// Returns ErrPeerNotFound on 404.
func (c *PeerBankClient) SendCheckStatus(ctx context.Context, txID string) (*CheckStatusResponse, error) {
	respEnv, err := c.do(ctx, "/check-status", messaging.Envelope{
		TransactionID:    txID,
		Action:           messaging.ActionCheckStatus,
		SenderBankCode:   c.ownBankCode,
		ReceiverBankCode: c.receiverBankCode,
		Timestamp:        messaging.NowRFC3339(),
		Body:             json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}
	if respEnv.Action != messaging.ActionStatus {
		return nil, fmt.Errorf("unexpected action in CheckStatus response: %q", respEnv.Action)
	}
	var sb messaging.StatusBody
	if err := json.Unmarshal(respEnv.Body, &sb); err != nil {
		return nil, fmt.Errorf("parse Status body: %w", err)
	}
	return &CheckStatusResponse{
		Role:          sb.Role,
		Status:        sb.Status,
		FinalAmount:   sb.FinalAmount,
		FinalCurrency: sb.FinalCurrency,
		UpdatedAt:     sb.UpdatedAt,
	}, nil
}

// do marshals the envelope, signs it, performs the HTTP POST, and parses
// the response envelope. Status-code-to-error mapping matches Spec 3 §7.2.
func (c *PeerBankClient) do(ctx context.Context, path string, env messaging.Envelope) (*messaging.Envelope, error) {
	bodyBytes, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Bank-Code", c.ownBankCode)
	httpReq.Header.Set("X-Idempotency-Key", env.TransactionID)
	httpReq.Header.Set("X-Timestamp", env.Timestamp)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	httpReq.Header.Set("X-Nonce", hex.EncodeToString(nonceBytes))
	mac := hmac.New(sha256.New, c.outboundKey)
	mac.Write(bodyBytes)
	httpReq.Header.Set("X-Bank-Signature", hex.EncodeToString(mac.Sum(nil)))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrPeerNotFound
	case resp.StatusCode == http.StatusConflict:
		return nil, fmt.Errorf("%w: %s", ErrPeerCommitMismatch, string(respBytes))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest:
		return nil, fmt.Errorf("peer rejected (HTTP %d): %s", resp.StatusCode, string(respBytes))
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("peer 5xx (HTTP %d): %s", resp.StatusCode, string(respBytes))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("peer unexpected status %d: %s", resp.StatusCode, string(respBytes))
	}

	var out messaging.Envelope
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("parse response envelope: %w", err)
	}
	return &out, nil
}

// PeerBankRouter maps a peer's 3-digit bank code to its PeerBankClient.
// Implementations are typically a small struct holding a map; the
// InterBankService accepts the interface to keep the unit-test surface
// small.
type PeerBankRouter interface {
	ClientFor(bankCode string) (*PeerBankClient, error)
}

// StaticPeerBankRouter is the production PeerBankRouter — a map populated
// from config at startup.
type StaticPeerBankRouter struct {
	clients map[string]*PeerBankClient
}

// NewStaticPeerBankRouter builds a router from the given client map. Map
// keys are 3-digit bank codes; values are pre-built PeerBankClient
// instances.
func NewStaticPeerBankRouter(clients map[string]*PeerBankClient) *StaticPeerBankRouter {
	return &StaticPeerBankRouter{clients: clients}
}

// ClientFor returns the client for the given peer or an error if the peer
// is not configured.
func (r *StaticPeerBankRouter) ClientFor(bankCode string) (*PeerBankClient, error) {
	c, ok := r.clients[bankCode]
	if !ok {
		return nil, fmt.Errorf("no peer-bank client configured for code %q", bankCode)
	}
	return c, nil
}

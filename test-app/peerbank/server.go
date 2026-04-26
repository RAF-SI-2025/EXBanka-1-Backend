// Package peerbank provides a mock peer-bank HTTP server that speaks the
// SI-TX-PROTO 2024/25 wire protocol (`Prepare` / `Ready` / `NotReady` /
// `Commit` / `Committed` / `CheckStatus` / `Status` envelopes — Spec 3 §6).
//
// The mock is for integration testing of EXBanka's inter-bank-2PC handlers.
// Drive it with ConfigureNext to queue per-request behaviors (Ready,
// NotReady, Timeout, Mismatch, 5xx, SilentDrop). Behaviors are consumed in
// FIFO order — once the queue is empty, the mock falls back to BehaviorReady
// for prepare and BehaviorCommitOK for commit.
//
// All inbound requests are HMAC-verified against `InboundKey`; outbound
// responses are NOT signed (the EXBanka HMAC middleware only verifies
// inbound traffic — peers calling US sign their requests, our gateway
// verifies). For symmetric-key testing scenarios where the unit under test
// is signing outbound, set InboundKey to whatever the SUT's outbound key is.
package peerbank

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Behavior controls how the mock responds to a single request.
type Behavior int

const (
	// BehaviorReady — Prepare reply is "Ready" with the configured terms.
	BehaviorReady Behavior = iota
	// BehaviorNotReady — Prepare reply is "NotReady" with the configured reason.
	BehaviorNotReady
	// BehaviorCommitOK — Commit reply is "Committed".
	BehaviorCommitOK
	// BehaviorCommitMismatch — Commit reply is HTTP 409 commit_mismatch.
	BehaviorCommitMismatch
	// BehaviorStatusKnown — CheckStatus reply is the configured status row.
	BehaviorStatusKnown
	// BehaviorStatusUnknown — CheckStatus reply is HTTP 404.
	BehaviorStatusUnknown
	// BehaviorTimeout — sleep past the SUT's HTTP-client timeout, then
	// return 200 with an empty body. Effectively a network black hole.
	BehaviorTimeout
	// BehaviorFiveXX — return HTTP 503 with no body.
	BehaviorFiveXX
	// BehaviorSilentDrop — close the connection without writing anything.
	BehaviorSilentDrop
)

// ReadyTerms is the FX/fees payload returned with BehaviorReady.
type ReadyTerms struct {
	OriginalAmount   string
	OriginalCurrency string
	FinalAmount      string
	FinalCurrency    string
	FxRate           string
	Fees             string
	ValidUntil       string
}

// CommittedTerms is the payload returned with BehaviorCommitOK.
type CommittedTerms struct {
	CreditedAt       string
	CreditedAmount   string
	CreditedCurrency string
}

// StatusTerms is the payload returned with BehaviorStatusKnown.
type StatusTerms struct {
	Role          string
	Status        string
	FinalAmount   string
	FinalCurrency string
	UpdatedAt     string
}

// Behaviors carries the configurable per-action queues.
type Behaviors struct {
	Prepare     []Behavior
	Commit      []Behavior
	CheckStatus []Behavior
}

// MockPeerBank is the HTTP server. Construct with New, then ConfigureNext to
// queue behaviors before driving the SUT.
type MockPeerBank struct {
	Server     *httptest.Server
	InboundKey string

	mu        sync.Mutex
	prepare   []prepareConfig
	commit    []commitConfig
	status    []statusConfig
	requests  []*RecordedRequest
	timeoutD  time.Duration
}

type prepareConfig struct {
	beh   Behavior
	terms ReadyTerms
	reason string
}

type commitConfig struct {
	beh   Behavior
	terms CommittedTerms
	reason string
}

type statusConfig struct {
	beh   Behavior
	terms StatusTerms
}

// RecordedRequest is the captured form of an inbound request, useful for
// asserting that the SUT sent the right body / headers.
type RecordedRequest struct {
	Path    string
	Headers http.Header
	Body    []byte
}

// New starts the mock server and registers t.Cleanup to shut it down.
// inboundKey is the HMAC key the SUT uses to sign requests to the mock.
func New(t *testing.T, inboundKey string) *MockPeerBank {
	t.Helper()
	mb := &MockPeerBank{InboundKey: inboundKey, timeoutD: 35 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/inter-bank/transfer/prepare", mb.handlePrepare)
	mux.HandleFunc("/internal/inter-bank/transfer/commit", mb.handleCommit)
	mux.HandleFunc("/internal/inter-bank/check-status", mb.handleStatus)
	// Backwards-compatible aliases for callers that pass the bare paths.
	mux.HandleFunc("/transfer/prepare", mb.handlePrepare)
	mux.HandleFunc("/transfer/commit", mb.handleCommit)
	mux.HandleFunc("/check-status", mb.handleStatus)
	mb.Server = httptest.NewServer(mux)
	t.Cleanup(mb.Server.Close)
	return mb
}

// NewOnPort starts the mock server bound to the given port (so the docker
// stack can reach it via host.docker.internal:<port>) and registers
// t.Cleanup to shut it down. Use this for full-stack integration tests
// where a containerized transaction-service needs to talk to a mock peer
// running on the test host.
func NewOnPort(t *testing.T, port int, inboundKey string) *MockPeerBank {
	t.Helper()
	mb := &MockPeerBank{InboundKey: inboundKey, timeoutD: 35 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/inter-bank/transfer/prepare", mb.handlePrepare)
	mux.HandleFunc("/internal/inter-bank/transfer/commit", mb.handleCommit)
	mux.HandleFunc("/internal/inter-bank/check-status", mb.handleStatus)
	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{Addr: addr, Handler: mux}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("peerbank: listen %s: %v", addr, err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	mb.Server = &httptest.Server{URL: "http://localhost" + addr}
	return mb
}

// SetTimeoutDuration overrides the sleep used by BehaviorTimeout.
// Default 35s is comfortably past the spec's 30s prepare timeout.
func (m *MockPeerBank) SetTimeoutDuration(d time.Duration) {
	m.mu.Lock()
	m.timeoutD = d
	m.mu.Unlock()
}

// ConfigureReady queues a Ready response for the next Prepare with the given terms.
func (m *MockPeerBank) ConfigureReady(terms ReadyTerms) {
	m.mu.Lock()
	m.prepare = append(m.prepare, prepareConfig{beh: BehaviorReady, terms: terms})
	m.mu.Unlock()
}

// ConfigureNotReady queues a NotReady response with the given reason.
func (m *MockPeerBank) ConfigureNotReady(reason string) {
	m.mu.Lock()
	m.prepare = append(m.prepare, prepareConfig{beh: BehaviorNotReady, reason: reason})
	m.mu.Unlock()
}

// ConfigurePrepareBehavior queues a non-success Prepare behavior (Timeout,
// FiveXX, SilentDrop).
func (m *MockPeerBank) ConfigurePrepareBehavior(b Behavior) {
	m.mu.Lock()
	m.prepare = append(m.prepare, prepareConfig{beh: b})
	m.mu.Unlock()
}

// ConfigureCommitOK queues a Committed response with the given credited terms.
func (m *MockPeerBank) ConfigureCommitOK(terms CommittedTerms) {
	m.mu.Lock()
	m.commit = append(m.commit, commitConfig{beh: BehaviorCommitOK, terms: terms})
	m.mu.Unlock()
}

// ConfigureCommitMismatch queues an HTTP 409 commit_mismatch response.
func (m *MockPeerBank) ConfigureCommitMismatch(reason string) {
	m.mu.Lock()
	m.commit = append(m.commit, commitConfig{beh: BehaviorCommitMismatch, reason: reason})
	m.mu.Unlock()
}

// ConfigureCommitBehavior queues a non-success Commit behavior.
func (m *MockPeerBank) ConfigureCommitBehavior(b Behavior) {
	m.mu.Lock()
	m.commit = append(m.commit, commitConfig{beh: b})
	m.mu.Unlock()
}

// ConfigureStatusKnown queues a CheckStatus reply with the given status row.
func (m *MockPeerBank) ConfigureStatusKnown(terms StatusTerms) {
	m.mu.Lock()
	m.status = append(m.status, statusConfig{beh: BehaviorStatusKnown, terms: terms})
	m.mu.Unlock()
}

// ConfigureStatusUnknown queues an HTTP 404 CheckStatus reply.
func (m *MockPeerBank) ConfigureStatusUnknown() {
	m.mu.Lock()
	m.status = append(m.status, statusConfig{beh: BehaviorStatusUnknown})
	m.mu.Unlock()
}

// ConfigureStatusBehavior queues a non-success CheckStatus behavior.
func (m *MockPeerBank) ConfigureStatusBehavior(b Behavior) {
	m.mu.Lock()
	m.status = append(m.status, statusConfig{beh: b})
	m.mu.Unlock()
}

// Requests returns a snapshot of all inbound requests received so far.
func (m *MockPeerBank) Requests() []*RecordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*RecordedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// verifyAndRecord HMAC-verifies an inbound request, records it, and returns
// the parsed envelope. On verification failure it writes the response and
// returns nil.
func (m *MockPeerBank) verifyAndRecord(w http.ResponseWriter, r *http.Request) *envelope {
	body, _ := io.ReadAll(r.Body)
	rec := &RecordedRequest{Path: r.URL.Path, Headers: r.Header.Clone(), Body: body}
	m.mu.Lock()
	m.requests = append(m.requests, rec)
	m.mu.Unlock()

	mac := hmac.New(sha256.New, []byte(m.InboundKey))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if r.Header.Get("X-Bank-Signature") != want {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad_signature"}`))
		return nil
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_envelope"}`))
		return nil
	}
	return &env
}

func (m *MockPeerBank) handlePrepare(w http.ResponseWriter, r *http.Request) {
	env := m.verifyAndRecord(w, r)
	if env == nil {
		return
	}
	m.mu.Lock()
	var cfg prepareConfig
	if len(m.prepare) > 0 {
		cfg = m.prepare[0]
		m.prepare = m.prepare[1:]
	} else {
		cfg = prepareConfig{beh: BehaviorReady, terms: defaultReady()}
	}
	timeoutD := m.timeoutD
	m.mu.Unlock()

	switch cfg.beh {
	case BehaviorTimeout:
		time.Sleep(timeoutD)
		w.WriteHeader(http.StatusOK)
	case BehaviorFiveXX:
		w.WriteHeader(http.StatusServiceUnavailable)
	case BehaviorSilentDrop:
		hijackAndClose(w)
	case BehaviorNotReady:
		body, _ := json.Marshal(notReadyBody{Status: "NotReady", Reason: cfg.reason})
		writeEnvelope(w, http.StatusOK, env.TransactionID, "NotReady", env.ReceiverBankCode, env.SenderBankCode, body)
	default: // BehaviorReady
		t := cfg.terms
		body, _ := json.Marshal(readyBody{
			Status: "Ready",
			OriginalAmount: t.OriginalAmount, OriginalCurrency: t.OriginalCurrency,
			FinalAmount: t.FinalAmount, FinalCurrency: t.FinalCurrency,
			FxRate: t.FxRate, Fees: t.Fees, ValidUntil: t.ValidUntil,
		})
		writeEnvelope(w, http.StatusOK, env.TransactionID, "Ready", env.ReceiverBankCode, env.SenderBankCode, body)
	}
}

func (m *MockPeerBank) handleCommit(w http.ResponseWriter, r *http.Request) {
	env := m.verifyAndRecord(w, r)
	if env == nil {
		return
	}
	m.mu.Lock()
	var cfg commitConfig
	if len(m.commit) > 0 {
		cfg = m.commit[0]
		m.commit = m.commit[1:]
	} else {
		cfg = commitConfig{beh: BehaviorCommitOK, terms: defaultCommitted()}
	}
	timeoutD := m.timeoutD
	m.mu.Unlock()

	switch cfg.beh {
	case BehaviorTimeout:
		time.Sleep(timeoutD)
		w.WriteHeader(http.StatusOK)
	case BehaviorFiveXX:
		w.WriteHeader(http.StatusServiceUnavailable)
	case BehaviorSilentDrop:
		hijackAndClose(w)
	case BehaviorCommitMismatch:
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"reason":"` + cfg.reason + `"}`))
	default: // BehaviorCommitOK
		t := cfg.terms
		body, _ := json.Marshal(committedBody{
			CreditedAt: t.CreditedAt, CreditedAmount: t.CreditedAmount, CreditedCurrency: t.CreditedCurrency,
		})
		writeEnvelope(w, http.StatusOK, env.TransactionID, "Committed", env.ReceiverBankCode, env.SenderBankCode, body)
	}
}

func (m *MockPeerBank) handleStatus(w http.ResponseWriter, r *http.Request) {
	env := m.verifyAndRecord(w, r)
	if env == nil {
		return
	}
	m.mu.Lock()
	var cfg statusConfig
	if len(m.status) > 0 {
		cfg = m.status[0]
		m.status = m.status[1:]
	} else {
		cfg = statusConfig{beh: BehaviorStatusUnknown}
	}
	timeoutD := m.timeoutD
	m.mu.Unlock()

	switch cfg.beh {
	case BehaviorStatusUnknown:
		w.WriteHeader(http.StatusNotFound)
	case BehaviorTimeout:
		time.Sleep(timeoutD)
		w.WriteHeader(http.StatusOK)
	case BehaviorFiveXX:
		w.WriteHeader(http.StatusServiceUnavailable)
	case BehaviorSilentDrop:
		hijackAndClose(w)
	default: // BehaviorStatusKnown
		t := cfg.terms
		body, _ := json.Marshal(statusBody{
			Role: t.Role, Status: t.Status,
			FinalAmount: t.FinalAmount, FinalCurrency: t.FinalCurrency,
			UpdatedAt: t.UpdatedAt,
		})
		writeEnvelope(w, http.StatusOK, env.TransactionID, "Status", env.ReceiverBankCode, env.SenderBankCode, body)
	}
}

func defaultReady() ReadyTerms {
	return ReadyTerms{
		FinalAmount: "8.50", FinalCurrency: "EUR",
		FxRate: "117.65", Fees: "0",
		ValidUntil: time.Now().Add(60 * time.Second).UTC().Format(time.RFC3339),
	}
}

func defaultCommitted() CommittedTerms {
	return CommittedTerms{
		CreditedAt: time.Now().UTC().Format(time.RFC3339),
		CreditedAmount: "8.50", CreditedCurrency: "EUR",
	}
}

// envelope mirrors the wire shape — defined locally so the test-app module
// has no compile-time dependency on the transaction-service package.
type envelope struct {
	TransactionID    string          `json:"transactionId"`
	Action           string          `json:"action"`
	SenderBankCode   string          `json:"senderBankCode"`
	ReceiverBankCode string          `json:"receiverBankCode"`
	Timestamp        string          `json:"timestamp"`
	Body             json.RawMessage `json:"body"`
}

type readyBody struct {
	Status           string `json:"status"`
	OriginalAmount   string `json:"originalAmount"`
	OriginalCurrency string `json:"originalCurrency"`
	FinalAmount      string `json:"finalAmount"`
	FinalCurrency    string `json:"finalCurrency"`
	FxRate           string `json:"fxRate"`
	Fees             string `json:"fees"`
	ValidUntil       string `json:"validUntil"`
}

type notReadyBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type committedBody struct {
	CreditedAt       string `json:"creditedAt"`
	CreditedAmount   string `json:"creditedAmount"`
	CreditedCurrency string `json:"creditedCurrency"`
}

type statusBody struct {
	Role          string `json:"role"`
	Status        string `json:"status"`
	FinalAmount   string `json:"finalAmount,omitempty"`
	FinalCurrency string `json:"finalCurrency,omitempty"`
	UpdatedAt     string `json:"updatedAt"`
}

func writeEnvelope(w http.ResponseWriter, status int, txID, action, sender, receiver string, body []byte) {
	out := envelope{
		TransactionID: txID, Action: action,
		SenderBankCode: sender, ReceiverBankCode: receiver,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Body:      body,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

// hijackAndClose closes the connection mid-response without writing
// anything, simulating a peer that drops the TCP connection silently.
func hijackAndClose(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		// Best-effort fallback when hijacking is unavailable.
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

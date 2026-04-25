package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exbanka/transaction-service/internal/messaging"
)

// TestPeerBankClient_SendPrepare_SignsAndParsesReady — the client signs the
// outbound body with the configured outbound key and correctly parses a
// Ready response.
func TestPeerBankClient_SendPrepare_SignsAndParsesReady(t *testing.T) {
	const outboundKey = "test-outbound-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify HMAC signature against the body bytes the client sent.
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(outboundKey))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-Bank-Signature"); got != want {
			t.Fatalf("X-Bank-Signature mismatch: got %s want %s", got, want)
		}
		if r.Header.Get("X-Bank-Code") != "111" {
			t.Errorf("X-Bank-Code = %q, want %q", r.Header.Get("X-Bank-Code"), "111")
		}
		if r.Header.Get("X-Idempotency-Key") == "" {
			t.Error("X-Idempotency-Key missing")
		}
		// Reply with a Ready envelope.
		readyBody, _ := json.Marshal(messaging.ReadyBody{
			Status:           "Ready",
			OriginalAmount:   "1000.00",
			OriginalCurrency: "RSD",
			FinalAmount:      "8.50",
			FinalCurrency:    "EUR",
			FxRate:           "117.65",
			Fees:             "0.00",
			ValidUntil:       "2026-04-25T14:00:00Z",
		})
		resp := messaging.Envelope{
			TransactionID: "tx-1", Action: messaging.ActionReady,
			Timestamp: messaging.NowRFC3339(), Body: readyBody,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewPeerBankClient("111", "222", outboundKey, srv.URL, 5*time.Second)
	resp, err := c.SendPrepare(context.Background(), PrepareRequest{
		TransactionID:   "tx-1",
		SenderAccount:   "1110000000001234",
		ReceiverAccount: "2220000000005678",
		Amount:          "1000.00",
		Currency:        "RSD",
	})
	if err != nil {
		t.Fatalf("SendPrepare: %v", err)
	}
	if !resp.Ready {
		t.Errorf("Ready=false, want true")
	}
	if resp.FinalAmount != "8.50" {
		t.Errorf("FinalAmount=%q, want 8.50", resp.FinalAmount)
	}
	if resp.FinalCurrency != "EUR" {
		t.Errorf("FinalCurrency=%q, want EUR", resp.FinalCurrency)
	}
}

func TestPeerBankClient_SendPrepare_NotReady_ParsesReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(messaging.NotReadyBody{Status: "NotReady", Reason: "account_not_found"})
		_ = json.NewEncoder(w).Encode(messaging.Envelope{
			TransactionID: "tx-2", Action: messaging.ActionNotReady,
			Timestamp: messaging.NowRFC3339(), Body: body,
		})
	}))
	defer srv.Close()
	c := NewPeerBankClient("111", "222", "k", srv.URL, 5*time.Second)
	resp, err := c.SendPrepare(context.Background(), PrepareRequest{TransactionID: "tx-2", Amount: "1", Currency: "RSD"})
	if err != nil {
		t.Fatalf("SendPrepare: %v", err)
	}
	if resp.Ready {
		t.Errorf("Ready=true, want false")
	}
	if resp.Reason != "account_not_found" {
		t.Errorf("Reason=%q, want account_not_found", resp.Reason)
	}
}

func TestPeerBankClient_SendCommit_404_ReturnsErrPeerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewPeerBankClient("111", "222", "k", srv.URL, 5*time.Second)
	_, err := c.SendCommit(context.Background(), CommitOutboundRequest{TransactionID: "tx-3", FinalAmount: "1", FinalCurrency: "RSD"})
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if err != ErrPeerNotFound {
		t.Errorf("err=%v, want ErrPeerNotFound", err)
	}
}

func TestPeerBankClient_SendCommit_409_ReturnsCommitMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"reason":"commit_mismatch"}`))
	}))
	defer srv.Close()
	c := NewPeerBankClient("111", "222", "k", srv.URL, 5*time.Second)
	_, err := c.SendCommit(context.Background(), CommitOutboundRequest{TransactionID: "tx-4", FinalAmount: "1", FinalCurrency: "RSD"})
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !isCommitMismatch(err) {
		t.Errorf("err=%v, want ErrPeerCommitMismatch", err)
	}
}

func isCommitMismatch(err error) bool {
	if err == nil {
		return false
	}
	// Production code wraps the sentinel with extra context — match by
	// presence of the sentinel in the chain.
	for cur := err; cur != nil; {
		if cur == ErrPeerCommitMismatch {
			return true
		}
		u, ok := cur.(interface{ Unwrap() error })
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	return false
}

package peerbank

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// signedPost makes a Prepare/Commit/CheckStatus request with a valid HMAC
// signature so the mock accepts it.
func signedPost(t *testing.T, mb *MockPeerBank, path, txID, action, body string) (*http.Response, []byte) {
	t.Helper()
	env := envelope{
		TransactionID: txID, Action: action,
		SenderBankCode: "111", ReceiverBankCode: "222",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Body:      json.RawMessage(body),
	}
	bodyBytes, _ := json.Marshal(env)
	mac := hmac.New(sha256.New, []byte(mb.InboundKey))
	mac.Write(bodyBytes)
	sig := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, mb.Server.URL+path, bytes.NewReader(bodyBytes))
	req.Header.Set("X-Bank-Code", "111")
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Idempotency-Key", txID)
	req.Header.Set("X-Timestamp", env.Timestamp)
	req.Header.Set("X-Nonce", "n1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody
}

func TestMock_DefaultPrepare_ReturnsReady(t *testing.T) {
	mb := New(t, "k")
	resp, body := signedPost(t, mb, "/transfer/prepare", "tx-1", "Prepare", `{"amount":"1000"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"action":"Ready"`) {
		t.Errorf("expected Ready action in default prepare reply, got %s", body)
	}
}

func TestMock_ConfigureNotReady_PassesThroughReason(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigureNotReady("account_not_found")
	resp, body := signedPost(t, mb, "/transfer/prepare", "tx-2", "Prepare", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"reason":"account_not_found"`) {
		t.Errorf("expected reason account_not_found, got %s", body)
	}
	if !strings.Contains(string(body), `"action":"NotReady"`) {
		t.Errorf("expected NotReady action, got %s", body)
	}
}

func TestMock_ConfigureCommitMismatch_Returns409(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigureCommitMismatch("amounts diverge")
	resp, body := signedPost(t, mb, "/transfer/commit", "tx-3", "Commit", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "amounts diverge") {
		t.Errorf("expected mismatch reason in body, got %s", body)
	}
}

func TestMock_ConfigureFiveXX_PrepareReturns503(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigurePrepareBehavior(BehaviorFiveXX)
	resp, _ := signedPost(t, mb, "/transfer/prepare", "tx-4", "Prepare", `{}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestMock_StatusUnknown_Returns404(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigureStatusUnknown()
	resp, _ := signedPost(t, mb, "/check-status", "tx-5", "CheckStatus", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMock_StatusKnown_ReturnsState(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigureStatusKnown(StatusTerms{Role: "receiver", Status: "committed", UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	resp, body := signedPost(t, mb, "/check-status", "tx-6", "CheckStatus", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"status":"committed"`) {
		t.Errorf("expected status:committed, got %s", body)
	}
	if !strings.Contains(string(body), `"role":"receiver"`) {
		t.Errorf("expected role:receiver, got %s", body)
	}
}

func TestMock_BadSignature_Returns401(t *testing.T) {
	mb := New(t, "k")
	body := []byte(`{"transactionId":"x","action":"Prepare"}`)
	req, _ := http.NewRequest(http.MethodPost, mb.Server.URL+"/transfer/prepare", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "111")
	req.Header.Set("X-Bank-Signature", "deadbeef")
	req.Header.Set("X-Idempotency-Key", "x")
	req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Nonce", "n")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMock_RecordsRequests(t *testing.T) {
	mb := New(t, "k")
	signedPost(t, mb, "/transfer/prepare", "tx-rec", "Prepare", `{"hello":"world"}`)
	got := mb.Requests()
	if len(got) != 1 {
		t.Fatalf("expected 1 recorded request, got %d", len(got))
	}
	if got[0].Path != "/transfer/prepare" {
		t.Errorf("path: %s", got[0].Path)
	}
	if !strings.Contains(string(got[0].Body), "hello") {
		t.Errorf("body did not include payload")
	}
}

func TestMock_QueueIsFIFO(t *testing.T) {
	mb := New(t, "k")
	mb.ConfigureNotReady("first")
	mb.ConfigureReady(ReadyTerms{FinalAmount: "5", FinalCurrency: "EUR", FxRate: "1", Fees: "0"})

	_, b1 := signedPost(t, mb, "/transfer/prepare", "tx-a", "Prepare", `{}`)
	if !strings.Contains(string(b1), `"reason":"first"`) {
		t.Errorf("first call should be NotReady, got %s", b1)
	}
	_, b2 := signedPost(t, mb, "/transfer/prepare", "tx-b", "Prepare", `{}`)
	if !strings.Contains(string(b2), `"action":"Ready"`) {
		t.Errorf("second call should be Ready, got %s", b2)
	}
}

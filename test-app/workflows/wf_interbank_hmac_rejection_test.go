//go:build integration

package workflows

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestInterBank_HMAC_Rejection covers the four standard reject paths the
// HMACMiddleware enforces (Spec 3 §8.2):
//   - missing required headers → 400
//   - unknown bank code → 401
//   - bad signature → 401
//   - stale timestamp → 401
//
// We don't test nonce-replay here because the middleware uses Redis with
// a 10-minute TTL — a flaky test if Redis state isn't isolated per run.
func TestInterBank_HMAC_Rejection(t *testing.T) {
	startMockPeerOnHost(t) // wire the outbound path even though it's not exercised

	gw := cfg.GatewayURL + "/internal/inter-bank/transfer/prepare"
	body := []byte(`{"transactionId":"tx-x","action":"Prepare","senderBankCode":"222","receiverBankCode":"111","timestamp":"","body":{}}`)
	cli := &http.Client{Timeout: 5 * time.Second}

	t.Run("missing_headers", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, gw, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status %d, want 400", resp.StatusCode)
		}
		bs, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(bs), "missing_headers") {
			t.Errorf("body: %s", bs)
		}
	})

	t.Run("unknown_bank", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, gw, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bank-Code", "999")
		req.Header.Set("X-Bank-Signature", "deadbeef")
		req.Header.Set("X-Idempotency-Key", "tx-x")
		req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
		req.Header.Set("X-Nonce", randomHexNonce(t))
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", resp.StatusCode)
		}
		bs, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(bs), "unknown_bank") {
			t.Errorf("body: %s", bs)
		}
	})

	t.Run("bad_signature", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, gw, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bank-Code", PeerCode)
		req.Header.Set("X-Bank-Signature", "deadbeef")
		req.Header.Set("X-Idempotency-Key", "tx-x")
		req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
		req.Header.Set("X-Nonce", randomHexNonce(t))
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", resp.StatusCode)
		}
		bs, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(bs), "bad_signature") {
			t.Errorf("body: %s", bs)
		}
	})

	t.Run("stale_timestamp", func(t *testing.T) {
		// Even with a valid signature, a timestamp outside ±5 min is rejected.
		// We use an obviously-bad sig because the timestamp check fires
		// before signature verification.
		req, _ := http.NewRequest(http.MethodPost, gw, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bank-Code", PeerCode)
		req.Header.Set("X-Bank-Signature", "deadbeef")
		req.Header.Set("X-Idempotency-Key", "tx-x")
		req.Header.Set("X-Timestamp", time.Now().UTC().Add(-1*time.Hour).Format(time.RFC3339))
		req.Header.Set("X-Nonce", randomHexNonce(t))
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", resp.StatusCode)
		}
		bs, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(bs), "stale_timestamp") {
			t.Errorf("body: %s", bs)
		}
	})
}

func randomHexNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// keep encoding/json import so the file is self-contained.
var _ = json.Marshal

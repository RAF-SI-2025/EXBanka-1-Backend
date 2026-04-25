package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/cache"
)

// fakeNonceClaim is an in-memory drop-in for cache.NonceStore that lets the
// test exercise the middleware without spinning up Redis.
type fakeNonceClaim struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newFakeNonceStore() *cache.NonceStore { return cache.NewNonceStore(nil, 0) }

// signRequest produces the headers and body the middleware expects.
func signRequest(t *testing.T, key string, bankCode, ts, nonce, idem string, body []byte) (string, string) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil)), bankCode
}

func newRouter(resolver PeerKeyResolver, nonces *cache.NonceStore, skew time.Duration) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/internal/inter-bank/transfer/prepare", HMACMiddleware(resolver, nonces, skew), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestHMACMiddleware_ValidSignature_Passes(t *testing.T) {
	const key = "test-key"
	resolver := func(code string) (string, bool) {
		if code == "222" {
			return key, true
		}
		return "", false
	}
	r := newRouter(resolver, newFakeNonceStore(), 5*time.Minute)

	body := []byte(`{"transactionId":"tx-1","action":"Prepare"}`)
	ts := time.Now().UTC().Format(time.RFC3339)
	sig, _ := signRequest(t, key, "222", ts, "n1", "tx-1", body)

	req := httptest.NewRequest(http.MethodPost, "/internal/inter-bank/transfer/prepare", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Idempotency-Key", "tx-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", "n1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHMACMiddleware_BadSignature_Returns401(t *testing.T) {
	resolver := func(code string) (string, bool) { return "real-key", true }
	r := newRouter(resolver, newFakeNonceStore(), 5*time.Minute)

	body := []byte(`{"hi":"there"}`)
	ts := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodPost, "/internal/inter-bank/transfer/prepare", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", "deadbeef")
	req.Header.Set("X-Idempotency-Key", "tx-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", "n1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad_signature") {
		t.Errorf("expected bad_signature error code, got %s", w.Body.String())
	}
}

func TestHMACMiddleware_StaleTimestamp_Returns401(t *testing.T) {
	const key = "k"
	resolver := func(code string) (string, bool) { return key, true }
	r := newRouter(resolver, newFakeNonceStore(), 5*time.Minute)

	body := []byte(`{}`)
	staleTs := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/internal/inter-bank/transfer/prepare", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Idempotency-Key", "tx-1")
	req.Header.Set("X-Timestamp", staleTs)
	req.Header.Set("X-Nonce", "n1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "stale_timestamp") {
		t.Errorf("expected stale_timestamp, got %s", w.Body.String())
	}
}

func TestHMACMiddleware_UnknownBank_Returns401(t *testing.T) {
	resolver := func(code string) (string, bool) { return "", false }
	r := newRouter(resolver, newFakeNonceStore(), 5*time.Minute)
	body := []byte(`{}`)
	ts := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodPost, "/internal/inter-bank/transfer/prepare", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "999")
	req.Header.Set("X-Bank-Signature", "any")
	req.Header.Set("X-Idempotency-Key", "tx-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", "n1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown_bank") {
		t.Errorf("expected unknown_bank, got %s", w.Body.String())
	}
}

func TestHMACMiddleware_MissingHeaders_Returns400(t *testing.T) {
	resolver := func(code string) (string, bool) { return "k", true }
	r := newRouter(resolver, newFakeNonceStore(), 5*time.Minute)
	req := httptest.NewRequest(http.MethodPost, "/internal/inter-bank/transfer/prepare", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing_headers") {
		t.Errorf("expected missing_headers, got %s", w.Body.String())
	}
}

// keep imports happy in case we add async-context tests later.
var _ context.Context = context.Background()
var _ = errors.New
var _ = fakeNonceClaim{}

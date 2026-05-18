package middleware_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/gin-gonic/gin"
)

type stubResolver struct {
	byCode  map[string]*middleware.PeerBankRecord
	byToken map[string]*middleware.PeerBankRecord
}

func (s *stubResolver) ResolveByBankCode(_ context.Context, code string) (*middleware.PeerBankRecord, bool, error) {
	r, ok := s.byCode[code]
	return r, ok, nil
}
func (s *stubResolver) ResolveByAPIToken(_ context.Context, tok string) (*middleware.PeerBankRecord, bool, error) {
	r, ok := s.byToken[tok]
	return r, ok, nil
}

type stubNonces struct{ seen map[string]bool }

func (s *stubNonces) Claim(_ context.Context, code, n string) (bool, error) {
	k := code + ":" + n
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[k] {
		return false, nil
	}
	s.seen[k] = true
	return true, nil
}

func newTestRouter(resolver middleware.PeerBankResolver, nonces middleware.PeerNonceClaimer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/probe", middleware.PeerAuth(resolver, nonces, 5*time.Minute), func(c *gin.Context) {
		code, _ := c.Get("peer_bank_code")
		c.JSON(200, gin.H{"peer_bank_code": code})
	})
	return r
}

func TestPeerAuth_APIKeyHappyPath(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", RoutingNumber: 222, APITokenPlaintext: "tok-222", Active: true}
	r := newTestRouter(&stubResolver{byToken: map[string]*middleware.PeerBankRecord{"tok-222": rec}}, &stubNonces{})

	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Api-Key", "tok-222")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}

func TestPeerAuth_APIKeyMissing(t *testing.T) {
	r := newTestRouter(&stubResolver{}, &stubNonces{})
	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerAuth_APIKeyInactiveBank(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", APITokenPlaintext: "tok-222", Active: false}
	r := newTestRouter(&stubResolver{byToken: map[string]*middleware.PeerBankRecord{"tok-222": rec}}, &stubNonces{})
	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Api-Key", "tok-222")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerAuth_HMACHappyPath(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", RoutingNumber: 222, HMACInboundKey: "shh", Active: true}
	resolver := &stubResolver{byCode: map[string]*middleware.PeerBankRecord{"222": rec}}
	nonces := &stubNonces{}
	r := newTestRouter(resolver, nonces)

	body := []byte(`{"hello":"world"}`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Nonce", "fresh-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}

func TestPeerAuth_HMACWrongSignature(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", HMACInboundKey: "shh", Active: true}
	resolver := &stubResolver{byCode: map[string]*middleware.PeerBankRecord{"222": rec}}
	r := newTestRouter(resolver, &stubNonces{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", "0000")
	req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Nonce", "n")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("status: %d", w.Code)
	}
}

func TestPeerAuth_HMACReplayedNonce(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", HMACInboundKey: "shh", Active: true}
	resolver := &stubResolver{byCode: map[string]*middleware.PeerBankRecord{"222": rec}}
	nonces := &stubNonces{}
	r := newTestRouter(resolver, nonces)

	body := []byte(`{}`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
		req.Header.Set("X-Bank-Code", "222")
		req.Header.Set("X-Bank-Signature", sig)
		req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
		req.Header.Set("X-Nonce", "n-once")
		return req
	}
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, makeReq())
	if w1.Code != 200 {
		t.Fatalf("first req: %d", w1.Code)
	}
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, makeReq())
	if w2.Code != 401 {
		t.Errorf("replay should 401, got %d", w2.Code)
	}
}

func TestPeerAuth_HMACTimestampSkew(t *testing.T) {
	rec := &middleware.PeerBankRecord{BankCode: "222", HMACInboundKey: "shh", Active: true}
	resolver := &stubResolver{byCode: map[string]*middleware.PeerBankRecord{"222": rec}}
	r := newTestRouter(resolver, &stubNonces{})

	body := []byte(`{}`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	skewed := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)

	req := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
	req.Header.Set("X-Bank-Code", "222")
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Timestamp", skewed)
	req.Header.Set("X-Nonce", "n")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401 for skew, got %d", w.Code)
	}
}

package sitxauth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/exbanka/contract/sitxauth"
)

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://example.com/interbank", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func TestSign_WithHMACKey(t *testing.T) {
	const (
		apiKey      = "test-api-key"
		hmacKey     = "super-secret-hmac-key"
		ownBankCode = "111"
	)
	body := []byte(`{"type":"NEW_TX","data":"payload"}`)

	req := newRequest(t)
	// Truncate to seconds because RFC3339 has 1-second resolution.
	before := time.Now().UTC().Truncate(time.Second)
	sitxauth.Sign(req, apiKey, hmacKey, ownBankCode, body)
	after := time.Now().UTC().Truncate(time.Second)

	// X-Api-Key must be set.
	if got := req.Header.Get("X-Api-Key"); got != apiKey {
		t.Errorf("X-Api-Key = %q; want %q", got, apiKey)
	}

	// X-Bank-Code must equal ownBankCode.
	if got := req.Header.Get("X-Bank-Code"); got != ownBankCode {
		t.Errorf("X-Bank-Code = %q; want %q", got, ownBankCode)
	}

	// X-Bank-Signature must equal independently computed HMAC-SHA256(key, body).
	mac := hmac.New(sha256.New, []byte(hmacKey))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got := req.Header.Get("X-Bank-Signature"); got != want {
		t.Errorf("X-Bank-Signature = %q; want %q", got, want)
	}

	// X-Nonce must be 32 hex chars (16 bytes → 32 hex digits).
	nonce := req.Header.Get("X-Nonce")
	if len(nonce) != 32 {
		t.Errorf("X-Nonce length = %d; want 32 (hex of 16 bytes)", len(nonce))
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		t.Errorf("X-Nonce is not valid hex: %v", err)
	}

	// X-Timestamp must parse as RFC3339 and be within the call window.
	ts := req.Header.Get("X-Timestamp")
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("X-Timestamp %q does not parse as RFC3339: %v", ts, err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("X-Timestamp %v outside expected window [%v, %v]", parsed, before, after)
	}
}

func TestSign_WithoutHMACKey(t *testing.T) {
	const apiKey = "only-api-key"
	body := []byte(`{"type":"NEW_TX"}`)

	req := newRequest(t)
	sitxauth.Sign(req, apiKey, "", "111", body)

	// X-Api-Key must be set.
	if got := req.Header.Get("X-Api-Key"); got != apiKey {
		t.Errorf("X-Api-Key = %q; want %q", got, apiKey)
	}

	// X-Bank-Code IS now sent even without HMAC — it identifies the sender so a
	// receiver can disambiguate among peers that share one api_token.
	if got := req.Header.Get("X-Bank-Code"); got != "111" {
		t.Errorf("X-Bank-Code = %q; want %q (sender identity, api-key mode)", got, "111")
	}

	// The HMAC-only headers must still be absent without an HMAC key.
	for _, h := range []string{"X-Bank-Signature", "X-Timestamp", "X-Nonce"} {
		if v := req.Header.Get(h); v != "" {
			t.Errorf("header %s = %q; want absent (no HMAC key)", h, v)
		}
	}
}

func TestSign_EmptyBody_GET(t *testing.T) {
	// Verify that a nil body and an empty []byte produce the same HMAC,
	// matching what the existing GET (CheckStatus) path does.
	const hmacKey = "key"

	reqNil := newRequest(t)
	sitxauth.Sign(reqNil, "k", hmacKey, "111", nil)

	reqEmpty := newRequest(t)
	sitxauth.Sign(reqEmpty, "k", hmacKey, "111", []byte{})

	sigNil := reqNil.Header.Get("X-Bank-Signature")
	sigEmpty := reqEmpty.Header.Get("X-Bank-Signature")
	if sigNil != sigEmpty {
		t.Errorf("nil body sig %q != empty body sig %q; want identical", sigNil, sigEmpty)
	}

	// Also cross-check against an independent computation.
	mac := hmac.New(sha256.New, []byte(hmacKey))
	mac.Write([]byte{}) // empty
	want := hex.EncodeToString(mac.Sum(nil))
	if sigNil != want {
		t.Errorf("signature = %q; want %q", sigNil, want)
	}
}

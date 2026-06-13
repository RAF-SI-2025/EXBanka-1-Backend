package sitxauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"
)

// Sign sets the SI-TX peer-auth headers on req. X-Api-Key is always set, and
// X-Bank-Code (the SENDER's own bank code) is always set too when known — so a
// receiver can disambiguate the sender even in api-key-only mode (this matters
// when several peers share one api_token, as a cohort commonly does: token-alone
// resolution would otherwise pick an arbitrary peer). When hmacOutboundKey != ""
// the HMAC bundle is added on top: X-Bank-Signature = HMAC-SHA256(hmacOutboundKey,
// body) hex-encoded, X-Timestamp (RFC3339 UTC now), X-Nonce (16 random bytes hex).
// body is the exact request body bytes (use nil/[]byte{} for GET).
func Sign(req *http.Request, apiKey, hmacOutboundKey, ownBankCode string, body []byte) {
	req.Header.Set("X-Api-Key", apiKey)
	// Identify the sender even without HMAC. Harmless when absent; the receiver
	// still verifies the api_token (and, in HMAC mode, the signature) — this only
	// names WHICH peer registration to check the secret against.
	if ownBankCode != "" {
		req.Header.Set("X-Bank-Code", ownBankCode)
	}
	if hmacOutboundKey == "" {
		return
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	mac := hmac.New(sha256.New, []byte(hmacOutboundKey))
	mac.Write(body)
	req.Header.Set("X-Bank-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Nonce", hex.EncodeToString(nonce))
}

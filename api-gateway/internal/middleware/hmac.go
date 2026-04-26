package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/cache"
)

// PeerKeyResolver returns the inbound HMAC key (plaintext) and the active
// flag for a given 3-digit bank code. Returns ("", false) for unknown peers.
type PeerKeyResolver func(bankCode string) (key string, active bool)

// HMACMiddleware verifies inbound /internal/inter-bank/* requests are
// signed by a known peer bank (Spec 3 §8). Steps:
//
//  1. Required headers present: X-Bank-Code, X-Bank-Signature,
//     X-Idempotency-Key, X-Timestamp, X-Nonce.
//  2. Bank code is registered and active.
//  3. Timestamp is within ±clockSkew of server time (default 5 minutes).
//  4. Nonce hasn't been seen in the configured window.
//  5. HMAC-SHA256(plaintext_inbound_key, body) matches X-Bank-Signature.
//
// On any failure → 400 (missing headers) or 401 (auth-related). Successful
// requests have the body re-armed on c.Request.Body so downstream handlers
// can read it as normal.
func HMACMiddleware(resolver PeerKeyResolver, nonces *cache.NonceStore, clockSkew time.Duration) gin.HandlerFunc {
	if clockSkew == 0 {
		clockSkew = 5 * time.Minute
	}
	return func(c *gin.Context) {
		bankCode := c.GetHeader("X-Bank-Code")
		signature := c.GetHeader("X-Bank-Signature")
		idemKey := c.GetHeader("X-Idempotency-Key")
		ts := c.GetHeader("X-Timestamp")
		nonce := c.GetHeader("X-Nonce")
		if bankCode == "" || signature == "" || idemKey == "" || ts == "" || nonce == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "missing_headers", "message": "missing HMAC headers"}})
			return
		}

		key, active := resolver(bankCode)
		if key == "" || !active {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unknown_bank", "message": "unknown or inactive peer bank"}})
			return
		}

		t, err := time.Parse(time.RFC3339, ts)
		if err != nil || abs(time.Since(t)) > clockSkew {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "stale_timestamp", "message": "timestamp out of range"}})
			return
		}

		if err := nonces.Claim(c.Request.Context(), bankCode, nonce); err != nil {
			if errors.Is(err, cache.ErrReplay) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "nonce_replay", "message": "nonce reused"}})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "nonce_check_failed", "message": err.Error()}})
			return
		}

		body, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(signature)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "bad_signature", "message": "HMAC mismatch"}})
			return
		}

		c.Set("inter_bank_verified", true)
		c.Set("inter_bank_sender", bankCode)
		c.Next()
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

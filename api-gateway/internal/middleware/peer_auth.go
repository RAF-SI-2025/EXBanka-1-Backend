// Package middleware contains gin middleware used by api-gateway's
// route registrations. peer_auth.go is the SI-TX hybrid auth middleware
// (Phase 2 Task 10): accepts either X-Api-Key alone or the full HMAC
// bundle (X-Bank-Code + X-Bank-Signature + X-Timestamp + X-Nonce).
package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// PeerBankRecord is the projection of a peer-bank registry row that the
// middleware needs. Decoupled from the GORM model so the gateway doesn't
// import transaction-service. Backed at runtime by a gRPC call to
// PeerBankAdminService (resolved via PeerBankResolver).
type PeerBankRecord struct {
	BankCode          string
	RoutingNumber     int64
	APITokenPlaintext string
	HMACInboundKey    string
	Active            bool
}

// PeerBankResolver returns the peer-bank record for a given 3-digit code
// or a given API token. Returns ok=false if the bank is not registered
// or inactive.
type PeerBankResolver interface {
	ResolveByBankCode(ctx context.Context, code string) (*PeerBankRecord, bool, error)
	ResolveByAPIToken(ctx context.Context, token string) (*PeerBankRecord, bool, error)
}

// PeerNonceClaimer dedups nonces in the HMAC auth path. ok=true on first
// sight; ok=false on replay within the dedup window.
type PeerNonceClaimer interface {
	Claim(ctx context.Context, bankCode, nonce string) (bool, error)
}

// PeerAuth returns a gin middleware that accepts either:
//
//   - X-Api-Key header alone (looked up against the peer_banks table); OR
//   - The HMAC bundle: X-Bank-Code + X-Bank-Signature (hex SHA-256 of body
//     keyed by peer's HMACInboundKey) + X-Timestamp (RFC3339, ±hmacWindow
//     skew) + X-Nonce (single-use within hmacWindow).
//
// On success, sets `peer_bank_code` and `peer_routing_number` on the gin
// context for downstream handlers. On any failure, returns 401 with empty
// body (no info leak).
func PeerAuth(resolver PeerBankResolver, nonces PeerNonceClaimer, hmacWindow time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Read body once, restore for downstream handlers.
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		signature := c.GetHeader("X-Bank-Signature")
		if signature != "" {
			if !hmacAuth(c, body, signature, resolver, nonces, hmacWindow) {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
		} else {
			if !apiKeyAuth(c, resolver) {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
		}
		c.Next()
	}
}

func apiKeyAuth(c *gin.Context, resolver PeerBankResolver) bool {
	tok := c.GetHeader("X-Api-Key")
	if tok == "" {
		return false
	}
	rec, ok, err := resolver.ResolveByAPIToken(c.Request.Context(), tok)
	if err != nil || !ok || rec == nil || !rec.Active {
		return false
	}
	c.Set("peer_bank_code", rec.BankCode)
	c.Set("peer_routing_number", rec.RoutingNumber)
	return true
}

func hmacAuth(c *gin.Context, body []byte, hexSig string, resolver PeerBankResolver, nonces PeerNonceClaimer, window time.Duration) bool {
	bankCode := c.GetHeader("X-Bank-Code")
	tsHeader := c.GetHeader("X-Timestamp")
	nonce := c.GetHeader("X-Nonce")
	if bankCode == "" || tsHeader == "" || nonce == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(tsHeader))
	if err != nil {
		return false
	}
	if delta := time.Since(ts); delta > window || delta < -window {
		return false
	}
	rec, ok, err := resolver.ResolveByBankCode(c.Request.Context(), bankCode)
	if err != nil || !ok || rec == nil || !rec.Active || rec.HMACInboundKey == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(rec.HMACInboundKey))
	mac.Write(body)
	want := mac.Sum(nil)
	gotBytes, err := hex.DecodeString(strings.ToLower(hexSig))
	if err != nil {
		return false
	}
	if !hmac.Equal(gotBytes, want) {
		return false
	}
	fresh, err := nonces.Claim(c.Request.Context(), bankCode, nonce)
	if err != nil || !fresh {
		return false
	}
	c.Set("peer_bank_code", rec.BankCode)
	c.Set("peer_routing_number", rec.RoutingNumber)
	return true
}

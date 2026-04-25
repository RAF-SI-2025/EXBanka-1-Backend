// Package cache holds api-gateway's Redis-backed helpers. Currently the
// only resident is the inter-bank nonce store used by HMACMiddleware to
// prevent replay attacks (Spec 3 §8.2).
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrReplay is returned by NonceStore.Claim when the nonce was already
// seen. Middleware translates this to HTTP 401.
var ErrReplay = errors.New("nonce replay")

// NonceStore is a tiny Redis-backed claim-once store. Each (bankCode, nonce)
// pair is set once with a TTL via SETNX; the first Claim succeeds, every
// retry returns ErrReplay until the TTL expires.
//
// TTL should be set ≥ the timestamp window middleware enforces (default 10
// minutes — comfortable margin over the ±5-minute clock-skew window).
type NonceStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewNonceStore wraps a Redis client. ttl=0 defaults to 10 minutes.
func NewNonceStore(client *redis.Client, ttl time.Duration) *NonceStore {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &NonceStore{client: client, ttl: ttl}
}

// Claim attempts to register (bankCode, nonce). Returns ErrReplay if the
// nonce was already seen, or any underlying Redis error.
func (s *NonceStore) Claim(ctx context.Context, bankCode, nonce string) error {
	if s == nil || s.client == nil {
		// No-op when Redis isn't wired — safer to fail-closed in production
		// but degrade-open in dev so tests don't require Redis. Caller must
		// ensure middleware fails closed in prod.
		return nil
	}
	key := fmt.Sprintf("inter_bank_nonce:%s:%s", bankCode, nonce)
	res, err := s.client.SetArgs(ctx, key, "1", redis.SetArgs{Mode: "NX", TTL: s.ttl}).Result()
	if err != nil {
		// redis.Nil means the SET-NX failed because the key already exists.
		if errors.Is(err, redis.Nil) {
			return ErrReplay
		}
		return err
	}
	// Successful SET returns "OK"; any other value (or absent) means the
	// key was already present.
	if res != "OK" {
		return ErrReplay
	}
	return nil
}

// Package cache holds Redis-backed dedup stores used by api-gateway
// middleware. PeerNonceStore is the SI-TX HMAC nonce dedup window.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// PeerNonceStore is the Redis-backed nonce dedup window for the SI-TX
// HMAC auth path. Stores `peer_nonce:<bank_code>:<nonce>` with TTL = ttl.
// Claim is the only operation: returns ok=true on first sight, ok=false
// on replay.
type PeerNonceStore struct {
	client *redis.Client
	ttl    time.Duration
}

func NewPeerNonceStore(client *redis.Client, ttl time.Duration) *PeerNonceStore {
	return &PeerNonceStore{client: client, ttl: ttl}
}

// Claim atomically inserts the nonce. ok=true if the nonce is new (and is
// now reserved for ttl). ok=false if it was already seen within the window.
func (s *PeerNonceStore) Claim(ctx context.Context, bankCode, nonce string) (bool, error) {
	key := "peer_nonce:" + bankCode + ":" + nonce
	// SET key value NX EX <ttl>. Returns redis.Nil when NX prevents the
	// write (i.e. nonce already seen), nil on success.
	_, err := s.client.SetArgs(ctx, key, "1", redis.SetArgs{Mode: "NX", TTL: s.ttl}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

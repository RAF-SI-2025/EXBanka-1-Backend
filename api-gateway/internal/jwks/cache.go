// Package jwks caches the ES256 PUBLIC signing keys fetched from auth-service
// (GetSigningKeys) so the gateway can verify access tokens locally instead of
// calling ValidateToken on every request. It refreshes on a timer and on an
// unknown-kid miss (so a key rotation is picked up without a redeploy).
package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
)

// SigningKeysClient is the narrow slice of authpb.AuthServiceClient this cache
// needs. Segregating it keeps the cache testable with a tiny stub.
type SigningKeysClient interface {
	GetSigningKeys(ctx context.Context, in *authpb.GetSigningKeysRequest, opts ...grpc.CallOption) (*authpb.GetSigningKeysResponse, error)
}

// Cache holds kid → ECDSA public key. Safe for concurrent use.
type Cache struct {
	client   SigningKeysClient
	interval time.Duration

	mu   sync.RWMutex
	keys map[string]*ecdsa.PublicKey

	refreshing sync.Mutex // serializes on-demand refreshes
}

// New builds a cache. interval is the periodic refresh cadence.
func New(client SigningKeysClient, interval time.Duration) *Cache {
	return &Cache{client: client, interval: interval, keys: map[string]*ecdsa.PublicKey{}}
}

// Start does one initial fetch (non-fatal on failure — the gateway can still
// fall back to gRPC ValidateToken) and launches the periodic refresher.
func (c *Cache) Start(ctx context.Context) {
	if err := c.Refresh(ctx); err != nil {
		log.Printf("warn: initial JWKS fetch failed (%v) — gateway will fall back to gRPC token validation until keys load", err)
	}
	go func() {
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := c.Refresh(ctx); err != nil {
					log.Printf("warn: JWKS refresh failed: %v", err)
				}
			}
		}
	}()
}

// Refresh pulls the current key set from auth-service and atomically swaps it in.
func (c *Cache) Refresh(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.client.GetSigningKeys(cctx, &authpb.GetSigningKeysRequest{})
	if err != nil {
		return err
	}
	next := make(map[string]*ecdsa.PublicKey, len(resp.Keys))
	for _, k := range resp.Keys {
		pub, perr := parseECPublicKeyPEM(k.PemPublicKey)
		if perr != nil {
			log.Printf("warn: skipping unparseable signing key kid=%s: %v", k.Kid, perr)
			continue
		}
		next[k.Kid] = pub
	}
	if len(next) == 0 {
		return errors.New("jwks: auth-service returned no usable keys")
	}
	c.mu.Lock()
	c.keys = next
	c.mu.Unlock()
	return nil
}

// PublicKey returns the key for kid. On a miss it refreshes once (the kid may
// belong to a just-rotated key) and retries. ok=false means the gateway has no
// such key (caller should fall back / reject).
func (c *Cache) PublicKey(ctx context.Context, kid string) (*ecdsa.PublicKey, bool) {
	if kid == "" {
		return nil, false
	}
	c.mu.RLock()
	pub, ok := c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return pub, true
	}
	// Unknown kid → refresh once (serialized) and retry.
	c.refreshing.Lock()
	defer c.refreshing.Unlock()
	c.mu.RLock()
	pub, ok = c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return pub, true
	}
	if err := c.Refresh(ctx); err != nil {
		return nil, false
	}
	c.mu.RLock()
	pub, ok = c.keys[kid]
	c.mu.RUnlock()
	return pub, ok
}

// HasKeys reports whether any signing keys are currently cached.
func (c *Cache) HasKeys() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.keys) > 0
}

func parseECPublicKeyPEM(pemStr string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("not an ECDSA public key")
	}
	return ec, nil
}

package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"sync"
)

// SigningKey is one ES256 (NIST P-256) keypair identified by a short kid.
// The private half signs access tokens; the public half is served to the
// gateway via GetSigningKeys so it can verify tokens locally.
type SigningKey struct {
	Kid     string
	Private *ecdsa.PrivateKey
}

// PublicKeyInfo is the wire-facing projection of a key's PUBLIC half. PEM is a
// PKIX-encoded ("PUBLIC KEY") block the gateway parses with x509.ParsePKIXPublicKey.
type PublicKeyInfo struct {
	Kid     string
	Alg     string
	PEM     string
	Primary bool
}

// KeyManager holds the active signing key plus any retired-but-still-valid keys
// (kept during a rotation overlap so tokens signed with the previous key keep
// verifying until they expire). Concurrency-safe.
type KeyManager struct {
	mu       sync.RWMutex
	current  *SigningKey
	previous []*SigningKey
}

// NewKeyManager builds a manager around an initial active key.
func NewKeyManager(current *SigningKey) *KeyManager {
	return &KeyManager{current: current}
}

// GenerateSigningKey mints a fresh ES256 key with a random kid. Used as the
// dev/local fallback when no key PEM is configured.
func GenerateSigningKey() (*SigningKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ES256 key: %w", err)
	}
	kid, err := randomKid()
	if err != nil {
		return nil, err
	}
	return &SigningKey{Kid: kid, Private: priv}, nil
}

// LoadSigningKeyFromPEM parses a PEM-encoded EC private key (SEC1 "EC PRIVATE
// KEY" or PKCS#8 "PRIVATE KEY") under the given kid. If kid is empty a random
// one is assigned.
func LoadSigningKeyFromPEM(kid, pemStr string) (*SigningKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in JWT_EC_PRIVATE_KEY")
	}
	var priv *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse EC private key: %w", err)
		}
		priv = k
	default: // "PRIVATE KEY" (PKCS#8) or anything else — try PKCS#8
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		ecKey, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("JWT_EC_PRIVATE_KEY is not an ECDSA key")
		}
		priv = ecKey
	}
	if priv.Curve != elliptic.P256() {
		return nil, fmt.Errorf("JWT signing key must be P-256 (ES256)")
	}
	if kid == "" {
		var err error
		if kid, err = randomKid(); err != nil {
			return nil, err
		}
	}
	return &SigningKey{Kid: kid, Private: priv}, nil
}

// Current returns the active signing key.
func (m *KeyManager) Current() *SigningKey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// PublicKeyByKid returns the public key for a kid (current or any retained
// previous key). ok=false when the kid is unknown.
func (m *KeyManager) PublicKeyByKid(kid string) (*ecdsa.PublicKey, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current != nil && m.current.Kid == kid {
		return &m.current.Private.PublicKey, true
	}
	for _, k := range m.previous {
		if k.Kid == kid {
			return &k.Private.PublicKey, true
		}
	}
	return nil, false
}

// JWKS returns the public half of every key (current first), for GetSigningKeys.
func (m *KeyManager) JWKS() ([]PublicKeyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PublicKeyInfo, 0, 1+len(m.previous))
	if m.current != nil {
		info, err := publicKeyInfo(m.current, true)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	for _, k := range m.previous {
		info, err := publicKeyInfo(k, false)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Rotate installs newKey as the active key, demoting the old current to the
// previous set so its tokens keep verifying during the overlap window. Callers
// are responsible for eventually dropping stale previous keys.
func (m *KeyManager) Rotate(newKey *SigningKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.previous = append(m.previous, m.current)
	}
	m.current = newKey
}

func publicKeyInfo(k *SigningKey, primary bool) (PublicKeyInfo, error) {
	der, err := x509.MarshalPKIXPublicKey(&k.Private.PublicKey)
	if err != nil {
		return PublicKeyInfo{}, fmt.Errorf("marshal public key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return PublicKeyInfo{Kid: k.Kid, Alg: "ES256", PEM: string(pemBytes), Primary: primary}, nil
}

func randomKid() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate kid: %w", err)
	}
	return hex.EncodeToString(b), nil
}

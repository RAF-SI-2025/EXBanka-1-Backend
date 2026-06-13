package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustTestKeyManager returns a KeyManager backed by a fresh throwaway ES256
// key. Each call mints a NEW key, so two managers never share a key (preserving
// cross-key rejection tests). Shared by every JWTService test in this package.
func mustTestKeyManager() *KeyManager {
	k, err := GenerateSigningKey()
	if err != nil {
		panic(err)
	}
	return NewKeyManager(k)
}

func TestKeyManager_PublicKeyByKidAndJWKS(t *testing.T) {
	key, err := GenerateSigningKey()
	require.NoError(t, err)
	km := NewKeyManager(key)

	pub, ok := km.PublicKeyByKid(key.Kid)
	require.True(t, ok)
	assert.Equal(t, &key.Private.PublicKey, pub)

	_, ok = km.PublicKeyByKid("nope")
	assert.False(t, ok)

	jwks, err := km.JWKS()
	require.NoError(t, err)
	require.Len(t, jwks, 1)
	assert.Equal(t, key.Kid, jwks[0].Kid)
	assert.Equal(t, "ES256", jwks[0].Alg)
	assert.True(t, jwks[0].Primary)
	// PEM must parse back to the same EC public key.
	block, _ := pem.Decode([]byte(jwks[0].PEM))
	require.NotNil(t, block)
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	assert.Equal(t, &key.Private.PublicKey, parsed.(*ecdsa.PublicKey))
}

func TestKeyManager_RotateKeepsPreviousValid(t *testing.T) {
	k1, _ := GenerateSigningKey()
	k2, _ := GenerateSigningKey()
	km := NewKeyManager(k1)
	km.Rotate(k2)

	assert.Equal(t, k2.Kid, km.Current().Kid)
	// Both the new and the rotated-out key still verify.
	_, ok := km.PublicKeyByKid(k1.Kid)
	assert.True(t, ok, "previous key must remain verifiable during overlap")
	_, ok = km.PublicKeyByKid(k2.Kid)
	assert.True(t, ok)

	jwks, err := km.JWKS()
	require.NoError(t, err)
	require.Len(t, jwks, 2)
	assert.True(t, jwks[0].Primary) // current first
	assert.False(t, jwks[1].Primary)
}

func TestLoadSigningKeyFromPEM_PKCS8RoundTrip(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	key, err := LoadSigningKeyFromPEM("kid-1", pemStr)
	require.NoError(t, err)
	assert.Equal(t, "kid-1", key.Kid)
	assert.Equal(t, priv.D, key.Private.D)
}

func TestLoadSigningKeyFromPEM_Rejects(t *testing.T) {
	_, err := LoadSigningKeyFromPEM("k", "not a pem")
	assert.Error(t, err)
}

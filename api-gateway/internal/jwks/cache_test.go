package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
)

type stubKeysClient struct {
	resp *authpb.GetSigningKeysResponse
	err  error
	hits int
}

func (s *stubKeysClient) GetSigningKeys(_ context.Context, _ *authpb.GetSigningKeysRequest, _ ...grpc.CallOption) (*authpb.GetSigningKeysResponse, error) {
	s.hits++
	return s.resp, s.err
}

func pubPEM(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestCache_RefreshAndPublicKey(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &stubKeysClient{resp: &authpb.GetSigningKeysResponse{Keys: []*authpb.JWK{
		{Kid: "kid-1", Alg: "ES256", PemPublicKey: pubPEM(t, &priv.PublicKey), Primary: true},
	}}}
	c := New(client, time.Minute)

	require.NoError(t, c.Refresh(context.Background()))
	require.True(t, c.HasKeys())

	pub, ok := c.PublicKey(context.Background(), "kid-1")
	require.True(t, ok)
	require.Equal(t, &priv.PublicKey, pub)
}

func TestCache_UnknownKidTriggersOneRefresh(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &stubKeysClient{resp: &authpb.GetSigningKeysResponse{Keys: []*authpb.JWK{
		{Kid: "kid-1", PemPublicKey: pubPEM(t, &priv.PublicKey), Alg: "ES256"},
	}}}
	c := New(client, time.Minute)
	require.NoError(t, c.Refresh(context.Background()))

	hitsBefore := client.hits
	_, ok := c.PublicKey(context.Background(), "missing")
	require.False(t, ok)
	require.Equal(t, hitsBefore+1, client.hits, "an unknown kid should trigger exactly one refresh")
}

func TestCache_NoKeysIsError(t *testing.T) {
	c := New(&stubKeysClient{resp: &authpb.GetSigningKeysResponse{}}, time.Minute)
	require.Error(t, c.Refresh(context.Background()))
	require.False(t, c.HasKeys())
}

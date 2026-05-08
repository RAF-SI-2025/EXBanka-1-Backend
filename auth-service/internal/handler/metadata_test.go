package handler

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// stubAddr satisfies net.Addr so we can attach a synthetic peer to a context.
type stubAddr struct{ s string }

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return a.s }

func TestExtractRequestMeta_WithMetadata(t *testing.T) {
	md := metadata.Pairs(
		"x-forwarded-for", "10.0.0.1",
		"x-user-agent", "Mozilla/5.0 Test",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	rm := extractRequestMeta(ctx)
	assert.Equal(t, "10.0.0.1", rm.IPAddress)
	assert.Equal(t, "Mozilla/5.0 Test", rm.UserAgent)
}

func TestExtractRequestMeta_CommaChainForwardedFor(t *testing.T) {
	md := metadata.Pairs(
		"x-forwarded-for", "10.0.0.1, 10.0.0.2, 10.0.0.3",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	rm := extractRequestMeta(ctx)
	assert.Equal(t, "10.0.0.1", rm.IPAddress)
}

func TestExtractRequestMeta_EmptyContext(t *testing.T) {
	rm := extractRequestMeta(context.Background())
	assert.Equal(t, "", rm.IPAddress)
	assert.Equal(t, "", rm.UserAgent)
}

// TestExtractRequestMeta_FallsBackToPeerAddr exercises the branch where
// no x-forwarded-for header is set but a gRPC peer is available.
func TestExtractRequestMeta_FallsBackToPeerAddr(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: stubAddr{s: "10.0.0.1:54321"},
	})
	rm := extractRequestMeta(ctx)
	assert.Equal(t, "10.0.0.1", rm.IPAddress, "must strip the port from the peer address")
}

// TestExtractRequestMeta_PeerAddrNoPort covers the branch where the peer
// address has no `:` separator.
func TestExtractRequestMeta_PeerAddrNoPort(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: stubAddr{s: "abc"},
	})
	rm := extractRequestMeta(ctx)
	assert.Equal(t, "abc", rm.IPAddress)
}

// TestExtractRequestMeta_NoPeerNoMetadata covers the all-empty fallback.
func TestExtractRequestMeta_NoPeerNoMetadata(t *testing.T) {
	rm := extractRequestMeta(context.Background())
	assert.Empty(t, rm.IPAddress)
}

// TestExtractRequestMeta_RealNetAddrCompiles ensures the helper handles a real
// net.Addr (TCP) returning host:port and strips the port. (Belt-and-braces
// alongside the synthetic stub above.)
func TestExtractRequestMeta_RealTCPAddr(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 9999},
	})
	rm := extractRequestMeta(ctx)
	assert.Equal(t, "192.168.1.50", rm.IPAddress)
}

func TestDetectDeviceType(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		expected  string
	}{
		{"browser", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)", "browser"},
		{"mobile android", "Mozilla/5.0 (Linux; Android 11; Pixel 5)", "mobile"},
		{"mobile iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0)", "mobile"},
		{"postman", "PostmanRuntime/7.29.0", "api"},
		{"curl", "curl/7.68.0", "api"},
		{"empty", "", "browser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectDeviceType(tt.userAgent))
		})
	}
}

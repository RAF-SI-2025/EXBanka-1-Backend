package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

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

package handler

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// requestMeta holds client info extracted from gRPC metadata.
type requestMeta struct {
	IPAddress string
	UserAgent string
}

// extractRequestMeta pulls x-forwarded-for and x-user-agent from gRPC metadata.
// Falls back to the peer address if no forwarded IP is present.
func extractRequestMeta(ctx context.Context) requestMeta {
	rm := requestMeta{}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-forwarded-for"); len(vals) > 0 {
			// Take first IP in case of comma-separated chain
			rm.IPAddress = strings.TrimSpace(strings.Split(vals[0], ",")[0])
		}
		if vals := md.Get("x-user-agent"); len(vals) > 0 {
			rm.UserAgent = vals[0]
		}
	}

	// Fallback to gRPC peer address if no forwarded IP
	if rm.IPAddress == "" {
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			addr := p.Addr.String()
			// Strip port
			if idx := strings.LastIndex(addr, ":"); idx != -1 {
				rm.IPAddress = addr[:idx]
			} else {
				rm.IPAddress = addr
			}
		}
	}

	return rm
}

// detectDeviceType infers device type from User-Agent string.
func detectDeviceType(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "mobile") || strings.Contains(ua, "android") || strings.Contains(ua, "iphone"):
		return "mobile"
	case strings.Contains(ua, "postman") || strings.Contains(ua, "curl") || strings.Contains(ua, "httpie"):
		return "api"
	default:
		return "browser"
	}
}

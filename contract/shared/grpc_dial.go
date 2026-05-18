package shared

import (
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// DialGRPC creates a gRPC client connection with retry and keepalive settings
// suitable for Kubernetes where services may start in any order.
//
// The connection is lazy — it won't block or fail if the target is not yet
// available. gRPC's built-in retry/backoff will keep trying in the background.
//
// Extra grpc.DialOption values may be passed; they are appended after the
// built-in defaults. Callers wire grpcmw.UnaryClientSagaContextInterceptor
// here so saga_id/step metadata propagates onto outgoing RPCs. The
// interceptor itself lives in contract/shared/grpcmw, which would create
// a package import cycle if referenced from this file (grpcmw → saga →
// shared) — so the wiring is done at the call site.
func DialGRPC(addr string, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{}],
				"retryPolicy": {
					"MaxAttempts": 5,
					"InitialBackoff": "0.5s",
					"MaxBackoff": "5s",
					"BackoffMultiplier": 2.0,
					"RetryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED"]
				}
			}]
		}`),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  500 * time.Millisecond,
				Multiplier: 2.0,
				MaxDelay:   10 * time.Second,
			},
			MinConnectTimeout: 5 * time.Second,
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	opts = append(opts, extra...)
	return grpc.NewClient(addr, opts...)
}

// MustDialGRPC is like DialGRPC but logs and continues on error
// (the connection will retry in the background). Extra DialOption values
// are forwarded to DialGRPC.
func MustDialGRPC(addr string, extra ...grpc.DialOption) *grpc.ClientConn {
	conn, err := DialGRPC(addr, extra...)
	if err != nil {
		log.Printf("warn: initial gRPC dial to %s failed: %v (will retry)", addr, err)
	}
	return conn
}

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
func DialGRPC(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
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
	)
}

// MustDialGRPC is like DialGRPC but logs and continues on error
// (the connection will retry in the background).
func MustDialGRPC(addr string) *grpc.ClientConn {
	conn, err := DialGRPC(addr)
	if err != nil {
		log.Printf("warn: initial gRPC dial to %s failed: %v (will retry)", addr, err)
	}
	return conn
}

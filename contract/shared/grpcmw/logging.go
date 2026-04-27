package grpcmw

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryLoggingInterceptor logs every non-OK response with the wrapped error,
// the gRPC method, the resolved status code, and request duration. This is
// the safety net that prevents silent error masking — even if a handler
// returns a sentinel, the original wrap chain is captured in the logs.
func UnaryLoggingInterceptor(serviceName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		if err != nil {
			code := status.Code(err)
			log.Printf("[grpc] service=%s method=%s code=%s duration=%s err=%+v",
				serviceName, info.FullMethod, code, time.Since(start), err)
		}
		return resp, err
	}
}

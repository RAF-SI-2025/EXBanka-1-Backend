package metrics

import (
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

// Singleton metrics instance shared by all interceptors within a process.
var grpcServerMetrics = grpcprom.NewServerMetrics(
	grpcprom.WithServerHandlingTimeHistogram(
		grpcprom.WithHistogramBuckets([]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
	),
)

func init() {
	prometheus.MustRegister(grpcServerMetrics)
}

// GRPCUnaryServerInterceptor returns a unary server interceptor that records
// Prometheus metrics for every gRPC call.
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return grpcServerMetrics.UnaryServerInterceptor()
}

// GRPCStreamServerInterceptor returns a stream server interceptor that records
// Prometheus metrics for every gRPC stream.
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	return grpcServerMetrics.StreamServerInterceptor()
}

// InitializeGRPCMetrics pre-populates gRPC metrics for all registered methods
// on the given server. Call this after all services are registered but before
// starting to serve.
func InitializeGRPCMetrics(s *grpc.Server) {
	grpcServerMetrics.InitializeMetrics(s)
}

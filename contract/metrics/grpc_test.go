package metrics

import (
	"testing"

	"google.golang.org/grpc"
)

func TestGRPCUnaryServerInterceptor_ReturnsNonNil(t *testing.T) {
	interceptor := GRPCUnaryServerInterceptor()
	if interceptor == nil {
		t.Fatal("GRPCUnaryServerInterceptor() returned nil")
	}
}

func TestGRPCStreamServerInterceptor_ReturnsNonNil(t *testing.T) {
	interceptor := GRPCStreamServerInterceptor()
	if interceptor == nil {
		t.Fatal("GRPCStreamServerInterceptor() returned nil")
	}
}

func TestInitializeGRPCMetrics_NoPanic(t *testing.T) {
	// Create a bare gRPC server with no registered services.
	s := grpc.NewServer()
	defer s.Stop()

	// InitializeGRPCMetrics should not panic even with an empty server.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InitializeGRPCMetrics panicked: %v", r)
		}
	}()
	InitializeGRPCMetrics(s)
}

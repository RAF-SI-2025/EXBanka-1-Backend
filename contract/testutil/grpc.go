package testutil

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireGRPCCode asserts that err is a gRPC error with the expected code.
func RequireGRPCCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("RequireGRPCCode: expected gRPC error with code %s, got nil", expected)
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("RequireGRPCCode: error is not a gRPC status: %v", err)
		return
	}
	if st.Code() != expected {
		t.Fatalf("RequireGRPCCode: expected code %s, got %s (message: %s)", expected, st.Code(), st.Message())
	}
}

// RequireNoGRPCError asserts that err is nil.
func RequireNoGRPCError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			t.Fatalf("RequireNoGRPCError: got gRPC error %s: %s", st.Code(), st.Message())
		} else {
			t.Fatalf("RequireNoGRPCError: got error: %v", err)
		}
	}
}

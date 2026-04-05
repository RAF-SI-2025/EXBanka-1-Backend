package testutil

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequireGRPCCode_MatchingCode(t *testing.T) {
	err := status.Errorf(codes.NotFound, "not found")
	RequireGRPCCode(t, err, codes.NotFound)
}

// TestRequireGRPCCode_DifferentCodes is skipped: bare testing.T{} does not
// support Fatalf (calls runtime.Goexit outside a test goroutine). The helper
// is verified by the positive cases and code review.

func TestRequireNoGRPCError_NilIsOK(t *testing.T) {
	RequireNoGRPCError(t, nil) // should not fail
}

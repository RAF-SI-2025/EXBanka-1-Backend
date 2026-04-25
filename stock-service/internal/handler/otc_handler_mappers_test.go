package handler

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapOTCError_NotFound(t *testing.T) {
	err := mapOTCError(errors.New("OTC offer not found"))
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestMapOTCError_PermissionDenied(t *testing.T) {
	err := mapOTCError(errors.New("cannot buy your own OTC offer"))
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", status.Code(err))
	}
}

func TestMapOTCError_FailedPrecondition(t *testing.T) {
	for _, msg := range []string{
		"insufficient public quantity for OTC purchase",
		"buyer account not found",
		"seller account not found",
	} {
		err := mapOTCError(errors.New(msg))
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%q: expected FailedPrecondition, got %v", msg, status.Code(err))
		}
	}
}

func TestMapOTCError_DefaultInternal(t *testing.T) {
	err := mapOTCError(errors.New("db connection lost"))
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

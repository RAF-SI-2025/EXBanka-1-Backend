package svcerr_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/shared/svcerr"
)

func TestSentinel_DirectStatus(t *testing.T) {
	e := svcerr.New(codes.PermissionDenied, "account locked")
	st, ok := status.FromError(e)
	if !ok {
		t.Fatal("expected status.FromError to recognize sentinel")
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("got code %v, want PermissionDenied", st.Code())
	}
	if st.Message() != "account locked" {
		t.Errorf("got message %q, want %q", st.Message(), "account locked")
	}
}

func TestSentinel_WrappedStatus(t *testing.T) {
	base := svcerr.New(codes.Unauthenticated, "invalid credentials")
	wrapped := fmt.Errorf("Login: %w", base)

	st, ok := status.FromError(wrapped)
	if !ok {
		t.Fatal("expected wrapped sentinel to satisfy status.FromError")
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("got code %v, want Unauthenticated", st.Code())
	}
}

func TestSentinel_ErrorsIs(t *testing.T) {
	a := svcerr.New(codes.NotFound, "not found")
	b := fmt.Errorf("ListThings: %w", a)
	if !errors.Is(b, a) {
		t.Error("errors.Is should match wrapped sentinel")
	}
}

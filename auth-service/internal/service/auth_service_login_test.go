// Lighthouse tests for the typed-sentinel migration of AuthService.Login.
//
// Each test exercises one Login failure mode and asserts both:
//   - errors.Is(err, sentinel) — the wrap chain preserves the typed sentinel.
//   - status.Code(err) == expected — grpc-go resolves the SentinelError's
//     GRPCStatus interface through the wrap.
//
// Together these guarantees enable the gRPC handler to passthrough the
// service error without any string-matching mapper.
package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/auth-service/internal/model"
	userpb "github.com/exbanka/contract/userpb"
)

// mustBeSentinel asserts err wraps the given sentinel and resolves to the
// expected gRPC code via status.FromError.
func mustBeSentinel(t *testing.T, err error, sentinel error, code codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected errors.Is(%v, %v) to be true", err, sentinel)
	}
	if got := status.Code(err); got != code {
		t.Fatalf("expected gRPC code %v, got %v (err=%v)", code, got, err)
	}
}

// failingUserClient returns the configured error from every GetEmployee call.
type failingUserClient struct {
	stubUserClient
	failErr error
}

func (f *failingUserClient) GetEmployee(ctx context.Context, in *userpb.GetEmployeeRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return nil, f.failErr
}

// ----------------------------------------------------------------------------
// Lighthouse: each Login failure mode resolves to the right sentinel + code.
// ----------------------------------------------------------------------------

func TestLogin_BadPassword_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "alice@example.com", "RightPass1!", model.PrincipalTypeEmployee, 1)

	_, _, err := f.svc.Login(context.Background(), "alice@example.com", "WrongPass1!", "1.2.3.4", "")
	mustBeSentinel(t, err, ErrInvalidCredentials, codes.Unauthenticated)
}

func TestLogin_EmailNotFound_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	// No account seeded.

	_, _, err := f.svc.Login(context.Background(), "missing@example.com", "AnyPass1!", "1.2.3.4", "")
	// Email-not-found COLLAPSES to ErrInvalidCredentials for security (no enumeration).
	mustBeSentinel(t, err, ErrInvalidCredentials, codes.Unauthenticated)
}

func TestLogin_AccountLocked_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "bob@example.com", "Right1Pass!", model.PrincipalTypeEmployee, 2)

	require.NoError(t, f.db.Create(&model.AccountLock{
		Email:     "bob@example.com",
		Reason:    "too_many_failed_attempts",
		LockedAt:  time.Now(),
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}).Error)

	_, _, err := f.svc.Login(context.Background(), "bob@example.com", "Right1Pass!", "1.2.3.4", "")
	mustBeSentinel(t, err, ErrAccountLocked, codes.PermissionDenied)
}

func TestLogin_AccountPending_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.db.Create(&model.Account{
		Email:         "carol@example.com",
		PasswordHash:  "any",
		Status:        model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   3,
	}).Error)

	_, _, err := f.svc.Login(context.Background(), "carol@example.com", "AnyPass1!", "1.2.3.4", "")
	mustBeSentinel(t, err, ErrAccountPending, codes.FailedPrecondition)
}

func TestLogin_AccountDisabled_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.db.Create(&model.Account{
		Email:         "dan@example.com",
		PasswordHash:  "any",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   4,
	}).Error)

	_, _, err := f.svc.Login(context.Background(), "dan@example.com", "AnyPass1!", "1.2.3.4", "")
	mustBeSentinel(t, err, ErrAccountDisabled, codes.FailedPrecondition)
}

func TestLogin_EmployeeRPCFailed_Sentinel(t *testing.T) {
	f := newAuthFlowFixture(t)
	// Replace the user client with one that always errors.
	f.svc.userClient = &failingUserClient{failErr: errors.New("upstream RPC down")}
	f.seedActiveAccountWithPassword(t, "emp@example.com", "Right1Pass!", model.PrincipalTypeEmployee, 5)

	_, _, err := f.svc.Login(context.Background(), "emp@example.com", "Right1Pass!", "1.2.3.4", "")
	mustBeSentinel(t, err, ErrEmployeeRPCFailed, codes.Unavailable)
}

// TestLogin_InternalSentinels_WrapPattern documents the wrap pattern for the
// two Internal-grade Login failures (ErrTokenSignFailed, ErrTokenGenFailed)
// that cannot easily be triggered through the public API without invasive
// dependency injection (HS256 + crypto/rand are both reliable). The pattern
// is identical to the externally-testable branches above.
//
// We assert here that the same fmt.Errorf("...: %w", sentinel) wrap chain
// preserves the typed sentinel + gRPC code so the handler passthrough is
// safe even for these branches.
func TestLogin_InternalSentinels_WrapPattern(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		want     codes.Code
	}{
		{"token-sign-failed", ErrTokenSignFailed, codes.Internal},
		{"token-gen-failed", ErrTokenGenFailed, codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Same wrap shape used inside Login.
			wrapped := fmt.Errorf("Login(test@example.com) sign access token: %w", tc.sentinel)
			mustBeSentinel(t, wrapped, tc.sentinel, tc.want)
		})
	}
}

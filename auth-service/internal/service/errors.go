// Package service: typed sentinel errors for auth-service operations.
//
// Each sentinel embeds a gRPC code (via svcerr.SentinelError), so wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no string-matching
// is required to map service errors back to gRPC status.
//
// Note: ErrInvalidCredentials is deliberately reused for both "email not
// found" and "wrong password" failures during Login. Collapsing the two into
// the same sentinel prevents email enumeration via response inspection.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrInvalidCredentials covers wrong password and unknown email — they
	// MUST resolve to the same wire status to prevent enumeration attacks.
	ErrInvalidCredentials = svcerr.New(codes.Unauthenticated, "invalid credentials")

	// ErrAccountLocked indicates the account is currently locked out due to
	// repeated failed login attempts.
	ErrAccountLocked = svcerr.New(codes.PermissionDenied, "account locked")

	// ErrAccountPending indicates the account exists but has not completed
	// activation (no password set yet).
	ErrAccountPending = svcerr.New(codes.FailedPrecondition, "account pending activation")

	// ErrAccountDisabled indicates an administrator has disabled the account.
	ErrAccountDisabled = svcerr.New(codes.FailedPrecondition, "account disabled")

	// ErrEmployeeRPCFailed indicates user-service was unreachable when
	// fetching employee profile during a Login.
	ErrEmployeeRPCFailed = svcerr.New(codes.Unavailable, "employee service unavailable")

	// ErrTokenGenFailed indicates random refresh-token generation failed.
	ErrTokenGenFailed = svcerr.New(codes.Internal, "token generation failed")

	// ErrTokenSignFailed indicates JWT signing failed.
	ErrTokenSignFailed = svcerr.New(codes.Internal, "token signing failed")
)

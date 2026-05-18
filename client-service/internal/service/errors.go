// Package service: typed sentinel errors for client-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrClientNotFound — the client does not exist.
	ErrClientNotFound = svcerr.New(codes.NotFound, "client not found")

	// ErrClientAlreadyExists — caller-supplied email/JMBG collides with an
	// existing client.
	ErrClientAlreadyExists = svcerr.New(codes.AlreadyExists, "client already exists")

	// ErrInvalidJMBG — JMBG fails validation (length / non-digit chars).
	ErrInvalidJMBG = svcerr.New(codes.InvalidArgument, "invalid JMBG")

	// ErrInvalidEmail — email fails validation (missing @ / domain).
	ErrInvalidEmail = svcerr.New(codes.InvalidArgument, "invalid email")

	// ErrInvalidCredentials — client login presented wrong credentials.
	// Note: this is a separate sentinel from auth-service's
	// ErrInvalidCredentials — different package, same semantics.
	ErrInvalidCredentials = svcerr.New(codes.Unauthenticated, "invalid credentials")

	// ErrAccountNotActivated — the client's authentication account has not
	// been activated yet (no password set).
	ErrAccountNotActivated = svcerr.New(codes.FailedPrecondition, "account not activated")

	// ErrLimitsExceedEmployee — the requested client limit exceeds the
	// employee's MaxClientDailyLimit / MaxClientMonthlyLimit.
	ErrLimitsExceedEmployee = svcerr.New(codes.FailedPrecondition, "limits exceed employee maximum")

	// ErrEmployeeLookupFailed — the user-service gRPC call to fetch the
	// employee's max-client limits failed.
	ErrEmployeeLookupFailed = svcerr.New(codes.Unavailable, "employee lookup failed")

	// ErrInvalidEmployeeLimits — the employee record returned a malformed
	// max_client_daily_limit / max_client_monthly_limit decimal.
	ErrInvalidEmployeeLimits = svcerr.New(codes.Internal, "invalid employee limits")
)

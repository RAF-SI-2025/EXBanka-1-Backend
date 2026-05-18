// Package service: typed sentinel errors for verification-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
//
// Note: shared.ErrOptimisticLock (already a typed sentinel carrying
// codes.Aborted) is reused directly for optimistic-lock returns; this
// package does not redeclare it.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrChallengeNotFound — verification challenge lookup failed.
	ErrChallengeNotFound = svcerr.New(codes.NotFound, "challenge not found")

	// ErrChallengeExpired — challenge is past its expiry timestamp.
	ErrChallengeExpired = svcerr.New(codes.FailedPrecondition, "challenge has expired")

	// ErrChallengeNotPending — challenge is no longer in the pending state
	// (verified, failed, or expired).
	ErrChallengeNotPending = svcerr.New(codes.FailedPrecondition, "challenge is not pending")

	// ErrTooManyAttempts — challenge has hit max submission attempts.
	ErrTooManyAttempts = svcerr.New(codes.FailedPrecondition, "max attempts exceeded for this challenge")

	// ErrInvalidCode — submitted code did not match.
	ErrInvalidCode = svcerr.New(codes.Unauthenticated, "invalid verification code")

	// ErrInvalidMethod — verification method is not in the supported set.
	ErrInvalidMethod = svcerr.New(codes.InvalidArgument, "invalid verification method")

	// ErrInvalidSourceService — source_service is not in the supported set.
	ErrInvalidSourceService = svcerr.New(codes.InvalidArgument, "invalid source_service")

	// ErrInvalidArguments — required argument missing or zero (user_id,
	// source_id, etc.).
	ErrInvalidArguments = svcerr.New(codes.InvalidArgument, "invalid arguments")

	// ErrMethodMismatch — operation attempted on the wrong method
	// (e.g., SubmitCode on a non-code_pull challenge).
	ErrMethodMismatch = svcerr.New(codes.FailedPrecondition, "operation not allowed for this method")

	// ErrDeviceMismatch — challenge already bound to a different device
	// than the one submitting now. Returned as FailedPrecondition (not
	// PermissionDenied) because the binding is a one-time event the
	// caller can retry from the bound device.
	ErrDeviceMismatch = svcerr.New(codes.FailedPrecondition, "challenge already bound to a different device")

	// ErrChallengeOwnership — challenge belongs to a different user.
	ErrChallengeOwnership = svcerr.New(codes.PermissionDenied, "challenge does not belong to this user")

	// ErrBiometricsDisabled — biometrics not enabled on the device.
	ErrBiometricsDisabled = svcerr.New(codes.PermissionDenied, "biometrics not enabled for this device")

	// ErrAuthServiceUnavailable — could not reach auth-service to check
	// biometrics status.
	ErrAuthServiceUnavailable = svcerr.New(codes.Unavailable, "auth service unavailable")

	// ErrChallengePersistFailed — DB error while creating or updating a
	// challenge.
	ErrChallengePersistFailed = svcerr.New(codes.Internal, "failed to persist challenge")

	// ErrChallengeDataBuild — could not assemble the challenge_data
	// payload (random failure or unsupported method).
	ErrChallengeDataBuild = svcerr.New(codes.Internal, "failed to build challenge data")
)

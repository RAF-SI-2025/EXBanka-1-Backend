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

	// --- Token-related sentinels (RefreshToken, ResetPassword, ActivateAccount, Logout) ---

	// ErrInvalidToken covers malformed/unknown opaque tokens (refresh, reset, activation).
	ErrInvalidToken = svcerr.New(codes.Unauthenticated, "invalid token")

	// ErrTokenRevoked covers refresh tokens that were explicitly revoked.
	ErrTokenRevoked = svcerr.New(codes.Unauthenticated, "token revoked")

	// ErrTokenExpired covers refresh / reset / activation token expiry.
	ErrTokenExpired = svcerr.New(codes.Unauthenticated, "token expired")

	// ErrAccountNotFound covers Account row lookup failures (by ID, email, principal).
	ErrAccountNotFound = svcerr.New(codes.NotFound, "account not found")

	// ErrPasswordsDoNotMatch covers password / confirm-password mismatch on
	// reset and activation flows.
	ErrPasswordsDoNotMatch = svcerr.New(codes.InvalidArgument, "passwords do not match")

	// ErrPasswordValidation covers password complexity rule violations.
	ErrPasswordValidation = svcerr.New(codes.InvalidArgument, "password does not meet requirements")

	// --- Mobile device sentinels ---

	// ErrDeviceNotFound covers device lookup failures (by user, by deviceID).
	ErrDeviceNotFound = svcerr.New(codes.NotFound, "device not found")

	// ErrDeviceMismatch covers a device-id presented that does not belong to
	// the authenticated user (or the refresh token).
	ErrDeviceMismatch = svcerr.New(codes.PermissionDenied, "device id mismatch")

	// ErrDeviceInactive covers attempts to use a device that is not in active status.
	ErrDeviceInactive = svcerr.New(codes.FailedPrecondition, "device is not active")

	// ErrActivationCodeNotFound covers missing activation codes.
	ErrActivationCodeNotFound = svcerr.New(codes.NotFound, "activation code not found")

	// ErrActivationCodeUsed covers replays of an already-consumed activation code.
	ErrActivationCodeUsed = svcerr.New(codes.FailedPrecondition, "activation code already used")

	// ErrActivationCodeExpired covers expired activation codes.
	ErrActivationCodeExpired = svcerr.New(codes.FailedPrecondition, "activation code expired")

	// ErrActivationCodeMaxAttempts covers exceeding the per-code attempt cap.
	ErrActivationCodeMaxAttempts = svcerr.New(codes.ResourceExhausted, "max attempts exceeded for activation code")

	// ErrActivationCodeInvalid covers a wrong (non-matching) activation code.
	ErrActivationCodeInvalid = svcerr.New(codes.InvalidArgument, "invalid activation code")

	// ErrInvalidSignature covers HMAC validation failures on mobile-signed requests.
	ErrInvalidSignature = svcerr.New(codes.Unauthenticated, "invalid request signature")

	// --- Session sentinels ---

	// ErrSessionNotFound covers session lookup failures.
	ErrSessionNotFound = svcerr.New(codes.NotFound, "session not found")

	// ErrSessionAlreadyRevoked covers double-revoke attempts.
	ErrSessionAlreadyRevoked = svcerr.New(codes.FailedPrecondition, "session already revoked")

	// ErrSessionForbidden covers attempts to revoke a session belonging to a
	// different user than the caller.
	ErrSessionForbidden = svcerr.New(codes.PermissionDenied, "session belongs to another user")

	// --- Account creation ---

	// ErrAccountCreationFailed covers internal failures while creating an Account row.
	ErrAccountCreationFailed = svcerr.New(codes.Internal, "failed to create account")
)

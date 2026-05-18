// Package service: typed sentinel errors for card-service operations.
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
	// ErrCardNotFound — the card does not exist.
	ErrCardNotFound = svcerr.New(codes.NotFound, "card not found")

	// ErrCardAlreadyApproved — operation rejected because the card is
	// already in the approved state.
	ErrCardAlreadyApproved = svcerr.New(codes.FailedPrecondition, "card already approved")

	// ErrCardAlreadyRejected — operation rejected because the card is
	// already in the rejected state.
	ErrCardAlreadyRejected = svcerr.New(codes.FailedPrecondition, "card already rejected")

	// ErrCardBlocked — operation requires an unblocked card.
	ErrCardBlocked = svcerr.New(codes.FailedPrecondition, "card is blocked")

	// ErrCardLocked — too many failed PIN attempts; card is locked.
	ErrCardLocked = svcerr.New(codes.PermissionDenied, "card locked from too many failed PIN attempts")

	// ErrInvalidPIN — PIN does not meet the 4-digit format requirement.
	ErrInvalidPIN = svcerr.New(codes.InvalidArgument, "PIN must be 4 digits")

	// ErrPINMismatch — supplied PIN does not match the stored hash.
	ErrPINMismatch = svcerr.New(codes.Unauthenticated, "PIN mismatch")

	// ErrSingleUseAlreadyUsed — a single-use virtual card has already been
	// consumed.
	ErrSingleUseAlreadyUsed = svcerr.New(codes.FailedPrecondition, "single-use virtual card already used")

	// ErrInvalidCard — card configuration fails validation (brand, owner
	// type, usage type, expiry, etc.).
	ErrInvalidCard = svcerr.New(codes.InvalidArgument, "invalid card configuration")

	// ErrCardLimitReached — issuing this card would exceed the per-account
	// or per-person card cap.
	ErrCardLimitReached = svcerr.New(codes.FailedPrecondition, "card limit reached for account/person")

	// ErrAccountInactive — owning account is not in an active state.
	ErrAccountInactive = svcerr.New(codes.FailedPrecondition, "account is not active")

	// ErrCardDeactivated — operation forbidden because the card is in
	// deactivated state.
	ErrCardDeactivated = svcerr.New(codes.FailedPrecondition, "card is deactivated")

	// ErrCardNotBlocked — unblock requested but card was not blocked.
	ErrCardNotBlocked = svcerr.New(codes.FailedPrecondition, "card is not blocked")

	// ErrCardRequestNotFound — card request lookup failed.
	ErrCardRequestNotFound = svcerr.New(codes.NotFound, "card request not found")

	// ErrCardRequestAlreadyDecided — approve/reject called on a request that
	// has already been decided.
	ErrCardRequestAlreadyDecided = svcerr.New(codes.FailedPrecondition, "card request already decided")

	// ErrInvalidBlockDuration — temporary-block duration outside the
	// allowed range (1-720 hours).
	ErrInvalidBlockDuration = svcerr.New(codes.InvalidArgument, "invalid block duration")
)

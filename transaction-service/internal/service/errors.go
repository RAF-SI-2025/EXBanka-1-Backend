// Package service: typed sentinel errors for transaction-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
//
// Note: saga-internal fmt.Errorf chains in the cross-service flow are
// deliberately left untouched — they carry compensation-routing context
// that the recovery layer relies on. Only the OUTERMOST error returned
// to handlers is wrapped here.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrTransferNotFound — transfer lookup failed.
	ErrTransferNotFound = svcerr.New(codes.NotFound, "transfer not found")

	// ErrPaymentNotFound — payment lookup failed.
	ErrPaymentNotFound = svcerr.New(codes.NotFound, "payment not found")

	// ErrInsufficientBalance — debit / reservation requires more funds than
	// the account has available.
	ErrInsufficientBalance = svcerr.New(codes.FailedPrecondition, "insufficient balance")

	// ErrAccountInactive — account exists but is not in an active state.
	ErrAccountInactive = svcerr.New(codes.FailedPrecondition, "account inactive")

	// ErrSameAccount — source and destination must differ.
	ErrSameAccount = svcerr.New(codes.InvalidArgument, "source and destination must differ")

	// ErrTransferLimitExceeded — daily/monthly spending or transfer limit
	// would be exceeded by this operation.
	ErrTransferLimitExceeded = svcerr.New(codes.ResourceExhausted, "transfer limit exceeded")

	// ErrFeeLookupFailed — fee-rules lookup failed (DB error). Unlike a
	// missing rule, a fee LOOKUP failure rejects the transaction.
	ErrFeeLookupFailed = svcerr.New(codes.Unavailable, "fee lookup failed")

	// ErrVerificationRequired — caller must complete mobile verification
	// before this operation proceeds.
	ErrVerificationRequired = svcerr.New(codes.PermissionDenied, "verification required")

	// ErrIdempotencyMissing — caller did not supply the required
	// idempotency_key header.
	ErrIdempotencyMissing = svcerr.New(codes.InvalidArgument, "idempotency_key required")

	// ErrInvalidPayment — payment payload fails validation (amount,
	// account format, etc.).
	ErrInvalidPayment = svcerr.New(codes.InvalidArgument, "invalid payment payload")

	// ErrInvalidTransfer — transfer payload fails validation.
	ErrInvalidTransfer = svcerr.New(codes.InvalidArgument, "invalid transfer payload")

	// ErrRecipientNotFound — payment recipient lookup failed.
	ErrRecipientNotFound = svcerr.New(codes.NotFound, "payment recipient not found")

	// ErrFeeNotFound — transfer fee rule not found.
	ErrFeeNotFound = svcerr.New(codes.NotFound, "fee rule not found")

	// ErrInvalidFee — fee rule payload fails validation.
	ErrInvalidFee = svcerr.New(codes.InvalidArgument, "invalid fee rule")
)

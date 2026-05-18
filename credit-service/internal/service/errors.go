// Package service: typed sentinel errors for credit-service operations.
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
	// ErrLoanNotFound — loan lookup failed.
	ErrLoanNotFound = svcerr.New(codes.NotFound, "loan not found")

	// ErrLoanRequestNotFound — loan request lookup failed.
	ErrLoanRequestNotFound = svcerr.New(codes.NotFound, "loan request not found")

	// ErrLoanRequestNotPending — approval/rejection requested on a loan
	// request that is no longer in the pending state.
	ErrLoanRequestNotPending = svcerr.New(codes.FailedPrecondition, "loan request is not pending")

	// ErrAmountExceedsApprovalLimit — loan amount exceeds the employee's
	// MaxLoanApprovalAmount limit.
	ErrAmountExceedsApprovalLimit = svcerr.New(codes.FailedPrecondition, "amount exceeds employee approval limit")

	// ErrInvalidLoanType — loan type is not one of the allowed values.
	ErrInvalidLoanType = svcerr.New(codes.InvalidArgument, "invalid loan type")

	// ErrInvalidInterestType — interest type is not one of the allowed
	// values (fixed | variable).
	ErrInvalidInterestType = svcerr.New(codes.InvalidArgument, "invalid interest type")

	// ErrInvalidAmount — loan amount must be greater than zero.
	ErrInvalidAmount = svcerr.New(codes.InvalidArgument, "loan amount must be positive")

	// ErrInvalidRepaymentPeriod — repayment period not allowed for the
	// given loan type.
	ErrInvalidRepaymentPeriod = svcerr.New(codes.InvalidArgument, "invalid repayment period for loan type")

	// ErrCurrencyMismatch — loan currency does not match account currency.
	ErrCurrencyMismatch = svcerr.New(codes.InvalidArgument, "loan currency does not match account currency")

	// ErrAccountVerificationFailed — failed to verify the borrower's
	// account through the account-service.
	ErrAccountVerificationFailed = svcerr.New(codes.Unavailable, "account verification failed")

	// ErrLoanRequestPersistFailed — DB error while saving a loan request.
	ErrLoanRequestPersistFailed = svcerr.New(codes.Internal, "failed to save loan request")

	// ErrLoanPersistFailed — DB error while creating loan or installments.
	ErrLoanPersistFailed = svcerr.New(codes.Internal, "failed to persist loan")

	// ErrInterestRateLookup — failed to determine the nominal interest
	// rate (missing tier, missing margin, or DB error).
	ErrInterestRateLookup = svcerr.New(codes.FailedPrecondition, "interest rate lookup failed")

	// ErrInterestRateTierNotFound — interest rate tier lookup failed.
	ErrInterestRateTierNotFound = svcerr.New(codes.NotFound, "interest rate tier not found")

	// ErrBankMarginNotFound — bank margin lookup failed.
	ErrBankMarginNotFound = svcerr.New(codes.NotFound, "bank margin not found")

	// ErrInvalidRateConfig — rate config validation failed (negative
	// rates, inverted ranges, etc.).
	ErrInvalidRateConfig = svcerr.New(codes.InvalidArgument, "invalid rate config")

	// ErrRateConfigPersistFailed — DB error while saving rate config.
	ErrRateConfigPersistFailed = svcerr.New(codes.Internal, "failed to persist rate config")

	// ErrInstallmentLookup — DB error while listing installments.
	ErrInstallmentLookup = svcerr.New(codes.Internal, "failed to list installments")

	// ErrLoanLookup — DB error while reading loan(s).
	ErrLoanLookup = svcerr.New(codes.Internal, "failed to read loan")
)

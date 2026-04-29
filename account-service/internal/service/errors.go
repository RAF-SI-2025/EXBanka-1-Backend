// Package service: typed sentinel errors for account-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError, so handlers become passthrough — no string-matching
// is needed to translate service errors to gRPC status.
//
// Note: optimistic-lock conflicts re-use shared.ErrOptimisticLock (which
// is itself a typed sentinel carrying codes.Aborted via GRPCStatus); we do
// NOT redeclare it here to keep one canonical concurrent-modification
// signal across services.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrAccountNotFound — the account does not exist.
	ErrAccountNotFound = svcerr.New(codes.NotFound, "account not found")

	// ErrInsufficientBalance — debit / reservation requires more funds than
	// the account has available.
	ErrInsufficientBalance = svcerr.New(codes.FailedPrecondition, "insufficient balance")

	// ErrAccountInactive — account exists but its status forbids the
	// requested operation (e.g. trying to debit a frozen account).
	ErrAccountInactive = svcerr.New(codes.FailedPrecondition, "account inactive")

	// ErrCurrencyNotFound — caller referenced a currency code not in the
	// catalog.
	ErrCurrencyNotFound = svcerr.New(codes.NotFound, "currency not found")

	// ErrCompanyNotFound — caller referenced a company that does not exist.
	ErrCompanyNotFound = svcerr.New(codes.NotFound, "company not found")

	// ErrSpendingLimitExceeded — spending would push the account beyond its
	// daily / monthly limit. Authoritative check lives in
	// repository.UpdateBalance under SELECT FOR UPDATE.
	ErrSpendingLimitExceeded = svcerr.New(codes.ResourceExhausted, "spending limit exceeded")

	// ErrLastBankAccount — bank must keep at least one account per currency
	// of (RSD or non-RSD); deletion would violate that invariant.
	ErrLastBankAccount = svcerr.New(codes.FailedPrecondition, "cannot delete last bank account of currency")

	// ErrInvalidAccount — caller-supplied account fields fail validation
	// (kind, currency, type combination).
	ErrInvalidAccount = svcerr.New(codes.InvalidArgument, "invalid account configuration")

	// ErrCompanyDuplicate — caller supplied a company registration / tax
	// number that already exists.
	ErrCompanyDuplicate = svcerr.New(codes.AlreadyExists, "company already exists")

	// ErrInvalidStatus — caller supplied an unknown account status.
	ErrInvalidStatus = svcerr.New(codes.InvalidArgument, "invalid account status")

	// ErrIdempotencyMissing — caller did not supply the required
	// idempotency_key on a saga-driven RPC. Saga steps must always carry a
	// key so retries are safe; rejecting empty keys is a fail-fast contract
	// check, mirroring transaction-service's sentinel of the same name.
	ErrIdempotencyMissing = svcerr.New(codes.InvalidArgument, "idempotency_key required")
)

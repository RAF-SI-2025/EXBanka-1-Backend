// Package service: typed sentinel errors for exchange-service operations.
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
	// ErrRateNotFound — exchange rate pair lookup returned no row.
	ErrRateNotFound = svcerr.New(codes.NotFound, "exchange rate not found")

	// ErrSameCurrency — from and to currencies must differ for conversion.
	ErrSameCurrency = svcerr.New(codes.InvalidArgument, "from and to currencies must differ")

	// ErrInvalidAmount — amount must parse to a positive decimal.
	ErrInvalidAmount = svcerr.New(codes.InvalidArgument, "amount must be positive")

	// ErrUnsupportedCurrency — currency code is not in the supported set.
	ErrUnsupportedCurrency = svcerr.New(codes.InvalidArgument, "unsupported currency code")

	// ErrRateLookupFailed — DB error while reading rate(s).
	ErrRateLookupFailed = svcerr.New(codes.Internal, "rate lookup failed")
)

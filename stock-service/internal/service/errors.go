// Package service: typed sentinel errors for stock-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
//
// Note: shared.ErrOptimisticLock (already a typed sentinel carrying
// codes.Aborted) is reused directly via shared.CheckRowsAffected; this
// package does not redeclare it.
//
// repository.ErrFundNameInUse is also a typed sentinel — passthrough wraps
// it with codes.AlreadyExists from the repository layer.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// --- Securities ---

	// ErrSecurityNotFound — generic "security record not found". Used as the
	// catch-all when a more specific lookup sentinel is not appropriate.
	ErrSecurityNotFound = svcerr.New(codes.NotFound, "security not found")

	// ErrStockNotFound — stock lookup failed.
	ErrStockNotFound = svcerr.New(codes.NotFound, "stock not found")

	// ErrFuturesNotFound — futures contract lookup failed.
	ErrFuturesNotFound = svcerr.New(codes.NotFound, "futures contract not found")

	// ErrForexPairNotFound — forex pair lookup failed.
	ErrForexPairNotFound = svcerr.New(codes.NotFound, "forex pair not found")

	// ErrOptionNotFound — option lookup failed.
	ErrOptionNotFound = svcerr.New(codes.NotFound, "option not found")

	// ErrListingNotFound — listing lookup failed (stock listing for an
	// option's underlying, etc.).
	ErrListingNotFound = svcerr.New(codes.NotFound, "listing not found")

	// ErrExchangeNotFound — stock exchange lookup failed.
	ErrExchangeNotFound = svcerr.New(codes.NotFound, "exchange not found")

	// --- Orders ---

	// ErrOrderNotFound — order lookup failed.
	ErrOrderNotFound = svcerr.New(codes.NotFound, "order not found")

	// ErrOrderOwnership — order does not belong to the calling user.
	ErrOrderOwnership = svcerr.New(codes.PermissionDenied, "order does not belong to user")

	// ErrOrderNotPending — approve/decline requested on a non-pending order.
	ErrOrderNotPending = svcerr.New(codes.FailedPrecondition, "order is not pending")

	// ErrOrderAlreadyCompleted — operation rejected because the order has
	// already been completed.
	ErrOrderAlreadyCompleted = svcerr.New(codes.FailedPrecondition, "order is already completed")

	// ErrOrderAlreadyClosed — operation rejected because the order is
	// declined or cancelled.
	ErrOrderAlreadyClosed = svcerr.New(codes.FailedPrecondition, "order is already declined/cancelled")

	// ErrOrderSettlementPassed — approval blocked because the settlement
	// date has already passed.
	ErrOrderSettlementPassed = svcerr.New(codes.FailedPrecondition, "settlement date has passed")

	// ErrOrderRequiresLimit — limit / stop_limit order missing limit_value.
	ErrOrderRequiresLimit = svcerr.New(codes.InvalidArgument, "limit_value required for limit/stop_limit orders")

	// ErrOrderRequiresStop — stop / stop_limit order missing stop_value.
	ErrOrderRequiresStop = svcerr.New(codes.InvalidArgument, "stop_value required for stop/stop_limit orders")

	// --- OTC ---

	// ErrOTCOfferNotFound — OTC offer lookup failed.
	ErrOTCOfferNotFound = svcerr.New(codes.NotFound, "OTC offer not found")

	// ErrOTCBuyOwnOffer — buyer attempted to buy their own OTC offer.
	ErrOTCBuyOwnOffer = svcerr.New(codes.PermissionDenied, "cannot buy your own OTC offer")

	// ErrOTCInsufficientPublicQuantity — OTC purchase exceeds available
	// public quantity.
	ErrOTCInsufficientPublicQuantity = svcerr.New(codes.FailedPrecondition, "insufficient public quantity for OTC purchase")

	// ErrOTCBuyerAccountNotFound — OTC buyer account lookup failed.
	ErrOTCBuyerAccountNotFound = svcerr.New(codes.FailedPrecondition, "buyer account not found")

	// ErrOTCSellerAccountNotFound — OTC seller account lookup failed.
	ErrOTCSellerAccountNotFound = svcerr.New(codes.FailedPrecondition, "seller account not found")

	// ErrOTCSellerNoAccount — seller holding has no recorded account
	// (legacy holding without account_id).
	ErrOTCSellerNoAccount = svcerr.New(codes.FailedPrecondition, "seller holding has no recorded account for OTC")

	// ErrOTCContractNotParticipant — caller is not a participant in the
	// OTC option contract.
	ErrOTCContractNotParticipant = svcerr.New(codes.PermissionDenied, "not a participant")

	// ErrOTCInsufficientShares — proposed OTC option exceeds available
	// shares of the underlying.
	ErrOTCInsufficientShares = svcerr.New(codes.FailedPrecondition, "insufficient available shares")

	// ErrOTCSettlementInvalid — settlement_date constraint violated
	// (in the past, or after underlying expiry).
	ErrOTCSettlementInvalid = svcerr.New(codes.FailedPrecondition, "invalid settlement_date")

	// ErrOTCTerminalState — operation rejected because the contract is in
	// a terminal state.
	ErrOTCTerminalState = svcerr.New(codes.FailedPrecondition, "contract in terminal state")

	// ErrOTCOnlyContractBuyer — only the contract buyer may perform this
	// operation (e.g., exercise).
	ErrOTCOnlyContractBuyer = svcerr.New(codes.PermissionDenied, "only the contract buyer can perform this operation")

	// ErrOTCAcceptOwnContract — caller attempted to accept their own
	// OTC option contract.
	ErrOTCAcceptOwnContract = svcerr.New(codes.PermissionDenied, "cannot accept your own contract")

	// ErrOTCCounterOwnContract — caller attempted to counter their own
	// OTC option contract.
	ErrOTCCounterOwnContract = svcerr.New(codes.PermissionDenied, "cannot counter your own contract")

	// --- Portfolio / holdings ---

	// ErrHoldingNotFound — holding lookup failed.
	ErrHoldingNotFound = svcerr.New(codes.NotFound, "holding not found")

	// ErrOptionHoldingNotFound — option holding lookup failed.
	ErrOptionHoldingNotFound = svcerr.New(codes.NotFound, "option holding not found")

	// ErrHoldingOwnership — holding does not belong to the calling user.
	ErrHoldingOwnership = svcerr.New(codes.PermissionDenied, "holding does not belong to user")

	// ErrHoldingNotOption — exercise requested on a non-option holding.
	ErrHoldingNotOption = svcerr.New(codes.FailedPrecondition, "holding is not an option")

	// ErrPublicOnlyStocks — only stocks can be made public for OTC trading.
	ErrPublicOnlyStocks = svcerr.New(codes.FailedPrecondition, "only stocks can be made public for OTC trading")

	// ErrInvalidPublicQuantity — make-public quantity is invalid (negative
	// or exceeds available).
	ErrInvalidPublicQuantity = svcerr.New(codes.FailedPrecondition, "invalid public quantity")

	// ErrOptionExpired — option has expired (settlement date passed).
	ErrOptionExpired = svcerr.New(codes.FailedPrecondition, "option has expired")

	// ErrCallNotInTheMoney — call option exercise rejected because it is
	// not in the money.
	ErrCallNotInTheMoney = svcerr.New(codes.FailedPrecondition, "call option is not in the money")

	// ErrPutNotInTheMoney — put option exercise rejected because it is
	// not in the money.
	ErrPutNotInTheMoney = svcerr.New(codes.FailedPrecondition, "put option is not in the money")

	// ErrInsufficientStockForPut — put option exercise lacks underlying
	// stock holdings.
	ErrInsufficientStockForPut = svcerr.New(codes.FailedPrecondition, "insufficient stock holdings to exercise put option")

	// ErrInsufficientHolding — sell order requires more shares than the
	// holding contains.
	ErrInsufficientHolding = svcerr.New(codes.FailedPrecondition, "insufficient holding quantity for sell")

	// --- Investment funds ---

	// ErrFundNotFound — fund lookup failed.
	ErrFundNotFound = svcerr.New(codes.NotFound, "fund not found")

	// ErrFundNotManager — caller is not the fund manager.
	ErrFundNotManager = svcerr.New(codes.InvalidArgument, "caller is not the fund manager")

	// ErrFundInactive — operation rejected because the fund is inactive.
	ErrFundInactive = svcerr.New(codes.InvalidArgument, "fund is inactive")

	// ErrFundContributionBelowMin — contribution amount below the fund's
	// minimum.
	ErrFundContributionBelowMin = svcerr.New(codes.FailedPrecondition, "contribution below fund minimum")

	// ErrFundExceedsPosition — redemption exceeds the contributor's position.
	ErrFundExceedsPosition = svcerr.New(codes.InvalidArgument, "redemption exceeds position")

	// ErrFundInvalidInput — fund payload validation failed (name length,
	// negative minimum, etc.).
	ErrFundInvalidInput = svcerr.New(codes.InvalidArgument, "invalid fund input")

	// ErrFundSagaUnavailable — fund Invest/Redeem saga dependencies not
	// wired (legacy path).
	ErrFundSagaUnavailable = svcerr.New(codes.Unavailable, "fund saga dependencies not wired")

	// ErrFundHoldingRepoMissing — on-behalf-of-fund order fill blocked
	// because the fund holding repository is not wired.
	ErrFundHoldingRepoMissing = svcerr.New(codes.Unavailable, "fund holding repository not wired")

	// --- Sagas / cross-cutting ---

	// ErrSagaInflight — another saga is already in flight for this resource
	// (concurrent placement / accept / exercise).
	ErrSagaInflight = svcerr.New(codes.FailedPrecondition, "another saga is in flight for this resource")

	// ErrActuaryLimitExceeded — actuary's per-trade or daily limit would be
	// exceeded by this order.
	ErrActuaryLimitExceeded = svcerr.New(codes.PermissionDenied, "actuary limit exceeded")

	// --- Generic catch-alls (used by handlers when wrapping bare
	// dependency errors before they become gRPC responses) ---

	// ErrInternal — generic non-classified internal error. Prefer a more
	// specific sentinel where possible.
	ErrInternal = svcerr.New(codes.Internal, "internal error")
)

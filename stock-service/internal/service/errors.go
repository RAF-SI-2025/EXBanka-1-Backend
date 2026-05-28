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

	// ErrIdempotencyMissing — caller did not supply the required
	// idempotency_key on a saga-driven RPC. Mirrors the same sentinel in
	// account-service and transaction-service.
	ErrIdempotencyMissing = svcerr.New(codes.InvalidArgument, "idempotency_key required")

	// --- OTC Stocks Marketplace (Phase 3 — buy + sell direction) ---

	// ErrOTCStockBuyOfferNotFound — buy-side offer lookup failed.
	ErrOTCStockBuyOfferNotFound = svcerr.New(codes.NotFound, "OTC stock buy offer not found")

	// ErrOTCStockBuyOfferNotActive — caller targeted a buy offer that is
	// no longer fillable (already filled / cancelled / expired).
	ErrOTCStockBuyOfferNotActive = svcerr.New(codes.FailedPrecondition, "OTC stock buy offer is not active")

	// ErrOTCStockBuyOfferOwnership — caller tried to cancel a buy offer
	// they do not own.
	ErrOTCStockBuyOfferOwnership = svcerr.New(codes.PermissionDenied, "OTC stock buy offer does not belong to caller")

	// ErrOTCStockInsufficientRemainingQty — fill attempt exceeds the
	// remaining_quantity on the buy offer.
	ErrOTCStockInsufficientRemainingQty = svcerr.New(codes.FailedPrecondition, "insufficient remaining quantity on buy offer")

	// ErrOTCStockNoActiveSellOffer — cancel-sell-offer called on a
	// holding with public_quantity == 0.
	ErrOTCStockNoActiveSellOffer = svcerr.New(codes.FailedPrecondition, "holding has no active sell offer to cancel")

	// ErrOTCStockDirectionRequired — POST /me/otc/stocks body must carry
	// direction in {sell, buy}.
	ErrOTCStockDirectionRequired = svcerr.New(codes.InvalidArgument, "direction must be sell or buy")

	// ErrOTCStockCurrencyMismatch — buy offer's listing currency does not
	// match the buyer's account currency. Required at create time so the
	// reserved amount = quantity * price_per_unit is deterministic.
	ErrOTCStockCurrencyMismatch = svcerr.New(codes.FailedPrecondition, "listing currency must match buyer account currency")

	// ErrOTCStockInsufficientShares — sell-offer create would exceed the
	// holding's OTC-safe available quantity (Quantity - ReservedQuantity -
	// PublicQuantity), which protects shares already committed to orders
	// or other sell offers.
	ErrOTCStockInsufficientShares = svcerr.New(codes.FailedPrecondition, "insufficient available shares for OTC sell offer")

	// ErrOTCStockSellOfferHoldingType — sell offer can only be created on
	// a security_type='stock' holding (no futures/forex/options).
	ErrOTCStockSellOfferHoldingType = svcerr.New(codes.FailedPrecondition, "sell offer only supported on stock holdings")

	// ErrOTCStockSellPriceRequired — Phase 11: seller must supply a
	// positive price_per_unit when creating an OTC sell offer.
	// Without it the cache + peer /public-stock endpoints can't show
	// an asking price.
	ErrOTCStockSellPriceRequired = svcerr.New(codes.InvalidArgument, "price_per_unit is required and must be > 0 for sell direction")

	// --- OTC Negotiation (Phase 2 — parallel chains + first-accept-wins) ---

	// ErrOTCNegotiationNotFound — negotiation lookup failed.
	ErrOTCNegotiationNotFound = svcerr.New(codes.NotFound, "OTC negotiation not found")

	// ErrOTCNegotiationTerminal — caller attempted to mutate a chain
	// that's already in a terminal status (accepted/rejected/cancelled/
	// expired).
	ErrOTCNegotiationTerminal = svcerr.New(codes.FailedPrecondition, "OTC negotiation is in terminal status")

	// ErrOTCParentNotOpen — bidder/accepter targeted an OTCOffer listing
	// that is no longer accepting negotiations (already consumed,
	// cancelled, or expired). Occurs when a parallel chain wins the
	// first-accept race.
	ErrOTCParentNotOpen = svcerr.New(codes.FailedPrecondition, "OTC listing is no longer open")

	// ErrOTCChainAlreadyExists — bidder already has an open chain against
	// this listing. The one-chain-per-bidder invariant prevents a single
	// user from opening multiple parallel chains on the same offer.
	ErrOTCChainAlreadyExists = svcerr.New(codes.AlreadyExists, "negotiation chain already open for this bidder/listing")

	// ErrOTCAcceptUnauthorized — only the party OPPOSITE to the most
	// recent mover may accept. The caller proposed the current terms or
	// is otherwise not authorized to accept them.
	ErrOTCAcceptUnauthorized = svcerr.New(codes.PermissionDenied, "caller cannot accept current terms")

	// ErrOTCCounterUnauthorized — caller is neither the bidder nor the
	// listing's poster, so cannot propose a counter.
	ErrOTCCounterUnauthorized = svcerr.New(codes.PermissionDenied, "caller cannot counter this chain")

	// ErrOTCBidOwnListing — bidder attempted to open a chain against
	// their OWN listing.
	ErrOTCBidOwnListing = svcerr.New(codes.FailedPrecondition, "cannot open negotiation on own listing")

	// ErrOTCAcceptorAccountRequired — AcceptNegotiation needs the
	// acceptor's account_id to bind the premium-payment side of the
	// minted contract (Phase 9). Without it the contract-formation
	// saga can't run, so we mark the negotiation as failed and return
	// this typed sentinel.
	ErrOTCAcceptorAccountRequired = svcerr.New(codes.InvalidArgument, "acceptor_account_id is required to mint the option contract")

	// ErrOTCCancelListingUnauthorized — caller tried to cancel a parent
	// OTCOffer listing they didn't post. Only the initiator may cancel.
	ErrOTCCancelListingUnauthorized = svcerr.New(codes.PermissionDenied, "only the listing's poster can cancel it")

	// ErrOTCListingNotOpen — caller tried to cancel a listing that is no
	// longer in an open status (e.g. already consumed, expired, or cancelled).
	ErrOTCListingNotOpen = svcerr.New(codes.FailedPrecondition, "OTC listing is not open")

	// ErrOTCRevisionsUnauthorized — caller is neither the bidder nor the
	// parent listing's poster so may not view the revision chain.
	ErrOTCRevisionsUnauthorized = svcerr.New(codes.PermissionDenied, "only the negotiation's participants may view its revision history")

	// --- Generic catch-alls (used by handlers when wrapping bare
	// dependency errors before they become gRPC responses) ---

	// ErrInternal — generic non-classified internal error. Prefer a more
	// specific sentinel where possible.
	ErrInternal = svcerr.New(codes.Internal, "internal error")
)

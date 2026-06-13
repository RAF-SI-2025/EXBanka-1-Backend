// Package service — MintContractFromAcceptedNegotiation is the contract-
// formation primitive used by OTCNegotiationService.AcceptNegotiation
// (Phase 9). It mirrors the existing legacy OTCOfferService.Accept saga
// but is driven by an OTCNegotiation snapshot (qty/strike/premium/
// settlement) rather than the parent OTCOffer's posted terms — the two
// can diverge after counter-offers in the parallel-chains model.
//
// Safety guarantees enforced (the user's "cannot sell what you don't
// have / cannot buy if you don't have money" invariant):
//
//   - Step 1 reserve_and_contract: ReserveForOTCContract on the
//     seller's holding atomically — if the seller no longer has the
//     shares, the entire saga aborts and the contract row is deleted
//     in the Backward path.
//   - Step 2 reserve_premium: ReserveFunds on the buyer's account
//     atomically — if the buyer's balance has dropped below the
//     premium, the saga aborts and the seller's share reservation
//     is released.
//   - Step 3 settle_premium_buyer: PartialSettleReservation only
//     succeeds against an existing reservation; can't double-charge.
//   - Step 4 credit_premium_seller: idempotent CreditAccount.
//
// Failure of any step triggers compensation in reverse order. The
// caller (OTCNegotiationService) is responsible for marking the
// negotiation's status (typically to "failed") when this returns an
// error so the front-end sees a coherent state.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
)

// MintFromNegotiationInput drives MintContractFromAcceptedNegotiation.
type MintFromNegotiationInput struct {
	Parent             *model.OTCOffer
	Negotiation        *model.OTCNegotiation
	AcceptorOwnerType  model.OwnerType
	AcceptorOwnerID    *uint64
	AcceptorAccountID  uint64
	ActorPrincipalType string
	ActorPrincipalID   uint64
	// OnBehalfOfFundID, when non-zero, tags the minted contract so that on
	// exercise the acquired shares land in fund_holdings. The fund manager
	// enforcement and account-ID match are verified by the caller before
	// this input is constructed.
	OnBehalfOfFundID uint64
}

// MintContractFromAcceptedNegotiation runs the contract-formation saga
// against a negotiation that has already been state-flipped to
// "accepted" by OTCNegotiationService.AcceptNegotiation. Returns the
// minted OptionContract on success; the caller is responsible for
// any negotiation-status compensation on error.
func (s *OTCOfferService) MintContractFromAcceptedNegotiation(ctx context.Context, in MintFromNegotiationInput) (*model.OptionContract, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil {
		return nil, errOTCSagaDepsNotWired
	}
	parent := in.Parent
	neg := in.Negotiation
	if parent == nil || neg == nil {
		return nil, svcerr.New(codes.Internal, "parent and negotiation are required")
	}
	// Settlement must still be in the future. If the parent has aged
	// out between create and accept, refuse to mint.
	if !neg.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, ErrOTCSettlementNotFuture
	}

	// Resolve buyer/seller from parent.Direction. The PARENT POSTER
	// is the side they declared at create (seller for sell_initiated,
	// buyer for buy_initiated); the BIDDER (chain opener) takes the
	// opposite side; the ACCEPTOR is whoever ran AcceptNegotiation —
	// could be either the bidder OR the parent poster.
	posterOwnerType := parent.InitiatorOwnerType
	posterOwnerID := parent.InitiatorOwnerID
	posterAccountID := parent.InitiatorAccountID
	bidderOwnerType := neg.BidderOwnerType
	bidderOwnerID := neg.BidderOwnerID
	bidderAccountID := neg.BidderAccountID

	var (
		buyerOwnerType, sellerOwnerType model.OwnerType
		buyerOwnerID, sellerOwnerID     *uint64
		buyerAccountID, sellerAccountID uint64
	)
	if parent.Direction == model.OTCDirectionSellInitiated {
		// Poster is selling; bidder is buying.
		sellerOwnerType, sellerOwnerID, sellerAccountID = posterOwnerType, posterOwnerID, posterAccountID
		buyerOwnerType, buyerOwnerID, buyerAccountID = bidderOwnerType, bidderOwnerID, bidderAccountID
	} else {
		// Poster is buying; bidder is selling.
		buyerOwnerType, buyerOwnerID, buyerAccountID = posterOwnerType, posterOwnerID, posterAccountID
		sellerOwnerType, sellerOwnerID, sellerAccountID = bidderOwnerType, bidderOwnerID, bidderAccountID
	}
	// The acceptor's account overrides whichever side they're on. The
	// accept TX already verified the acceptor is one of the two
	// parties; here we just bind the acceptor's chosen account to
	// their side.
	if ownerMatches(buyerOwnerType, buyerOwnerID, in.AcceptorOwnerType, in.AcceptorOwnerID) {
		buyerAccountID = in.AcceptorAccountID
	} else {
		sellerAccountID = in.AcceptorAccountID
	}
	if buyerAccountID == 0 || sellerAccountID == 0 {
		return nil, ErrOTCAccountsNotBound
	}

	// Seller's account currency denominates the premium; capture it on the
	// contract (PremiumCurrency/StrikeCurrency). buildAcceptSaga re-fetches both
	// accounts and re-derives the FX/buyer-side amounts from the contract, so the
	// saga can be rebuilt identically on crash recovery.
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: sellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
	premiumCcy := sellerAcct.CurrencyCode

	sagaID := uuid.NewString()

	var onBehalfFundPtr *uint64
	if in.OnBehalfOfFundID != 0 {
		fid := in.OnBehalfOfFundID
		onBehalfFundPtr = &fid
	}

	parentOfferID := parent.ID
	contract := &model.OptionContract{
		OfferID:          &parentOfferID,
		BuyerOwnerType:   buyerOwnerType,
		BuyerOwnerID:     buyerOwnerID,
		SellerOwnerType:  sellerOwnerType,
		SellerOwnerID:    sellerOwnerID,
		StockID:          parent.StockID,
		Ticker:           parent.Ticker,
		Quantity:         neg.Quantity,
		StrikePrice:      neg.StrikePrice,
		PremiumPaid:      neg.Premium,
		PremiumCurrency:  premiumCcy,
		StrikeCurrency:   premiumCcy,
		SettlementDate:   neg.SettlementDate,
		Status:           model.OptionContractStatusActive,
		SagaID:           sagaID,
		PremiumPaidAt:    time.Now().UTC(),
		BuyerAccountID:   buyerAccountID,
		SellerAccountID:  sellerAccountID,
		OnBehalfOfFundID: onBehalfFundPtr, // E2: nil for personal, non-nil for fund
	}

	sg, state, err := s.buildAcceptSaga(ctx, sagaID, contract)
	if err != nil {
		return nil, err
	}
	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

	// Post-saga: publish Kafka + in-app notifications. Best-effort —
	// money already moved.
	payload := kafkamsg.OTCContractCreatedMessage{
		MessageID:      uuid.NewString(),
		OccurredAt:     time.Now().UTC().Format(time.RFC3339),
		ContractID:     contract.ID,
		OfferID:        parent.ID,
		Buyer:          kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID},
		Seller:         kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID},
		Quantity:       contract.Quantity.String(),
		StrikePrice:    contract.StrikePrice.String(),
		PremiumPaid:    contract.PremiumPaid.String(),
		SettlementDate: contract.SettlementDate.Format("2006-01-02"),
		PremiumPaidAt:  contract.PremiumPaidAt.Format(time.RFC3339),
	}
	if data, err := json.Marshal(payload); err == nil {
		s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractCreated, data, sagaID)
	} else {
		log.Printf("WARN: OTC accept(neg=%d) marshal kafka: %v", neg.ID, err)
	}
	ccData := map[string]string{
		"ticker":       contract.Ticker,
		"quantity":     contract.Quantity.String(),
		"strike_price": contract.StrikePrice.String(),
		"premium_paid": contract.PremiumPaid.String(),
	}
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)

	// Partial-accept re-listing: the accepted negotiation consumed the WHOLE
	// listing (it is immutable), but the formed contract only took neg.Quantity.
	// Re-advertise the unsold remainder (parent.Quantity - neg.Quantity) as a
	// FRESH open listing so it keeps trading as a NEW negotiation surface — a bid
	// on it starts a new chain, not a continuation of the consumed listing.
	// Best-effort, after the contract formed.
	s.relistAcceptRemainder(ctx, parent, neg.Quantity)

	return contract, nil
}

// relistAcceptRemainder re-advertises the seller's unsold quantity after a
// PARTIAL accept. The accepted negotiation consumed the original (immutable)
// listing for its WHOLE quantity, but the formed contract only took `accepted`
// units — the seller still holds (parent.Quantity - accepted) free shares. Those
// are re-listed as a brand-new OPEN offer (fresh id, fresh negotiation chains)
// so the remainder keeps trading instead of vanishing. Best-effort: a failure
// logs and leaves the remainder unlisted (the seller can re-post manually).
// No-op when the listing was fully taken (remainder <= 0) or the parent is a
// remote mirror (only the listing's host bank re-lists its own inventory).
func (s *OTCOfferService) relistAcceptRemainder(ctx context.Context, parent *model.OTCOffer, accepted decimal.Decimal) {
	_ = ctx
	if parent == nil || !parent.Local {
		return
	}
	remainder := parent.Quantity.Sub(accepted)
	if !remainder.IsPositive() {
		return
	}
	fresh := &model.OTCOffer{
		InitiatorOwnerType:          parent.InitiatorOwnerType,
		InitiatorOwnerID:            parent.InitiatorOwnerID,
		CounterpartyOwnerType:       parent.CounterpartyOwnerType,
		CounterpartyOwnerID:         parent.CounterpartyOwnerID,
		Direction:                   parent.Direction,
		StockID:                     parent.StockID,
		Ticker:                      parent.Ticker,
		Quantity:                    remainder,
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: parent.LastModifiedByPrincipalType,
		LastModifiedByPrincipalID:   parent.LastModifiedByPrincipalID,
		InitiatorAccountID:          parent.InitiatorAccountID,
		ActingEmployeeID:            parent.ActingEmployeeID,
		Public:                      parent.Public,
		Private:                     parent.Private,
		PrivateToBankCode:           parent.PrivateToBankCode,
	}
	if err := s.offers.Create(fresh); err != nil {
		log.Printf("WARN: OTC partial-accept relist (ticker=%s remainder=%s) failed: %v", parent.Ticker, remainder, err)
		return
	}
	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID:                 fresh.ID,
		RevisionNumber:          1,
		Quantity:                fresh.Quantity,
		StrikePrice:             decimal.Zero,
		Premium:                 decimal.Zero,
		SettlementDate:          time.Time{},
		ModifiedByPrincipalType: fresh.LastModifiedByPrincipalType,
		ModifiedByPrincipalID:   fresh.LastModifiedByPrincipalID,
		Action:                  model.OTCActionCreate,
	}); err != nil {
		log.Printf("WARN: OTC partial-accept relist revision (offer=%d) failed: %v", fresh.ID, err)
	}
	log.Printf("OTC partial-accept: re-listed remainder %s %s as fresh offer %d (parent %d consumed)", remainder, parent.Ticker, fresh.ID, parent.ID)
}

package handler

import (
	"context"
	"strconv"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// OfferReaderByID is the subset of *repository.OTCOfferRepository the seller-
// account resolver needs: load a local OTCOffer (the parent listing) by id.
// Declared as an interface so tests can stub it without a DB.
type OfferReaderByID interface {
	GetByID(id uint64) (*model.OTCOffer, error)
}

// DefaultSellerAccountResolver is the production SellerAccountResolver. For a
// cross-bank negotiation WE host the seller side of, it returns the seller's
// NOMINATED account number — the local parent listing's InitiatorAccountID
// resolved to its 18-digit account number — so the seller-credit legs we compose
// target that exact account (ACCOUNT{num}) instead of being resolved loosely to
// the seller's first active account in the premium currency.
//
// It returns "" (caller falls back to the participant id) whenever the
// nomination genuinely isn't available:
//   - the negotiation has no LOCAL parent listing (free-form bid, or the parent
//     lives on a peer bank — its InitiatorAccountID is not readable here);
//   - the parent listing is not sell_initiated (only the seller-as-initiator
//     binds a receive account; a buy_initiated parent's initiator is the buyer);
//   - InitiatorAccountID is unbound (0);
//   - the bound account is missing, inactive, or in a different currency than the
//     premium — pinning a wrong account would make the executor reject the leg,
//     so we prefer the lenient participant-id resolution there.
type DefaultSellerAccountResolver struct {
	offers     OfferReaderByID
	accounts   OTCAccountClient
	ownRouting int64
}

// NewSellerAccountResolver wires a DefaultSellerAccountResolver.
func NewSellerAccountResolver(offers OfferReaderByID, accounts OTCAccountClient, ownRouting int64) *DefaultSellerAccountResolver {
	return &DefaultSellerAccountResolver{offers: offers, accounts: accounts, ownRouting: ownRouting}
}

// ResolveSellerAccountNumber implements SellerAccountResolver.
func (r *DefaultSellerAccountResolver) ResolveSellerAccountNumber(ctx context.Context, neg *model.OTCNegotiation, premiumCurrency string) string {
	if r == nil || r.offers == nil || r.accounts == nil || neg == nil {
		return ""
	}
	// The parent listing must be LOCAL (we host it) for its InitiatorAccountID to
	// be readable. A nil parent (free-form negotiation) or a peer-hosted parent
	// yields no nomination.
	if neg.RemoteParentRouting == nil || neg.RemoteParentNativeID == nil {
		return ""
	}
	if *neg.RemoteParentRouting != r.ownRouting {
		return ""
	}
	offerID, perr := strconv.ParseUint(*neg.RemoteParentNativeID, 10, 64)
	if perr != nil || offerID == 0 {
		return ""
	}
	offer, oerr := r.offers.GetByID(offerID)
	if oerr != nil || offer == nil {
		return ""
	}
	// Only a sell_initiated listing binds the seller's RECEIVE account in
	// InitiatorAccountID (mirrors the local accept saga). On a buy_initiated
	// listing the initiator is the buyer; the seller's account is bound at accept
	// — not modelled cross-bank — so leave it to participant resolution.
	if offer.Direction != model.OTCDirectionSellInitiated {
		return ""
	}
	if offer.InitiatorAccountID == 0 {
		return ""
	}
	acct, aerr := r.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: offer.InitiatorAccountID})
	if aerr != nil || acct == nil {
		return ""
	}
	if acct.GetStatus() != "active" {
		return ""
	}
	if premiumCurrency != "" && acct.GetCurrencyCode() != premiumCurrency {
		return ""
	}
	if acct.GetAccountNumber() == "" {
		return ""
	}
	return acct.GetAccountNumber()
}

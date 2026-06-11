package handler

import (
	"strconv"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

// OfferTerms is the string-formatted projection of one set of option terms
// (strike / premium / settlement_date) re-sourced onto the unified offer DTO
// per viewer (D2). Decimals are StringFixed(2); the date is RFC3339 UTC — the
// same shape the option cache uses for the listing-derived fields it replaces.
type OfferTerms struct {
	StrikePrice    string
	Premium        string
	SettlementDate string
}

// OwnerLatestCounterFn returns the acting OWNER's most recent counter terms on
// a LOCAL offer (the offer-row term projection for me_owner==true rows), or nil
// when that principal never authored a revision on the offer. principalType is
// the revision-author key ("client" | "employee"); principalID is its id. Wired
// in cmd/main.go over OTCNegotiationRepository.LatestRevisionByAuthorForOffer.
type OwnerLatestCounterFn func(offerID uint64, principalType string, principalID uint64) (*OfferTerms, error)

// MyNegotiationLister fetches the AUTHENTICATED caller's own negotiation chains
// (as BIDDER) so the offer-read paths (ListUnifiedOptionOffers + GetOffer) can
// stamp my_negotiation_id / my_negotiation_status on each offer the caller is
// negotiating (SP-2b). It exposes both the LOCAL bidder chains
// (intra-bank otc_negotiations rows keyed by parent_offer_id) and the REMOTE
// ones (cross-bank rows keyed by remote parent routing+native). Satisfied by
// *repository.OTCNegotiationRepository.
type MyNegotiationLister interface {
	ListByBidder(ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int) ([]model.OTCNegotiation, int64, error)
	ListRemoteNegByClient(ownRouting int64, clientPrincipal, role string) ([]model.OTCNegotiation, error)
	// ListRemoteNegByBankParty surfaces the bank's REMOTE bidder chains (party id
	// "employee-<N>") so a bank that bid on a remote offer sees its
	// my_negotiation_id on that offer in discovery (SP-3 Task 5b). Prefix-matched
	// (the bank has no single wire principal); a CLIENT acting identity must never
	// reach it.
	ListRemoteNegByBankParty(ownRouting int64, role string) ([]model.OTCNegotiation, error)
}

// myNegStamp is the caller's resolved chain on one offer. It carries the chain
// id + status (for the my_negotiation_id/status stamp) AND that chain's CURRENT
// terms (string-formatted), so the BIDDER branch of the offer-row term
// projection can show the caller their own position without a second lookup (D2).
type myNegStamp struct {
	id     uint64
	status string
	terms  OfferTerms
}

// negStatusRank ranks a chain status for the active-chain tie-break. A LOWER
// rank wins. Non-terminal chains (the caller can still act on them) outrank
// terminal ones; among non-terminal, "accepted" (a formed/forming contract)
// is the most relevant; among terminal, all share a rank and the most-recent
// breaks the tie (handled by the caller). The vocabularies of local
// (open/countered/accepted/rejected/cancelled/expired) and remote
// (ongoing/accepted/cancelled/rejected) chains are both covered.
func negStatusRank(status string) int {
	switch status {
	case model.OTCNegotiationStatusAccepted: // "accepted" — local AND remote vocab
		return 0 // contract minted/forming — most relevant
	case model.OTCNegotiationStatusOpen, model.OTCNegotiationStatusCountered, "ongoing":
		return 1 // actionable / live (open/countered local, ongoing remote)
	default:
		return 2 // terminal (rejected/cancelled/expired)
	}
}

// pickActiveChain selects the caller's most relevant chain on a single offer
// when several exist. Tie-break (documented):
//  1. Lowest negStatusRank wins — accepted > live(open/countered/ongoing) >
//     terminal(rejected/cancelled/expired). A non-terminal chain always beats
//     a terminal one; an accepted chain (contract) beats a still-open one.
//  2. Equal rank → the most recently CREATED chain wins (CreatedAt newest).
//
// Returns the surrogate id + status of the chosen chain. With one chain it is
// trivially that chain.
func pickActiveChain(chains []*model.OTCNegotiation) myNegStamp {
	var best *model.OTCNegotiation
	for _, c := range chains {
		if best == nil {
			best = c
			continue
		}
		cr, br := negStatusRank(c.Status), negStatusRank(best.Status)
		if cr < br || (cr == br && c.CreatedAt.After(best.CreatedAt)) {
			best = c
		}
	}
	if best == nil {
		return myNegStamp{}
	}
	return myNegStamp{
		id:     best.ID,
		status: best.Status,
		terms: OfferTerms{
			StrikePrice:    best.StrikePrice.StringFixed(2),
			Premium:        best.Premium.StringFixed(2),
			SettlementDate: best.SettlementDate.UTC().Format(time.RFC3339),
		},
	}
}

// remoteParentKey is the lookup key for a REMOTE offer / remote chain's parent:
// the parent listing's (routing, native id) on the hosting peer bank.
func remoteParentKey(routing int64, native string) string {
	return strconv.FormatInt(routing, 10) + "|" + native
}

// myNegotiationIndex holds the caller's chains keyed by the offer they bid on,
// split into local (by parent_offer_id) and remote (by remote parent key).
type myNegotiationIndex struct {
	local  map[uint64]myNegStamp // parent_offer_id -> chosen chain
	remote map[string]myNegStamp // remoteParentKey  -> chosen chain
}

// localFor returns the caller's chosen chain on the local offer with the given
// surrogate id (parent_offer_id == local offer id), or false when absent.
func (idx myNegotiationIndex) localFor(parentOfferID uint64) (myNegStamp, bool) {
	if idx.local == nil {
		return myNegStamp{}, false
	}
	s, ok := idx.local[parentOfferID]
	return s, ok
}

// remoteFor returns the caller's chosen chain on the remote offer hosted at
// (routing, native) — i.e. the chain whose RemoteParentRouting/NativeID match.
func (idx myNegotiationIndex) remoteFor(routing int64, native string) (myNegStamp, bool) {
	if idx.remote == nil {
		return myNegStamp{}, false
	}
	s, ok := idx.remote[remoteParentKey(routing, native)]
	return s, ok
}

// buildMyNegotiationIndex loads the caller's bidder chains (local + remote) and
// folds them into a per-offer index, applying the active-chain tie-break when
// the caller has several chains on the same offer. The acting identity is the
// SAME plumbing me_owner uses (acting_owner_type / acting_owner_id):
//
//   - LOCAL chains: only a concrete owner (a client, or the bank) can be a
//     bidder. ListByBidder returns the caller's chains; they are keyed by
//     parent_offer_id (the local offer id the chain bids on).
//   - REMOTE chains: meaningful for both a CLIENT principal and the BANK.
//     Client chains are keyed by exact "client-<N>" wire principal via
//     ListRemoteNegByClient. Bank chains (party id "employee-<N>") are
//     prefix-matched via ListRemoteNegByBankParty (SP-3 Task 5b), restricted
//     to the BUYER role so only cross-bank bids the bank placed are indexed.
//     Keyed by (RemoteParentRouting, RemoteParentNativeID) — the peer-hosted
//     parent listing the chain bids on.
//
// lister/peerNegs may be nil; a nil source contributes no chains (the index
// stays empty for that source). actingOwnerType is "client" | "bank";
// actingOwnerID is 0 when acting as the bank.
func buildMyNegotiationIndex(
	lister MyNegotiationLister,
	actingOwnerType string,
	actingOwnerID uint64,
	ownRouting int64,
) (myNegotiationIndex, error) {
	idx := myNegotiationIndex{
		local:  map[uint64]myNegStamp{},
		remote: map[string]myNegStamp{},
	}
	if lister == nil {
		return idx, nil
	}

	ownerType, ownerID, ok := actingBidderIdentity(actingOwnerType, actingOwnerID)
	if !ok {
		return idx, nil
	}

	// LOCAL bidder chains. page_size=0 → repository default; we want ALL of the
	// caller's chains, so request a generous page. A single bidder cannot have
	// many open listings, but pull a large page to be safe.
	localRows, _, err := lister.ListByBidder(ownerType, ownerID, nil, 1, 100000)
	if err != nil {
		return idx, err
	}
	localGroups := map[uint64][]*model.OTCNegotiation{}
	for i := range localRows {
		r := &localRows[i]
		localGroups[r.ParentOfferID] = append(localGroups[r.ParentOfferID], r)
	}
	for pid, group := range localGroups {
		idx.local[pid] = pickActiveChain(group)
	}

	// REMOTE bidder chains. Both a CLIENT principal and the BANK (an employee
	// acting AS THE BANK) can have a cross-bank bidder identity:
	//
	//   - CLIENT: exact wire principal "client-<N>" via ListRemoteNegByClient.
	//   - BANK: party id "employee-<N>" with no single wire principal across
	//     chains, so prefix-matched via ListRemoteNegByBankParty (SP-3 Task 5b).
	//     This lets a bank that bid on a remote offer see its my_negotiation_id
	//     on that offer in discovery.
	//
	// Both restrict to the BIDDER (buyer) side: the my-nid feature stamps the
	// caller's chains AS BIDDER, so seller-side chains (which carry
	// RemoteParentRouting==ownRouting and can never match a peer-hosted discovery
	// offer) are excluded by the explicit "buyer" role. A client never reaches
	// the bank lister and vice versa.
	var remoteRows []model.OTCNegotiation
	var rerr error
	switch {
	case ownerType == model.OwnerClient && ownerID != nil:
		principal := "client-" + strconv.FormatUint(*ownerID, 10)
		remoteRows, rerr = lister.ListRemoteNegByClient(ownRouting, principal, "buyer")
	case ownerType == model.OwnerBank:
		remoteRows, rerr = lister.ListRemoteNegByBankParty(ownRouting, "buyer")
	}
	if rerr != nil {
		return idx, rerr
	}
	remoteGroups := map[string][]*model.OTCNegotiation{}
	for i := range remoteRows {
		r := &remoteRows[i]
		if r.RemoteParentRouting == nil || r.RemoteParentNativeID == nil || *r.RemoteParentNativeID == "" {
			continue // chain without a resolvable parent key — can't match an offer
		}
		key := remoteParentKey(*r.RemoteParentRouting, *r.RemoteParentNativeID)
		remoteGroups[key] = append(remoteGroups[key], r)
	}
	for key, group := range remoteGroups {
		idx.remote[key] = pickActiveChain(group)
	}

	return idx, nil
}

// actingBidderIdentity maps the wire acting identity to a (OwnerType, *id) pair
// usable as a bidder key, or ok=false when the identity cannot be a bidder
// (e.g. a client acting identity without an id). The bank acts with a nil id.
func actingBidderIdentity(actingOwnerType string, actingOwnerID uint64) (model.OwnerType, *uint64, bool) {
	switch actingOwnerType {
	case "bank":
		return model.OwnerBank, nil, true
	case "client":
		if actingOwnerID == 0 {
			return "", nil, false
		}
		id := actingOwnerID
		return model.OwnerClient, &id, true
	default:
		return "", nil, false
	}
}

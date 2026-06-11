// Package handler — cross-bank (REMOTE) bid dispatch for OpenNegotiation
// (Unified OTC SP-2b). When the bid route's parent :id resolves to a folded-in
// REMOTE OTCOffer (a peer-hosted listing) rather than a local one, the bid is
// dispatched to the seller's bank over SI-TX instead of running the local
// first-accept-wins saga.
//
// This composes the SI-TX OtcOffer, validates the bidder account, POSTs it to
// the peer through the PeerNegotiationDispatcher, and records the local mirror
// row. As of the 2026-06-07 cutover the dispatcher (peeregress.Dispatcher)
// routes the POST through interbank-service's ProxyToPeer egress — peer
// resolution + signing live there, not in stock-service. The gateway stays
// thin: it forwards every bid through OpenNegotiation and the dispatch happens
// here based on whether the parent listing is local or remote.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
)

// bankOwnerSentinel is the legacy owner_id stamped on bank-owned accounts that
// pre-date the account_kind="bank" flag. Mirrors the gateway's bankSentinelOwnerID
// (api-gateway/internal/handler/validation.go) and account-service's check.
const bankOwnerSentinel uint64 = 1_000_000_000

// isBankAccount reports whether an account-service AccountResponse is a
// bank-owned account, using the same predicate the gateway and account-service
// use: the account_kind == "bank" flag OR the legacy owner sentinel.
func isBankAccount(acct *accountpb.AccountResponse) bool {
	return acct.GetAccountKind() == "bank" || acct.GetOwnerId() == bankOwnerSentinel
}

// openRemoteNegotiation places a cross-bank bid against a REMOTE (peer-hosted)
// OTC option listing (SP-2b). It is invoked by OpenNegotiation when the local
// path reports the parent :id is not a local offer.
//
// The bool reports whether the :id was a remote listing this handler could
// dispatch to. false (with nil error) means "not a remote listing either" —
// the caller surfaces the original local NotFound. A non-nil error is a real
// failure (bad account, peer rejected, persistence) to surface.
//
//   - Both CLIENT and BANK bidders are supported (SP-3 Task 4). The wire buyer
//     id is "client-<ownerID>" for a client bid, or the stable
//     "employee-<actingEmployeeID>" for an employee acting AS the bank.
//   - The bidder account is validated against account-service (status active,
//     currency == the listing's premium currency — no cross-bank FX in SI-TX),
//     and its account number is threaded to the seller's bank as
//     buyerAccountNumber. The OWNERSHIP assertion branches on owner type: a
//     client bid requires owner_id == the client principal; a bank bid requires
//     a BANK account (account_kind == "bank" or the bank owner sentinel).
//   - The composed SI-TX OtcOffer mirrors CreatePeerNegotiation's wire shape;
//     parentOfferId carries the remote listing's (routing, native_id) lot key
//     so the seller's bank can cascade-cancel siblings on accept.
func (h *OTCOptionsHandler) openRemoteNegotiation(
	ctx context.Context,
	in *stockpb.OpenNegotiationRequest,
	bidderOwnerType model.OwnerType,
	bidderOwnerID *uint64,
	actingEmployeeID uint64,
	qty, strike, premium decimal.Decimal,
	settle time.Time,
) (*stockpb.OTCNegotiationResponse, bool, error) {
	if h.remoteOffers == nil || h.peerDispatch == nil || h.remoteNegWriter == nil {
		return nil, false, nil // cross-bank dispatch not wired → not a remote listing here
	}

	remoteOffer, err := h.remoteOffers.GetRemoteByID(in.GetParentOfferId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil // not a remote listing → surface the local NotFound
		}
		return nil, false, status.Errorf(codes.Internal, "remote listing lookup failed: %v", err)
	}

	// buy_initiated cross-bank is OUT OF SCOPE of the SI-TX protocol, by design.
	// The protocol's OTC discovery + negotiation model is strictly SELLER-CENTRIC:
	//   - §3 / §3.1: a bank publishes only its SELLERS' public stock (PublicStock
	//     lists `sellers`); there is no "buyer wants to acquire" listing on the wire.
	//   - §3.2: a negotiation is created by POST /negotiations sent "from a Buyer's
	//     bank to a Seller's bank" — the receiver is always the SELLER's bank.
	//   - §3.6.1: "the option pseudo-account is always in the bank of the seller".
	// So a buy_initiated offer (poster = BUYER, bidder = SELLER) has NO conformant
	// cross-bank representation: the OtcOffer wire is symmetric {buyerId, sellerId}
	// but the discovery + locality rules pin the seller to the listing-hosting bank.
	// We therefore (a) never PUBLISH buy_initiated offers to peers, and (b) drop
	// any buy_initiated offer a non-conformant peer sends at the ingest boundary
	// (otccache.buildAndMirrorRemoteOffers) — so this branch should be unreachable
	// for a folded-in remote listing. It remains as defense-in-depth: failing closed
	// is correct because this bid flow hardcodes the bidder as the BUYER, which on a
	// buy_initiated listing would invert the economic roles (verified live 2026-06-05).
	// LOCAL buy_initiated is fully supported and unaffected — it never reaches here.
	if remoteOffer.Direction == model.OTCDirectionBuyInitiated {
		return nil, false, status.Error(codes.FailedPrecondition,
			"cross-bank bidding on a buy_initiated listing is not supported: the SI-TX OTC discovery model is seller-centric (spec §3.1/§3.2/§3.6.1), so buy_initiated offers are intra-bank only")
	}

	// Freshness guard (uniform termless inventory): every remote offer is treated
	// as a /public-stock shell, so before dispatching we re-confirm the seller
	// still publicly offers this ticker. The mirror can lag a refresh cycle; this
	// avoids dispatching a doomed negotiation. This runs for EVERY remote offer —
	// there is no longer a preset-terms branch that skips it.
	//
	// Bug-2 fix: use PublicStock (not Proxy) so the egress path is exactly
	// /public-stock. Proxy hardcodes /negotiations/{rid}/{foreignID}{subpath},
	// which would build /negotiations///public-stock on the peer — a 404 on
	// every conformant base-spec implementation.
	guardBankCode := derefStr(remoteOffer.InitiatorBankCode)
	if guardBankCode == "" {
		guardBankCode = strconv.FormatInt(remoteOffer.RoutingNumber, 10)
	}
	liveBody, _, perr := h.peerDispatch.PublicStock(ctx, guardBankCode)
	if perr != nil {
		return nil, false, status.Errorf(codes.FailedPrecondition, "cannot re-validate peer stock listing: %v", perr)
	}
	if !publicStockHasSeller(liveBody, derefStr(remoteOffer.RemoteSellerID), remoteOffer.Ticker) {
		return nil, false, status.Error(codes.FailedPrecondition, "peer no longer offers this stock for OTC")
	}

	// Build the SI-TX wire buyer identity per owner type (SP-3 Task 4).
	//   - client bid → "client-<ownerID>"
	//   - bank bid   → "employee-<actingEmployeeID>" (the stable wire identity
	//     for the bank party; independent of which employee acts later).
	var buyerID string
	switch bidderOwnerType {
	case model.OwnerClient:
		if bidderOwnerID == nil {
			return nil, false, status.Error(codes.InvalidArgument, "client bidder requires an owner id")
		}
		buyerID = "client-" + strconv.FormatUint(*bidderOwnerID, 10)
	case model.OwnerBank:
		if actingEmployeeID == 0 {
			return nil, false, status.Error(codes.InvalidArgument, "bank bidder requires an acting employee id")
		}
		buyerID = "employee-" + strconv.FormatUint(actingEmployeeID, 10)
	default:
		return nil, false, status.Error(codes.InvalidArgument, "unsupported bidder owner type")
	}

	// Validate the bidder account against account-service: ownership and active.
	// The premium/strike currency is then derived from this account — the buyer
	// proposes the currency by choosing which account to bind (uniform termless
	// inventory; the listing's legacy preset currency columns are never read).
	if h.accounts == nil {
		return nil, false, status.Error(codes.FailedPrecondition, "account-service client not wired for cross-bank bidding")
	}
	acct, gerr := h.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.GetBidderAccountId()})
	if gerr != nil {
		return nil, false, status.Errorf(codes.NotFound, "bidder account not found: %v", gerr)
	}
	// Ownership assertion branches on owner type. A client bid must bind an
	// account the client owns; a bank bid must bind a BANK account (the gateway
	// already forces this via ResolveAndCheckAccount, but re-assert defensively).
	switch bidderOwnerType {
	case model.OwnerClient:
		if acct.GetOwnerId() != *bidderOwnerID {
			return nil, false, status.Error(codes.PermissionDenied, "bidder account does not belong to caller")
		}
	case model.OwnerBank:
		if !isBankAccount(acct) {
			return nil, false, status.Error(codes.PermissionDenied, "bank bidder must bind a bank account")
		}
	}
	if acct.GetStatus() != "active" {
		return nil, false, status.Error(codes.FailedPrecondition, "bidder account is not active")
	}
	// Derive the premium (and strike) currency from the bound account. Every remote
	// offer is a termless shell, so the buyer proposes the currency by choosing
	// which account to bind; the carried currency is the account's. The listing's
	// preset currency columns are intentionally NOT read (uniform inventory).
	premiumCurrency := acct.GetCurrencyCode()
	strikeCurrency := premiumCurrency
	// Defense-in-depth: a malformed bidder account with no currency would
	// otherwise dispatch an empty-currency OtcOffer the seller's bank rejects.
	if premiumCurrency == "" {
		return nil, false, status.Error(codes.FailedPrecondition,
			"bidder account has no currency; cannot derive shell offer currency")
	}
	buyerAccountNumber := acct.GetAccountNumber()

	// The cross-bank cascade-cancel lot key = the remote listing's
	// (peer routing, native_id).
	peerRouting := remoteOffer.RoutingNumber
	peerBankCode := ""
	if remoteOffer.InitiatorBankCode != nil {
		peerBankCode = *remoteOffer.InitiatorBankCode
	}
	if peerBankCode == "" {
		peerBankCode = strconv.FormatInt(peerRouting, 10)
	}
	parentNativeID := ""
	if remoteOffer.NativeID != nil {
		parentNativeID = *remoteOffer.NativeID
	}
	sellerID := ""
	if remoteOffer.RemoteSellerID != nil {
		sellerID = *remoteOffer.RemoteSellerID
	}

	// Compose the SI-TX OtcOffer (mirrors CreatePeerNegotiation's wire shape).
	// SI-TX §2.5 / §2.8.1 require monetary amounts to be JSON numbers, not
	// quoted strings. DecimalNumber.MarshalJSON emits a bare numeric token.
	settlementDate := settle.Format("2006-01-02")
	offer := map[string]any{
		"stock":              map[string]any{"ticker": remoteOffer.Ticker},
		"settlementDate":     settlementDate,
		"pricePerUnit":       map[string]any{"amount": contractsitx.DecimalNumber{Decimal: strike}, "currency": strikeCurrency},
		"premium":            map[string]any{"amount": contractsitx.DecimalNumber{Decimal: premium}, "currency": premiumCurrency},
		"buyerId":            map[string]any{"routingNumber": h.ownRouting, "id": buyerID},
		"sellerId":           map[string]any{"routingNumber": peerRouting, "id": sellerID},
		"amount":             qty.IntPart(),
		"lastModifiedBy":     map[string]any{"routingNumber": h.ownRouting, "id": buyerID},
		"buyerAccountNumber": buyerAccountNumber,
	}
	// parentOfferId — the per-listing cascade-cancel grouping key. The seller's
	// bank groups sibling chains by this key and cascade-cancels them on accept.
	if parentNativeID != "" {
		offer["parentOfferId"] = map[string]any{
			"routingNumber": peerRouting,
			"id":            parentNativeID,
		}
	}

	// Dispatch to the seller's bank.
	gotRouting, foreignID, derr := h.peerDispatch.CreateNegotiation(ctx, peerBankCode, offer)
	if derr != nil {
		return nil, false, status.Errorf(codes.FailedPrecondition, "cross-bank dispatch failed: %v", derr)
	}

	// Record the local REMOTE mirror row so the bidder can list / drive the
	// negotiation. native_id = peer foreign id; routing_number = peer routing.
	// buildRemoteNeg sets the NOT-NULL terms + ValidateOwner-valid bidder
	// columns; the cross-bank parties live in the Remote* columns.
	offerStruct := contractsitx.OtcOffer{
		Ticker:          remoteOffer.Ticker,
		Amount:          qty.IntPart(),
		PricePerStock:   strike,
		Currency:        strikeCurrency,
		Premium:         premium,
		PremiumCurrency: premiumCurrency,
		SettlementDate:  settlementDate,
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: buyerID},
		// Retain the bidder's bound account number on OUR OWN mirror too (not just
		// in the offer dispatched to the seller's bank). On a cross-bank accept the
		// buyer's bank (this bank) reads this mirror to compose the premium-DEBIT
		// posting; without it the debit falls back to the participant id, which the
		// SI-TX executor cannot resolve to an account for a BANK buyer ("employee-N")
		// → NO_SUCH_ACCOUNT. Pinning it lets the accept debit the exact bound account.
		BuyerAccountNumber: buyerAccountNumber,
	}
	offerJSON, jerr := json.Marshal(offerStruct)
	if jerr != nil {
		return nil, false, status.Errorf(codes.Internal, "marshal mirror offer: %v", jerr)
	}
	var parentRoutingPtr *int64
	var parentNativePtr *string
	if parentNativeID != "" {
		pr := peerRouting
		pn := parentNativeID
		parentRoutingPtr = &pr
		parentNativePtr = &pn
	}
	mirror := buildRemoteNeg(
		gotRouting, foreignID, offerStruct, string(offerJSON),
		h.ownRouting, buyerID,
		peerRouting, sellerID,
		parentRoutingPtr, parentNativePtr, "ongoing",
	)
	// SP-3 Task 4: a bank bid stamps the stable acting-employee wire identity.
	// buildRemoteNeg already sets BidderOwnerType=bank for every remote mirror
	// (the real parties live in the Remote* columns), so the ActingEmployeeID
	// nil-unless-bank invariant (model BeforeSave) holds in both cases. A client
	// bid leaves it nil — the bank wire identity is meaningless there.
	if bidderOwnerType == model.OwnerBank {
		emp := actingEmployeeID
		mirror.ActingEmployeeID = &emp
	}
	// Record the opening BID as the chain's first revision (full-history parity
	// with local chains). We are the BUYER on an outbound bid; the wire id is the
	// composed buyerID. Idempotent: a retried create won't double-append.
	bidWire := buyerID
	bidRev := &model.OTCNegotiationRevision{
		Quantity:                qty,
		StrikePrice:             strike,
		Premium:                 premium,
		SettlementDate:          settle,
		Action:                  model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer",
		RemoteActorWireID:       &bidWire,
	}
	if err := h.remoteNegWriter.UpsertRemoteNegWithRevision(mirror, bidRev); err != nil {
		return nil, false, status.Errorf(codes.Internal, "record remote negotiation mirror: %v", err)
	}

	// Return the unified negotiation response (kind=remote derived from
	// routing; id = the upserted surrogate id; terms from the mirror).
	resp, _ := peerNegToProto(mirror, h.ownRouting)
	return resp, true, nil
}

// derefStr safely dereferences a *string, returning "" for nil.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// publicStockHasSeller reports whether a /public-stock response body (§3.1 bare
// array) contains an entry for the given (sellerID, ticker) pair.
func publicStockHasSeller(body []byte, sellerID, ticker string) bool {
	var resp contractsitx.PublicStocksResponse
	if json.Unmarshal(body, &resp) != nil {
		return false
	}
	for _, ps := range resp {
		if ps.Stock.Ticker != ticker {
			continue
		}
		for _, s := range ps.Sellers {
			if s.Seller.ID == sellerID {
				return true
			}
		}
	}
	return false
}

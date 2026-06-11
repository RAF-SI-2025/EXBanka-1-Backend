// Package handler — cross-bank (REMOTE) dispatch for the negotiation ACTIONS
// (counter / accept / reject / cancel) on an OTCNegotiation chain (Unified OTC
// SP-2b Task 4).
//
// When an action's :nid resolves to a folded-in REMOTE OTCNegotiation row (a
// chain whose authoritative state lives on a PEER bank), the action is proxied
// to that peer over SI-TX and the local mirror row's status/offer is updated to
// match. This relocates the egress logic that previously lived in the
// api-gateway (peer_otc_initiate_handler.go's CounterPeerNegotiation /
// AcceptPeerNegotiation / CancelPeerNegotiation) into stock-service per
// decision A, so the gateway stays a thin passthrough: every counter/accept/
// reject/cancel flows through the same RPC and the local-vs-remote dispatch
// happens here based on the row's routing_number.
//
//   - LOCAL row (routing == own) → the existing local service path (unchanged).
//   - REMOTE row (routing == peer) → proxy + mirror, implemented below.
//
// SI-TX wire rules honoured here (same as Task 3's bid dispatch):
//   - Monetary amounts in the counter OtcOffer are JSON NUMBERS, not quoted
//     strings (contract/sitx.DecimalNumber).
//   - accept is GET {peer}/negotiations/{rid}/{id}/accept (spec §3.6).
//   - reject AND cancel are DELETE {peer}/negotiations/{rid}/{id} — the SI-TX
//     protocol's single terminal soft-cancel (spec §3.5).
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// isOTCNegotiationNotFound reports whether an error means "the chain is not a
// local OTCNegotiation". Both the service sentinel and the raw GORM not-found
// are matched so the remote-dispatch fallback fires for either. (LockByID
// treats a remote row as not-found, so a REMOTE :nid always lands here.)
func isOTCNegotiationNotFound(err error) bool {
	return errors.Is(err, service.ErrOTCNegotiationNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// remoteNegContext bundles the data the remote-action dispatch needs once a
// REMOTE chain has been resolved and the caller authorized as a party.
type remoteNegContext struct {
	row              *model.OTCNegotiation
	rid              string // the peer routing that issued the foreign id (= row.RoutingNumber)
	foreignID        string // the peer foreign negotiation id (= row.NativeID)
	counterpartyCode string // the OTHER party's routing, stringified (the bank we proxy to)
	offer            contractsitx.OtcOffer
	// hostedPartyID is the STABLE SI-TX wire id of the side WE host — read from
	// the ROW, never recomputed from the acting caller. For a client-hosted side
	// it is "client-<N>"; for a bank-hosted side it is "employee-<row.ActingEmployeeID>".
	// The counter compose uses it as lastModifiedBy so a counter performed by a
	// DIFFERENT employee than the originator keeps the SAME employee-<N> the peer
	// expects (wire-id stability — SP-3 Task 5).
	hostedPartyID string
}

// resolveRemoteNegAction loads the REMOTE chain for :nid and authorizes the
// caller as the party WE host. The bool is false (nil error) when :nid is NOT a
// remote chain this handler can dispatch (unwired, or a local id) — the caller
// then surfaces the original local error. A non-party caller gets NotFound
// (existence must not leak to outsiders — same policy as resolveRemoteContract).
//
// Authorization (SP-3 Task 5 — lifts the bank party): the caller may drive the
// chain iff their acting identity matches the side WE host. We host the buyer
// when RemoteBuyerRouting == ownRouting and the seller when RemoteSellerRouting
// == ownRouting. The hosted side is one of:
//
//   - CLIENT-owned — its wire id is "client-<N>". A client principal whose
//     "client-<callerOwnerID>" equals that id is authorized (the original path).
//   - BANK-owned — its wire id is "employee-<N>" (a chain we OPENED carries
//     row.ActingEmployeeID; a chain where we host the SELLER of a bank-owned
//     offer carries RemoteSellerID "employee-<N>"). A caller acting AS THE BANK
//     (callerOwnerType == OwnerBank) is authorized — ANY employee may drive the
//     bank's chain; the published wire id stays the ROW's stable employee-<N>.
//
// Either way the hosted party id, the (rid, foreignID), and the counterparty
// come from the ROW — never from the caller. The counterparty bank code is the
// OTHER party's routing as a string.
func (h *OTCOptionsHandler) resolveRemoteNegAction(
	nid uint64, callerOwnerType model.OwnerType, callerOwnerID *uint64,
) (*remoteNegContext, bool, error) {
	if h.remoteNegOps == nil || h.peerDispatch == nil {
		return nil, false, nil // cross-bank dispatch not wired → not a remote chain here
	}
	row, err := h.remoteNegOps.GetRemoteNegByID(nid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil // not a remote chain → surface the local error
		}
		return nil, false, status.Errorf(codes.Internal, "remote negotiation lookup failed: %v", err)
	}

	buyerRouting, buyerID := remoteBuyer(row)
	sellerRouting, sellerID := remoteSeller(row)
	own := h.ownRouting

	// Identify the side WE host (exactly one of buyer/seller routing == own on a
	// well-formed remote row). The hosted party id is the STABLE wire id read off
	// the row — "client-<N>" for a client side, "employee-<N>" for a bank side.
	var hostedPartyID string
	var counterpartyRouting int64
	switch {
	case buyerRouting == own:
		hostedPartyID = buyerID
		counterpartyRouting = sellerRouting // counterparty is the seller's bank
	case sellerRouting == own:
		hostedPartyID = sellerID
		counterpartyRouting = buyerRouting // counterparty is the buyer's bank
	default:
		// Neither side is hosted here — not our chain to drive.
		return nil, false, status.Error(codes.NotFound, "negotiation not found")
	}

	// Authorize the caller against the hosted side. A bank-owned hosted side
	// (wire id "employee-<N>") is driven by a caller acting AS THE BANK; a
	// client-owned hosted side ("client-<N>") by the matching client principal.
	if isBankWireID(hostedPartyID) {
		if callerOwnerType != model.OwnerBank {
			return nil, false, status.Error(codes.NotFound, "negotiation not found")
		}
	} else {
		if callerOwnerType != model.OwnerClient || callerOwnerID == nil ||
			hostedPartyID != "client-"+strconv.FormatUint(*callerOwnerID, 10) {
			return nil, false, status.Error(codes.NotFound, "negotiation not found")
		}
	}

	var offer contractsitx.OtcOffer
	if jerr := json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer); jerr != nil {
		// best-effort: terms left zero; the counter path re-supplies the
		// strike/premium from the request but reuses this offer's ticker +
		// currencies, so a malformed mirror silently composes a zero-value
		// (empty ticker/currency) counter — log it so it's diagnosable.
		// accept/reject/cancel don't need the offer body.
		log.Printf("WARN resolveRemoteNegAction: row %d RemoteOfferJSON decode failed: %v", row.ID, jerr)
		offer = contractsitx.OtcOffer{}
	}

	return &remoteNegContext{
		row: row,
		// SI-TX path {rn} is ALWAYS the seller's routing (the seller mints the
		// negotiation id). For a buyer-hosted chain sellerRouting == row.RoutingNumber,
		// but for a SELLER-hosted chain (we minted the id) row.RoutingNumber holds the
		// BUYER's bank — dispatching with it sends /negotiations/<buyer>/... and the peer
		// rejects "routingNumber does not identify this negotiation". Always use seller.
		rid:              strconv.FormatInt(sellerRouting, 10),
		foreignID:        remoteNativeIDOf(row),
		counterpartyCode: strconv.FormatInt(counterpartyRouting, 10),
		offer:            offer,
		hostedPartyID:    hostedPartyID,
	}, true, nil
}

// parseSITXDate parses an SI-TX settlement date (RFC3339 or YYYY-MM-DD) into a
// time, falling back to the zero time on an unparseable/empty input. Mirrors the
// parse in buildRemoteNeg so revision settlement dates round-trip consistently.
func parseSITXDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, e := time.Parse(time.RFC3339, s); e == nil {
		return t
	}
	if t, e := time.Parse("2006-01-02", s); e == nil {
		return t
	}
	return time.Time{}
}

// isBankWireID reports whether an SI-TX party id denotes the bank party. The
// bank publishes "employee-<N>" (the stable acting-employee wire identity);
// clients publish "client-<N>". Anything without the employee- prefix is a
// non-bank (client) id.
func isBankWireID(id string) bool {
	return strings.HasPrefix(id, "employee-")
}

// counterRemoteNegotiation proxies a counter (PUT) to the counterparty's bank
// and mirrors the new terms onto the local REMOTE row. It relocates the
// gateway's CounterPeerNegotiation → proxyPeerNegotiation(PUT, "") +
// UpdateNegotiation mirror.
//
// Wire-id stability (SP-3 Task 5): the buyer/seller ids AND lastModifiedBy.id
// are read from the ROW (remoteBuyer/remoteSeller + rc.hostedPartyID), NOT
// recomputed from the acting caller. So a counter performed by a DIFFERENT
// employee than the one who originated a bank-owned chain still publishes the
// SAME "employee-<N>" the peer expects.
func (h *OTCOptionsHandler) counterRemoteNegotiation(
	ctx context.Context, rc *remoteNegContext,
	qty, strike, premium decimal.Decimal, settle time.Time,
) (*stockpb.OTCNegotiationResponse, error) {
	// Compose the SI-TX OtcOffer body from the request terms. SI-TX §2.5 /
	// §2.8.1 require monetary amounts to be JSON NUMBERS — DecimalNumber emits
	// a bare numeric token. Reuse the existing currencies carried on the chain.
	settlementDate := settle.Format("2006-01-02")
	buyerRouting, buyerID := remoteBuyer(rc.row)
	sellerRouting, sellerID := remoteSeller(rc.row)
	// lastModifiedBy = the side WE host, with its STABLE wire id from the row.
	hostedPartyID := rc.hostedPartyID
	offerBody := map[string]any{
		"stock":          map[string]any{"ticker": rc.offer.Ticker},
		"settlementDate": settlementDate,
		"pricePerUnit":   map[string]any{"amount": contractsitx.DecimalNumber{Decimal: strike}, "currency": rc.offer.Currency},
		"premium":        map[string]any{"amount": contractsitx.DecimalNumber{Decimal: premium}, "currency": rc.offer.PremiumCurrency},
		"buyerId":        map[string]any{"routingNumber": buyerRouting, "id": buyerID},
		"sellerId":       map[string]any{"routingNumber": sellerRouting, "id": sellerID},
		"amount":         qty.IntPart(),
		// lastModifiedBy = the party we host placing the counter (stable row id).
		"lastModifiedBy": map[string]any{"routingNumber": h.ownRouting, "id": hostedPartyID},
	}
	// buyerAccountNumber is an immutable cohort-extension field that some peers
	// (e.g. Banka 4) REQUIRE echoed on every PUT counter (missing → 400). The
	// create path already sends it; carry it through here from the stored offer.
	if rc.offer.BuyerAccountNumber != "" {
		offerBody["buyerAccountNumber"] = rc.offer.BuyerAccountNumber
	}
	// Preserve the cascade-cancel lot key when this chain carries one.
	if rc.row.RemoteParentRouting != nil && rc.row.RemoteParentNativeID != nil {
		offerBody["parentOfferId"] = map[string]any{
			"routingNumber": *rc.row.RemoteParentRouting,
			"id":            *rc.row.RemoteParentNativeID,
		}
	}
	body, jerr := json.Marshal(offerBody)
	if jerr != nil {
		return nil, status.Errorf(codes.Internal, "marshal counter offer: %v", jerr)
	}

	resp, code, err := h.peerDispatch.Proxy(ctx, rc.counterpartyCode, rc.rid, rc.foreignID, "PUT", "", body)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cross-bank counter dispatch failed: %v", err)
	}
	if code < 200 || code >= 300 {
		return nil, status.Errorf(codes.FailedPrecondition, "peer rejected counter (%d): %s", code, string(resp))
	}

	// Mirror the new terms on the local REMOTE row so the caller's own list
	// reflects the counter immediately.
	mirrorOffer := contractsitx.OtcOffer{
		Ticker:          rc.offer.Ticker,
		Amount:          qty.IntPart(),
		PricePerStock:   strike,
		Currency:        rc.offer.Currency,
		Premium:         premium,
		PremiumCurrency: rc.offer.PremiumCurrency,
		SettlementDate:  settlementDate,
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: hostedPartyID},
		// Preserve the immutable buyerAccountNumber on the mirror so a SUBSEQUENT
		// counter still echoes it to the peer (else the 2nd counter omits it and a
		// peer that requires it — e.g. Banka 4 — rejects with 400).
		BuyerAccountNumber: rc.offer.BuyerAccountNumber,
	}
	mirrorJSON, _ := json.Marshal(mirrorOffer)
	// Record the counter as a revision (full-history parity). The mover is the
	// side WE host (this is our outbound counter); role + wire id come from the row.
	role, wireID := remoteSideAtRouting(rc.row, h.ownRouting)
	counterRev := &model.OTCNegotiationRevision{
		Quantity:                qty,
		StrikePrice:             strike,
		Premium:                 premium,
		SettlementDate:          settle,
		Action:                  model.OTCNegotiationActionCounter,
		ModifiedByPrincipalType: role,
		RemoteActorWireID:       &wireID,
	}
	if err := h.remoteNegOps.UpdateRemoteNegOfferWithRevision(rc.row.RoutingNumber, rc.foreignID, string(mirrorJSON), counterRev); err != nil {
		return nil, status.Errorf(codes.Internal, "mirror counter offer: %v", err)
	}

	// Re-read the row so the response carries the refreshed offer JSON.
	updated, gerr := h.remoteNegOps.GetRemoteNegByID(rc.row.ID)
	if gerr != nil {
		updated = rc.row // best-effort: fall back to the stale row for shaping
	}
	out, _ := peerNegToProto(updated, h.ownRouting)
	return out, nil
}

// acceptRemoteNegotiation proxies an accept (GET /accept) to the counterparty's
// bank, flips the local mirror to accepted, and runs the cross-bank cascade-
// cancel: every sibling REMOTE chain sharing the accepted chain's lot key is
// flipped cancelled locally AND a DELETE is fired to each sibling bidder's bank.
// It relocates the gateway's AcceptPeerNegotiation → proxyPeerNegotiation(GET,
// "/accept") + MarkNegotiationAccepted mirror + CascadeCancelSiblings.
func (h *OTCOptionsHandler) acceptRemoteNegotiation(
	ctx context.Context, rc *remoteNegContext,
) (*stockpb.OTCAcceptNegotiationResponse, error) {
	// Anti-self-accept guard (mirrors the LOCAL path's "caller must be OPPOSITE
	// to whoever proposed the current terms" rule). resolveRemoteNegAction has
	// already proven the caller owns the side WE host (rc.hostedPartyID); here we
	// additionally forbid that hosted side from accepting terms IT last proposed.
	// Without it, a bidder could accept their OWN cross-bank bid — forming a
	// contract and settling the premium with NO agreement from the counterparty
	// (verified live 2026-06-05). lastModifiedBy is carried on the chain's offer
	// JSON and round-tripped identically on both banks, so the comparison is
	// authoritative. A malformed/absent lastModifiedBy (zero value) cannot match
	// a real hosted id, so it fails OPEN only in the degenerate case where the
	// proposer is unknown — never granting a self-accept on a well-formed chain.
	lm := rc.offer.LastModifiedBy
	if lm.RoutingNumber == h.ownRouting && lm.ID != "" && lm.ID == rc.hostedPartyID {
		return nil, status.Error(codes.PermissionDenied,
			"cannot accept the terms you last proposed — the counterparty must accept")
	}

	// Orphan-accept guard: when the parent listing is LOCAL to this bank
	// (remote_parent_routing == ownRouting), the listing's native id is our local
	// offer id. The accept happens on the seller's bank (the listing's host), so
	// we can authoritatively reject an accept against a listing the poster has
	// CANCELLED/CONSUMED — mirroring the LOCAL accept path's ErrOTCParentNotOpen.
	// Without this a child chain of a cancelled cross-bank listing could still be
	// accepted, forming a contract + settling money on a withdrawn offer
	// (verified live 2026-06-05). Skipped when the parent is on a peer bank
	// (we can't read its status) or when the negotiations service isn't wired.
	if h.negotiations != nil && rc.row.RemoteParentRouting != nil &&
		*rc.row.RemoteParentRouting == h.ownRouting && rc.row.RemoteParentNativeID != nil {
		if parentID, perr := strconv.ParseUint(*rc.row.RemoteParentNativeID, 10, 64); perr == nil {
			if !h.negotiations.LocalParentIsOpen(parentID) {
				return nil, status.Error(codes.FailedPrecondition,
					"parent listing is no longer open (cancelled or already consumed)")
			}
		}
	}

	// Termless-model orphan-accept guard: the /public-stock wire carries no offer
	// id, so a cross-bank bid has no RemoteParentNativeID for the linked-parent
	// guard above. When the seller side is LOCAL (Direction 2) resolve the listing
	// by its (owner, ticker, sell_initiated) unique key and reject an accept once
	// the listing is no longer open — the seller already CONSUMED it (a prior
	// accept) or CANCELLED it. This keeps one listing backing exactly one accepted
	// contract: without it every still-"ongoing" sibling bid could be accepted and
	// over-commit the seller's shares. Skipped when the offer ticker is unknown
	// (malformed mirror) so a decode failure never blocks a legitimate accept.
	if h.negotiations != nil && rc.offer.Ticker != "" {
		if sellerRouting, sellerID := remoteSeller(rc.row); sellerRouting == h.ownRouting {
			if ot, oid, perr := parseSellerOwner(sellerID); perr == nil {
				if !h.negotiations.LocalSellOfferOpenForSeller(ot, oid, rc.offer.Ticker) {
					return nil, status.Error(codes.FailedPrecondition,
						"listing is no longer open (cancelled or already consumed)")
				}
			}
		}
	}

	resp, code, err := h.peerDispatch.Proxy(ctx, rc.counterpartyCode, rc.rid, rc.foreignID, "GET", "/accept", nil)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cross-bank accept dispatch failed: %v", err)
	}
	if code < 200 || code >= 300 {
		return nil, status.Errorf(codes.FailedPrecondition, "peer rejected accept (%d): %s", code, string(resp))
	}

	// Parse the peer's SI-TX accept body ({ transactionId, status }) for the
	// cross-bank transaction id. The FE uses this to poll cross-bank settlement
	// (GET /me/otc/transactions/:txid/status) during the accept→contract-mirror
	// window. Best-effort: a non-JSON / field-absent body leaves it empty —
	// never fail the accept (the contract already formed on the peer).
	var crossBankTxID string
	if len(resp) > 0 {
		var peerBody struct {
			TransactionID string `json:"transactionId"`
		}
		if jerr := json.Unmarshal(resp, &peerBody); jerr != nil {
			log.Printf("WARN acceptRemoteNegotiation: row %d peer /accept body decode failed: %v", rc.row.ID, jerr)
		} else {
			crossBankTxID = peerBody.TransactionID
		}
	}

	// Flip the local mirror to accepted (ongoing → accepted) AND record the ACCEPT
	// revision atomically (full-history parity). The CAS serialises concurrent
	// accepts so only one wins; a no-match is tolerated (the row may already be
	// accepted via a peer-driven webhook) and records no duplicate revision. The
	// acceptor is the side WE host (this is our outbound accept).
	acceptRole, acceptWire := remoteSideAtRouting(rc.row, h.ownRouting)
	acceptRev := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(rc.offer.Amount),
		StrikePrice:             rc.offer.PricePerStock,
		Premium:                 rc.offer.Premium,
		SettlementDate:          parseSITXDate(rc.offer.SettlementDate),
		Action:                  model.OTCNegotiationActionAccept,
		ModifiedByPrincipalType: acceptRole,
		RemoteActorWireID:       &acceptWire,
	}
	if _, serr := h.remoteNegOps.CompareAndSetRemoteNegStatusWithRevision(rc.row.RoutingNumber, rc.foreignID, "ongoing", "accepted", acceptRev); serr != nil {
		return nil, status.Errorf(codes.Internal, "mirror accept status: %v", serr)
	}

	// Cross-bank cascade-cancel — every sibling REMOTE chain sharing the
	// accepted chain's (parentRouting, parentNativeID) lot key under the same
	// seller. Flip them cancelled locally AND DELETE to each sibling bidder's
	// bank so their mirrors flip too. Best-effort: a cascade failure must NOT
	// reverse the accept (the contract already formed on the peer).
	cancelled := h.cascadeCancelRemoteSiblings(ctx, rc)

	// Consume the LOCAL listing the contract was written against (Direction 2:
	// we host the seller). The termless /public-stock model carries no offer id
	// on the wire, so the listing is resolved by the seller's (owner, ticker,
	// sell_initiated) unique key — at most one open row. Without this the listing
	// keeps advertising inventory already under contract. Best-effort: a consume
	// failure must NOT reverse the accept (the contract already formed on the
	// peer); log and continue. No-op when the seller is on a peer bank
	// (Direction 1: that bank's local accept path consumes its own listing).
	if h.negotiations != nil {
		if sellerRouting, sellerID := remoteSeller(rc.row); sellerRouting == h.ownRouting {
			if ot, oid, perr := parseSellerOwner(sellerID); perr == nil {
				if cerr := h.negotiations.ConsumeLocalSellOfferForSeller(ot, oid, rc.offer.Ticker); cerr != nil {
					log.Printf("WARN acceptRemoteNegotiation: row %d consume local listing failed: %v", rc.row.ID, cerr)
				}
			} else {
				log.Printf("WARN acceptRemoteNegotiation: row %d local seller id %q unparseable: %v", rc.row.ID, sellerID, perr)
			}
		}
	}

	// Re-read the accepted row for the winning projection.
	winningRow, gerr := h.remoteNegOps.GetRemoteNegByID(rc.row.ID)
	if gerr != nil {
		winningRow = rc.row
	}
	winning, _ := peerNegToProto(winningRow, h.ownRouting)

	return &stockpb.OTCAcceptNegotiationResponse{
		Winning:                winning,
		ParentStatus:           "accepted",
		CancelledSiblings:      cancelled,
		CrossBankTransactionId: crossBankTxID,
	}, nil
}

// cascadeCancelRemoteSiblings flips every sibling REMOTE chain under the
// accepted chain's lot key to cancelled locally and DELETEs to each sibling
// bidder's bank. Returns the cancelled siblings projected onto the wire shape.
// The just-accepted chain itself is excluded (it is not its own sibling).
func (h *OTCOptionsHandler) cascadeCancelRemoteSiblings(
	ctx context.Context, rc *remoteNegContext,
) []*stockpb.OTCNegotiationResponse {
	cancelled := []*stockpb.OTCNegotiationResponse{}
	if rc.row.RemoteParentRouting == nil || rc.row.RemoteParentNativeID == nil {
		return cancelled // free-form chain (no lot key) → no sibling group
	}
	sellerRouting, sellerID := remoteSeller(rc.row)
	siblings, lerr := h.remoteNegOps.ListRemoteNegBySellerAndParent(
		sellerRouting, sellerID, *rc.row.RemoteParentRouting, *rc.row.RemoteParentNativeID,
	)
	if lerr != nil {
		return cancelled // best-effort: a cascade list failure must not reverse the accept
	}
	for i := range siblings {
		sib := &siblings[i]
		if sib.ID == rc.row.ID {
			continue // the winning chain itself
		}
		sibNative := remoteNativeIDOf(sib)
		// Flip the sibling mirror cancelled locally.
		if err := h.remoteNegOps.UpdateRemoteNegStatus(sib.RoutingNumber, sibNative, "cancelled"); err == nil {
			sib.Status = "cancelled"
		}
		// DELETE to the sibling bidder's bank so their mirror flips too. The
		// counterparty for a sibling chain (from this seller-hosting bank's
		// perspective) is the sibling's BUYER bank.
		sibBuyerRouting, _ := remoteBuyer(sib)
		sibPeerCode := strconv.FormatInt(sibBuyerRouting, 10)
		sibRID := strconv.FormatInt(sib.RoutingNumber, 10)
		_, _, _ = h.peerDispatch.Proxy(ctx, sibPeerCode, sibRID, sibNative, "DELETE", "", nil)

		if item, _ := peerNegToProto(sib, h.ownRouting); item != nil {
			cancelled = append(cancelled, item)
		}
	}
	return cancelled
}

// cancelRemoteNegotiation proxies a reject/cancel (DELETE) to the counterparty's
// bank and flips the local mirror to cancelled. Both reject and cancel map onto
// the SI-TX DELETE terminal (spec §3.5). It relocates the gateway's
// CancelPeerNegotiation → proxyPeerNegotiation(DELETE, "") + status mirror.
func (h *OTCOptionsHandler) cancelRemoteNegotiation(
	ctx context.Context, rc *remoteNegContext, recordReject bool,
) (*stockpb.OTCNegotiationResponse, error) {
	resp, code, err := h.peerDispatch.Proxy(ctx, rc.counterpartyCode, rc.rid, rc.foreignID, "DELETE", "", nil)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cross-bank cancel dispatch failed: %v", err)
	}
	if code < 200 || code >= 300 {
		return nil, status.Errorf(codes.FailedPrecondition, "peer rejected cancel (%d): %s", code, string(resp))
	}
	// A two-party REJECT records a terminal revision (full-history parity, same as
	// the local RejectNegotiation); a bidder's own CANCEL does not (parity with
	// local CancelNegotiation, which appends no revision). Both map to the SI-TX
	// DELETE terminal, so the RPC that called us decides via recordReject.
	if recordReject {
		rejRole, rejWire := remoteSideAtRouting(rc.row, h.ownRouting)
		rejectRev := &model.OTCNegotiationRevision{
			Quantity:                decimal.NewFromInt(rc.offer.Amount),
			StrikePrice:             rc.offer.PricePerStock,
			Premium:                 rc.offer.Premium,
			SettlementDate:          parseSITXDate(rc.offer.SettlementDate),
			Action:                  model.OTCNegotiationActionReject,
			ModifiedByPrincipalType: rejRole,
			RemoteActorWireID:       &rejWire,
		}
		if _, serr := h.remoteNegOps.SetRemoteNegStatusWithRevision(rc.row.RoutingNumber, rc.foreignID, "cancelled", rejectRev); serr != nil {
			return nil, status.Errorf(codes.Internal, "mirror cancel status: %v", serr)
		}
	} else if err := h.remoteNegOps.UpdateRemoteNegStatus(rc.row.RoutingNumber, rc.foreignID, "cancelled"); err != nil {
		return nil, status.Errorf(codes.Internal, "mirror cancel status: %v", err)
	}
	updated, gerr := h.remoteNegOps.GetRemoteNegByID(rc.row.ID)
	if gerr != nil {
		updated = rc.row
	}
	out, _ := peerNegToProto(updated, h.ownRouting)
	return out, nil
}

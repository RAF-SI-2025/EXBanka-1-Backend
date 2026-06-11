// Package handler — gRPC surface for the OTCNegotiationService (Phase 2).
// Methods are attached to the existing OTCOptionsHandler so they can
// share the embedded UnimplementedOTCOptionsServiceServer and the
// already-registered server instance.
//
// Wiring: cmd/main.go calls otcOptionsHandler.WithNegotiations(svc)
// before registering; without the wire-up these methods return
// Unimplemented (typed sentinel) instead of panicking on nil deref.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strconv"
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

// WithNegotiations wires the Phase-2 parallel-negotiations service into
// the handler. Returns a copy (mutating-copy style matches WithRatings,
// WithPeerContracts).
func (h *OTCOptionsHandler) WithNegotiations(neg *service.OTCNegotiationService) *OTCOptionsHandler {
	cp := *h
	cp.negotiations = neg
	return &cp
}

// ---------- helpers ----------

func ownerTypeFromProto(s string) (model.OwnerType, error) {
	switch s {
	case "client":
		return model.OwnerClient, nil
	case "bank":
		return model.OwnerBank, nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "owner_type must be 'client' or 'bank', got %q", s)
	}
}

// resolveOwnerID returns nil for OwnerBank, &id for OwnerClient (with
// a zero-id rejection to catch accidental "0 means bank" callers).
func resolveOwnerID(ot model.OwnerType, rawID uint64) (*uint64, error) {
	if ot == model.OwnerBank {
		return nil, nil
	}
	if rawID == 0 {
		return nil, status.Error(codes.InvalidArgument, "client owner requires non-zero owner_id")
	}
	id := rawID
	return &id, nil
}

func parseDecimalArg(name, v string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero, status.Errorf(codes.InvalidArgument, "%s must be a decimal: %v", name, err)
	}
	return d, nil
}

func parseTimestampArg(name, v string) (parsed time.Time, err error) {
	// Try RFC3339 then date-only; both are accepted by the gateway.
	if t, e := time.Parse(time.RFC3339, v); e == nil {
		return t, nil
	}
	if t, e := time.Parse("2006-01-02", v); e == nil {
		return t, nil
	}
	return time.Time{}, status.Errorf(codes.InvalidArgument, "%s must be RFC3339 or YYYY-MM-DD", name)
}

func negToProto(n *model.OTCNegotiation) *stockpb.OTCNegotiationResponse {
	if n == nil {
		return nil
	}
	bidderID := uint64(0)
	if n.BidderOwnerID != nil {
		bidderID = *n.BidderOwnerID
	}
	lastOwnerID := uint64(0)
	if n.LastActionByOwnerID != nil {
		lastOwnerID = *n.LastActionByOwnerID
	}
	mintedContractID := uint64(0)
	if n.MintedContractID != nil {
		mintedContractID = *n.MintedContractID
	}
	return &stockpb.OTCNegotiationResponse{
		Id:                    n.ID,
		ParentOfferId:         n.ParentOfferID,
		BidderOwnerType:       string(n.BidderOwnerType),
		BidderOwnerId:         bidderID,
		BidderAccountId:       n.BidderAccountID,
		Quantity:              n.Quantity.String(),
		StrikePrice:           n.StrikePrice.String(),
		Premium:               n.Premium.String(),
		SettlementDate:        n.SettlementDate.UTC().Format(time.RFC3339),
		Status:                n.Status,
		LastActionByOwnerType: n.LastActionByOwnerType,
		LastActionByOwnerId:   lastOwnerID,
		LastActionAt:          n.LastActionAt.UTC().Format(time.RFC3339),
		CreatedAt:             n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:             n.UpdatedAt.UTC().Format(time.RFC3339),
		Version:               n.Version,
		MintedContractId:      mintedContractID,
	}
}

func negsToProto(rows []model.OTCNegotiation) []*stockpb.OTCNegotiationResponse {
	out := make([]*stockpb.OTCNegotiationResponse, 0, len(rows))
	for i := range rows {
		out = append(out, negToProto(&rows[i]))
	}
	return out
}

// ---------- RPCs ----------

func (h *OTCOptionsHandler) OpenNegotiation(ctx context.Context, in *stockpb.OpenNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetBidderOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetBidderOwnerId())
	if err != nil {
		return nil, err
	}
	qty, err := parseDecimalArg("quantity", in.GetQuantity())
	if err != nil {
		return nil, err
	}
	strike, err := parseDecimalArg("strike_price", in.GetStrikePrice())
	if err != nil {
		return nil, err
	}
	premium, err := parseDecimalArg("premium", in.GetPremium())
	if err != nil {
		return nil, err
	}
	settle, err := parseTimestampArg("settlement_date", in.GetSettlementDate())
	if err != nil {
		return nil, err
	}
	actingEmp := optionalPtr(in.GetActingEmployeeId())

	neg, err := h.negotiations.OpenNegotiation(ctx, service.OpenNegotiationInput{
		ParentOfferID:       in.GetParentOfferId(),
		BidderOwnerType:     ot,
		BidderOwnerID:       oid,
		BidderAccountID:     in.GetBidderAccountId(),
		Quantity:            qty,
		StrikePrice:         strike,
		Premium:             premium,
		SettlementDate:      settle,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    actingEmp,
	})
	if err != nil {
		// The local path could not find the parent listing. It may be a
		// folded-in REMOTE offer (a peer-hosted listing) — dispatch the bid
		// cross-bank (SP-2b). Same fallback pattern as ListNegotiationsByListing.
		if isOTCOfferNotFound(err) {
			remoteResp, ok, rerr := h.openRemoteNegotiation(ctx, in, ot, oid, in.GetActingEmployeeId(), qty, strike, premium, settle)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				return remoteResp, nil
			}
		}
		return nil, err
	}
	return negToProto(neg), nil
}

func (h *OTCOptionsHandler) CounterNegotiation(ctx context.Context, in *stockpb.CounterNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	qty, err := parseDecimalArg("quantity", in.GetQuantity())
	if err != nil {
		return nil, err
	}
	strike, err := parseDecimalArg("strike_price", in.GetStrikePrice())
	if err != nil {
		return nil, err
	}
	premium, err := parseDecimalArg("premium", in.GetPremium())
	if err != nil {
		return nil, err
	}
	settle, err := parseTimestampArg("settlement_date", in.GetSettlementDate())
	if err != nil {
		return nil, err
	}
	neg, err := h.negotiations.CounterNegotiation(ctx, service.CounterNegotiationInput{
		NegotiationID:       in.GetNegotiationId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		Quantity:            qty,
		StrikePrice:         strike,
		Premium:             premium,
		SettlementDate:      settle,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
	})
	if err != nil {
		// The chain is not a LOCAL row. It may be a folded-in REMOTE chain
		// (peer-hosted) — dispatch the counter cross-bank (SP-2b Task 4).
		if isOTCNegotiationNotFound(err) {
			rc, ok, rerr := h.resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				// The counter's wire ids (buyer/seller + lastModifiedBy) are read
				// from the ROW inside counterRemoteNegotiation, so a bank-driven
				// counter keeps the stable employee-<N> regardless of which
				// employee performs it (SP-3 Task 5 wire-id stability).
				return h.counterRemoteNegotiation(ctx, rc, qty, strike, premium, settle)
			}
		}
		return nil, err
	}
	return negToProto(neg), nil
}

func (h *OTCOptionsHandler) AcceptNegotiationChain(ctx context.Context, in *stockpb.OTCAcceptNegotiationRequest) (*stockpb.OTCAcceptNegotiationResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}

	// E2: on_behalf_of_fund_id validation — manager-only, account must be fund's RSD.
	onBehalfOfFundID := in.GetOnBehalfOfFundId()
	if onBehalfOfFundID != 0 {
		if h.fundRepo == nil {
			return nil, status.Error(codes.FailedPrecondition, "fund support not configured on OTC handler")
		}
		fund, ferr := h.fundRepo.GetByID(onBehalfOfFundID)
		if ferr != nil {
			return nil, status.Errorf(codes.NotFound, "fund %d not found", onBehalfOfFundID)
		}
		actingEmpID := int64(in.GetActingEmployeeId())
		if actingEmpID == 0 {
			return nil, status.Error(codes.PermissionDenied, "fund orders require acting_employee_id")
		}
		if actingEmpID != fund.ManagerEmployeeID {
			return nil, status.Error(codes.PermissionDenied, "fund_not_managed_by_actor")
		}
		if in.GetAcceptorAccountId() != fund.RSDAccountID {
			return nil, status.Error(codes.InvalidArgument, "acceptor_account_id must equal fund RSD account for fund orders")
		}
	}

	result, err := h.negotiations.AcceptNegotiation(ctx, service.AcceptNegotiationInput{
		NegotiationID:       in.GetNegotiationId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
		AcceptorAccountID:   in.GetAcceptorAccountId(),
		OnBehalfOfFundID:    onBehalfOfFundID,
	})
	if err != nil {
		// The chain is not a LOCAL row. It may be a folded-in REMOTE chain
		// (peer-hosted) — dispatch the accept cross-bank, mirror the status,
		// and cascade-cancel siblings (SP-2b Task 4). Fund-accept is a
		// local-only flow; a remote chain never carries on_behalf_of_fund_id.
		if isOTCNegotiationNotFound(err) {
			rc, ok, rerr := h.resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				return h.acceptRemoteNegotiation(ctx, rc)
			}
		}
		return nil, err
	}
	return &stockpb.OTCAcceptNegotiationResponse{
		Winning:           negToProto(result.WinningNegotiation),
		ParentOfferId:     result.ParentOffer.ID,
		ParentStatus:      result.ParentOffer.Status,
		CancelledSiblings: negsToProto(result.CancelledSiblings),
		Contract:          mintedContractToProto(result.Contract),
	}, nil
}

func (h *OTCOptionsHandler) RejectNegotiation(ctx context.Context, in *stockpb.RejectNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	neg, err := h.negotiations.RejectNegotiation(ctx, service.RejectNegotiationInput{
		NegotiationID:       in.GetNegotiationId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
	})
	if err != nil {
		// Not a LOCAL row — a REMOTE chain rejects via the SI-TX DELETE
		// terminal (reject and cancel both DELETE on the peer; SP-2b Task 4).
		if isOTCNegotiationNotFound(err) {
			rc, ok, rerr := h.resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				// RejectNegotiation → record a REJECT revision (two-party terminal).
				return h.cancelRemoteNegotiation(ctx, rc, true)
			}
		}
		return nil, err
	}
	return negToProto(neg), nil
}

func (h *OTCOptionsHandler) CancelNegotiation(ctx context.Context, in *stockpb.CancelNegotiationRequest) (*stockpb.OTCNegotiationResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	neg, err := h.negotiations.CancelNegotiation(ctx, service.CancelNegotiationInput{
		NegotiationID:       in.GetNegotiationId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
	})
	if err != nil {
		// Not a LOCAL row — a REMOTE chain cancels via the SI-TX DELETE
		// terminal (SP-2b Task 4).
		if isOTCNegotiationNotFound(err) {
			rc, ok, rerr := h.resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				// CancelNegotiation (bidder withdraw) → no revision (parity with
				// local CancelNegotiation, which records none).
				return h.cancelRemoteNegotiation(ctx, rc, false)
			}
		}
		return nil, err
	}
	return negToProto(neg), nil
}

func (h *OTCOptionsHandler) CancelListing(ctx context.Context, in *stockpb.CancelListingRequest) (*stockpb.CancelListingResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	res, err := h.negotiations.CancelListing(ctx, service.CancelListingInput{
		OfferID:             in.GetOfferId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*stockpb.OTCNegotiationResponse, 0, len(res.CancelledChains))
	for i := range res.CancelledChains {
		out = append(out, negToProto(&res.CancelledChains[i]))
	}
	// Cross-bank cascade: the local CancelListing only cancelled LOCAL child
	// chains (keyed on the numeric parent_offer_id). REMOTE child chains carry
	// parent_offer_id=0 and group on (remote_parent_routing, remote_parent_native_id),
	// so they are NOT touched above. Cancel them too — flip each ongoing remote
	// child to cancelled locally AND DELETE to the bidder's bank so its mirror
	// flips. Without this, a cross-bank child of a cancelled listing stays
	// "ongoing" and the orphan-accept gate (acceptRemoteNegotiation) is the only
	// thing stopping a contract from forming on a dead listing — this makes the
	// chain terminal too (verified live 2026-06-05). Best-effort: a cascade
	// failure must not undo the listing cancel (which already committed).
	remoteCancelled := h.cascadeCancelRemoteChildrenOfListing(ctx, in.GetOfferId())
	out = append(out, remoteCancelled...)
	return &stockpb.CancelListingResponse{
		OfferId:         res.Offer.ID,
		Status:          res.Offer.Status,
		CancelledChains: out,
	}, nil
}

// cascadeCancelRemoteChildrenOfListing flips every ongoing REMOTE child chain
// grouped under the just-cancelled LOCAL listing (parent routing == ours, native
// id == the local offer id) to cancelled and DELETEs to each bidder's bank.
// Returns the cancelled children projected onto the wire shape. No-op (and nil)
// when the cross-bank ops/dispatch aren't wired.
func (h *OTCOptionsHandler) cascadeCancelRemoteChildrenOfListing(
	ctx context.Context, offerID uint64,
) []*stockpb.OTCNegotiationResponse {
	cancelled := []*stockpb.OTCNegotiationResponse{}
	if h.remoteNegOps == nil || h.peerDispatch == nil {
		return cancelled
	}
	parentNative := strconv.FormatUint(offerID, 10)
	children, err := h.remoteNegOps.ListRemoteNegByParent(h.ownRouting, parentNative)
	if err != nil {
		return cancelled // best-effort
	}
	for i := range children {
		ch := &children[i]
		chNative := remoteNativeIDOf(ch)
		if err := h.remoteNegOps.UpdateRemoteNegStatus(ch.RoutingNumber, chNative, "cancelled"); err == nil {
			ch.Status = "cancelled"
		}
		// DELETE to the bidder's bank. From the listing-host's perspective the
		// counterparty of a child chain is its BUYER bank.
		buyerRouting, _ := remoteBuyer(ch)
		_, _, _ = h.peerDispatch.Proxy(ctx,
			strconv.FormatInt(buyerRouting, 10),
			strconv.FormatInt(ch.RoutingNumber, 10),
			chNative, "DELETE", "", nil)
		if item, _ := peerNegToProto(ch, h.ownRouting); item != nil {
			cancelled = append(cancelled, item)
		}
	}
	return cancelled
}

// ListMyNegotiations merges the caller's LOCAL (intra-bank) and REMOTE
// (cross-bank peer) negotiation chains into one list, stamping provenance
// (kind / routing_number / bank_code) and me_owner on every item (SP-1
// Task 7). The gateway is a uniform passthrough — it forwards the merged
// list and the new fields flow through automatically.
//
// me_owner = "I posted/originated the parent listing", NOT "I'm a party".
// A bidder is never an owner. For LOCAL chains, ListMyNegotiations returns
// only the caller's BIDDER chains (the poster sees their chains via the
// per-listing path), so me_owner is always false there. For REMOTE chains,
// me_owner is true only when WE host the seller/poster side (the row's
// seller routing == our own routing).
//
// Paging: page/page_size apply to the LOCAL set only (the repository
// paginates it). Remote rows are appended in full after the local page —
// they are never silently truncated. total reflects the local total only,
// matching the local pagination semantics; the merged slice length may
// exceed it by the remote count. This is a deliberate "don't drop remote"
// choice; unified cross-source paging is out of scope for SP-1.
func (h *OTCOptionsHandler) ListMyNegotiations(ctx context.Context, in *stockpb.ListMyNegotiationsRequest) (*stockpb.ListNegotiationsResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	rows, total, err := h.negotiations.ListMyNegotiations(ctx, ot, oid, in.GetStatuses(), int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}

	out := make([]*stockpb.OTCNegotiationResponse, 0, len(rows))
	for i := range rows {
		item := negToProto(&rows[i])
		// LOCAL provenance. These are bidder chains (the service returns
		// only chains where the caller is the bidder), so me_owner is
		// false by the strict rule — a bidder is not an owner.
		item.Kind = kindFromLocal(rows[i].Local)
		item.RoutingNumber = h.ownRouting
		item.BankCode = h.ownBankCode
		item.MeOwner = false
		out = append(out, item)
	}

	// REMOTE merge — cross-bank peer negotiations where the caller is a party,
	// restricted to the caller's BIDDER chains (ListMyNegotiations is the
	// "bids I placed" view). Two principal kinds have a cross-bank identity:
	//
	//   - CLIENT (cross-bank party id "client-<N>"): match the exact principal
	//     via ListRemoteNegByClient.
	//   - BANK (an employee acting AS THE BANK; party id "employee-<N>"): the
	//     bank has no single wire principal across chains, so match by the
	//     "employee-" prefix on the side WE host as BUYER (our cross-bank bids)
	//     via ListRemoteNegByBankParty (SP-3 Task 5b). Local bank bidder chains
	//     already come back from the service above; this only adds the remote
	//     ones. A client caller never reaches the bank lister (and vice versa).
	if h.peerNegs != nil {
		var peerRows []model.OTCNegotiation
		var perr error
		switch {
		case ot == model.OwnerClient && oid != nil:
			principal := "client-" + strconv.FormatUint(*oid, 10)
			peerRows, perr = h.peerNegs.ListRemoteNegByClient(h.ownRouting, principal, "")
		case ot == model.OwnerBank:
			peerRows, perr = h.peerNegs.ListRemoteNegByBankParty(h.ownRouting, "buyer")
		}
		if perr != nil {
			return nil, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
		}
		statusFilter := statusSet(in.GetStatuses())
		for i := range peerRows {
			item, _ := peerNegToProto(&peerRows[i], h.ownRouting)
			if item == nil {
				continue
			}
			if statusFilter != nil {
				if _, ok := statusFilter[item.GetStatus()]; !ok {
					continue
				}
			}
			out = append(out, item)
		}
	}

	return &stockpb.ListNegotiationsResponse{
		Negotiations: out,
		Total:        total,
	}, nil
}

// statusSet builds a lookup set from the request's status filter, or nil
// when no filter was supplied (all statuses pass).
func statusSet(statuses []string) map[string]struct{} {
	if len(statuses) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(statuses))
	for _, s := range statuses {
		set[s] = struct{}{}
	}
	return set
}

// peerNegToProto maps a cross-bank peer-negotiation mirror row onto the
// unified OTCNegotiationResponse wire shape (SP-1 Task 7).
//
//   - Id is the local surrogate primary key of the mirror row (so callers
//     can correlate within THIS bank's namespace).
//   - kind = "remote"; routing_number + bank_code identify the
//     COUNTERPARTY/peer bank — the side WE do NOT host. When we host the
//     buyer, the counterparty is the seller's bank; when we host the
//     seller, the counterparty is the buyer's bank.
//   - terms are read from the parsed sitx.OtcOffer carried in OfferJSON.
//   - me_owner = WE host the seller/poster side = SellerRoutingNumber
//     == our own routing — i.e. someone is bidding on a listing we host.
//
// peerNegToProto maps a cross-bank REMOTE negotiation row (in the unified
// otc_negotiations table) onto the unified OTCNegotiationResponse wire shape
// (SP-1 Task 7). It also returns the decoded offer's Ticker so callers that
// need both the proto and the ticker (e.g. peerNegToOfferProto) can avoid a
// second JSON decode. The cross-bank parties live in the Remote* columns
// (SP-2a); the authoritative terms are the parsed RemoteOfferJSON.
func peerNegToProto(row *model.OTCNegotiation, ownRouting int64) (*stockpb.OTCNegotiationResponse, string) {
	if row == nil {
		return nil, ""
	}
	var offer contractsitx.OtcOffer
	if err := json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer); err != nil {
		log.Printf("WARN peerNegToProto: row %d RemoteOfferJSON decode failed: %v", row.ID, err)
		// best-effort: id + status still valid; terms left zero
	}

	buyerRouting, _ := remoteBuyer(row)
	sellerRouting, _ := remoteSeller(row)
	meOwner := sellerRouting == ownRouting
	// The counterparty is the side we do NOT host. If we host the seller,
	// the peer bank is the buyer's; otherwise the peer is the seller's.
	peerRouting := sellerRouting
	if meOwner {
		peerRouting = buyerRouting
	}
	// The remote row's routing_number is the counterparty/peer bank that
	// issued the foreign id; its string form is the human-readable bank code.
	peerBankCode := strconv.FormatInt(peerRouting, 10)

	return &stockpb.OTCNegotiationResponse{
		Id:             row.ID,
		Quantity:       strconv.FormatInt(offer.Amount, 10),
		StrikePrice:    offer.PricePerStock.String(),
		Premium:        offer.Premium.String(),
		SettlementDate: offer.SettlementDate,
		Status:         row.Status,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339),
		Kind:           "remote",
		RoutingNumber:  peerRouting,
		BankCode:       peerBankCode,
		MeOwner:        meOwner,
	}, offer.Ticker
}

func (h *OTCOptionsHandler) ListNegotiationRevisions(ctx context.Context, in *stockpb.ListNegotiationRevisionsRequest) (*stockpb.ListNegotiationRevisionsResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	revs, err := h.negotiations.ListRevisions(ctx, in.GetNegotiationId(), ot, oid)
	if err != nil {
		// Not a LOCAL chain — it may be a folded-in REMOTE chain. Authorize the
		// caller as the hosted party (resolveRemoteNegAction returns NotFound to a
		// non-party) and return its full recorded history (2026-06-06 parity).
		if isOTCNegotiationNotFound(err) {
			rc, ok, rerr := h.resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				remoteRevs, lerr := h.negotiations.ListRevisionsUnchecked(rc.row.ID)
				if lerr != nil {
					return nil, status.Errorf(codes.Internal, "list remote revisions: %v", lerr)
				}
				return &stockpb.ListNegotiationRevisionsResponse{Revisions: revsToProto(remoteRevs)}, nil
			}
		}
		return nil, err
	}
	return &stockpb.ListNegotiationRevisionsResponse{Revisions: revsToProto(revs)}, nil
}

// revsToProto maps revision rows onto the wire shape. action_by_wire_id is set for
// remote revisions (the mover's opaque id) and empty for local ones.
func revsToProto(revs []model.OTCNegotiationRevision) []*stockpb.OTCNegotiationRevisionResponse {
	out := make([]*stockpb.OTCNegotiationRevisionResponse, 0, len(revs))
	for i := range revs {
		r := &revs[i]
		wire := ""
		if r.RemoteActorWireID != nil {
			wire = *r.RemoteActorWireID
		}
		out = append(out, &stockpb.OTCNegotiationRevisionResponse{
			Id:                    r.ID,
			NegotiationId:         r.NegotiationID,
			RevisionNumber:        int32(r.RevisionNumber),
			Action:                r.Action,
			Quantity:              r.Quantity.String(),
			StrikePrice:           r.StrikePrice.String(),
			Premium:               r.Premium.String(),
			SettlementDate:        r.SettlementDate.UTC().Format(time.RFC3339),
			ActionByPrincipalType: r.ModifiedByPrincipalType,
			ActionByPrincipalId:   r.ModifiedByPrincipalID,
			ActionByWireId:        wire,
			CreatedAt:             r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ListNegotiationsByListing returns the chains on a single OTC listing (SP-1
// Task 8b unifies local + remote).
//
// LOCAL :id (a local OTCOffer) — UNCHANGED behavior: the listing's poster (or a
// permission-gated employee) sees ALL chains on it. Each item is now stamped
// kind="local" + own provenance; me_owner follows the negotiation rule (the
// poster owns the LISTING, but each chain's me_owner reflects the chain's
// BIDDER ownership, which is false for bids on someone else's listing).
//
// REMOTE :id (resolves to a folded-in remote OTCOffer row, NOT a local offer)
// — we do NOT host the listing, so per spec §6 (umbrella req 6) we can only
// surface the CALLER'S OWN chain(s) against it, never other parties'. We match
// the caller's peer_otc_negotiation rows on (ParentOfferRouting, ParentOfferID)
// == (mirror.RoutingNumber, mirror.NativeID). No chain → empty list. An :id
// that is neither a local offer nor a remote row → NotFound (as today).
func (h *OTCOptionsHandler) ListNegotiationsByListing(ctx context.Context, in *stockpb.ListNegotiationsByListingRequest) (*stockpb.ListNegotiationsResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	parentOffer, rows, err := h.negotiations.ListByParentOffer(ctx, in.GetParentOfferId(), ot, oid)
	if err != nil {
		// Not a local listing — try the cross-bank mirror and surface the
		// caller's own chain(s) before returning NotFound.
		if isOTCOfferNotFound(err) {
			remote, ok, rerr := h.remoteListingOwnChains(in.GetParentOfferId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				return &stockpb.ListNegotiationsResponse{
					Negotiations: remote,
					Total:        int64(len(remote)),
				}, nil
			}
		}
		return nil, err
	}
	// me_owner = caller is the parent listing's poster/seller (per spec §5,
	// a negotiation's me_owner ⇔ the caller owns the PARENT OFFER). All
	// chains on this listing share the same parent offer, so me_owner is
	// identical for every item — compute it once from the parent offer's
	// initiator identity. authorizeListingAudience already fetched the parent
	// offer and ListByParentOffer now returns it, so no extra DB round-trip.
	meOwner := otcMeOwner(
		string(ot), model.OwnerIDOrZero(oid),
		"local", sellerIDForOwner(parentOffer.InitiatorOwnerType, parentOffer.InitiatorOwnerID),
	)
	out := make([]*stockpb.OTCNegotiationResponse, 0, len(rows))
	for i := range rows {
		item := negToProto(&rows[i])
		item.Kind = kindFromLocal(rows[i].Local)
		item.RoutingNumber = h.ownRouting
		item.BankCode = h.ownBankCode
		item.MeOwner = meOwner
		out = append(out, item)
	}

	// REMOTE merge — a PEER bidding on our LOCAL listing creates a REMOTE row
	// where WE host the seller (the listing's poster). Surface those bids to the
	// owner so they can act on them. Applies to BOTH bank-owned and client-owned
	// listings: the seller principal is derived from the listing's INITIATOR (not
	// the caller), so the poster AND a permission-gated employee (otc.read.all)
	// both see the cross-bank bids. Correlation is by the (RemoteParentRouting,
	// RemoteParentNativeID) lot key == the parent offer's (RoutingNumber, native).
	peerRows, perr := h.remoteBidderChainsOnLocalListing(parentOffer)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
	}
	for i := range peerRows {
		item, _ := peerNegToProto(&peerRows[i], h.ownRouting)
		if item == nil {
			continue
		}
		// me_owner: the listing's poster (we host the seller) owns the parent
		// offer — uniform with the local branch.
		item.MeOwner = meOwner
		out = append(out, item)
	}

	return &stockpb.ListNegotiationsResponse{
		Negotiations: out,
		Total:        int64(len(out)),
	}, nil
}

// remoteBidderChainsOnLocalListing returns the REMOTE chains a peer placed on a
// LOCAL listing WE host, correlated to that listing by (seller, ticker): the
// query scopes to chains WE host this exact seller of, then the ticker uniquely
// identifies the listing (one open offer per owner+ticker+direction). It does NOT
// key on the cross-bank lot key — the termless /public-stock wire carries no
// offer id, so a peer bid carries either no parentOfferId or our "ps:" shell id,
// never the surrogate id, and a lot-key match would drop every cross-bank bid.
// The seller principal is derived from the listing's INITIATOR so both the poster
// and a permission-gated employee see the same bids:
//   - bank-owned listing   → seller wire id "employee-<N>"   (ListRemoteNegByBankParty seller)
//   - client-owned listing → seller wire id "client-<initiatorID>" (ListRemoteNegByClient seller)
//
// Returns nil (no error) when the cross-bank lister isn't wired or the initiator
// has no cross-bank seller identity. Shared by ListNegotiationsByListing (P3) and
// GetOfferTimeline (P1 owner facet) so the two views never diverge.
func (h *OTCOptionsHandler) remoteBidderChainsOnLocalListing(parentOffer *model.OTCOffer) ([]model.OTCNegotiation, error) {
	if h.peerNegs == nil || parentOffer == nil {
		return nil, nil
	}
	var rows []model.OTCNegotiation
	var err error
	switch parentOffer.InitiatorOwnerType {
	case model.OwnerBank:
		rows, err = h.peerNegs.ListRemoteNegByBankParty(h.ownRouting, "seller")
	case model.OwnerClient:
		if parentOffer.InitiatorOwnerID == nil {
			return nil, nil
		}
		principal := "client-" + strconv.FormatUint(*parentOffer.InitiatorOwnerID, 10)
		rows, err = h.peerNegs.ListRemoteNegByClient(h.ownRouting, principal, "seller")
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Correlate by TICKER, not by the parent lot key. The termless /public-stock
	// wire carries NO offer id, so a peer's bid on our listing has either no
	// parentOfferId (a peer that omits it — e.g. our Banka-4 cohort partner, which
	// leaves RemoteParentNativeID nil) or our shell id "ps:<rn>:<seller>:<ticker>"
	// — NEVER the local surrogate id the old filter demanded. So the old lot-key
	// match silently dropped EVERY cross-bank bid and the seller saw none of them.
	// The query above already scoped rows to chains WE host this exact seller of,
	// so within that set the ticker uniquely identifies the listing (the partial
	// unique index guarantees one open offer per owner+ticker+direction).
	out := make([]model.OTCNegotiation, 0, len(rows))
	for i := range rows {
		row := rows[i]
		if remoteNegTicker(&row) != parentOffer.Ticker {
			continue // a chain on a different ticker (different listing)
		}
		out = append(out, row)
	}
	return out, nil
}

// remoteNegTicker returns a remote chain's ticker, read from its authoritative
// RemoteOfferJSON (serialised sitx.OtcOffer). OTCNegotiation has no ticker
// column. Empty on a nil row or a malformed/absent body — which the ticker
// filter treats as "not this listing".
func remoteNegTicker(row *model.OTCNegotiation) string {
	if row == nil {
		return ""
	}
	var offer contractsitx.OtcOffer
	if err := json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer); err != nil {
		return ""
	}
	return offer.Ticker
}

// isOTCOfferNotFound reports whether an error means "the parent listing is not
// a local OTCOffer". Both the service sentinel and the raw GORM not-found are
// matched so the remote-mirror fallback fires for either.
func isOTCOfferNotFound(err error) bool {
	return errors.Is(err, service.ErrOTCOfferNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// remoteListingOwnChains resolves a folded-in remote OTCOffer row by surrogate
// id and returns the CALLER'S OWN peer negotiation chain(s) against it, stamped
// kind="remote". The bool is false when the id is not a remote mirror (so the
// caller should surface the original local NotFound). We never return other
// parties' chains on a listing we don't host (spec §6 umbrella req 6).
//
// Both CLIENT and BANK callers have a cross-bank bidder identity: clients via
// ListRemoteNegByClient (exact "client-<N>"), the bank via
// ListRemoteNegByBankParty(role="buyer") (prefix-matched "employee-<N>", SP-3
// Task 5b). All other callers yield an empty (ok=true) list.
func (h *OTCOptionsHandler) remoteListingOwnChains(
	listingID uint64, callerOwnerType model.OwnerType, callerOwnerID *uint64,
) ([]*stockpb.OTCNegotiationResponse, bool, error) {
	if h.remoteOffers == nil {
		return nil, false, nil
	}
	mirror, err := h.remoteOffers.GetRemoteByID(listingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil // not a remote listing either
		}
		return nil, false, status.Errorf(codes.Internal, "remote listing lookup failed: %v", err)
	}
	// A remote listing exists. Surface the CALLER'S OWN chains against it,
	// scoped to this listing by the (RemoteParentRouting, RemoteParentNativeID)
	// lot key. Two principal kinds have a cross-bank bidder identity:
	//
	//   - CLIENT (cross-bank party id "client-<N>"): match via ListRemoteNegByClient.
	//   - BANK (an employee acting AS THE BANK; party id "employee-<N>"): match by
	//     prefix via ListRemoteNegByBankParty(role="buyer") — the bank bids on
	//     remote listings as a buyer (SP-3 Task 5b completeness).
	//
	// Callers that are neither produce an empty (ok=true) result (no chains).
	if h.peerNegs == nil {
		return []*stockpb.OTCNegotiationResponse{}, true, nil
	}
	mirrorNativeID := ""
	if mirror.NativeID != nil {
		mirrorNativeID = *mirror.NativeID
	}
	var peerRows []model.OTCNegotiation
	var perr error
	switch {
	case callerOwnerType == model.OwnerClient && callerOwnerID != nil:
		principal := "client-" + strconv.FormatUint(*callerOwnerID, 10)
		peerRows, perr = h.peerNegs.ListRemoteNegByClient(h.ownRouting, principal, "")
	case callerOwnerType == model.OwnerBank:
		peerRows, perr = h.peerNegs.ListRemoteNegByBankParty(h.ownRouting, "buyer")
	default:
		return []*stockpb.OTCNegotiationResponse{}, true, nil
	}
	if perr != nil {
		return nil, false, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
	}
	out := make([]*stockpb.OTCNegotiationResponse, 0)
	for i := range peerRows {
		row := &peerRows[i]
		// Match on the precise lot key carried by the bidder at initiate time.
		if row.RemoteParentRouting == nil || row.RemoteParentNativeID == nil {
			continue
		}
		if *row.RemoteParentRouting != mirror.RoutingNumber || *row.RemoteParentNativeID != mirrorNativeID {
			continue
		}
		if item, _ := peerNegToProto(row, h.ownRouting); item != nil {
			out = append(out, item)
		}
	}
	return out, true, nil
}

// GetOfferTimeline returns the parent offer plus every chain's revisions
// merged and sorted by created_at — the poster's cross-chain audit view.
// Audience authorization is enforced in the service layer (SP-1 Task 8b adds
// remote-id handling).
//
// LOCAL :id — UNCHANGED: the full cross-chain timeline of the local listing.
//
// REMOTE :id (resolves to a folded-in remote OTCOffer row) — we don't host the
// listing, so we surface only the CALLER'S OWN chain(s) (spec §6 umbrella req
// 6). The mirror provides the offer header; each of the caller's peer chains
// against it becomes one timeline entry (the peer mirror keeps only current
// terms, not a per-revision history). No chain → offer header + empty timeline.
func (h *OTCOptionsHandler) GetOfferTimeline(ctx context.Context, in *stockpb.GetOfferTimelineRequest) (*stockpb.GetOfferTimelineResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	ot, err := ownerTypeFromProto(in.GetCallerOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetCallerOwnerId())
	if err != nil {
		return nil, err
	}
	offer, items, err := h.negotiations.OfferTimeline(ctx, in.GetParentOfferId(), ot, oid)
	if err != nil {
		// Not a local listing — try the cross-bank mirror.
		if isOTCOfferNotFound(err) {
			remote, ok, rerr := h.remoteOfferTimeline(in.GetParentOfferId(), ot, oid)
			if rerr != nil {
				return nil, rerr
			}
			if ok {
				return remote, nil
			}
		}
		return nil, err
	}
	timeline := make([]*stockpb.OTCTimelineEntry, 0, len(items))
	for i := range items {
		neg := items[i].Negotiation
		r := items[i].Revision
		bidderID := uint64(0)
		if neg.BidderOwnerID != nil {
			bidderID = *neg.BidderOwnerID
		}
		timeline = append(timeline, &stockpb.OTCTimelineEntry{
			NegotiationId:         neg.ID,
			BidderOwnerType:       string(neg.BidderOwnerType),
			BidderOwnerId:         bidderID,
			RevisionNumber:        int32(r.RevisionNumber),
			Action:                r.Action,
			Quantity:              r.Quantity.String(),
			StrikePrice:           r.StrikePrice.String(),
			Premium:               r.Premium.String(),
			SettlementDate:        r.SettlementDate.UTC().Format(time.RFC3339),
			ActionByPrincipalType: r.ModifiedByPrincipalType,
			ActionByPrincipalId:   r.ModifiedByPrincipalID,
			CreatedAt:             r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	// Merge the REMOTE chains a peer placed on this LOCAL listing (P1: the
	// timeline must show BOTH local and remote chains, not just local). Correlated
	// to this listing by (seller, ticker) via the shared helper — the termless wire
	// carries no offer id, so a lot-key match would drop every cross-bank bid — and
	// never pulls chains on another ticker.
	peerRows, perr := h.remoteBidderChainsOnLocalListing(offer)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
	}
	for i := range peerRows {
		// Expand each remote chain into its full recorded history (one entry per
		// revision), falling back to a single current-terms entry for legacy rows.
		timeline = append(timeline, h.remoteChainTimelineEntries(&peerRows[i])...)
	}
	// Keep the merged local+remote stream chronological (the documented contract).
	// RFC3339 UTC strings sort lexically == chronologically.
	sort.SliceStable(timeline, func(a, b int) bool {
		return timeline[a].GetCreatedAt() < timeline[b].GetCreatedAt()
	})
	// me_owner = caller owns the parent listing (same rule as GetOffer/ListNegotiationsByListing).
	// OfferTimeline is the poster's cross-chain audit view; all chains belong
	// to the same listing, so me_owner is uniform and computed once.
	offerProto := toOTCOfferProto(offer, false)
	offerProto.Kind = kindFromLocal(offer.Local)
	offerProto.RoutingNumber = h.ownRouting
	offerProto.BankCode = h.ownBankCode
	offerProto.MeOwner = otcMeOwner(
		string(ot), model.OwnerIDOrZero(oid),
		offerProto.Kind, sellerIDForOwner(offer.InitiatorOwnerType, offer.InitiatorOwnerID),
	)
	return &stockpb.GetOfferTimelineResponse{
		Offer:    offerProto,
		Timeline: timeline,
	}, nil
}

// remoteOfferTimeline builds a timeline response for a folded-in remote
// OTCOffer row id, surfacing ONLY the caller's own peer chain(s) against that
// listing (spec §6 umbrella req 6 — we never expose other parties' chains on a
// listing we don't host). The bool is false when the id is not a remote row (so
// the caller surfaces the original local NotFound). The remote row provides the
// offer header; each of the caller's matching peer chains becomes one timeline
// entry. Both CLIENT and BANK callers have a cross-bank bidder identity (SP-3
// Task 5b completeness); all other callers return a header + empty timeline.
func (h *OTCOptionsHandler) remoteOfferTimeline(
	listingID uint64, callerOwnerType model.OwnerType, callerOwnerID *uint64,
) (*stockpb.GetOfferTimelineResponse, bool, error) {
	if h.remoteOffers == nil {
		return nil, false, nil
	}
	mirror, err := h.remoteOffers.GetRemoteByID(listingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil // not a remote listing either
		}
		return nil, false, status.Errorf(codes.Internal, "remote listing lookup failed: %v", err)
	}
	offer := remoteOfferToProto(mirror)
	mirrorNativeID := ""
	if mirror.NativeID != nil {
		mirrorNativeID = *mirror.NativeID
	}

	// Surface the CALLER'S OWN chain(s) against this remote listing. Two
	// principal kinds have a cross-bank bidder identity; all others return a
	// header + empty timeline.
	//
	//   - CLIENT (cross-bank party id "client-<N>"): match via ListRemoteNegByClient.
	//   - BANK (an employee acting AS THE BANK; party id "employee-<N>"): match
	//     by prefix via ListRemoteNegByBankParty(role="buyer") — the bank bids
	//     on remote listings as the buyer (SP-3 Task 5b completeness).
	if h.peerNegs == nil {
		return &stockpb.GetOfferTimelineResponse{Offer: offer, Timeline: []*stockpb.OTCTimelineEntry{}}, true, nil
	}
	var peerRows []model.OTCNegotiation
	var perr error
	switch {
	case callerOwnerType == model.OwnerClient && callerOwnerID != nil:
		principal := "client-" + strconv.FormatUint(*callerOwnerID, 10)
		peerRows, perr = h.peerNegs.ListRemoteNegByClient(h.ownRouting, principal, "")
	case callerOwnerType == model.OwnerBank:
		peerRows, perr = h.peerNegs.ListRemoteNegByBankParty(h.ownRouting, "buyer")
	default:
		return &stockpb.GetOfferTimelineResponse{Offer: offer, Timeline: []*stockpb.OTCTimelineEntry{}}, true, nil
	}
	if perr != nil {
		return nil, false, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
	}
	timeline := make([]*stockpb.OTCTimelineEntry, 0)
	for i := range peerRows {
		row := &peerRows[i]
		if row.RemoteParentRouting == nil || row.RemoteParentNativeID == nil {
			continue
		}
		if *row.RemoteParentRouting != mirror.RoutingNumber || *row.RemoteParentNativeID != mirrorNativeID {
			continue
		}
		// Expand the chain into its full recorded history (one entry per revision),
		// falling back to a single current-terms entry for legacy rows.
		timeline = append(timeline, h.remoteChainTimelineEntries(row)...)
	}
	// Keep the merged stream chronological (matters once a chain has multiple
	// revisions). RFC3339 UTC strings sort lexically == chronologically.
	sort.SliceStable(timeline, func(a, b int) bool {
		return timeline[a].GetCreatedAt() < timeline[b].GetCreatedAt()
	})
	return &stockpb.GetOfferTimelineResponse{Offer: offer, Timeline: timeline}, true, nil
}

// remoteChainTimelineEntries expands a REMOTE chain into timeline entries: one per
// recorded revision (full bid→counter→…→accept/reject history, 2026-06-06). Falls
// back to a single current-terms entry only when the chain has NO revisions yet
// (legacy chains created before history logging). Each entry carries the mover's
// role (action_by_principal_type = "buyer"/"seller") and exact wire id
// (action_by_wire_id).
func (h *OTCOptionsHandler) remoteChainTimelineEntries(row *model.OTCNegotiation) []*stockpb.OTCTimelineEntry {
	if h.negotiations != nil {
		if revs, err := h.negotiations.ListRevisionsUnchecked(row.ID); err == nil && len(revs) > 0 {
			out := make([]*stockpb.OTCTimelineEntry, 0, len(revs))
			for i := range revs {
				r := &revs[i]
				wire := ""
				if r.RemoteActorWireID != nil {
					wire = *r.RemoteActorWireID
				}
				out = append(out, &stockpb.OTCTimelineEntry{
					NegotiationId:         row.ID,
					RevisionNumber:        int32(r.RevisionNumber),
					Action:                r.Action,
					Quantity:              r.Quantity.String(),
					StrikePrice:           r.StrikePrice.String(),
					Premium:               r.Premium.String(),
					SettlementDate:        r.SettlementDate.UTC().Format(time.RFC3339),
					ActionByPrincipalType: r.ModifiedByPrincipalType,
					ActionByWireId:        wire,
					CreatedAt:             r.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
			return out
		}
	}
	// Legacy fallback: a single current-terms entry from the mirror snapshot.
	var off contractsitx.OtcOffer
	if jerr := json.Unmarshal([]byte(remoteOfferJSONOf(row)), &off); jerr != nil {
		log.Printf("WARN remoteChainTimelineEntries: row %d RemoteOfferJSON decode failed: %v", row.ID, jerr)
	}
	return []*stockpb.OTCTimelineEntry{{
		NegotiationId:  row.ID,
		Quantity:       strconv.FormatInt(off.Amount, 10),
		StrikePrice:    off.PricePerStock.String(),
		Premium:        off.Premium.String(),
		SettlementDate: off.SettlementDate,
		Action:         "COUNTER", // current terms only; no per-revision history yet
		CreatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339),
	}}
}

func optionalPtr(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

// mintedContractToProto projects a minted OptionContract onto the
// thin wire shape carried in OTCAcceptNegotiationResponse.contract.
// Returns nil for a nil input so the proto field stays unset when
// the negotiation state flipped but the formation saga failed
// (caller can detect this and surface a "minted=false" warning).
func mintedContractToProto(c *model.OptionContract) *stockpb.OTCMintedContract {
	if c == nil {
		return nil
	}
	buyerID := uint64(0)
	if c.BuyerOwnerID != nil {
		buyerID = *c.BuyerOwnerID
	}
	sellerID := uint64(0)
	if c.SellerOwnerID != nil {
		sellerID = *c.SellerOwnerID
	}
	return &stockpb.OTCMintedContract{
		Id:              c.ID,
		OfferId:         derefU64(c.OfferID),
		BuyerOwnerType:  string(c.BuyerOwnerType),
		BuyerOwnerId:    buyerID,
		SellerOwnerType: string(c.SellerOwnerType),
		SellerOwnerId:   sellerID,
		Ticker:          c.Ticker,
		Quantity:        c.Quantity.String(),
		StrikePrice:     c.StrikePrice.String(),
		PremiumPaid:     c.PremiumPaid.String(),
		PremiumCurrency: c.PremiumCurrency,
		StrikeCurrency:  c.StrikeCurrency,
		SettlementDate:  c.SettlementDate.UTC().Format(time.RFC3339),
		BuyerAccountId:  c.BuyerAccountID,
		SellerAccountId: c.SellerAccountID,
		Status:          c.Status,
		PremiumPaidAt:   c.PremiumPaidAt.UTC().Format(time.RFC3339),
	}
}

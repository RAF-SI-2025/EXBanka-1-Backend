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
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	result, err := h.negotiations.AcceptNegotiation(ctx, service.AcceptNegotiationInput{
		NegotiationID:       in.GetNegotiationId(),
		CallerOwnerType:     ot,
		CallerOwnerID:       oid,
		ActingPrincipalType: in.GetActingPrincipalType(),
		ActingPrincipalID:   in.GetActingPrincipalId(),
		ActingEmployeeID:    optionalPtr(in.GetActingEmployeeId()),
		AcceptorAccountID:   in.GetAcceptorAccountId(),
	})
	if err != nil {
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
	return &stockpb.CancelListingResponse{
		OfferId:         res.Offer.ID,
		Status:          res.Offer.Status,
		CancelledChains: out,
	}, nil
}

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
	return &stockpb.ListNegotiationsResponse{
		Negotiations: negsToProto(rows),
		Total:        total,
	}, nil
}

func (h *OTCOptionsHandler) ListNegotiationsByListing(ctx context.Context, in *stockpb.ListNegotiationsByListingRequest) (*stockpb.ListNegotiationsResponse, error) {
	if h.negotiations == nil {
		return nil, status.Error(codes.Unimplemented, "OTCNegotiationService not wired")
	}
	rows, err := h.negotiations.ListByParentOffer(ctx, in.GetParentOfferId())
	if err != nil {
		return nil, err
	}
	return &stockpb.ListNegotiationsResponse{
		Negotiations: negsToProto(rows),
		Total:        int64(len(rows)),
	}, nil
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
		OfferId:         c.OfferID,
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

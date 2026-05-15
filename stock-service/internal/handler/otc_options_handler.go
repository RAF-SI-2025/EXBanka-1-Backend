package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// OTCOptionsHandler implements stockpb.OTCOptionsServiceServer.
//
// Account-id resolution for Accept/Exercise: the gateway-side request
// validates that the caller passes BuyerAccountID/SellerAccountID; the
// gRPC layer forwards them through. (Same pattern as the existing
// stock OrderGRPCService — gateway resolves user identity, downstream
// service trusts the IDs.)
type OTCOptionsHandler struct {
	stockpb.UnimplementedOTCOptionsServiceServer
	svc           *service.OTCOfferService
	contracts     *repository.OptionContractRepository
	peerContracts *repository.PeerOptionContractRepository // optional; surfaces cross-bank contracts in /me/otc/contracts
	listings      *repository.ListingRepository            // optional; populates market_reference_price
	ownRouting    int64
}

func NewOTCOptionsHandler(svc *service.OTCOfferService, contracts *repository.OptionContractRepository) *OTCOptionsHandler {
	return &OTCOptionsHandler{svc: svc, contracts: contracts}
}

// WithPeerContracts wires the cross-bank option-contracts repository
// and this bank's routing number so ListMyContracts can also return
// peer_option_contracts rows where the caller is a participant.
func (h *OTCOptionsHandler) WithPeerContracts(peer *repository.PeerOptionContractRepository, ownRouting int64) *OTCOptionsHandler {
	cp := *h
	cp.peerContracts = peer
	cp.ownRouting = ownRouting
	return &cp
}

// WithListings wires the listing repo so contract / offer responses can
// surface market_reference_price for the UI to compute profit-vs-strike
// (Celina-4 §Sklopljeni ugovori "Profit" column).
func (h *OTCOptionsHandler) WithListings(listings *repository.ListingRepository) *OTCOptionsHandler {
	cp := *h
	cp.listings = listings
	return &cp
}

// marketRefPrice returns the listing's current price for the stock id, or
// "" when no listing is wired or the lookup fails.
func (h *OTCOptionsHandler) marketRefPrice(stockID uint64) string {
	if h.listings == nil {
		return ""
	}
	listing, err := h.listings.GetBySecurityIDAndType(stockID, "stock")
	if err != nil || listing == nil {
		return ""
	}
	return listing.Price.String()
}

func (h *OTCOptionsHandler) CreateOffer(ctx context.Context, in *stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
	qty, err := decimal.NewFromString(in.Quantity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "quantity is not a valid decimal")
	}
	strike, err := decimal.NewFromString(in.StrikePrice)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "strike_price is not a valid decimal")
	}
	prem, err := decimal.NewFromString(in.Premium)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "premium is not a valid decimal")
	}
	settle, err := time.Parse("2006-01-02", in.SettlementDate)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "settlement_date must be YYYY-MM-DD")
	}
	if in.AccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	input := service.CreateOfferInput{
		ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		Direction: in.Direction, StockID: in.StockId,
		Ticker:   in.Ticker,
		Quantity: qty, StrikePrice: strike, Premium: prem,
		SettlementDate:     settle,
		InitiatorAccountID: in.AccountId,
	}
	if in.Counterparty != nil && in.Counterparty.UserId != 0 {
		uid := in.Counterparty.UserId
		st := in.Counterparty.SystemType
		input.CounterpartyUserID = &uid
		input.CounterpartySystemType = &st
	}
	o, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return h.withOfferMarketRef(o, toOTCOfferProto(o, false)), nil
}

func (h *OTCOptionsHandler) ListMyOffers(ctx context.Context, in *stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error) {
	rows, total, err := h.svc.ListMyOffers(in.ActorUserId, in.ActorSystemType, in.Role, in.Statuses, in.StockId, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListMyOTCOffersResponse{Total: total, Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		// Celina-4 §Aktivne ponude: unread = caller hasn't seen this update
		// yet. Computed from the read-receipt comparing to the offer's
		// updated_at. Caller is its own last-modifier => always read.
		unread := h.computeUnread(&rows[i], in.ActorUserId, in.ActorSystemType)
		out.Offers = append(out.Offers, h.withOfferMarketRef(&rows[i], toOTCOfferProto(&rows[i], unread)))
	}
	return out, nil
}

// ListNegotiationHistory returns terminal OTC offers (accepted, rejected,
// expired, failed) for the caller. Celina-3 "Istorija pregovora".
func (h *OTCOptionsHandler) ListNegotiationHistory(ctx context.Context, in *stockpb.ListNegotiationHistoryRequest) (*stockpb.ListMyOTCOffersResponse, error) {
	f := repository.HistoryFilter{
		Statuses: in.Statuses,
		Page:     int(in.Page),
		PageSize: int(in.PageSize),
	}
	if in.SinceUnix > 0 {
		t := time.Unix(in.SinceUnix, 0).UTC()
		f.Since = &t
	}
	if in.UntilUnix > 0 {
		t := time.Unix(in.UntilUnix, 0).UTC()
		f.Until = &t
	}
	if in.CounterpartyId > 0 {
		cp := in.CounterpartyId
		f.CounterpartyID = &cp
	}
	rows, total, err := h.svc.ListNegotiationHistory(in.ActorUserId, in.ActorSystemType, f)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListMyOTCOffersResponse{Total: total, Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		// History entries are immutable from the caller's perspective so
		// "unread" is always false — they're explicitly viewing past data.
		out.Offers = append(out.Offers, h.withOfferMarketRef(&rows[i], toOTCOfferProto(&rows[i], false)))
	}
	return out, nil
}

// computeUnread returns true if the offer has been touched since the
// caller last opened it AND the caller wasn't the one who touched it.
func (h *OTCOptionsHandler) computeUnread(o *model.OTCOffer, callerID int64, callerType string) bool {
	if o.LastModifiedByPrincipalID == uint64(callerID) && o.LastModifiedByPrincipalType == callerType {
		return false
	}
	rec, err := h.svc.LastReadReceipt(callerID, callerType, o.ID)
	if err != nil || rec == nil {
		return true // never opened
	}
	return o.UpdatedAt.After(rec.LastSeenUpdatedAt)
}

func (h *OTCOptionsHandler) GetOffer(ctx context.Context, in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
	o, revs, err := h.svc.GetOffer(in.OfferId, in.ActorUserId, in.ActorSystemType)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	out := &stockpb.OTCOfferDetailResponse{
		Offer:     h.withOfferMarketRef(o, toOTCOfferProto(o, false)),
		Revisions: make([]*stockpb.OTCOfferRevisionItem, 0, len(revs)),
	}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, &stockpb.OTCOfferRevisionItem{
			RevisionNumber: int32(r.RevisionNumber),
			Quantity:       r.Quantity.String(),
			StrikePrice:    r.StrikePrice.String(),
			Premium:        r.Premium.String(),
			SettlementDate: r.SettlementDate.Format("2006-01-02"),
			Action:         r.Action,
			ModifiedBy:     &stockpb.PartyRef{UserId: int64(r.ModifiedByPrincipalID), SystemType: r.ModifiedByPrincipalType},
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

func (h *OTCOptionsHandler) CounterOffer(ctx context.Context, in *stockpb.CounterOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
	qty, err := decimal.NewFromString(in.Quantity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "quantity is not a valid decimal")
	}
	strike, err := decimal.NewFromString(in.StrikePrice)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "strike_price is not a valid decimal")
	}
	prem, err := decimal.NewFromString(in.Premium)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "premium is not a valid decimal")
	}
	settle, err := time.Parse("2006-01-02", in.SettlementDate)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "settlement_date must be YYYY-MM-DD")
	}
	o, err := h.svc.Counter(ctx, service.CounterInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		Quantity: qty, StrikePrice: strike, Premium: prem, SettlementDate: settle,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return h.withOfferMarketRef(o, toOTCOfferProto(o, false)), nil
}

func (h *OTCOptionsHandler) AcceptOffer(ctx context.Context, in *stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error) {
	if in.AccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	c, err := h.svc.Accept(ctx, service.AcceptInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		AcceptorAccountID: in.AccountId,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return &stockpb.AcceptOfferResponse{
		OfferId: c.OfferID, ContractId: c.ID, Status: c.Status,
		SagaId: c.SagaID, Contract: h.withMarketRef(c, toContractProto(c)),
	}, nil
}

func (h *OTCOptionsHandler) RejectOffer(ctx context.Context, in *stockpb.RejectOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
	o, err := h.svc.Reject(ctx, service.RejectInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return h.withOfferMarketRef(o, toOTCOfferProto(o, false)), nil
}

func (h *OTCOptionsHandler) ListMyContracts(ctx context.Context, in *stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error) {
	if h.contracts == nil {
		return &stockpb.ListContractsResponse{}, nil
	}
	ownerType, ownerID := model.OwnerFromLegacy(uint64(in.ActorUserId), in.ActorSystemType)
	rows, total, err := h.contracts.ListByOwner(ownerType, ownerID, in.Role, in.Statuses, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListContractsResponse{Total: total, Contracts: make([]*stockpb.OptionContractResponse, 0, len(rows))}
	for i := range rows {
		out.Contracts = append(out.Contracts, h.withMarketRef(&rows[i], toContractProto(&rows[i])))
	}

	// Cross-bank contracts. Only fetched when the peer-contract repo is
	// wired (post-Celina-5) AND the caller is a client (cross-bank
	// participant ids are "client-<n>"; bank-side cross-bank contracts
	// surface elsewhere). page/page_size pass through unchanged so the
	// peer list paginates the same way as the intra-bank list.
	if h.peerContracts != nil && ownerType == model.OwnerClient && ownerID != nil {
		participantID := "client-" + strconv.FormatUint(*ownerID, 10)
		peerRows, peerTotal, perr := h.peerContracts.ListByLocalParticipant(participantID, h.ownRouting, in.Role, int(in.Page), int(in.PageSize))
		if perr != nil {
			return nil, status.Errorf(codes.Internal, "list peer contracts: %v", perr)
		}
		out.PeerContracts = make([]*stockpb.PeerOptionContractResponse, 0, len(peerRows))
		out.PeerTotal = peerTotal
		for i := range peerRows {
			out.PeerContracts = append(out.PeerContracts, peerContractToProto(&peerRows[i]))
		}
	}

	return out, nil
}

// peerContractToProto translates a PeerOptionContract row into the
// wire-shape PeerOptionContractResponse for ListMyContracts callers.
func peerContractToProto(p *model.PeerOptionContract) *stockpb.PeerOptionContractResponse {
	return &stockpb.PeerOptionContractResponse{
		Id:                       p.ID,
		CrossbankTxId:            p.CrossbankTxID,
		PostingIndex:             p.PostingIndex,
		NegotiationRoutingNumber: p.NegotiationRoutingNumber,
		NegotiationId:            p.NegotiationID,
		BuyerId:                  &stockpb.PeerForeignBankId{RoutingNumber: p.BuyerRoutingNumber, Id: p.BuyerID},
		SellerId:                 &stockpb.PeerForeignBankId{RoutingNumber: p.SellerRoutingNumber, Id: p.SellerID},
		Ticker:                   p.Ticker,
		Quantity:                 p.Quantity,
		StrikePrice:              p.StrikePrice.String(),
		Currency:                 p.Currency,
		SettlementDate:           p.SettlementDate,
		Direction:                p.Direction,
		Status:                   p.Status,
		CreatedAtUnix:            p.CreatedAt.Unix(),
	}
}

func (h *OTCOptionsHandler) GetContract(ctx context.Context, in *stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error) {
	if h.contracts == nil {
		return nil, status.Error(codes.Unimplemented, "contracts repo not wired")
	}
	c, err := h.contracts.GetByID(in.ContractId)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserId), in.ActorSystemType)
	isBuyer := c.BuyerOwnerType == actorOwnerType && ownerIDEqual(c.BuyerOwnerID, actorOwnerID)
	isSeller := c.SellerOwnerType == actorOwnerType && ownerIDEqual(c.SellerOwnerID, actorOwnerID)
	if !isBuyer && !isSeller {
		return nil, status.Error(codes.PermissionDenied, "not a participant")
	}
	return h.withMarketRef(c, toContractProto(c)), nil
}

func (h *OTCOptionsHandler) ExerciseContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
	c, err := h.svc.ExerciseContract(ctx, service.ExerciseInput{
		ContractID: in.ContractId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	// The saga doesn't return per-currency amounts — surface the seller-side
	// figure here. Cross-currency callers can compute the buyer-side
	// amount client-side via the exchange rate they observed.
	strikeAmt := c.Quantity.Mul(c.StrikePrice)
	return &stockpb.ExerciseResponse{
		ContractId:            c.ID,
		Status:                c.Status,
		SagaId:                c.SagaID,
		StrikeAmountSellerCcy: strikeAmt.String(),
		StrikeAmountBuyerCcy:  strikeAmt.String(),
		SellerCurrency:        c.StrikeCurrency,
		BuyerCurrency:         c.StrikeCurrency,
		SharesTransferred:     c.Quantity.String(),
	}, nil
}

func toContractProto(c *model.OptionContract) *stockpb.OptionContractResponse {
	resp := &stockpb.OptionContractResponse{
		Id:              c.ID,
		OfferId:         c.OfferID,
		StockId:         c.StockID,
		Quantity:        c.Quantity.String(),
		StrikePrice:     c.StrikePrice.String(),
		PremiumPaid:     c.PremiumPaid.String(),
		PremiumCurrency: c.PremiumCurrency,
		StrikeCurrency:  c.StrikeCurrency,
		SettlementDate:  c.SettlementDate.Format("2006-01-02"),
		Status:          c.Status,
		Buyer:           &stockpb.PartyRef{UserId: int64(model.OwnerIDOrZero(c.BuyerOwnerID)), SystemType: string(c.BuyerOwnerType)},
		Seller:          &stockpb.PartyRef{UserId: int64(model.OwnerIDOrZero(c.SellerOwnerID)), SystemType: string(c.SellerOwnerType)},
		PremiumPaidAt:   c.PremiumPaidAt.Format(time.RFC3339),
		CreatedAt:       c.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       c.UpdatedAt.Format(time.RFC3339),
		Version:         c.Version,
	}
	if c.ExercisedAt != nil {
		resp.ExercisedAt = c.ExercisedAt.Format(time.RFC3339)
	}
	if c.ExpiredAt != nil {
		resp.ExpiredAt = c.ExpiredAt.Format(time.RFC3339)
	}
	return resp
}

// withMarketRef returns a fresh response with MarketReferencePrice
// populated. Caller wraps a base proto so the toContractProto helper can
// stay simple and free of repo dependencies.
func (h *OTCOptionsHandler) withMarketRef(c *model.OptionContract, resp *stockpb.OptionContractResponse) *stockpb.OptionContractResponse {
	resp.MarketReferencePrice = h.marketRefPrice(c.StockID)
	return resp
}

// withOfferMarketRef populates MarketReferencePrice on an offer response.
func (h *OTCOptionsHandler) withOfferMarketRef(o *model.OTCOffer, resp *stockpb.OTCOfferResponse) *stockpb.OTCOfferResponse {
	resp.MarketReferencePrice = h.marketRefPrice(o.StockID)
	return resp
}

func toOTCOfferProto(o *model.OTCOffer, unread bool) *stockpb.OTCOfferResponse {
	resp := &stockpb.OTCOfferResponse{
		Id:             o.ID,
		Direction:      o.Direction,
		StockId:        o.StockID,
		Quantity:       o.Quantity.String(),
		StrikePrice:    o.StrikePrice.String(),
		Premium:        o.Premium.String(),
		SettlementDate: o.SettlementDate.Format("2006-01-02"),
		Status:         o.Status,
		Initiator: &stockpb.PartyRef{
			UserId: int64(model.OwnerIDOrZero(o.InitiatorOwnerID)), SystemType: string(o.InitiatorOwnerType),
		},
		LastModifiedBy: &stockpb.PartyRef{
			UserId: int64(o.LastModifiedByPrincipalID), SystemType: o.LastModifiedByPrincipalType,
		},
		CreatedAt: o.CreatedAt.Format(time.RFC3339),
		UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
		Version:   o.Version,
		Unread:    unread,
	}
	if o.CounterpartyOwnerType != nil {
		resp.Counterparty = &stockpb.PartyRef{
			UserId:     int64(model.OwnerIDOrZero(o.CounterpartyOwnerID)),
			SystemType: string(*o.CounterpartyOwnerType),
		}
	}
	return resp
}

// ownerIDEqual reports whether two nullable owner-id pointers reference the
// same logical owner. Both nil = same (bank == bank). Mirror of the helper in
// internal/service.
func ownerIDEqual(a, b *uint64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// mapOTCErr is now a passthrough. Service-layer sentinels (and the typed
// repository.ErrOptimisticLock sentinel) carry their own gRPC code via
// svcerr.SentinelError. The bare gorm.ErrRecordNotFound branch remains
// because some legacy paths still surface the raw GORM error.
func mapOTCErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	return err
}

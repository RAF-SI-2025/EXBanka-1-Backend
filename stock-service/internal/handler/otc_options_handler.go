package handler

import (
	"context"
	"errors"
	"strings"
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
	svc       *service.OTCOfferService
	contracts *repository.OptionContractRepository
}

func NewOTCOptionsHandler(svc *service.OTCOfferService, contracts *repository.OptionContractRepository) *OTCOptionsHandler {
	return &OTCOptionsHandler{svc: svc, contracts: contracts}
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
	input := service.CreateOfferInput{
		ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		Direction: in.Direction, StockID: in.StockId,
		Quantity: qty, StrikePrice: strike, Premium: prem,
		SettlementDate: settle,
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
	return toOTCOfferProto(o, false), nil
}

func (h *OTCOptionsHandler) ListMyOffers(ctx context.Context, in *stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error) {
	rows, total, err := h.svc.ListMyOffers(in.ActorUserId, in.ActorSystemType, in.Role, in.Statuses, in.StockId, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListMyOTCOffersResponse{Total: total, Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		out.Offers = append(out.Offers, toOTCOfferProto(&rows[i], false))
	}
	return out, nil
}

func (h *OTCOptionsHandler) GetOffer(ctx context.Context, in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
	o, revs, err := h.svc.GetOffer(in.OfferId, in.ActorUserId, in.ActorSystemType)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	out := &stockpb.OTCOfferDetailResponse{
		Offer:     toOTCOfferProto(o, false),
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
			ModifiedBy:     &stockpb.PartyRef{UserId: r.ModifiedByUserID, SystemType: r.ModifiedBySystemType},
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
	return toOTCOfferProto(o, false), nil
}

func (h *OTCOptionsHandler) AcceptOffer(ctx context.Context, in *stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error) {
	if in.BuyerAccountId == 0 || in.SellerAccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_account_id and seller_account_id are required")
	}
	c, err := h.svc.Accept(ctx, service.AcceptInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		BuyerAccountID: in.BuyerAccountId, SellerAccountID: in.SellerAccountId,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return &stockpb.AcceptOfferResponse{
		OfferId: c.OfferID, ContractId: c.ID, Status: c.Status,
		SagaId: c.SagaID, Contract: toContractProto(c),
	}, nil
}

func (h *OTCOptionsHandler) RejectOffer(ctx context.Context, in *stockpb.RejectOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
	o, err := h.svc.Reject(ctx, service.RejectInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return toOTCOfferProto(o, false), nil
}

func (h *OTCOptionsHandler) ListMyContracts(ctx context.Context, in *stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error) {
	if h.contracts == nil {
		return &stockpb.ListContractsResponse{}, nil
	}
	rows, total, err := h.contracts.ListByOwner(in.ActorUserId, in.ActorSystemType, in.Role, in.Statuses, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListContractsResponse{Total: total, Contracts: make([]*stockpb.OptionContractResponse, 0, len(rows))}
	for i := range rows {
		out.Contracts = append(out.Contracts, toContractProto(&rows[i]))
	}
	return out, nil
}

func (h *OTCOptionsHandler) GetContract(ctx context.Context, in *stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error) {
	if h.contracts == nil {
		return nil, status.Error(codes.Unimplemented, "contracts repo not wired")
	}
	c, err := h.contracts.GetByID(in.ContractId)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	if (c.BuyerUserID != in.ActorUserId || c.BuyerSystemType != in.ActorSystemType) &&
		(c.SellerUserID != in.ActorUserId || c.SellerSystemType != in.ActorSystemType) {
		return nil, status.Error(codes.PermissionDenied, "not a participant")
	}
	return toContractProto(c), nil
}

func (h *OTCOptionsHandler) ExerciseContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
	if in.BuyerAccountId == 0 || in.SellerAccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_account_id and seller_account_id are required")
	}
	c, err := h.svc.ExerciseContract(ctx, service.ExerciseInput{
		ContractID: in.ContractId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		BuyerAccountID: in.BuyerAccountId, SellerAccountID: in.SellerAccountId,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
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
		Buyer:           &stockpb.PartyRef{UserId: c.BuyerUserID, SystemType: c.BuyerSystemType},
		Seller:          &stockpb.PartyRef{UserId: c.SellerUserID, SystemType: c.SellerSystemType},
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
			UserId: o.InitiatorUserID, SystemType: o.InitiatorSystemType,
		},
		LastModifiedBy: &stockpb.PartyRef{
			UserId: o.LastModifiedByUserID, SystemType: o.LastModifiedBySystemType,
		},
		CreatedAt: o.CreatedAt.Format(time.RFC3339),
		UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
		Version:   o.Version,
		Unread:    unread,
	}
	if o.CounterpartyUserID != nil {
		resp.Counterparty = &stockpb.PartyRef{
			UserId:     *o.CounterpartyUserID,
			SystemType: derefStr(o.CounterpartySystemType),
		}
	}
	return resp
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mapOTCErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	if errors.Is(err, repository.ErrOptimisticLock) {
		return status.Error(codes.Aborted, err.Error())
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "insufficient available shares"),
		strings.Contains(msg, "settlement_date"),
		strings.Contains(msg, "terminal state"):
		return status.Error(codes.FailedPrecondition, msg)
	case strings.Contains(msg, "only the contract buyer"),
		strings.Contains(msg, "cannot accept your own"),
		strings.Contains(msg, "cannot counter your own"),
		strings.Contains(msg, "not a participant"):
		return status.Error(codes.PermissionDenied, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// HoldingReader is the subset of HoldingRepository methods that
// PeerOTCGRPCHandler needs for GetPublicStocks. Decoupled for testability.
type HoldingReader interface {
	ListPublic() ([]model.Holding, error)
}

// PeerOTCGRPCHandler implements stockpb.PeerOTCServiceServer.
//
// GetPublicStocks queries the local holdings table for rows flagged
// public_quantity > 0 and returns them as PeerPublicStock entries.
//
// Negotiation lifecycle: peers POST/PUT/GET/DELETE on
// /negotiations/{rid}/{id}; we persist in peer_otc_negotiations.
//
// Acceptance: GET /negotiations/{rid}/{id}/accept composes 4 postings
// (premium money + 1× OptionDescription both directions) and dispatches
// via transaction-service.PeerTxService.InitiateOutboundTxWithPostings.
type PeerOTCGRPCHandler struct {
	stockpb.UnimplementedPeerOTCServiceServer
	negRepo    *repository.PeerOtcNegotiationRepository
	holdings   HoldingReader
	peerTx     transactionpb.PeerTxServiceClient
	ownRouting int64
}

func NewPeerOTCGRPCHandler(
	negRepo *repository.PeerOtcNegotiationRepository,
	holdings HoldingReader,
	peerTx transactionpb.PeerTxServiceClient,
	ownRouting int64,
) *PeerOTCGRPCHandler {
	return &PeerOTCGRPCHandler{negRepo: negRepo, holdings: holdings, peerTx: peerTx, ownRouting: ownRouting}
}

func (h *PeerOTCGRPCHandler) GetPublicStocks(ctx context.Context, req *stockpb.GetPublicStocksRequest) (*stockpb.GetPublicStocksResponse, error) {
	rows, err := h.holdings.ListPublic()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list public holdings: %v", err)
	}
	out := make([]*stockpb.PeerPublicStock, 0, len(rows))
	for i := range rows {
		hd := rows[i]
		// Owner ID maps to (h.ownRouting, owner_id-as-string). Bank-owned
		// holdings (OwnerID == nil) are surfaced as id "0".
		var ownerID string
		if hd.OwnerID != nil {
			ownerID = strconv.FormatUint(*hd.OwnerID, 10)
		} else {
			ownerID = "0"
		}
		// PricePerStock is not on Holding directly — use a zero placeholder
		// for now (Phase 4 minimum surface; future iteration can join with
		// listing_daily_price_info or stocks table for live pricing).
		out = append(out, &stockpb.PeerPublicStock{
			OwnerId:       &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: ownerID},
			Ticker:        hd.Ticker,
			Amount:        hd.PublicQuantity,
			PricePerStock: "0",
			Currency:      "USD",
		})
	}
	return &stockpb.GetPublicStocksResponse{Stocks: out}, nil
}

func (h *PeerOTCGRPCHandler) CreateNegotiation(ctx context.Context, req *stockpb.CreateNegotiationRequest) (*stockpb.CreateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer, buyer_id, seller_id are required")
	}
	offerJSON, err := json.Marshal(protoToOffer(req.GetOffer()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	foreignID := uuid.NewString()
	neg := &model.PeerOtcNegotiation{
		PeerBankCode:        req.GetPeerBankCode(),
		ForeignID:           foreignID,
		BuyerRoutingNumber:  req.GetBuyerId().GetRoutingNumber(),
		BuyerID:             req.GetBuyerId().GetId(),
		SellerRoutingNumber: req.GetSellerId().GetRoutingNumber(),
		SellerID:            req.GetSellerId().GetId(),
		OfferJSON:           string(offerJSON),
		Status:              "ongoing",
	}
	if err := h.negRepo.Create(neg); err != nil {
		return nil, status.Errorf(codes.Internal, "create: %v", err)
	}
	return &stockpb.CreateNegotiationResponse{
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: foreignID},
	}, nil
}

func (h *PeerOTCGRPCHandler) UpdateNegotiation(ctx context.Context, req *stockpb.UpdateNegotiationRequest) (*stockpb.UpdateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer and negotiation_id required")
	}
	offerJSON, err := json.Marshal(protoToOffer(req.GetOffer()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	if err := h.negRepo.UpdateOffer(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), string(offerJSON)); err != nil {
		return nil, status.Errorf(codes.Internal, "update: %v", err)
	}
	return &stockpb.UpdateNegotiationResponse{}, nil
}

func (h *PeerOTCGRPCHandler) GetNegotiation(ctx context.Context, req *stockpb.GetNegotiationRequest) (*stockpb.GetNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	row, err := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(row.OfferJSON), &offer)
	return &stockpb.GetNegotiationResponse{
		Id:        &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: row.ForeignID},
		BuyerId:   &stockpb.PeerForeignBankId{RoutingNumber: row.BuyerRoutingNumber, Id: row.BuyerID},
		SellerId:  &stockpb.PeerForeignBankId{RoutingNumber: row.SellerRoutingNumber, Id: row.SellerID},
		Offer:     offerToProto(offer),
		Status:    row.Status,
		UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (h *PeerOTCGRPCHandler) DeleteNegotiation(ctx context.Context, req *stockpb.DeleteNegotiationRequest) (*stockpb.DeleteNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	if err := h.negRepo.Delete(req.GetPeerBankCode(), req.GetNegotiationId().GetId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete: %v", err)
	}
	return &stockpb.DeleteNegotiationResponse{}, nil
}

func (h *PeerOTCGRPCHandler) AcceptNegotiation(ctx context.Context, req *stockpb.AcceptNegotiationRequest) (*stockpb.AcceptNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	row, err := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	if err := json.Unmarshal([]byte(row.OfferJSON), &offer); err != nil {
		return nil, status.Errorf(codes.Internal, "decode offer: %v", err)
	}

	// Compose the 4 postings:
	// 1. Buyer debits premium (in premium currency)
	// 2. Seller credits premium
	// 3. Seller debits 1× OptionDescription (asset)
	// 4. Buyer credits 1× OptionDescription
	optDesc := contractsitx.OptionDescription{
		Ticker:         offer.Ticker,
		Amount:         offer.Amount,
		StrikePrice:    offer.PricePerStock,
		Currency:       offer.Currency,
		SettlementDate: offer.SettlementDate,
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: row.ForeignID},
	}
	optDescJSON, err := json.Marshal(optDesc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal option description: %v", err)
	}
	optAssetID := string(optDescJSON)

	premium := offer.Premium.String()
	postings := []*transactionpb.SiTxPosting{
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: row.BuyerID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionCredit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: row.BuyerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionCredit},
	}

	resp, err := h.peerTx.InitiateOutboundTxWithPostings(ctx, &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: req.GetPeerBankCode(),
		Postings:     postings,
		TxKind:       "otc-accept",
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dispatch: %v", err)
	}

	if uerr := h.negRepo.UpdateStatus(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), "accepted"); uerr != nil {
		// Status update failure is non-fatal — the TX has already been
		// dispatched and the negotiation is functionally accepted.
		// Background sweep can reconcile.
		log.Printf("WARN: peer-otc accept status update failed for %s/%s: %v",
			req.GetPeerBankCode(), req.GetNegotiationId().GetId(), uerr)
	}

	return &stockpb.AcceptNegotiationResponse{
		TransactionId: resp.GetTransactionId(),
		Status:        resp.GetStatus(),
	}, nil
}

func protoToOffer(p *stockpb.PeerOtcOffer) contractsitx.OtcOffer {
	pricePerStock, _ := decimal.NewFromString(p.GetPricePerStock())
	premium, _ := decimal.NewFromString(p.GetPremium())
	var lastModBy contractsitx.ForeignBankId
	if p.GetLastModifiedBy() != nil {
		lastModBy = contractsitx.ForeignBankId{
			RoutingNumber: p.GetLastModifiedBy().GetRoutingNumber(),
			ID:            p.GetLastModifiedBy().GetId(),
		}
	}
	return contractsitx.OtcOffer{
		Ticker:          p.GetTicker(),
		Amount:          p.GetAmount(),
		PricePerStock:   pricePerStock,
		Currency:        p.GetCurrency(),
		Premium:         premium,
		PremiumCurrency: p.GetPremiumCurrency(),
		SettlementDate:  p.GetSettlementDate(),
		LastModifiedBy:  lastModBy,
	}
}

func offerToProto(o contractsitx.OtcOffer) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker:          o.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerStock.String(),
		Currency:        o.Currency,
		Premium:         o.Premium.String(),
		PremiumCurrency: o.PremiumCurrency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
	}
}

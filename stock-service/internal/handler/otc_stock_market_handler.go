// Package handler — gRPC surface for the Phase-3 OTCStockService.
// Implements stockpb.OTCStockMarketGRPCServiceServer with thin
// translation from proto request → service input → proto response.
//
// Ownership + validation is gateway-side; this layer trusts the
// (owner_type, owner_id) fields and re-checks only the easy-to-miss
// invariants (positive quantity, valid direction).
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
	"github.com/exbanka/stock-service/internal/service"
)

// OTCStockMarketHandler implements stockpb.OTCStockMarketGRPCServiceServer.
type OTCStockMarketHandler struct {
	stockpb.UnimplementedOTCStockMarketGRPCServiceServer
	svc *service.OTCStockService
}

func NewOTCStockMarketHandler(svc *service.OTCStockService) *OTCStockMarketHandler {
	return &OTCStockMarketHandler{svc: svc}
}

// CreateOTCStockOffer dispatches on direction:
//   - "sell" → svc.CreateSellOffer (returns the updated Holding)
//   - "buy"  → svc.CreateBuyOffer (returns the new OTCStockBuyOffer)
//
// The response shape is unified so the gateway gets the same struct
// regardless of direction.
func (h *OTCStockMarketHandler) CreateOTCStockOffer(ctx context.Context, in *stockpb.CreateOTCStockOfferRequest) (*stockpb.OTCStockOfferResponse, error) {
	if h.svc == nil {
		return nil, status.Error(codes.Unimplemented, "OTCStockService not wired")
	}
	direction := strings.ToLower(strings.TrimSpace(in.GetDirection()))
	if direction != "sell" && direction != "buy" {
		return nil, status.Error(codes.InvalidArgument, "direction must be 'sell' or 'buy'")
	}
	if in.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be > 0")
	}
	ot, err := ownerTypeFromProto(in.GetOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetOwnerId())
	if err != nil {
		return nil, err
	}

	if direction == "sell" {
		if in.GetHoldingId() == 0 {
			return nil, status.Error(codes.InvalidArgument, "holding_id required for direction=sell")
		}
		h2, err := h.svc.CreateSellOffer(ctx, service.CreateSellOfferInput{
			HoldingID:       in.GetHoldingId(),
			CallerOwnerType: ot,
			CallerOwnerID:   oid,
			Quantity:        in.GetQuantity(),
		})
		if err != nil {
			return nil, err
		}
		return holdingToOTCStockOfferProto(h2), nil
	}

	// direction == "buy"
	if in.GetListingId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id required for direction=buy")
	}
	if in.GetBuyerAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_account_id required for direction=buy")
	}
	price, err := decimal.NewFromString(in.GetPricePerUnit())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "price_per_unit must be a decimal: %v", err)
	}
	actingEmp := optionalPtr(in.GetActingEmployeeId())
	b, err := h.svc.CreateBuyOffer(ctx, service.CreateBuyOfferInput{
		BuyerOwnerType:   ot,
		BuyerOwnerID:     oid,
		BuyerAccountID:   in.GetBuyerAccountId(),
		ListingID:        in.GetListingId(),
		Quantity:         in.GetQuantity(),
		PricePerUnit:     price,
		ActingEmployeeID: actingEmp,
	})
	if err != nil {
		return nil, err
	}
	return buyOfferToOTCStockOfferProto(b), nil
}

func (h *OTCStockMarketHandler) CancelOTCStockOffer(ctx context.Context, in *stockpb.CancelOTCStockOfferRequest) (*stockpb.CancelOTCStockOfferResponse, error) {
	if h.svc == nil {
		return nil, status.Error(codes.Unimplemented, "OTCStockService not wired")
	}
	direction := strings.ToLower(strings.TrimSpace(in.GetDirection()))
	if direction != "sell" && direction != "buy" {
		return nil, status.Error(codes.InvalidArgument, "direction must be 'sell' or 'buy'")
	}
	ot, err := ownerTypeFromProto(in.GetOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if in.GetId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if direction == "sell" {
		if err := h.svc.CancelSellOffer(ctx, service.CancelSellOfferInput{
			HoldingID:       in.GetId(),
			CallerOwnerType: ot,
			CallerOwnerID:   oid,
		}); err != nil {
			return nil, err
		}
	} else {
		if err := h.svc.CancelBuyOffer(ctx, service.CancelBuyOfferInput{
			OfferID:         in.GetId(),
			CallerOwnerType: ot,
			CallerOwnerID:   oid,
		}); err != nil {
			// Translate gorm not-found into the service's typed sentinel.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, service.ErrOTCStockBuyOfferNotFound
			}
			return nil, err
		}
	}
	return &stockpb.CancelOTCStockOfferResponse{Ok: true}, nil
}

func (h *OTCStockMarketHandler) ListMyOTCStocks(ctx context.Context, in *stockpb.ListMyOTCStocksRequest) (*stockpb.ListMyOTCStocksResponse, error) {
	if h.svc == nil {
		return nil, status.Error(codes.Unimplemented, "OTCStockService not wired")
	}
	direction := strings.ToLower(strings.TrimSpace(in.GetDirection()))
	if direction != "" && direction != "sell" && direction != "buy" {
		return nil, status.Error(codes.InvalidArgument, "direction must be '', 'sell' or 'buy'")
	}
	ot, err := ownerTypeFromProto(in.GetOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	result, err := h.svc.ListMyListings(ctx, service.ListMyOTCStocksInput{
		OwnerType: ot,
		OwnerID:   oid,
		Direction: direction,
		Page:      int(in.GetPage()),
		PageSize:  int(in.GetPageSize()),
	})
	if err != nil {
		return nil, err
	}
	rows := make([]*stockpb.OTCStockOfferResponse, 0, len(result.Listings))
	for _, l := range result.Listings {
		rows = append(rows, listingToOTCStockOfferProto(ot, oid, l))
	}
	return &stockpb.ListMyOTCStocksResponse{
		Offers: rows,
		Total:  result.Total,
	}, nil
}

// ---------- projection helpers ----------

func holdingToOTCStockOfferProto(h *model.Holding) *stockpb.OTCStockOfferResponse {
	ownerID := uint64(0)
	if h.OwnerID != nil {
		ownerID = *h.OwnerID
	}
	return &stockpb.OTCStockOfferResponse{
		Direction:    "sell",
		Id:           h.ID,
		OwnerType:    string(h.OwnerType),
		OwnerId:      ownerID,
		AccountId:    h.AccountID,
		Ticker:       h.Ticker,
		Name:         h.Name,
		Quantity:     h.PublicQuantity,
		PricePerUnit: h.AveragePrice.String(),
		Currency:     "",
		Status:       "", // public_quantity > 0 IS the active signal
		CreatedAt:    h.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func buyOfferToOTCStockOfferProto(b *model.OTCStockBuyOffer) *stockpb.OTCStockOfferResponse {
	ownerID := uint64(0)
	if b.BuyerOwnerID != nil {
		ownerID = *b.BuyerOwnerID
	}
	return &stockpb.OTCStockOfferResponse{
		Direction:    "buy",
		Id:           b.ID,
		OwnerType:    string(b.BuyerOwnerType),
		OwnerId:      ownerID,
		AccountId:    b.BuyerAccountID,
		Ticker:       b.Ticker,
		Name:         b.Name,
		Quantity:     b.RemainingQuantity,
		PricePerUnit: b.PricePerUnit.String(),
		Currency:     b.CurrencyCode,
		Status:       b.Status,
		CreatedAt:    b.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func listingToOTCStockOfferProto(ot model.OwnerType, oid *uint64, l service.OTCStockListing) *stockpb.OTCStockOfferResponse {
	ownerID := uint64(0)
	if oid != nil {
		ownerID = *oid
	}
	return &stockpb.OTCStockOfferResponse{
		Direction:    l.Direction,
		Id:           l.ID,
		OwnerType:    string(ot),
		OwnerId:      ownerID,
		AccountId:    l.AccountID,
		Ticker:       l.Ticker,
		Name:         l.Name,
		Quantity:     l.Quantity,
		PricePerUnit: l.PricePerUnit.String(),
		Currency:     l.Currency,
		Status:       l.Status,
		CreatedAt:    l.CreatedAt.UTC().Format(time.RFC3339),
	}
}

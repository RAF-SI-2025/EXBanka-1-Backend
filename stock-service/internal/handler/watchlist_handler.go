package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// WatchlistHandler wires the WatchlistService over gRPC.
type WatchlistHandler struct {
	pb.UnimplementedWatchlistServiceServer
	svc *service.WatchlistService
}

func NewWatchlistHandler(svc *service.WatchlistService) *WatchlistHandler {
	return &WatchlistHandler{svc: svc}
}

func (h *WatchlistHandler) AddItem(ctx context.Context, req *pb.AddWatchlistItemRequest) (*pb.WatchlistItemResponse, error) {
	ownerType, ownerID, err := ownerFromRequest(req.GetOwnerType(), req.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if req.GetListingId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id is required")
	}
	if err := h.svc.Add(ownerType, ownerID, req.GetListingId()); err != nil {
		return nil, err
	}
	// Re-fetch the enriched entry so the caller gets the same shape as List.
	items, err := h.svc.List(ownerType, ownerID, "")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ListingID == req.GetListingId() {
			return entryToProto(it), nil
		}
	}
	return &pb.WatchlistItemResponse{ListingId: req.GetListingId()}, nil
}

func (h *WatchlistHandler) RemoveItem(ctx context.Context, req *pb.RemoveWatchlistItemRequest) (*pb.RemoveWatchlistItemResponse, error) {
	ownerType, ownerID, err := ownerFromRequest(req.GetOwnerType(), req.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if req.GetListingId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id is required")
	}
	if err := h.svc.Remove(ownerType, ownerID, req.GetListingId()); err != nil {
		return nil, err
	}
	return &pb.RemoveWatchlistItemResponse{Removed: true}, nil
}

func (h *WatchlistHandler) ListMy(ctx context.Context, req *pb.ListMyWatchlistRequest) (*pb.ListMyWatchlistResponse, error) {
	ownerType, ownerID, err := ownerFromRequest(req.GetOwnerType(), req.GetOwnerId())
	if err != nil {
		return nil, err
	}
	items, err := h.svc.List(ownerType, ownerID, req.GetListingType())
	if err != nil {
		return nil, err
	}
	out := &pb.ListMyWatchlistResponse{Items: make([]*pb.WatchlistItemResponse, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, entryToProto(it))
	}
	return out, nil
}

// ownerFromRequest interprets the wire (owner_type, owner_id) into the
// internal (OwnerType, *uint64) pair. The gateway is responsible for
// supplying these from the resolved identity; both sides must agree on
// the "bank → owner_id omitted" convention.
func ownerFromRequest(ownerType string, ownerID uint64) (model.OwnerType, *uint64, error) {
	ot := model.OwnerType(ownerType)
	if !ot.Valid() {
		return "", nil, status.Errorf(codes.InvalidArgument, "invalid owner_type %q", ownerType)
	}
	if ot == model.OwnerBank {
		return ot, nil, nil
	}
	if ownerID == 0 {
		return "", nil, status.Error(codes.InvalidArgument, "owner_id is required for non-bank owners")
	}
	uid := ownerID
	return ot, &uid, nil
}

func entryToProto(e service.WatchlistEntry) *pb.WatchlistItemResponse {
	return &pb.WatchlistItemResponse{
		Id:                 e.ID,
		ListingId:          e.ListingID,
		SecurityType:       e.SecurityType,
		Ticker:             e.Ticker,
		CurrentPrice:       e.CurrentPrice.String(),
		DailyChange:        e.DailyChange.String(),
		DailyChangePercent: e.DailyChangePercent.String(),
		AddedAtUnix:        e.AddedAtUnix,
	}
}

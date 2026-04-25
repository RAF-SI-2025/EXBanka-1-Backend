package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

type OTCHandler struct {
	pb.UnimplementedOTCGRPCServiceServer
	otcSvc *service.OTCService
}

func NewOTCHandler(otcSvc *service.OTCService) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}

func (h *OTCHandler) ListOffers(ctx context.Context, req *pb.ListOTCOffersRequest) (*pb.ListOTCOffersResponse, error) {
	filter := service.OTCFilter{
		SecurityType: req.SecurityType,
		Ticker:       req.Ticker,
		Page:         int(req.Page),
		PageSize:     int(req.PageSize),
	}

	holdings, total, err := h.otcSvc.ListOffers(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	offers := make([]*pb.OTCOffer, len(holdings))
	for i, hld := range holdings {
		offers[i] = &pb.OTCOffer{
			Id:           hld.ID,
			SellerId:     hld.UserID,
			SellerName:   hld.UserFirstName + " " + hld.UserLastName,
			SecurityType: hld.SecurityType,
			Ticker:       hld.Ticker,
			Name:         hld.Name,
			Quantity:     hld.PublicQuantity,
			PricePerUnit: hld.AveragePrice.StringFixed(2),
			CreatedAt:    hld.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return &pb.ListOTCOffersResponse{
		Offers:     offers,
		TotalCount: total,
	}, nil
}

func (h *OTCHandler) BuyOffer(ctx context.Context, req *pb.BuyOTCOfferRequest) (*pb.OTCTransaction, error) {
	// Both the buyer ID and system_type must flip to the client when an
	// employee acts on behalf, so the resulting holding lands in the
	// client's portfolio. Same rule as order placement; see resolveOrderOwner.
	effectiveBuyerID, effectiveSystemType, rerr := resolveOrderOwner(req.BuyerId, req.SystemType, req.ActingEmployeeId, req.OnBehalfOfClientId)
	if rerr != nil {
		return nil, rerr
	}

	result, err := h.otcSvc.BuyOffer(
		req.OfferId,
		effectiveBuyerID,
		effectiveSystemType,
		req.Quantity,
		req.AccountId,
	)
	if err != nil {
		return nil, mapOTCError(err)
	}

	return &pb.OTCTransaction{
		Id:           result.ID,
		OfferId:      result.OfferID,
		Quantity:     result.Quantity,
		PricePerUnit: result.PricePerUnit.StringFixed(2),
		TotalPrice:   result.TotalPrice.StringFixed(2),
		Commission:   result.Commission.StringFixed(2),
	}, nil
}

func mapOTCError(err error) error {
	switch err.Error() {
	case "OTC offer not found":
		return status.Error(codes.NotFound, err.Error())
	case "cannot buy your own OTC offer":
		return status.Error(codes.PermissionDenied, err.Error())
	case "insufficient public quantity for OTC purchase",
		"buyer account not found", "seller account not found":
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

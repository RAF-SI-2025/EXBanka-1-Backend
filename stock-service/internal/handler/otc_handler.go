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
	// Stub: fully implemented in Plan 6 (Portfolio & Holdings)
	return nil, status.Error(codes.Unimplemented, "OTC offers: pending Plan 6 implementation")
}

func (h *OTCHandler) BuyOffer(ctx context.Context, req *pb.BuyOTCOfferRequest) (*pb.OTCTransaction, error) {
	// Stub: fully implemented in Plan 6 (Portfolio & Holdings)
	return nil, status.Error(codes.Unimplemented, "OTC buy: pending Plan 6 implementation")
}

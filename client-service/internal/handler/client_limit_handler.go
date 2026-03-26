package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/service"
	"github.com/shopspring/decimal"
)

// ClientLimitGRPCHandler implements the ClientLimitServiceServer interface.
type ClientLimitGRPCHandler struct {
	pb.UnimplementedClientLimitServiceServer
	limitSvc *service.ClientLimitService
}

func NewClientLimitGRPCHandler(limitSvc *service.ClientLimitService) *ClientLimitGRPCHandler {
	return &ClientLimitGRPCHandler{limitSvc: limitSvc}
}

func (h *ClientLimitGRPCHandler) GetClientLimits(ctx context.Context, req *pb.GetClientLimitRequest) (*pb.ClientLimitResponse, error) {
	limit, err := h.limitSvc.GetClientLimits(req.ClientId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to get client limits: %v", err)
	}
	return toClientLimitResponse(limit), nil
}

func (h *ClientLimitGRPCHandler) SetClientLimits(ctx context.Context, req *pb.SetClientLimitRequest) (*pb.ClientLimitResponse, error) {
	daily, err := decimal.NewFromString(req.DailyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid daily_limit: %v", err)
	}
	monthly, err := decimal.NewFromString(req.MonthlyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid monthly_limit: %v", err)
	}
	transfer, err := decimal.NewFromString(req.TransferLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transfer_limit: %v", err)
	}

	limit := model.ClientLimit{
		ClientID:      req.ClientId,
		DailyLimit:    daily,
		MonthlyLimit:  monthly,
		TransferLimit: transfer,
		SetByEmployee: req.SetByEmployee,
	}

	result, err := h.limitSvc.SetClientLimits(ctx, limit)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to set client limits: %v", err)
	}
	return toClientLimitResponse(result), nil
}

func toClientLimitResponse(l *model.ClientLimit) *pb.ClientLimitResponse {
	return &pb.ClientLimitResponse{
		Id:            l.ID,
		ClientId:      l.ClientID,
		DailyLimit:    l.DailyLimit.String(),
		MonthlyLimit:  l.MonthlyLimit.String(),
		TransferLimit: l.TransferLimit.String(),
		SetByEmployee: l.SetByEmployee,
	}
}

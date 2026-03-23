package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/cardpb"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/service"
)

type CardRequestGRPCHandler struct {
	pb.UnimplementedCardRequestServiceServer
	cardRequestSvc *service.CardRequestService
}

func NewCardRequestGRPCHandler(cardRequestSvc *service.CardRequestService) *CardRequestGRPCHandler {
	return &CardRequestGRPCHandler{cardRequestSvc: cardRequestSvc}
}

func (h *CardRequestGRPCHandler) CreateCardRequest(ctx context.Context, req *pb.CreateCardRequestRequest) (*pb.CardRequestResponse, error) {
	cardReq := &model.CardRequest{
		ClientID:      req.ClientId,
		AccountNumber: req.AccountNumber,
		CardBrand:     req.CardBrand,
		CardType:      req.CardType,
		CardName:      req.CardName,
	}
	if err := h.cardRequestSvc.CreateRequest(ctx, cardReq); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create card request: %v", err)
	}
	return toCardRequestResponse(cardReq), nil
}

func (h *CardRequestGRPCHandler) GetCardRequest(ctx context.Context, req *pb.GetCardRequestRequest) (*pb.CardRequestResponse, error) {
	cardReq, err := h.cardRequestSvc.GetRequest(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card request not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get card request: %v", err)
	}
	return toCardRequestResponse(cardReq), nil
}

func (h *CardRequestGRPCHandler) ListCardRequests(ctx context.Context, req *pb.ListCardRequestsRequest) (*pb.ListCardRequestsResponse, error) {
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	requests, total, err := h.cardRequestSvc.ListRequests(req.Status, page, pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list card requests: %v", err)
	}

	resp := &pb.ListCardRequestsResponse{Total: total}
	for _, r := range requests {
		r := r
		resp.Requests = append(resp.Requests, toCardRequestResponse(&r))
	}
	return resp, nil
}

func (h *CardRequestGRPCHandler) ListCardRequestsByClient(ctx context.Context, req *pb.ListCardRequestsByClientRequest) (*pb.ListCardRequestsResponse, error) {
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	requests, total, err := h.cardRequestSvc.ListByClient(req.ClientId, page, pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list card requests by client: %v", err)
	}

	resp := &pb.ListCardRequestsResponse{Total: total}
	for _, r := range requests {
		r := r
		resp.Requests = append(resp.Requests, toCardRequestResponse(&r))
	}
	return resp, nil
}

func (h *CardRequestGRPCHandler) ApproveCardRequest(ctx context.Context, req *pb.ApproveCardRequestRequest) (*pb.CardRequestApprovedResponse, error) {
	card, err := h.cardRequestSvc.ApproveRequest(ctx, req.Id, req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to approve card request: %v", err)
	}

	// Fetch updated request for response
	cardReq, err := h.cardRequestSvc.GetRequest(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated card request: %v", err)
	}

	return &pb.CardRequestApprovedResponse{
		Request: toCardRequestResponse(cardReq),
		Card:    toCardResponse(card),
	}, nil
}

func (h *CardRequestGRPCHandler) RejectCardRequest(ctx context.Context, req *pb.RejectCardRequestRequest) (*pb.CardRequestResponse, error) {
	if err := h.cardRequestSvc.RejectRequest(ctx, req.Id, req.EmployeeId, req.Reason); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to reject card request: %v", err)
	}

	cardReq, err := h.cardRequestSvc.GetRequest(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated card request: %v", err)
	}
	return toCardRequestResponse(cardReq), nil
}

func toCardRequestResponse(r *model.CardRequest) *pb.CardRequestResponse {
	return &pb.CardRequestResponse{
		Id:            r.ID,
		ClientId:      r.ClientID,
		AccountNumber: r.AccountNumber,
		CardBrand:     r.CardBrand,
		CardType:      r.CardType,
		CardName:      r.CardName,
		Status:        r.Status,
		Reason:        r.Reason,
		ApprovedBy:    r.ApprovedBy,
		CreatedAt:     r.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     r.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

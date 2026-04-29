package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
)

type VirtualCardGRPCHandler struct {
	pb.UnimplementedVirtualCardServiceServer
	cardService cardServiceFacade
}

func NewVirtualCardGRPCHandler(cardService *service.CardService) *VirtualCardGRPCHandler {
	return &VirtualCardGRPCHandler{cardService: cardService}
}

func (h *VirtualCardGRPCHandler) CreateVirtualCard(ctx context.Context, req *pb.CreateVirtualCardRequest) (*pb.CardResponse, error) {
	card, cvv, err := h.cardService.CreateVirtualCard(ctx, req.AccountNumber, req.OwnerId, req.CardBrand, req.UsageType, int(req.MaxUses), int(req.ExpiryMonths), req.CardLimit)
	if err != nil {
		return nil, err
	}
	resp := toCardResponseFull(card)
	resp.Cvv = cvv
	return resp, nil
}

func (h *VirtualCardGRPCHandler) SetCardPin(ctx context.Context, req *pb.SetCardPinRequest) (*pb.SetCardPinResponse, error) {
	if err := h.cardService.SetPin(req.Id, req.Pin); err != nil {
		return nil, err
	}
	return &pb.SetCardPinResponse{Success: true, Message: "PIN set successfully"}, nil
}

func (h *VirtualCardGRPCHandler) VerifyCardPin(ctx context.Context, req *pb.VerifyCardPinRequest) (*pb.VerifyCardPinResponse, error) {
	valid, err := h.cardService.VerifyPin(req.Id, req.Pin)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s", err.Error())
	}
	msg := "PIN verified"
	if !valid {
		msg = "incorrect PIN"
	}
	return &pb.VerifyCardPinResponse{Valid: valid, Message: msg}, nil
}

func (h *VirtualCardGRPCHandler) TemporaryBlockCard(ctx context.Context, req *pb.TemporaryBlockCardRequest) (*pb.CardResponse, error) {
	card, err := h.cardService.TemporaryBlockCard(ctx, req.Id, int(req.DurationHours), req.Reason)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, err
	}
	return toCardResponseFull(card), nil
}

func (h *VirtualCardGRPCHandler) UseCard(ctx context.Context, req *pb.UseCardRequest) (*pb.UseCardResponse, error) {
	if err := h.cardService.UseCard(req.Id); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to use card: %v", err)
	}
	return &pb.UseCardResponse{Success: true}, nil
}

// toCardResponseFull converts a Card model to a CardResponse.
func toCardResponseFull(c *model.Card) *pb.CardResponse {
	resp := &pb.CardResponse{
		Id:             c.ID,
		CardNumber:     c.CardNumber,
		CardNumberFull: c.CardNumberFull,
		CardType:       c.CardType,
		CardName:       c.CardName,
		CardBrand:      c.CardBrand,
		AccountNumber:  c.AccountNumber,
		CardLimit:      c.CardLimit.StringFixed(4),
		Status:         c.Status,
		OwnerType:      c.OwnerType,
		OwnerId:        c.OwnerID,
		ExpiresAt:      c.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:      c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	return resp
}

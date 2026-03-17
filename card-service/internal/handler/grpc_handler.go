package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/cardpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/service"
)

type CardGRPCHandler struct {
	pb.UnimplementedCardServiceServer
	cardService *service.CardService
	producer    *kafkaprod.Producer
}

func NewCardGRPCHandler(cardService *service.CardService, producer *kafkaprod.Producer) *CardGRPCHandler {
	return &CardGRPCHandler{
		cardService: cardService,
		producer:    producer,
	}
}

func (h *CardGRPCHandler) CreateCard(ctx context.Context, req *pb.CreateCardRequest) (*pb.CardResponse, error) {
	card, cvv, err := h.cardService.CreateCard(ctx, req.AccountNumber, req.OwnerId, req.OwnerType, req.CardBrand)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create card: %v", err)
	}

	_ = h.producer.PublishCardCreated(ctx, kafkamsg.CardCreatedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		CardBrand:     card.CardBrand,
	})

	resp := toCardResponse(card)
	resp.Cvv = cvv
	return resp, nil
}

func (h *CardGRPCHandler) GetCard(ctx context.Context, req *pb.GetCardRequest) (*pb.CardResponse, error) {
	card, err := h.cardService.GetCard(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get card: %v", err)
	}
	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) ListCardsByAccount(ctx context.Context, req *pb.ListCardsByAccountRequest) (*pb.ListCardsResponse, error) {
	cards, err := h.cardService.ListCardsByAccount(req.AccountNumber)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list cards: %v", err)
	}
	resp := &pb.ListCardsResponse{}
	for _, c := range cards {
		c := c
		resp.Cards = append(resp.Cards, toCardResponse(&c))
	}
	return resp, nil
}

func (h *CardGRPCHandler) ListCardsByClient(ctx context.Context, req *pb.ListCardsByClientRequest) (*pb.ListCardsResponse, error) {
	cards, err := h.cardService.ListCardsByClient(req.ClientId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list cards: %v", err)
	}
	resp := &pb.ListCardsResponse{}
	for _, c := range cards {
		c := c
		resp.Cards = append(resp.Cards, toCardResponse(&c))
	}
	return resp, nil
}

func (h *CardGRPCHandler) BlockCard(ctx context.Context, req *pb.BlockCardRequest) (*pb.CardResponse, error) {
	card, err := h.cardService.BlockCard(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to block card: %v", err)
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) UnblockCard(ctx context.Context, req *pb.UnblockCardRequest) (*pb.CardResponse, error) {
	card, err := h.cardService.UnblockCard(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to unblock card: %v", err)
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) DeactivateCard(ctx context.Context, req *pb.DeactivateCardRequest) (*pb.CardResponse, error) {
	card, err := h.cardService.DeactivateCard(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to deactivate card: %v", err)
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) CreateAuthorizedPerson(ctx context.Context, req *pb.CreateAuthorizedPersonRequest) (*pb.AuthorizedPersonResponse, error) {
	ap := &model.AuthorizedPerson{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		AccountID:   req.AccountId,
	}
	if err := h.cardService.CreateAuthorizedPerson(ctx, ap); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorized person: %v", err)
	}
	return toAuthorizedPersonResponse(ap), nil
}

func (h *CardGRPCHandler) GetAuthorizedPerson(ctx context.Context, req *pb.GetAuthorizedPersonRequest) (*pb.AuthorizedPersonResponse, error) {
	ap, err := h.cardService.GetAuthorizedPerson(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "authorized person not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get authorized person: %v", err)
	}
	return toAuthorizedPersonResponse(ap), nil
}

func toCardResponse(c *model.Card) *pb.CardResponse {
	return &pb.CardResponse{
		Id:            c.ID,
		CardNumber:    c.CardNumber,
		CardNumberFull: c.CardNumberFull,
		CardType:      c.CardType,
		CardName:      c.CardName,
		CardBrand:     c.CardBrand,
		AccountNumber: c.AccountNumber,
		CardLimit:     c.CardLimit,
		Status:        c.Status,
		OwnerType:     c.OwnerType,
		OwnerId:       c.OwnerID,
		ExpiresAt:     c.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:     c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toAuthorizedPersonResponse(ap *model.AuthorizedPerson) *pb.AuthorizedPersonResponse {
	return &pb.AuthorizedPersonResponse{
		Id:          ap.ID,
		FirstName:   ap.FirstName,
		LastName:    ap.LastName,
		DateOfBirth: ap.DateOfBirth,
		Gender:      ap.Gender,
		Email:       ap.Email,
		Phone:       ap.Phone,
		Address:     ap.Address,
		AccountId:   ap.AccountID,
		CreatedAt:   ap.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

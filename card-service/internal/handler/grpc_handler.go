package handler

import (
	"context"
	"errors"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
	"github.com/exbanka/contract/changelog"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type CardGRPCHandler struct {
	pb.UnimplementedCardServiceServer
	cardService  cardServiceFacade
	producer     producerFacade
	clientClient clientpb.ClientServiceClient
}

func NewCardGRPCHandler(cardService *service.CardService, producer *kafkaprod.Producer, clientClient clientpb.ClientServiceClient) *CardGRPCHandler {
	return &CardGRPCHandler{
		cardService:  cardService,
		producer:     producer,
		clientClient: clientClient,
	}
}

func (h *CardGRPCHandler) CreateCard(ctx context.Context, req *pb.CreateCardRequest) (*pb.CardResponse, error) {
	card, cvv, err := h.cardService.CreateCard(ctx, req.AccountNumber, req.OwnerId, req.OwnerType, req.CardBrand)
	if err != nil {
		return nil, err
	}

	_ = h.producer.PublishCardCreated(ctx, kafkamsg.CardCreatedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		CardBrand:     card.CardBrand,
	})

	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  card.OwnerID,
		Type:    "card_issued",
		Title:   "New Card Issued",
		Message: fmt.Sprintf("A new %s card has been issued for account %s.", card.CardBrand, card.AccountNumber),
		RefType: "card",
		RefID:   card.ID,
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
		return nil, err
	}
	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) ListCardsByAccount(ctx context.Context, req *pb.ListCardsByAccountRequest) (*pb.ListCardsResponse, error) {
	cards, err := h.cardService.ListCardsByAccount(req.AccountNumber)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListCardsResponse{Cards: make([]*pb.CardResponse, 0, len(cards))}
	for _, c := range cards {
		c := c
		resp.Cards = append(resp.Cards, toCardResponse(&c))
	}
	return resp, nil
}

func (h *CardGRPCHandler) ListCardsByClient(ctx context.Context, req *pb.ListCardsByClientRequest) (*pb.ListCardsResponse, error) {
	cards, err := h.cardService.ListCardsByClient(req.ClientId)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListCardsResponse{Cards: make([]*pb.CardResponse, 0, len(cards))}
	for _, c := range cards {
		c := c
		resp.Cards = append(resp.Cards, toCardResponse(&c))
	}
	return resp, nil
}

func (h *CardGRPCHandler) BlockCard(ctx context.Context, req *pb.BlockCardRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.BlockCard(req.Id, changedBy)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, err
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  card.OwnerID,
		Type:    "card_blocked",
		Title:   "Card Blocked",
		Message: fmt.Sprintf("Your card ending in %s has been blocked.", maskCardNumber(card.CardNumber)),
		RefType: "card",
		RefID:   card.ID,
	})

	// Send email notification to card owner
	if h.clientClient != nil && h.producer != nil {
		clientResp, clientErr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: card.OwnerID})
		if clientErr == nil {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        clientResp.Email,
				EmailType: kafkamsg.EmailTypeCardStatusChanged,
				Data: map[string]string{
					"card_last_four": maskCardNumber(card.CardNumber),
					"new_status":     card.Status,
					"account_number": card.AccountNumber,
				},
			})
			if emailErr != nil {
				log.Printf("CardGRPCHandler: failed to send block card email for card %d: %v", card.ID, emailErr)
			}
		} else {
			log.Printf("CardGRPCHandler: failed to fetch client for card %d: %v", card.ID, clientErr)
		}
	}

	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) UnblockCard(ctx context.Context, req *pb.UnblockCardRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.UnblockCard(req.Id, changedBy)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, err
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	// Send email notification to card owner
	if h.clientClient != nil && h.producer != nil {
		clientResp, clientErr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: card.OwnerID})
		if clientErr == nil {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        clientResp.Email,
				EmailType: kafkamsg.EmailTypeCardStatusChanged,
				Data: map[string]string{
					"card_last_four": maskCardNumber(card.CardNumber),
					"new_status":     card.Status,
					"account_number": card.AccountNumber,
				},
			})
			if emailErr != nil {
				log.Printf("CardGRPCHandler: failed to send unblock card email for card %d: %v", card.ID, emailErr)
			}
		} else {
			log.Printf("CardGRPCHandler: failed to fetch client for card %d: %v", card.ID, clientErr)
		}
	}

	return toCardResponse(card), nil
}

func (h *CardGRPCHandler) DeactivateCard(ctx context.Context, req *pb.DeactivateCardRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.DeactivateCard(req.Id, changedBy)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		return nil, err
	}

	_ = h.producer.PublishCardStatusChanged(ctx, kafkamsg.CardStatusChangedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		NewStatus:     card.Status,
	})

	// Send email notification to card owner
	if h.clientClient != nil && h.producer != nil {
		clientResp, clientErr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: card.OwnerID})
		if clientErr == nil {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        clientResp.Email,
				EmailType: kafkamsg.EmailTypeCardStatusChanged,
				Data: map[string]string{
					"card_last_four": maskCardNumber(card.CardNumber),
					"new_status":     card.Status,
					"account_number": card.AccountNumber,
				},
			})
			if emailErr != nil {
				log.Printf("CardGRPCHandler: failed to send deactivate card email for card %d: %v", card.ID, emailErr)
			}
		} else {
			log.Printf("CardGRPCHandler: failed to fetch client for card %d: %v", card.ID, clientErr)
		}
	}

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
		return nil, err
	}
	return toAuthorizedPersonResponse(ap), nil
}

func (h *CardGRPCHandler) GetAuthorizedPerson(ctx context.Context, req *pb.GetAuthorizedPersonRequest) (*pb.AuthorizedPersonResponse, error) {
	ap, err := h.cardService.GetAuthorizedPerson(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "authorized person not found")
		}
		return nil, err
	}
	return toAuthorizedPersonResponse(ap), nil
}

// maskCardNumber returns a masked card number showing only the last 4 digits.
func maskCardNumber(cardNumber string) string {
	if len(cardNumber) < 4 {
		return cardNumber
	}
	return cardNumber[len(cardNumber)-4:]
}

func toCardResponse(c *model.Card) *pb.CardResponse {
	return &pb.CardResponse{
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

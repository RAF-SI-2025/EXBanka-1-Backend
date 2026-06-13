package handler

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
	"github.com/exbanka/contract/changelog"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// clientReplicaReader is the local read-model the handler consults before
// falling back to a synchronous GetClient (SP-1 hybrid lazy fallback).
type clientReplicaReader interface {
	GetByID(ctx context.Context, id uint64) (model.ClientReplica, error)
	Upsert(ctx context.Context, in model.ClientReplica) error
}

type CardGRPCHandler struct {
	pb.UnimplementedCardServiceServer
	cardService      cardServiceFacade
	producer         producerFacade
	clientClient     clientpb.ClientServiceClient
	changelogService *service.ChangelogService
	clientReplica    clientReplicaReader
}

func NewCardGRPCHandler(cardService *service.CardService, producer *kafkaprod.Producer, clientClient clientpb.ClientServiceClient, changelogService *service.ChangelogService, clientReplica clientReplicaReader) *CardGRPCHandler {
	return &CardGRPCHandler{
		cardService:      cardService,
		producer:         producer,
		clientClient:     clientClient,
		changelogService: changelogService,
		clientReplica:    clientReplica,
	}
}

// resolveClientEmail returns the client's email from the local replica, falling
// back to a single synchronous GetClient on a miss and backfilling the replica
// (SP-1 hybrid lazy fallback). Returns "" only if both sources fail.
func (h *CardGRPCHandler) resolveClientEmail(ctx context.Context, ownerID uint64) string {
	if h.clientReplica != nil {
		if rep, err := h.clientReplica.GetByID(ctx, ownerID); err == nil {
			return rep.Email
		}
	}
	if h.clientClient == nil {
		return ""
	}
	resp, err := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: ownerID})
	if err != nil {
		log.Printf("CardGRPCHandler: client resolve fallback failed for %d: %v", ownerID, err)
		return ""
	}
	if h.clientReplica != nil {
		// ClientResponse has no Version; backfill at 0 so a later versioned
		// event overwrites it via the repo's version guard.
		_ = h.clientReplica.Upsert(ctx, model.ClientReplica{
			ID: ownerID, Email: resp.Email, FirstName: resp.FirstName, LastName: resp.LastName, JMBG: resp.Jmbg,
		})
	}
	return resp.Email
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

	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_CREATED",
			Data:    map[string]string{"card_brand": card.CardBrand},
			RefType: "card",
			RefID:   card.ID,
		})
	}

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
	// OWN-1: a client may only read its own card (others → 404, no leak).
	if !ownsCard(ctx, card.OwnerID) {
		return nil, service.ErrCardNotFound
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
	// OWN-1: a client may only list its own cards.
	if !ownsCard(ctx, req.ClientId) {
		return nil, service.ErrForbidden
	}
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
	// OWN-1: a client may only block its own card.
	if err := requireCardOwnerByID(ctx, h.cardService, req.Id); err != nil {
		return nil, err
	}
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

	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_STATUS_CHANGED",
			Data:    map[string]string{"new_status": card.Status},
			RefType: "card",
			RefID:   card.ID,
		})
	}

	// Send email notification to card owner
	if h.producer != nil {
		if email := h.resolveClientEmail(ctx, card.OwnerID); email != "" {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        email,
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
			log.Printf("CardGRPCHandler: skipping card-status notification for card %d: client %d email unresolved", card.ID, card.OwnerID)
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

	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_STATUS_CHANGED",
			Data:    map[string]string{"new_status": card.Status},
			RefType: "card",
			RefID:   card.ID,
		})
	}

	// Send email notification to card owner
	if h.producer != nil {
		if email := h.resolveClientEmail(ctx, card.OwnerID); email != "" {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        email,
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
			log.Printf("CardGRPCHandler: skipping card-status notification for card %d: client %d email unresolved", card.ID, card.OwnerID)
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

	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_STATUS_CHANGED",
			Data:    map[string]string{"new_status": card.Status},
			RefType: "card",
			RefID:   card.ID,
		})
	}

	// Send email notification to card owner
	if h.producer != nil {
		if email := h.resolveClientEmail(ctx, card.OwnerID); email != "" {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        email,
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
			log.Printf("CardGRPCHandler: skipping card-status notification for card %d: client %d email unresolved", card.ID, card.OwnerID)
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

// ListChangelog returns paginated audit-log entries for an entity.
func (h *CardGRPCHandler) ListChangelog(ctx context.Context, req *pb.ListChangelogRequest) (*pb.ListChangelogResponse, error) {
	entries, total, err := h.changelogService.ListChangelog(req.GetEntityType(), req.GetEntityId(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	protoEntries := make([]*pb.ChangelogEntry, len(entries))
	for i, e := range entries {
		protoEntries[i] = &pb.ChangelogEntry{
			Id:         e.ID,
			EntityType: e.EntityType,
			EntityId:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt.Unix(),
			Reason:     e.Reason,
		}
	}
	return &pb.ListChangelogResponse{Entries: protoEntries, Total: total}, nil
}

// ListAllChangelogs returns paginated audit-log entries across all entities
// (global view, admin-only).
func (h *CardGRPCHandler) ListAllChangelogs(ctx context.Context, req *pb.ListAllChangelogsRequest) (*pb.ListAllChangelogsResponse, error) {
	page := int(req.GetPage())
	pageSize := int(req.GetPageSize())
	filters := repository.ChangelogFilters{
		Since:   req.GetSince(),
		Until:   req.GetUntil(),
		ActorID: req.GetActorId(),
		Action:  req.GetAction(),
	}
	entries, total, err := h.changelogService.ListAllChangelogs(filters, page, pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	protoEntries := make([]*pb.ChangelogEntry, len(entries))
	for i, e := range entries {
		protoEntries[i] = &pb.ChangelogEntry{
			Id:         e.ID,
			EntityType: e.EntityType,
			EntityId:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt.Unix(),
			Reason:     e.Reason,
		}
	}
	return &pb.ListAllChangelogsResponse{
		Entries:  protoEntries,
		Total:    total,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}, nil
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

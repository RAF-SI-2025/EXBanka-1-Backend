package handler

import (
	"context"

	"github.com/exbanka/card-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// cardServiceFacade is the subset of *service.CardService that the gRPC
// handlers depend on. Extracting this interface allows handler tests to
// substitute a hand-written stub instead of wiring a full service plus DB.
type cardServiceFacade interface {
	CreateCard(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand string) (*model.Card, string, error)
	GetCard(id uint64) (*model.Card, error)
	ListCardsByAccount(accountNumber string) ([]model.Card, error)
	ListCardsByClient(clientID uint64) ([]model.Card, error)
	BlockCard(id uint64, changedBy int64) (*model.Card, error)
	UnblockCard(id uint64, changedBy int64) (*model.Card, error)
	DeactivateCard(id uint64, changedBy int64) (*model.Card, error)
	CreateAuthorizedPerson(ctx context.Context, ap *model.AuthorizedPerson) error
	GetAuthorizedPerson(id uint64) (*model.AuthorizedPerson, error)
	CreateVirtualCard(ctx context.Context, accountNumber string, ownerID uint64, cardBrand, usageType string, maxUses, expiryMonths int, limitStr string) (*model.Card, string, error)
	SetPin(cardID uint64, pin string) error
	VerifyPin(cardID uint64, pin string) (bool, error)
	TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error)
	UseCard(cardID uint64) error
}

// cardRequestServiceFacade is the subset of *service.CardRequestService that
// the gRPC handlers depend on.
type cardRequestServiceFacade interface {
	CreateRequest(ctx context.Context, req *model.CardRequest) error
	GetRequest(id uint64) (*model.CardRequest, error)
	ListRequests(status string, page, pageSize int) ([]model.CardRequest, int64, error)
	ListByClient(clientID uint64, page, pageSize int) ([]model.CardRequest, int64, error)
	ApproveRequest(ctx context.Context, id, employeeID uint64) (*model.Card, error)
	RejectRequest(ctx context.Context, id, employeeID uint64, reason string) error
}

// producerFacade is the subset of *kafkaprod.Producer that the gRPC handlers
// publish to. Tests substitute a no-op stub.
type producerFacade interface {
	PublishCardCreated(ctx context.Context, msg kafkamsg.CardCreatedMessage) error
	PublishCardStatusChanged(ctx context.Context, msg kafkamsg.CardStatusChangedMessage) error
	SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

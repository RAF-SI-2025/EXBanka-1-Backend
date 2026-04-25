package handler

import (
	"context"
	"errors"

	"github.com/exbanka/card-service/internal/model"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// stubCardService — function-field stub of cardServiceFacade
// ---------------------------------------------------------------------------

type stubCardService struct {
	createCardFn             func(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand string) (*model.Card, string, error)
	getCardFn                func(id uint64) (*model.Card, error)
	listCardsByAccountFn     func(accountNumber string) ([]model.Card, error)
	listCardsByClientFn      func(clientID uint64) ([]model.Card, error)
	blockCardFn              func(id uint64, changedBy int64) (*model.Card, error)
	unblockCardFn            func(id uint64, changedBy int64) (*model.Card, error)
	deactivateCardFn         func(id uint64, changedBy int64) (*model.Card, error)
	createAuthorizedPersonFn func(ctx context.Context, ap *model.AuthorizedPerson) error
	getAuthorizedPersonFn    func(id uint64) (*model.AuthorizedPerson, error)
	createVirtualCardFn      func(ctx context.Context, accountNumber string, ownerID uint64, cardBrand, usageType string, maxUses, expiryMonths int, limitStr string) (*model.Card, string, error)
	setPinFn                 func(cardID uint64, pin string) error
	verifyPinFn              func(cardID uint64, pin string) (bool, error)
	temporaryBlockCardFn     func(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error)
	useCardFn                func(cardID uint64) error
}

func (s *stubCardService) CreateCard(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand string) (*model.Card, string, error) {
	return s.createCardFn(ctx, accountNumber, ownerID, ownerType, cardBrand)
}
func (s *stubCardService) GetCard(id uint64) (*model.Card, error) { return s.getCardFn(id) }
func (s *stubCardService) ListCardsByAccount(accountNumber string) ([]model.Card, error) {
	return s.listCardsByAccountFn(accountNumber)
}
func (s *stubCardService) ListCardsByClient(clientID uint64) ([]model.Card, error) {
	return s.listCardsByClientFn(clientID)
}
func (s *stubCardService) BlockCard(id uint64, changedBy int64) (*model.Card, error) {
	return s.blockCardFn(id, changedBy)
}
func (s *stubCardService) UnblockCard(id uint64, changedBy int64) (*model.Card, error) {
	return s.unblockCardFn(id, changedBy)
}
func (s *stubCardService) DeactivateCard(id uint64, changedBy int64) (*model.Card, error) {
	return s.deactivateCardFn(id, changedBy)
}
func (s *stubCardService) CreateAuthorizedPerson(ctx context.Context, ap *model.AuthorizedPerson) error {
	return s.createAuthorizedPersonFn(ctx, ap)
}
func (s *stubCardService) GetAuthorizedPerson(id uint64) (*model.AuthorizedPerson, error) {
	return s.getAuthorizedPersonFn(id)
}
func (s *stubCardService) CreateVirtualCard(ctx context.Context, accountNumber string, ownerID uint64, cardBrand, usageType string, maxUses, expiryMonths int, limitStr string) (*model.Card, string, error) {
	return s.createVirtualCardFn(ctx, accountNumber, ownerID, cardBrand, usageType, maxUses, expiryMonths, limitStr)
}
func (s *stubCardService) SetPin(cardID uint64, pin string) error { return s.setPinFn(cardID, pin) }
func (s *stubCardService) VerifyPin(cardID uint64, pin string) (bool, error) {
	return s.verifyPinFn(cardID, pin)
}
func (s *stubCardService) TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error) {
	return s.temporaryBlockCardFn(ctx, cardID, durationHours, reason)
}
func (s *stubCardService) UseCard(cardID uint64) error { return s.useCardFn(cardID) }

// ---------------------------------------------------------------------------
// stubCardRequestService — function-field stub of cardRequestServiceFacade
// ---------------------------------------------------------------------------

type stubCardRequestService struct {
	createRequestFn  func(ctx context.Context, req *model.CardRequest) error
	getRequestFn     func(id uint64) (*model.CardRequest, error)
	listRequestsFn   func(status string, page, pageSize int) ([]model.CardRequest, int64, error)
	listByClientFn   func(clientID uint64, page, pageSize int) ([]model.CardRequest, int64, error)
	approveRequestFn func(ctx context.Context, id, employeeID uint64) (*model.Card, error)
	rejectRequestFn  func(ctx context.Context, id, employeeID uint64, reason string) error
}

func (s *stubCardRequestService) CreateRequest(ctx context.Context, req *model.CardRequest) error {
	return s.createRequestFn(ctx, req)
}
func (s *stubCardRequestService) GetRequest(id uint64) (*model.CardRequest, error) {
	return s.getRequestFn(id)
}
func (s *stubCardRequestService) ListRequests(status string, page, pageSize int) ([]model.CardRequest, int64, error) {
	return s.listRequestsFn(status, page, pageSize)
}
func (s *stubCardRequestService) ListByClient(clientID uint64, page, pageSize int) ([]model.CardRequest, int64, error) {
	return s.listByClientFn(clientID, page, pageSize)
}
func (s *stubCardRequestService) ApproveRequest(ctx context.Context, id, employeeID uint64) (*model.Card, error) {
	return s.approveRequestFn(ctx, id, employeeID)
}
func (s *stubCardRequestService) RejectRequest(ctx context.Context, id, employeeID uint64, reason string) error {
	return s.rejectRequestFn(ctx, id, employeeID, reason)
}

// ---------------------------------------------------------------------------
// stubProducer — no-op producerFacade that records calls
// ---------------------------------------------------------------------------

type stubProducer struct {
	cardCreatedCount         int
	cardStatusChangedCount   int
	sendEmailCount           int
	generalNotificationCount int
	failSendEmail            bool
}

func (p *stubProducer) PublishCardCreated(_ context.Context, _ kafkamsg.CardCreatedMessage) error {
	p.cardCreatedCount++
	return nil
}
func (p *stubProducer) PublishCardStatusChanged(_ context.Context, _ kafkamsg.CardStatusChangedMessage) error {
	p.cardStatusChangedCount++
	return nil
}
func (p *stubProducer) SendEmail(_ context.Context, _ kafkamsg.SendEmailMessage) error {
	p.sendEmailCount++
	if p.failSendEmail {
		return errors.New("smtp unavailable")
	}
	return nil
}
func (p *stubProducer) PublishGeneralNotification(_ context.Context, _ kafkamsg.GeneralNotificationMessage) error {
	p.generalNotificationCount++
	return nil
}

// ---------------------------------------------------------------------------
// stubClientClient — clientpb.ClientServiceClient stub
// ---------------------------------------------------------------------------

type stubClientClient struct {
	getClientFn func(ctx context.Context, in *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error)
}

func (c *stubClientClient) CreateClient(ctx context.Context, in *clientpb.CreateClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}
func (c *stubClientClient) GetClient(ctx context.Context, in *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if c.getClientFn != nil {
		return c.getClientFn(ctx, in, opts...)
	}
	return &clientpb.ClientResponse{Id: in.Id, Email: "owner@example.com"}, nil
}
func (c *stubClientClient) GetClientByEmail(ctx context.Context, in *clientpb.GetClientByEmailRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}
func (c *stubClientClient) UpdateClient(ctx context.Context, in *clientpb.UpdateClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}
func (c *stubClientClient) ListClients(ctx context.Context, in *clientpb.ListClientsRequest, opts ...grpc.CallOption) (*clientpb.ListClientsResponse, error) {
	return nil, errors.New("not implemented")
}

package service

import (
	"context"
	"fmt"

	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type CardRequestService struct {
	repo     *repository.CardRequestRepository
	cardSvc  *CardService
	producer *kafkaprod.Producer
	notifier cardNotifier
}

func NewCardRequestService(repo *repository.CardRequestRepository, cardSvc *CardService, producer *kafkaprod.Producer) *CardRequestService {
	s := &CardRequestService{repo: repo, cardSvc: cardSvc, producer: producer}
	if producer != nil {
		s.notifier = producer
	}
	return s
}

func (s *CardRequestService) CreateRequest(ctx context.Context, req *model.CardRequest) error {
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[req.CardBrand] {
		return fmt.Errorf("CreateRequest: invalid card brand: %s: %w", req.CardBrand, ErrInvalidCard)
	}
	if req.CardType == "" {
		req.CardType = "debit"
	}
	req.Status = "pending"
	if err := s.repo.Create(req); err != nil {
		return err
	}
	_ = s.producer.PublishCardRequestCreated(ctx, kafkamsg.CardRequestCreatedMessage{
		RequestID:     req.ID,
		ClientID:      req.ClientID,
		AccountNumber: req.AccountNumber,
		CardBrand:     req.CardBrand,
	})
	s.notifyRequest(ctx, req, "CARD_REQUEST_CREATED", map[string]string{"card_brand": req.CardBrand})
	return nil
}

func (s *CardRequestService) GetRequest(id uint64) (*model.CardRequest, error) {
	return s.repo.GetByID(id)
}

func (s *CardRequestService) ListRequests(status string, page, pageSize int) ([]model.CardRequest, int64, error) {
	return s.repo.List(status, page, pageSize)
}

func (s *CardRequestService) ListByClient(clientID uint64, page, pageSize int) ([]model.CardRequest, int64, error) {
	return s.repo.ListByClient(clientID, page, pageSize)
}

func (s *CardRequestService) ApproveRequest(ctx context.Context, id, employeeID uint64) (*model.Card, error) {
	req, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("ApproveRequest(id=%d): %w", id, ErrCardRequestNotFound)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("ApproveRequest(id=%d): request is already %s: %w", id, req.Status, ErrCardRequestAlreadyDecided)
	}

	// Create the actual card using the existing CardService
	card, _, err := s.cardSvc.CreateCard(ctx, req.AccountNumber, req.ClientID, "client", req.CardBrand)
	if err != nil {
		return nil, fmt.Errorf("failed to create card: %w", err)
	}

	if err := s.repo.UpdateStatus(id, "approved", "", employeeID); err != nil {
		return nil, err
	}

	_ = s.producer.PublishCardRequestApproved(ctx, kafkamsg.CardRequestApprovedMessage{
		RequestID:  id,
		CardID:     card.ID,
		EmployeeID: employeeID,
	})
	s.notifyRequest(ctx, req, "CARD_REQUEST_APPROVED", map[string]string{"card_brand": req.CardBrand})

	return card, nil
}

func (s *CardRequestService) RejectRequest(ctx context.Context, id, employeeID uint64, reason string) error {
	req, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("RejectRequest(id=%d): %w", id, ErrCardRequestNotFound)
	}
	if req.Status != "pending" {
		return fmt.Errorf("RejectRequest(id=%d): request is already %s: %w", id, req.Status, ErrCardRequestAlreadyDecided)
	}
	if err := s.repo.UpdateStatus(id, "rejected", reason, employeeID); err != nil {
		return err
	}
	_ = s.producer.PublishCardRequestRejected(ctx, kafkamsg.CardRequestRejectedMessage{
		RequestID:  id,
		EmployeeID: employeeID,
		Reason:     reason,
	})
	s.notifyRequest(ctx, req, "CARD_REQUEST_REJECTED", map[string]string{"reason": reason})
	return nil
}

// notifyRequest emits a card-request-related in-app notification for the
// request's owning client. Best-effort: errors are discarded, and no-op when
// the notifier is unset (e.g. card-service started without Kafka) or when the
// request is nil. CardRequest.ClientID is always a client owner (no owner_type
// distinction), so no skip rule is required.
func (s *CardRequestService) notifyRequest(ctx context.Context, req *model.CardRequest, notifType string, data map[string]string) {
	if s.notifier == nil || req == nil {
		return
	}
	_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  req.ClientID,
		Type:    notifType,
		Data:    data,
		RefType: "card_request",
		RefID:   req.ID,
	})
}

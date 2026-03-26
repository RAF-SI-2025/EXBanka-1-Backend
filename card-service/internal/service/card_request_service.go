package service

import (
	"context"
	"fmt"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

type CardRequestService struct {
	repo     *repository.CardRequestRepository
	cardSvc  *CardService
	producer *kafkaprod.Producer
}

func NewCardRequestService(repo *repository.CardRequestRepository, cardSvc *CardService, producer *kafkaprod.Producer) *CardRequestService {
	return &CardRequestService{repo: repo, cardSvc: cardSvc, producer: producer}
}

func (s *CardRequestService) CreateRequest(ctx context.Context, req *model.CardRequest) error {
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[req.CardBrand] {
		return fmt.Errorf("invalid card brand: %s", req.CardBrand)
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
		return nil, fmt.Errorf("card request not found: %w", err)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("card request %d is already %s", id, req.Status)
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

	return card, nil
}

func (s *CardRequestService) RejectRequest(ctx context.Context, id, employeeID uint64, reason string) error {
	req, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("card request not found: %w", err)
	}
	if req.Status != "pending" {
		return fmt.Errorf("card request %d is already %s", id, req.Status)
	}
	if err := s.repo.UpdateStatus(id, "rejected", reason, employeeID); err != nil {
		return err
	}
	_ = s.producer.PublishCardRequestRejected(ctx, kafkamsg.CardRequestRejectedMessage{
		RequestID:  id,
		EmployeeID: employeeID,
		Reason:     reason,
	})
	return nil
}

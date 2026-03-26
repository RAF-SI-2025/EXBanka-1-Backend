package service

import (
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type PaymentRecipientService struct {
	repo *repository.PaymentRecipientRepository
}

func NewPaymentRecipientService(repo *repository.PaymentRecipientRepository) *PaymentRecipientService {
	return &PaymentRecipientService{repo: repo}
}

func (s *PaymentRecipientService) Create(pr *model.PaymentRecipient) error {
	return s.repo.Create(pr)
}

func (s *PaymentRecipientService) ListByClient(clientID uint64) ([]model.PaymentRecipient, error) {
	return s.repo.ListByClient(clientID)
}

func (s *PaymentRecipientService) Update(id uint64, recipientName, accountNumber *string) (*model.PaymentRecipient, error) {
	pr, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if recipientName != nil {
		pr.RecipientName = *recipientName
	}
	if accountNumber != nil {
		pr.AccountNumber = *accountNumber
	}
	if err := s.repo.Update(pr); err != nil {
		return nil, err
	}
	return pr, nil
}

func (s *PaymentRecipientService) Delete(id uint64) error {
	return s.repo.Delete(id)
}

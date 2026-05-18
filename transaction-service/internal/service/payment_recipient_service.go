package service

import (
	"errors"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// ErrPaymentRecipientNotFound — caller referenced a payment recipient that
// doesn't exist. Surfaces as gRPC NotFound → HTTP 404 at the gateway.
// Defined locally rather than in errors.go because the recipient service
// is the only place it's produced.
var ErrPaymentRecipientNotFound = svcerr.New(codes.NotFound, "payment recipient not found")

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

func (s *PaymentRecipientService) GetByID(id uint64) (*model.PaymentRecipient, error) {
	pr, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPaymentRecipientNotFound
		}
		return nil, err
	}
	return pr, nil
}

func (s *PaymentRecipientService) Update(id uint64, recipientName, accountNumber *string) (*model.PaymentRecipient, error) {
	pr, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPaymentRecipientNotFound
		}
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
	// repository.Delete returns nil + RowsAffected=0 when the row is
	// absent; surface a typed not-found so the gateway returns 404
	// instead of 200/Empty.
	ok, err := s.repo.DeleteIfExists(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPaymentRecipientNotFound
	}
	return nil
}

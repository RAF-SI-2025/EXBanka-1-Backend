package service

import (
	"context"
	"errors"
	"time"

	"github.com/exbanka/card-service/internal/cache"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

type CardService struct {
	cardRepo *repository.CardRepository
	authRepo *repository.AuthorizedPersonRepository
	cache    *cache.RedisCache
}

func NewCardService(cardRepo *repository.CardRepository, authRepo *repository.AuthorizedPersonRepository, cache *cache.RedisCache) *CardService {
	return &CardService{cardRepo: cardRepo, authRepo: authRepo, cache: cache}
}

func (s *CardService) CreateCard(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand string) (*model.Card, string, error) {
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[cardBrand] {
		return nil, "", errors.New("invalid card brand")
	}
	validOwnerTypes := map[string]bool{"client": true, "authorized_person": true}
	if !validOwnerTypes[ownerType] {
		return nil, "", errors.New("invalid owner type")
	}

	cardNumber := GenerateCardNumber(cardBrand)
	cvv := GenerateCVV()
	masked := MaskCardNumber(cardNumber)

	card := &model.Card{
		CardNumber:     masked,
		CardNumberFull: cardNumber,
		CVV:            cvv,
		CardType:       "debit",
		CardBrand:      cardBrand,
		AccountNumber:  accountNumber,
		OwnerID:        ownerID,
		OwnerType:      ownerType,
		Status:         "active",
		ExpiresAt:      time.Now().AddDate(3, 0, 0),
	}

	if err := s.cardRepo.Create(card); err != nil {
		return nil, "", err
	}
	return card, cvv, nil
}

func (s *CardService) GetCard(id uint64) (*model.Card, error) {
	return s.cardRepo.GetByID(id)
}

func (s *CardService) ListCardsByAccount(accountNumber string) ([]model.Card, error) {
	return s.cardRepo.ListByAccount(accountNumber)
}

func (s *CardService) ListCardsByClient(clientID uint64) ([]model.Card, error) {
	return s.cardRepo.ListByClient(clientID)
}

func (s *CardService) BlockCard(id uint64) (*model.Card, error) {
	return s.cardRepo.UpdateStatus(id, "blocked")
}

func (s *CardService) UnblockCard(id uint64) (*model.Card, error) {
	return s.cardRepo.UpdateStatus(id, "active")
}

func (s *CardService) DeactivateCard(id uint64) (*model.Card, error) {
	return s.cardRepo.UpdateStatus(id, "deactivated")
}

func (s *CardService) CreateAuthorizedPerson(ctx context.Context, ap *model.AuthorizedPerson) error {
	return s.authRepo.Create(ap)
}

func (s *CardService) GetAuthorizedPerson(id uint64) (*model.AuthorizedPerson, error) {
	return s.authRepo.GetByID(id)
}

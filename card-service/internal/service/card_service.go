package service

import (
	"context"
	"errors"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/shopspring/decimal"
	"github.com/exbanka/card-service/internal/cache"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

type CardService struct {
	cardRepo  *repository.CardRepository
	blockRepo *repository.CardBlockRepository
	authRepo  *repository.AuthorizedPersonRepository
	producer  *kafkaprod.Producer
	cache     *cache.RedisCache
}

func NewCardService(cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository, authRepo *repository.AuthorizedPersonRepository, producer *kafkaprod.Producer, cache *cache.RedisCache) *CardService {
	return &CardService{cardRepo: cardRepo, blockRepo: blockRepo, authRepo: authRepo, producer: producer, cache: cache}
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

func (s *CardService) CreateVirtualCard(ctx context.Context, accountNumber string, ownerID uint64, cardBrand, usageType string, maxUses, expiryMonths int, limitStr string) (*model.Card, string, error) {
	validUsageTypes := map[string]bool{"single_use": true, "multi_use": true}
	if !validUsageTypes[usageType] {
		return nil, "", errors.New("usage_type must be 'single_use' or 'multi_use'")
	}
	if expiryMonths < 1 || expiryMonths > 3 {
		return nil, "", errors.New("expiry_months must be between 1 and 3")
	}
	if usageType == "multi_use" && maxUses < 2 {
		return nil, "", errors.New("multi_use cards must have max_uses >= 2")
	}
	if usageType == "single_use" {
		maxUses = 1
	}
	limit, err := decimal.NewFromString(limitStr)
	if err != nil || limit.LessThanOrEqual(decimal.Zero) {
		return nil, "", errors.New("invalid limit value")
	}
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[cardBrand] {
		return nil, "", errors.New("invalid card brand")
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
		OwnerType:      "client",
		Status:         "active",
		CardLimit:      limit,
		IsVirtual:      true,
		UsageType:      usageType,
		MaxUses:        maxUses,
		UsesRemaining:  maxUses,
		ExpiresAt:      time.Now().AddDate(0, expiryMonths, 0),
	}
	if err := s.cardRepo.Create(card); err != nil {
		return nil, "", err
	}
	_ = s.producer.PublishVirtualCardCreated(ctx, kafkamsg.VirtualCardCreatedMessage{
		CardID:        card.ID,
		AccountNumber: card.AccountNumber,
		UsageType:     card.UsageType,
		MaxUses:       card.MaxUses,
	})
	return card, cvv, nil
}

func (s *CardService) SetPin(cardID uint64, pin string) error {
	matched, _ := regexp.MatchString(`^\d{4}$`, pin)
	if !matched {
		return errors.New("PIN must be exactly 4 digits")
	}
	card, err := s.cardRepo.GetByID(cardID)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	card.PinHash = string(hash)
	card.PinAttempts = 0
	return s.cardRepo.Update(card)
}

func (s *CardService) VerifyPin(cardID uint64, pin string) (bool, error) {
	card, err := s.cardRepo.GetByID(cardID)
	if err != nil {
		return false, err
	}
	if card.PinAttempts >= 3 {
		return false, errors.New("card blocked due to too many failed PIN attempts")
	}
	err = bcrypt.CompareHashAndPassword([]byte(card.PinHash), []byte(pin))
	if err != nil {
		card.PinAttempts++
		if card.PinAttempts >= 3 {
			card.Status = "blocked"
		}
		_ = s.cardRepo.Update(card)
		return false, nil
	}
	card.PinAttempts = 0
	_ = s.cardRepo.Update(card)
	return true, nil
}

func (s *CardService) TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error) {
	if durationHours <= 0 || durationHours > 720 {
		return nil, errors.New("duration_hours must be between 1 and 720")
	}
	card, err := s.cardRepo.GetByID(cardID)
	if err != nil {
		return nil, err
	}
	card.Status = "blocked"
	if err := s.cardRepo.Update(card); err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(time.Duration(durationHours) * time.Hour)
	block := &model.CardBlock{
		CardID:    cardID,
		Reason:    reason,
		BlockedAt: time.Now(),
		ExpiresAt: &expiresAt,
		Active:    true,
	}
	if err := s.blockRepo.Create(block); err != nil {
		return nil, err
	}
	_ = s.producer.PublishCardTemporaryBlocked(ctx, kafkamsg.CardTemporaryBlockedMessage{
		CardID:    card.ID,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Reason:    reason,
	})
	return card, nil
}

func (s *CardService) UseCard(cardID uint64) error {
	card, err := s.cardRepo.GetByID(cardID)
	if err != nil {
		return err
	}
	if !card.IsVirtual || card.UsageType == "unlimited" {
		return nil
	}
	if card.UsesRemaining <= 0 {
		return errors.New("virtual card has no remaining uses")
	}
	card.UsesRemaining--
	if card.UsesRemaining == 0 {
		card.Status = "deactivated"
	}
	return s.cardRepo.Update(card)
}

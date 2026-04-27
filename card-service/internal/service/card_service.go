package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/cache"
	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/changelog"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/shopspring/decimal"
)

type CardService struct {
	cardRepo      *repository.CardRepository
	blockRepo     *repository.CardBlockRepository
	authRepo      *repository.AuthorizedPersonRepository
	producer      *kafkaprod.Producer
	cache         *cache.RedisCache
	changelogRepo *repository.ChangelogRepository
	db            *gorm.DB
}

func NewCardService(cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository, authRepo *repository.AuthorizedPersonRepository, producer *kafkaprod.Producer, cache *cache.RedisCache, db *gorm.DB, changelogRepo ...*repository.ChangelogRepository) *CardService {
	svc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, authRepo: authRepo, producer: producer, cache: cache, db: db}
	if len(changelogRepo) > 0 {
		svc.changelogRepo = changelogRepo[0]
	}
	return svc
}

func (s *CardService) CreateCard(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand string) (*model.Card, string, error) {
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[cardBrand] {
		return nil, "", fmt.Errorf("CreateCard: card brand must be one of: visa, mastercard, dinacard, amex; got: %s: %w", cardBrand, ErrInvalidCard)
	}
	validOwnerTypes := map[string]bool{"client": true, "authorized_person": true}
	if !validOwnerTypes[ownerType] {
		return nil, "", fmt.Errorf("CreateCard: owner type must be one of: client, authorized_person; got: %s: %w", ownerType, ErrInvalidCard)
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

	// Serialize card creation for this account using a PostgreSQL advisory lock
	// to eliminate the TOCTOU race between the card count check and the INSERT.
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", accountNumber).Error; e != nil {
			return e
		}

		// Enforce card-per-account limits based on owner type.
		// "authorized_person" indicates a business account context: max 1 card per owner per account.
		// "client" indicates a personal account context: max 2 cards total per account.
		if ownerType == "authorized_person" {
			var count int64
			if e := tx.Model(&model.Card{}).Where("account_number = ? AND owner_id = ? AND status != ?", accountNumber, ownerID, "deactivated").Count(&count).Error; e != nil {
				return fmt.Errorf("failed to check card count: %w", e)
			}
			if count >= 1 {
				return fmt.Errorf("CreateCard: business accounts can have at most 1 card per person; person %d already has a card on account %s: %w", ownerID, accountNumber, ErrCardLimitReached)
			}
		} else {
			var count int64
			if e := tx.Model(&model.Card{}).Where("account_number = ? AND status != ?", accountNumber, "deactivated").Count(&count).Error; e != nil {
				return fmt.Errorf("failed to check card count: %w", e)
			}
			if count >= 2 {
				return fmt.Errorf("CreateCard: personal accounts can have at most 2 cards; account %s already has %d: %w", accountNumber, count, ErrCardLimitReached)
			}
		}

		if e := tx.Create(card).Error; e != nil {
			return fmt.Errorf("CreateCard: account %s not found or inactive: %w", accountNumber, ErrAccountInactive)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	CardCreatedTotal.WithLabelValues("physical").Inc()
	return card, cvv, nil
}

const cardCacheTTL = 3 * time.Minute

func (s *CardService) GetCard(id uint64) (*model.Card, error) {
	ctx := context.Background()
	key := fmt.Sprintf("card:id:%d", id)

	if s.cache != nil {
		var cached model.Card
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	card, err := s.cardRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, card, cardCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return card, nil
}

func (s *CardService) ListCardsByAccount(accountNumber string) ([]model.Card, error) {
	return s.cardRepo.ListByAccount(accountNumber)
}

func (s *CardService) ListCardsByClient(clientID uint64) ([]model.Card, error) {
	return s.cardRepo.ListByClient(clientID)
}

func (s *CardService) BlockCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	var oldStatus string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card id=%d: %w", id, ErrCardNotFound)
			}
			return e
		}
		oldStatus = card.Status
		if card.Status == "blocked" {
			return fmt.Errorf("BlockCard(id=%d): %w", id, ErrCardBlocked)
		}
		if card.Status == "deactivated" {
			return fmt.Errorf("BlockCard(id=%d): %w", id, ErrCardDeactivated)
		}
		card.Status = "blocked"
		return tx.Save(card).Error
	})
	if err == nil {
		CardStatusChangesTotal.WithLabelValues("block").Inc()
		s.invalidateCardCache(id)
		if s.changelogRepo != nil {
			entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, oldStatus, "blocked", "")
			_ = s.changelogRepo.Create(entry)
		}
	}
	return card, err
}

func (s *CardService) UnblockCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card id=%d: %w", id, ErrCardNotFound)
			}
			return e
		}
		if card.Status != "blocked" {
			return fmt.Errorf("UnblockCard(id=%d): %w", id, ErrCardNotBlocked)
		}
		card.Status = "active"
		return tx.Save(card).Error
	})
	if err == nil {
		CardStatusChangesTotal.WithLabelValues("unblock").Inc()
		s.invalidateCardCache(id)
		if s.changelogRepo != nil {
			entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, "blocked", "active", "")
			_ = s.changelogRepo.Create(entry)
		}
	}
	return card, err
}

func (s *CardService) DeactivateCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	var oldStatus string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card id=%d: %w", id, ErrCardNotFound)
			}
			return e
		}
		if card.Status == "deactivated" {
			return fmt.Errorf("DeactivateCard(id=%d): %w", id, ErrCardDeactivated)
		}
		oldStatus = card.Status
		card.Status = "deactivated"
		return tx.Save(card).Error
	})
	if err == nil {
		CardStatusChangesTotal.WithLabelValues("deactivate").Inc()
		s.invalidateCardCache(id)
		if s.changelogRepo != nil {
			entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, oldStatus, "deactivated", "")
			_ = s.changelogRepo.Create(entry)
		}
	}
	return card, err
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
		return nil, "", fmt.Errorf("CreateVirtualCard: usage_type must be one of: single_use, multi_use; got: %s: %w", usageType, ErrInvalidCard)
	}
	if expiryMonths < 1 || expiryMonths > 3 {
		return nil, "", fmt.Errorf("CreateVirtualCard: expiry_months must be between 1 and 3; got: %d: %w", expiryMonths, ErrInvalidCard)
	}
	if usageType == "multi_use" && maxUses < 2 {
		return nil, "", fmt.Errorf("CreateVirtualCard: multi_use cards must have max_uses >= 2; got: %d: %w", maxUses, ErrInvalidCard)
	}
	if usageType == "single_use" {
		maxUses = 1
	}
	limit, err := decimal.NewFromString(limitStr)
	if err != nil || limit.LessThanOrEqual(decimal.Zero) {
		return nil, "", fmt.Errorf("CreateVirtualCard: limit must be a positive number; got: %s: %w", limitStr, ErrInvalidCard)
	}
	validBrands := map[string]bool{"visa": true, "mastercard": true, "dinacard": true, "amex": true}
	if !validBrands[cardBrand] {
		return nil, "", fmt.Errorf("CreateVirtualCard: card brand must be one of: visa, mastercard, dinacard, amex; got: %s: %w", cardBrand, ErrInvalidCard)
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
	CardCreatedTotal.WithLabelValues("virtual").Inc()
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
		return fmt.Errorf("SetPin(card=%d): %w", cardID, ErrInvalidPIN)
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
	if err := s.cardRepo.Update(card); err != nil {
		return err
	}
	s.invalidateCardCache(cardID)
	return nil
}

func (s *CardService) VerifyPin(cardID uint64, pin string) (bool, error) {
	var ok bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		card, e := s.cardRepo.GetByIDForUpdate(tx, cardID)
		if e != nil {
			return e
		}
		if card.PinAttempts >= 3 {
			return fmt.Errorf("VerifyPin(card=%d): %w", cardID, ErrCardLocked)
		}
		if e = bcrypt.CompareHashAndPassword([]byte(card.PinHash), []byte(pin)); e != nil {
			card.PinAttempts++
			if card.PinAttempts >= 3 {
				card.Status = "blocked"
				CardPinAttemptsTotal.WithLabelValues("locked").Inc()
			} else {
				CardPinAttemptsTotal.WithLabelValues("failure").Inc()
			}
			ok = false
			return tx.Save(card).Error
		}
		card.PinAttempts = 0
		ok = true
		CardPinAttemptsTotal.WithLabelValues("success").Inc()
		return tx.Save(card).Error
	})
	return ok, err
}

func (s *CardService) TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error) {
	if durationHours <= 0 || durationHours > 720 {
		return nil, fmt.Errorf("TemporaryBlockCard(card=%d, duration=%d): %w", cardID, durationHours, ErrInvalidBlockDuration)
	}
	expiresAt := time.Now().Add(time.Duration(durationHours) * time.Hour)
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, cardID)
		if e != nil {
			return e
		}
		card.Status = "blocked"
		if e = tx.Save(card).Error; e != nil {
			return e
		}
		block := &model.CardBlock{
			CardID:    cardID,
			Reason:    reason,
			BlockedAt: time.Now(),
			ExpiresAt: &expiresAt,
			Active:    true,
		}
		return tx.Create(block).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCardCache(cardID)
	// Publish Kafka event after the transaction commits (not inside TX).
	_ = s.producer.PublishCardTemporaryBlocked(ctx, kafkamsg.CardTemporaryBlockedMessage{
		CardID:    card.ID,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Reason:    reason,
	})
	return card, nil
}

func (s *CardService) UseCard(cardID uint64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		card, err := s.cardRepo.GetByIDForUpdate(tx, cardID)
		if err != nil {
			return err
		}
		if !card.IsVirtual || card.UsageType == "unlimited" {
			return nil
		}
		if card.UsesRemaining <= 0 {
			return fmt.Errorf("UseCard(card=%d): %w", cardID, ErrSingleUseAlreadyUsed)
		}
		card.UsesRemaining--
		if card.UsesRemaining == 0 {
			card.Status = "deactivated"
		}
		return tx.Save(card).Error
	})
	if err == nil {
		s.invalidateCardCache(cardID)
	}
	return err
}

// invalidateCardCache removes a card from Redis cache after a mutation.
func (s *CardService) invalidateCardCache(cardID uint64) {
	if s.cache != nil {
		_ = s.cache.Delete(context.Background(), fmt.Sprintf("card:id:%d", cardID))
	}
}

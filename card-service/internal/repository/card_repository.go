package repository

import (
	"fmt"
	"time"

	"github.com/exbanka/card-service/internal/model"
	shared "github.com/exbanka/contract/shared"
	"gorm.io/gorm"
)

type CardRepository struct {
	db *gorm.DB
}

func NewCardRepository(db *gorm.DB) *CardRepository {
	return &CardRepository{db: db}
}

func (r *CardRepository) Create(card *model.Card) error {
	return r.db.Create(card).Error
}

func (r *CardRepository) GetByID(id uint64) (*model.Card, error) {
	var card model.Card
	if err := r.db.First(&card, id).Error; err != nil {
		return nil, err
	}
	return &card, nil
}

func (r *CardRepository) ListByAccount(accountNumber string) ([]model.Card, error) {
	var cards []model.Card
	if err := r.db.Where("account_number = ?", accountNumber).Find(&cards).Error; err != nil {
		return nil, err
	}
	return cards, nil
}

func (r *CardRepository) ListByClient(clientID uint64) ([]model.Card, error) {
	var cards []model.Card
	if err := r.db.Where("owner_id = ? AND owner_type = ?", clientID, "client").Find(&cards).Error; err != nil {
		return nil, err
	}
	return cards, nil
}

func (r *CardRepository) UpdateStatus(id uint64, status string) (*model.Card, error) {
	var card model.Card
	if err := r.db.First(&card, id).Error; err != nil {
		return nil, err
	}
	card.Status = status
	result := r.db.Save(&card)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, fmt.Errorf("%w: card %d was modified concurrently", shared.ErrOptimisticLock, id)
	}
	return &card, nil
}

func (r *CardRepository) Update(card *model.Card) error {
	result := r.db.Save(card)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: card %d was modified concurrently", shared.ErrOptimisticLock, card.ID)
	}
	return nil
}

func (r *CardRepository) FindExpiredVirtual(now time.Time) ([]model.Card, error) {
	var cards []model.Card
	err := r.db.Where("is_virtual = ? AND status = ? AND expires_at <= ?", true, "active", now).Find(&cards).Error
	return cards, err
}

func (r *CardRepository) CountByAccount(accountNumber string) (int64, error) {
	var count int64
	err := r.db.Model(&model.Card{}).Where("account_number = ? AND status != ?", accountNumber, "deactivated").Count(&count).Error
	return count, err
}

func (r *CardRepository) CountByAccountAndOwner(accountNumber string, ownerID uint64) (int64, error) {
	var count int64
	err := r.db.Model(&model.Card{}).Where("account_number = ? AND owner_id = ? AND status != ?", accountNumber, ownerID, "deactivated").Count(&count).Error
	return count, err
}

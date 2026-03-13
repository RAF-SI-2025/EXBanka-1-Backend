package repository

import (
	"github.com/exbanka/card-service/internal/model"
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
	if err := r.db.Save(&card).Error; err != nil {
		return nil, err
	}
	return &card, nil
}

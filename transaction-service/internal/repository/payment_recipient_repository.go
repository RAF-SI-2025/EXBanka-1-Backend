package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type PaymentRecipientRepository struct {
	db *gorm.DB
}

func NewPaymentRecipientRepository(db *gorm.DB) *PaymentRecipientRepository {
	return &PaymentRecipientRepository{db: db}
}

func (r *PaymentRecipientRepository) Create(pr *model.PaymentRecipient) error {
	return r.db.Create(pr).Error
}

func (r *PaymentRecipientRepository) ListByClient(clientID uint64) ([]model.PaymentRecipient, error) {
	var recipients []model.PaymentRecipient
	if err := r.db.Where("client_id = ?", clientID).Find(&recipients).Error; err != nil {
		return nil, err
	}
	return recipients, nil
}

func (r *PaymentRecipientRepository) GetByID(id uint64) (*model.PaymentRecipient, error) {
	var recipient model.PaymentRecipient
	if err := r.db.First(&recipient, id).Error; err != nil {
		return nil, err
	}
	return &recipient, nil
}

func (r *PaymentRecipientRepository) Update(pr *model.PaymentRecipient) error {
	return r.db.Save(pr).Error
}

func (r *PaymentRecipientRepository) Delete(id uint64) error {
	return r.db.Delete(&model.PaymentRecipient{}, id).Error
}

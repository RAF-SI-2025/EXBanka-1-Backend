package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type VerificationCodeRepository struct {
	db *gorm.DB
}

func NewVerificationCodeRepository(db *gorm.DB) *VerificationCodeRepository {
	return &VerificationCodeRepository{db: db}
}

func (r *VerificationCodeRepository) Create(vc *model.VerificationCode) error {
	return r.db.Create(vc).Error
}

func (r *VerificationCodeRepository) GetByClientAndTransaction(clientID, transactionID uint64, txType string) (*model.VerificationCode, error) {
	var vc model.VerificationCode
	if err := r.db.Where("client_id = ? AND transaction_id = ? AND transaction_type = ?", clientID, transactionID, txType).
		Order("id DESC").First(&vc).Error; err != nil {
		return nil, err
	}
	return &vc, nil
}

func (r *VerificationCodeRepository) IncrementAttempts(id uint64) error {
	return r.db.Model(&model.VerificationCode{}).Where("id = ?", id).
		UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error
}

func (r *VerificationCodeRepository) MarkUsed(id uint64) error {
	return r.db.Model(&model.VerificationCode{}).Where("id = ?", id).Update("used", true).Error
}

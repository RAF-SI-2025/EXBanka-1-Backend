package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type TransferRepository struct {
	db *gorm.DB
}

func NewTransferRepository(db *gorm.DB) *TransferRepository {
	return &TransferRepository{db: db}
}

func (r *TransferRepository) Create(transfer *model.Transfer) error {
	return r.db.Create(transfer).Error
}

func (r *TransferRepository) GetByID(id uint64) (*model.Transfer, error) {
	var transfer model.Transfer
	if err := r.db.First(&transfer, id).Error; err != nil {
		return nil, err
	}
	return &transfer, nil
}

func (r *TransferRepository) GetByIdempotencyKey(key string) (*model.Transfer, error) {
	var transfer model.Transfer
	if err := r.db.Where("idempotency_key = ?", key).First(&transfer).Error; err != nil {
		return nil, err
	}
	return &transfer, nil
}

func (r *TransferRepository) UpdateStatus(id uint64, status string) error {
	return r.db.Model(&model.Transfer{}).Where("id = ?", id).Update("status", status).Error
}

func (r *TransferRepository) UpdateStatusWithReason(id uint64, status, reason string) error {
	return r.db.Model(&model.Transfer{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":         status,
		"failure_reason": reason,
	}).Error
}

func (r *TransferRepository) ListByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error) {
	if len(accountNumbers) == 0 {
		return nil, 0, nil
	}

	q := r.db.Model(&model.Transfer{}).Where(
		"from_account_number IN ? OR to_account_number IN ?",
		accountNumbers, accountNumbers,
	)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var transfers []model.Transfer
	if err := q.Order("timestamp DESC").Offset(offset).Limit(pageSize).Find(&transfers).Error; err != nil {
		return nil, 0, err
	}
	return transfers, total, nil
}

package repository

import (
	"fmt"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
	"gorm.io/gorm"
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
	var transfer model.Transfer
	if err := r.db.First(&transfer, id).Error; err != nil {
		return err
	}
	transfer.Status = status
	res := r.db.Save(&transfer)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: transfer %d", shared.ErrOptimisticLock, id)
	}
	return nil
}

func (r *TransferRepository) UpdateStatusWithReason(id uint64, status, reason string) error {
	var transfer model.Transfer
	if err := r.db.First(&transfer, id).Error; err != nil {
		return err
	}
	transfer.Status = status
	transfer.FailureReason = reason
	res := r.db.Save(&transfer)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: transfer %d", shared.ErrOptimisticLock, id)
	}
	return nil
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

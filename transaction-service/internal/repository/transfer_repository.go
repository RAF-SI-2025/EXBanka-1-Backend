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

func (r *TransferRepository) ListByClient(clientID uint64, page, pageSize int) ([]model.Transfer, int64, error) {
	q := r.db.Model(&model.Transfer{})

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

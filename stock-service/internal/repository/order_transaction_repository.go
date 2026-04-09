package repository

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

type OrderTransactionRepository struct {
	db *gorm.DB
}

func NewOrderTransactionRepository(db *gorm.DB) *OrderTransactionRepository {
	return &OrderTransactionRepository{db: db}
}

func (r *OrderTransactionRepository) Create(tx *model.OrderTransaction) error {
	return r.db.Create(tx).Error
}

func (r *OrderTransactionRepository) ListByOrderID(orderID uint64) ([]model.OrderTransaction, error) {
	var txns []model.OrderTransaction
	if err := r.db.Where("order_id = ?", orderID).
		Order("executed_at ASC").Find(&txns).Error; err != nil {
		return nil, err
	}
	return txns, nil
}

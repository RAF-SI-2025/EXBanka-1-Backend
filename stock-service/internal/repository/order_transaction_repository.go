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

// Update persists changes to an existing OrderTransaction. Used by the fill
// saga's convert_amount step to record native/converted amounts and FX rate
// on the transaction row before settlement proceeds.
func (r *OrderTransactionRepository) Update(tx *model.OrderTransaction) error {
	return r.db.Save(tx).Error
}

func (r *OrderTransactionRepository) ListByOrderID(orderID uint64) ([]model.OrderTransaction, error) {
	var txns []model.OrderTransaction
	if err := r.db.Where("order_id = ?", orderID).
		Order("executed_at ASC").Find(&txns).Error; err != nil {
		return nil, err
	}
	return txns, nil
}

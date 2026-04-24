package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

func (r *OrderRepository) Create(order *model.Order) error {
	return r.db.Create(order).Error
}

func (r *OrderRepository) GetByID(id uint64) (*model.Order, error) {
	var order model.Order
	if err := r.db.Preload("Listing").Preload("Listing.Exchange").First(&order, id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepository) Update(order *model.Order) error {
	result := r.db.Save(order)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// Delete removes an order by id. Used by placement-saga compensation when a
// later step fails after the order row was persisted. No-op if the row does
// not exist (returns nil) so compensation paths can be called unconditionally.
func (r *OrderRepository) Delete(id uint64) error {
	return r.db.Delete(&model.Order{}, id).Error
}

func (r *OrderRepository) ListByUser(userID uint64, filter OrderFilter) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	q := r.db.Model(&model.Order{}).Where("user_id = ?", userID)
	q = applyOrderFilters(q, filter)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("created_at DESC").Preload("Listing").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (r *OrderRepository) ListAll(filter OrderFilter) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	q := r.db.Model(&model.Order{})
	q = applyOrderFilters(q, filter)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("created_at DESC").Preload("Listing").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (r *OrderRepository) ListActiveApproved() ([]model.Order, error) {
	var orders []model.Order
	err := r.db.Where("status = ? AND is_done = ?", "approved", false).
		Preload("Listing").Preload("Listing.Exchange").
		Find(&orders).Error
	return orders, err
}

func applyOrderFilters(q *gorm.DB, filter OrderFilter) *gorm.DB {
	if filter.Status != "" {
		if filter.Status == "done" {
			q = q.Where("is_done = ?", true)
		} else {
			q = q.Where("status = ?", filter.Status)
		}
	}
	if filter.Direction != "" {
		q = q.Where("direction = ?", filter.Direction)
	}
	if filter.OrderType != "" {
		q = q.Where("order_type = ?", filter.OrderType)
	}
	return q
}

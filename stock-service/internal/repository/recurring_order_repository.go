package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type RecurringOrderRepository struct {
	db *gorm.DB
}

func NewRecurringOrderRepository(db *gorm.DB) *RecurringOrderRepository {
	return &RecurringOrderRepository{db: db}
}

func (r *RecurringOrderRepository) Create(row *model.RecurringOrder) error {
	return r.db.Create(row).Error
}

func (r *RecurringOrderRepository) GetByID(id uint64) (*model.RecurringOrder, error) {
	var out model.RecurringOrder
	if err := r.db.First(&out, id).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *RecurringOrderRepository) Save(row *model.RecurringOrder) error {
	res := r.db.Save(row)
	return CheckRowsAffected(res)
}

func (r *RecurringOrderRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64) ([]model.RecurringOrder, error) {
	q := scopeOwner(r.db.Model(&model.RecurringOrder{}), "owner_type", "owner_id", ownerType, ownerID)
	var out []model.RecurringOrder
	err := q.Order("created_at DESC").Find(&out).Error
	return out, err
}

// ListDue returns active rows whose NextRun is in the past and whose
// optional EndDate hasn't elapsed. Used by the cron tick.
func (r *RecurringOrderRepository) ListDue(now time.Time) ([]model.RecurringOrder, error) {
	var out []model.RecurringOrder
	err := r.db.Where(
		"status = ? AND next_run <= ? AND (end_date IS NULL OR end_date > ?)",
		model.RecurringOrderStatusActive, now, now,
	).Find(&out).Error
	return out, err
}

package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type PriceAlertRepository struct {
	db *gorm.DB
}

func NewPriceAlertRepository(db *gorm.DB) *PriceAlertRepository {
	return &PriceAlertRepository{db: db}
}

// DB exposes the underlying *gorm.DB for callers that need raw queries
// (e.g. PriceAlertCron scans active alerts across all owners).
func (r *PriceAlertRepository) DB() *gorm.DB { return r.db }

func (r *PriceAlertRepository) Create(a *model.PriceAlert) error {
	return r.db.Create(a).Error
}

func (r *PriceAlertRepository) GetByID(id uint64) (*model.PriceAlert, error) {
	var out model.PriceAlert
	if err := r.db.First(&out, id).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// Save uses the BeforeUpdate hook for optimistic locking.
func (r *PriceAlertRepository) Save(a *model.PriceAlert) error {
	res := r.db.Save(a)
	return CheckRowsAffected(res)
}

func (r *PriceAlertRepository) Delete(id uint64, ownerType model.OwnerType, ownerID *uint64) (bool, error) {
	q := scopeOwner(r.db, "owner_type", "owner_id", ownerType, ownerID)
	res := q.Where("id = ?", id).Delete(&model.PriceAlert{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *PriceAlertRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64) ([]model.PriceAlert, error) {
	q := scopeOwner(r.db.Model(&model.PriceAlert{}), "owner_type", "owner_id", ownerType, ownerID)
	var out []model.PriceAlert
	if err := q.Order("created_at DESC").Find(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []model.PriceAlert{}, nil
		}
		return nil, err
	}
	return out, nil
}

// ListActiveByListing returns active alerts for a single listing —
// the input to the reactive evaluator after a price refresh.
func (r *PriceAlertRepository) ListActiveByListing(listingID uint64) ([]model.PriceAlert, error) {
	var out []model.PriceAlert
	if err := r.db.Where("listing_id = ? AND active = ?", listingID, true).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

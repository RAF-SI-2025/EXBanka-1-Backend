package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingDailyPriceRepository struct {
	db *gorm.DB
}

func NewListingDailyPriceRepository(db *gorm.DB) *ListingDailyPriceRepository {
	return &ListingDailyPriceRepository{db: db}
}

func (r *ListingDailyPriceRepository) Create(info *model.ListingDailyPriceInfo) error {
	return r.db.Create(info).Error
}

func (r *ListingDailyPriceRepository) UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ListingDailyPriceInfo
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("listing_id = ? AND date = ?", info.ListingID, info.Date).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(info).Error
			}
			return err
		}
		existing.Price = info.Price
		existing.High = info.High
		existing.Low = info.Low
		existing.Change = info.Change
		existing.Volume = info.Volume
		return tx.Save(&existing).Error
	})
}

func (r *ListingDailyPriceRepository) GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	var history []model.ListingDailyPriceInfo
	var total int64

	q := r.db.Model(&model.ListingDailyPriceInfo{}).
		Where("listing_id = ?", listingID)

	if !from.IsZero() {
		q = q.Where("date >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where("date <= ?", to)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 365 {
		pageSize = 30
	}

	if err := q.Order("date DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&history).Error; err != nil {
		return nil, 0, err
	}
	return history, total, nil
}

package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingRepository struct {
	db *gorm.DB
}

func NewListingRepository(db *gorm.DB) *ListingRepository {
	return &ListingRepository{db: db}
}

func (r *ListingRepository) Create(listing *model.Listing) error {
	return r.db.Create(listing).Error
}

func (r *ListingRepository) GetByID(id uint64) (*model.Listing, error) {
	var listing model.Listing
	if err := r.db.Preload("Exchange").First(&listing, id).Error; err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *ListingRepository) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	var listing model.Listing
	if err := r.db.Where("security_id = ? AND security_type = ?", securityID, securityType).
		Preload("Exchange").First(&listing).Error; err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *ListingRepository) Update(listing *model.Listing) error {
	result := r.db.Save(listing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *ListingRepository) UpsertBySecurity(listing *model.Listing) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Listing
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_id = ? AND security_type = ?", listing.SecurityID, listing.SecurityType).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(listing).Error
			}
			return err
		}
		// Update price data
		existing.ExchangeID = listing.ExchangeID
		existing.Price = listing.Price
		existing.High = listing.High
		existing.Low = listing.Low
		existing.Change = listing.Change
		existing.Volume = listing.Volume
		existing.LastRefresh = listing.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *ListingRepository) ListAll() ([]model.Listing, error) {
	var listings []model.Listing
	if err := r.db.Preload("Exchange").Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}

func (r *ListingRepository) ListBySecurityType(securityType string) ([]model.Listing, error) {
	var listings []model.Listing
	if err := r.db.Where("security_type = ?", securityType).
		Preload("Exchange").Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}

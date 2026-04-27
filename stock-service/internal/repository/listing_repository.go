package repository

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shopspring/decimal"

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

// ListBySecurityIDsAndType returns all listings for the given security IDs of the given type.
// Used for batch resolution in list handlers.
func (r *ListingRepository) ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error) {
	if len(securityIDs) == 0 {
		return nil, nil
	}
	var listings []model.Listing
	if err := r.db.Where("security_type = ? AND security_id IN ?", securityType, securityIDs).
		Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}

// Update persists a loaded-then-mutated listing through GORM's Save (UPDATE
// by primary key). The Listing.BeforeUpdate hook attaches the optimistic-lock
// WHERE version=? clause and increments Version on the caller's struct.
//
// We use Select("*").Save(...) intentionally: bare db.Save in GORM v1.31.1
// falls back to INSERT...ON CONFLICT(id) DO UPDATE when the initial UPDATE
// matches zero rows (finisher_api.go:109-110), which would silently overwrite
// the winner of an optimistic-lock race and hide the conflict. Selecting "*"
// sets the `selectedUpdate` flag in GORM's Save and disables that fallback
// path, so RowsAffected==0 correctly indicates an optimistic-lock conflict.
func (r *ListingRepository) Update(listing *model.Listing) error {
	result := r.db.Select("*").Save(listing)
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

// UpsertForOption upserts a listing for an option and returns the final row (with ID populated).
func (r *ListingRepository) UpsertForOption(listing *model.Listing) (*model.Listing, error) {
	var result model.Listing
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Listing
		dbErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_id = ? AND security_type = ?", listing.SecurityID, listing.SecurityType).
			First(&existing).Error
		if dbErr != nil {
			if dbErr == gorm.ErrRecordNotFound {
				if createErr := tx.Create(listing).Error; createErr != nil {
					return createErr
				}
				result = *listing
				return nil
			}
			return dbErr
		}
		existing.ExchangeID = listing.ExchangeID
		existing.Price = listing.Price
		existing.LastRefresh = listing.LastRefresh
		if saveErr := tx.Save(&existing).Error; saveErr != nil {
			return saveErr
		}
		result = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdatePriceByTicker updates a listing's denormalized price columns by joining
// on the underlying security table via ticker. The securityType must be one of
// "stock", "futures", or "forex".
func (r *ListingRepository) UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error {
	var table string
	switch securityType {
	case "stock":
		table = "stocks"
	case "futures":
		table = "futures_contracts"
	case "forex":
		table = "forex_pairs"
	default:
		return fmt.Errorf("unsupported security_type %q", securityType)
	}
	return r.db.Exec(
		"UPDATE listings SET price = ?, high = ?, low = ?, last_refresh = NOW() "+
			"FROM "+table+" s WHERE listings.security_id = s.id AND listings.security_type = ? AND s.ticker = ?", //nolint:gosec // table names are hardcoded
		price, high, low, securityType, ticker,
	).Error
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

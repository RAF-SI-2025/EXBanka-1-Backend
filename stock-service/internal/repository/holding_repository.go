package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

type HoldingRepository struct {
	db *gorm.DB
}

func NewHoldingRepository(db *gorm.DB) *HoldingRepository {
	return &HoldingRepository{db: db}
}

// Upsert creates a new holding or updates an existing one with weighted average price.
// Uses SELECT FOR UPDATE to prevent race conditions on concurrent fills.
// The aggregation key is (owner_type, owner_id, security_type, security_id)
// so an owner buying the same security from two different accounts aggregates
// into a single row. The incoming holding's AccountID is treated as a
// last-used audit field and overwrites the existing row's value (but never
// participates in the lookup).
func (r *HoldingRepository) Upsert(holding *model.Holding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Holding
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		q = scopeOwner(q, "owner_type", "owner_id", holding.OwnerType, holding.OwnerID)
		q = q.Where("security_type = ? AND security_id = ?", holding.SecurityType, holding.SecurityID)
		err := q.First(&existing).Error

		if err == gorm.ErrRecordNotFound {
			return tx.Create(holding).Error
		}
		if err != nil {
			return err
		}

		// Weighted average price
		oldTotal := existing.AveragePrice.Mul(decimal.NewFromInt(existing.Quantity))
		newTotal := holding.AveragePrice.Mul(decimal.NewFromInt(holding.Quantity))
		totalQty := existing.Quantity + holding.Quantity

		if totalQty > 0 {
			existing.AveragePrice = oldTotal.Add(newTotal).Div(decimal.NewFromInt(totalQty))
		}
		existing.Quantity = totalQty
		existing.ListingID = holding.ListingID
		existing.Ticker = holding.Ticker
		existing.Name = holding.Name
		// Update the audit "last account used" pointer when the caller
		// supplied one (zero is treated as "don't overwrite" so a
		// reservation-only update doesn't wipe the last-used account).
		if holding.AccountID != 0 {
			existing.AccountID = holding.AccountID
		}

		result := tx.Save(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrOptimisticLock
		}

		*holding = existing
		return nil
	})
}

func (r *HoldingRepository) GetByID(id uint64) (*model.Holding, error) {
	var holding model.Holding
	if err := r.db.First(&holding, id).Error; err != nil {
		return nil, err
	}
	return &holding, nil
}

func (r *HoldingRepository) Update(holding *model.Holding) error {
	result := r.db.Save(holding)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *HoldingRepository) Delete(id uint64) error {
	return r.db.Delete(&model.Holding{}, id).Error
}

// GetByOwnerAndSecurity returns the aggregated holding for an owner's
// (security_type, security_id) tuple. Since holdings are aggregated across
// accounts, the account_id is no longer part of the key.
func (r *HoldingRepository) GetByOwnerAndSecurity(ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64) (*model.Holding, error) {
	var holding model.Holding
	q := r.db.Where("security_type = ? AND security_id = ?", securityType, securityID)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	if err := q.First(&holding).Error; err != nil {
		return nil, err
	}
	return &holding, nil
}

func (r *HoldingRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, filter HoldingFilter) ([]model.Holding, int64, error) {
	var holdings []model.Holding
	var total int64

	q := r.db.Model(&model.Holding{}).Where("quantity > 0")
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	if filter.SecurityType != "" {
		q = q.Where("security_type = ?", filter.SecurityType)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("updated_at DESC").Find(&holdings).Error; err != nil {
		return nil, 0, err
	}
	return holdings, total, nil
}

// FindOldestLongOptionHolding returns the oldest (by created_at) holding for
// a given owner and option (security_id) with quantity > 0.
// Returns (nil, nil) when no such holding exists.
func (r *HoldingRepository) FindOldestLongOptionHolding(ownerType model.OwnerType, ownerID *uint64, optionID uint64) (*model.Holding, error) {
	var h model.Holding
	q := r.db.Where("security_type = ? AND security_id = ? AND quantity > 0", "option", optionID)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Order("created_at ASC").First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// ListPublicOffers returns holdings with public_quantity > 0 (for OTC).
func (r *HoldingRepository) ListPublicOffers(filter OTCFilter) ([]model.Holding, int64, error) {
	var holdings []model.Holding
	var total int64

	q := r.db.Model(&model.Holding{}).Where("public_quantity > 0 AND security_type = 'stock'")
	if filter.SecurityType != "" {
		q = q.Where("security_type = ?", filter.SecurityType)
	}
	if filter.Ticker != "" {
		q = q.Where("ticker ILIKE ?", "%"+filter.Ticker+"%")
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("updated_at DESC").Find(&holdings).Error; err != nil {
		return nil, 0, err
	}
	return holdings, total, nil
}

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
// The aggregation key is (user_id, system_type, security_type, security_id, account_id)
// so client and employee holdings with colliding user_ids stay separated.
func (r *HoldingRepository) Upsert(holding *model.Holding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Holding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ? AND account_id = ?",
				holding.UserID, holding.SystemType, holding.SecurityType, holding.SecurityID, holding.AccountID).
			First(&existing).Error

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

func (r *HoldingRepository) GetByUserAndSecurity(userID uint64, systemType, securityType string, securityID uint64, accountID uint64) (*model.Holding, error) {
	var holding model.Holding
	err := r.db.Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ? AND account_id = ?",
		userID, systemType, securityType, securityID, accountID).
		First(&holding).Error
	if err != nil {
		return nil, err
	}
	return &holding, nil
}

func (r *HoldingRepository) ListByUser(userID uint64, systemType string, filter HoldingFilter) ([]model.Holding, int64, error) {
	var holdings []model.Holding
	var total int64

	q := r.db.Model(&model.Holding{}).Where("user_id = ? AND system_type = ? AND quantity > 0", userID, systemType)
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
// a given user and option (security_id) with quantity > 0.
// Returns (nil, nil) when no such holding exists.
func (r *HoldingRepository) FindOldestLongOptionHolding(userID uint64, systemType string, optionID uint64) (*model.Holding, error) {
	var h model.Holding
	err := r.db.
		Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ? AND quantity > 0", userID, systemType, "option", optionID).
		Order("created_at ASC").
		First(&h).Error
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

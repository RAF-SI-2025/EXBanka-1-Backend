package repository

import (
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
func (r *HoldingRepository) Upsert(holding *model.Holding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Holding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND security_type = ? AND security_id = ? AND account_id = ?",
				holding.UserID, holding.SecurityType, holding.SecurityID, holding.AccountID).
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

func (r *HoldingRepository) GetByUserAndSecurity(userID uint64, securityType string, securityID uint64, accountID uint64) (*model.Holding, error) {
	var holding model.Holding
	err := r.db.Where("user_id = ? AND security_type = ? AND security_id = ? AND account_id = ?",
		userID, securityType, securityID, accountID).
		First(&holding).Error
	if err != nil {
		return nil, err
	}
	return &holding, nil
}

func (r *HoldingRepository) ListByUser(userID uint64, filter HoldingFilter) ([]model.Holding, int64, error) {
	var holdings []model.Holding
	var total int64

	q := r.db.Model(&model.Holding{}).Where("user_id = ? AND quantity > 0", userID)
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

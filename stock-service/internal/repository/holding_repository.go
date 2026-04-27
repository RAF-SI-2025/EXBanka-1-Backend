package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

// stampHoldingSaga copies saga_id and saga_step from ctx onto a Holding so
// cross-service audit queries can find every row a saga touched. Both
// fields are nullable; non-saga callers leave them empty.
func stampHoldingSaga(ctx context.Context, h *model.Holding) {
	if id, ok := saga.SagaIDFromContext(ctx); ok && id != "" {
		v := id
		h.SagaID = &v
	}
	if step, ok := saga.SagaStepFromContext(ctx); ok && step != "" {
		v := string(step)
		h.SagaStep = &v
	}
}

type HoldingRepository struct {
	db *gorm.DB
}

func NewHoldingRepository(db *gorm.DB) *HoldingRepository {
	return &HoldingRepository{db: db}
}

// Upsert creates a new holding or updates an existing one with weighted average price.
// Uses SELECT FOR UPDATE to prevent race conditions on concurrent fills.
// The aggregation key is (user_id, system_type, security_type, security_id)
// so a user buying the same security from two different accounts aggregates
// into a single row. The incoming holding's AccountID is treated as a
// last-used audit field and overwrites the existing row's value (but never
// participates in the lookup).
//
// Saga context: when ctx carries a saga_id / saga_step (set by the gRPC
// server saga-context interceptor on incoming RPCs, or by direct in-process
// saga code), both are stamped onto the holding row for cross-service
// audit. The most recent saga overwrites prior values on update.
func (r *HoldingRepository) Upsert(ctx context.Context, holding *model.Holding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Holding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ?",
				holding.UserID, holding.SystemType, holding.SecurityType, holding.SecurityID).
			First(&existing).Error

		if err == gorm.ErrRecordNotFound {
			stampHoldingSaga(ctx, holding)
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
		// Re-stamp saga context on update so the row reflects the most
		// recent saga that touched it (older sagas remain queryable via
		// ledger_entries cross-reference).
		stampHoldingSaga(ctx, &existing)

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

// GetByUserAndSecurity returns the aggregated holding for a user's
// (system_type, security_type, security_id) tuple. Since holdings are
// aggregated across accounts, the account_id is no longer part of the key.
func (r *HoldingRepository) GetByUserAndSecurity(userID uint64, systemType, securityType string, securityID uint64) (*model.Holding, error) {
	var holding model.Holding
	err := r.db.Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ?",
		userID, systemType, securityType, securityID).
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

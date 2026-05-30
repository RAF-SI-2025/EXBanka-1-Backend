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

// DB exposes the underlying *gorm.DB. Used by service-layer callers
// that need to drive db.Transaction directly (read-check-decrement
// patterns where the lock must span multiple repo calls).
func (r *HoldingRepository) DB() *gorm.DB { return r.db }

// Upsert creates a new holding or updates an existing one with weighted average price.
// Uses SELECT FOR UPDATE to prevent race conditions on concurrent fills.
// The aggregation key is (owner_type, owner_id, security_type, security_id)
// so an owner buying the same security from two different accounts aggregates
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
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		q = scopeOwner(q, "owner_type", "owner_id", holding.OwnerType, holding.OwnerID)
		q = q.Where("security_type = ? AND security_id = ?", holding.SecurityType, holding.SecurityID)
		err := q.First(&existing).Error

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
		// Display-metadata fields are only overwritten when the incoming
		// caller provided a non-empty value. This prevents a partial
		// upsert (e.g. one that knows quantity + average_price but not
		// listing_id) from wiping the existing row's display fields.
		// (Fix 2026-05-16: the OTC exercise saga previously called Upsert
		// without Ticker/Name/ListingID and silently overwrote them on
		// pre-existing rows.) New rows still get whatever the caller
		// passed via the Create path above.
		if holding.ListingID != 0 {
			existing.ListingID = holding.ListingID
		}
		if holding.Ticker != "" {
			existing.Ticker = holding.Ticker
		}
		if holding.Name != "" {
			existing.Name = holding.Name
		}
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

// UpsertIdempotent is Upsert guarded by a credit marker so a replay (saga
// retry or crash-recovery re-run) credits the shares exactly once. In one
// transaction it inserts a HoldingCreditMarker for idemKey ON CONFLICT DO
// NOTHING; if the marker is newly inserted it applies the weighted-average
// upsert, otherwise it returns without mutating (the credit already landed).
// Paired with DecrementForOwnerIdempotent which deletes the marker as it
// reverses the credit.
func (r *HoldingRepository) UpsertIdempotent(ctx context.Context, holding *model.Holding, idemKey string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		marker := &model.HoldingCreditMarker{IdempotencyKey: idemKey}
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "idempotency_key"}},
			DoNothing: true,
		}).Create(marker)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// Marker already present — the credit was applied by a prior run.
			return nil
		}
		return NewHoldingRepository(tx).Upsert(ctx, holding)
	})
}

// DecrementForOwnerIdempotent reverses an UpsertIdempotent credit. In one
// transaction it checks for the credit marker; if absent it is a no-op (the
// credit was never applied or was already reversed). If present it decrements
// the owner's holding and deletes the marker, so a subsequent re-credit under
// the same key applies again. Safe to retry — the marker presence gates the
// single decrement.
func (r *HoldingRepository) DecrementForOwnerIdempotent(ctx context.Context, ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64, qty int64, idemKey string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var marker model.HoldingCreditMarker
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("idempotency_key = ?", idemKey).First(&marker).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if derr := NewHoldingRepository(tx).DecrementForOwner(ctx, ownerType, ownerID, securityType, securityID, qty); derr != nil {
			return derr
		}
		return tx.Delete(&marker).Error
	})
}

func (r *HoldingRepository) GetByID(id uint64) (*model.Holding, error) {
	var holding model.Holding
	if err := r.db.First(&holding, id).Error; err != nil {
		return nil, err
	}
	return &holding, nil
}

// DecrementForOwner subtracts qty from an owner's holding for a security,
// deleting the row when it reaches zero. Backward compensator for an
// OTC-exercise buyer credit (pivot removal — 2026-05-29). Uses a FOR UPDATE
// lock per the concurrency rules; no-op if the holding does not exist, so the
// saga's backward pass is safe to retry.
func (r *HoldingRepository) DecrementForOwner(ctx context.Context, ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64, qty int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		h, err := r.LockByOwnerAndSecurityTx(tx, ownerType, ownerID, securityType, securityID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		h.Quantity -= qty
		if h.Quantity <= 0 {
			return tx.Delete(&model.Holding{}, h.ID).Error
		}
		return tx.Save(h).Error
	})
}

// LockByIDTx does SELECT FOR UPDATE inside an active transaction. Used by
// OTCStockService's CreateSellOffer/CancelSellOffer/FillSellOffer to
// serialize concurrent public_quantity mutations on the same holding.
func (r *HoldingRepository) LockByIDTx(tx *gorm.DB, id uint64) (*model.Holding, error) {
	var holding model.Holding
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&holding, id).Error; err != nil {
		return nil, err
	}
	return &holding, nil
}

// SaveTx variant for use inside an existing transaction.
func (r *HoldingRepository) SaveTx(tx *gorm.DB, holding *model.Holding) error {
	result := tx.Save(holding)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
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

// GetByOwnerAndTicker is the cross-bank-friendly variant: locates a
// holding by ticker rather than by local security_id, since the SI-TX
// OptionDescription only carries the ticker. Used by the seller-side
// pre-check on NEW_TX.
func (r *HoldingRepository) GetByOwnerAndTicker(ownerType model.OwnerType, ownerID *uint64, securityType, ticker string) (*model.Holding, error) {
	var holding model.Holding
	q := r.db.Where("security_type = ? AND ticker = ?", securityType, ticker)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	if err := q.First(&holding).Error; err != nil {
		return nil, err
	}
	return &holding, nil
}

// LockByOwnerAndSecurityTx does SELECT FOR UPDATE inside an active
// transaction. Used by OTCStockService.FillBuyOffer to serialise
// concurrent fills against the same seller holding so two parallel
// sellers can't both "sell" the same shares.
func (r *HoldingRepository) LockByOwnerAndSecurityTx(tx *gorm.DB, ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64) (*model.Holding, error) {
	var holding model.Holding
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("security_type = ? AND security_id = ?", securityType, securityID)
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

// ListPublic returns all holdings flagged for OTC public trading
// (public_quantity > 0). Used by PeerOTCGRPCHandler.GetPublicStocks
// to satisfy SI-TX `GET /public-stock` from peer banks. Unlike
// ListPublicOffers, this returns *all* matching rows without pagination
// or ticker filtering — the SI-TX response shape is a flat list.
func (r *HoldingRepository) ListPublic() ([]model.Holding, error) {
	var rows []model.Holding
	if err := r.db.Where("public_quantity > 0 AND security_type = 'stock'").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListBySecurityID returns all holdings with quantity > 0 for a given security.
// Used by DividendService.Payout to fan out dividend credits to every holder.
func (r *HoldingRepository) ListBySecurityID(securityID uint64) ([]model.Holding, error) {
	var rows []model.Holding
	if err := r.db.Where("security_id = ? AND quantity > 0", securityID).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
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

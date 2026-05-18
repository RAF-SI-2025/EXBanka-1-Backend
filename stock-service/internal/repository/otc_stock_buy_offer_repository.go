// Package repository — OTCStockBuyOfferRepository persists OTC stock
// BUY offers (the inverse of holdings.public_quantity-backed sell offers).
package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCStockBuyOfferRepository struct {
	db *gorm.DB
}

func NewOTCStockBuyOfferRepository(db *gorm.DB) *OTCStockBuyOfferRepository {
	return &OTCStockBuyOfferRepository{db: db}
}

func (r *OTCStockBuyOfferRepository) DB() *gorm.DB { return r.db }

func (r *OTCStockBuyOfferRepository) Create(o *model.OTCStockBuyOffer) error {
	return r.db.Create(o).Error
}

func (r *OTCStockBuyOfferRepository) CreateTx(tx *gorm.DB, o *model.OTCStockBuyOffer) error {
	return tx.Create(o).Error
}

func (r *OTCStockBuyOfferRepository) GetByID(id uint64) (*model.OTCStockBuyOffer, error) {
	var o model.OTCStockBuyOffer
	err := r.db.First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &o, err
}

// LockByID does SELECT FOR UPDATE inside an active transaction. Required
// before any state mutation (fill or cancel) so concurrent operations
// serialize correctly.
func (r *OTCStockBuyOfferRepository) LockByID(tx *gorm.DB, id uint64) (*model.OTCStockBuyOffer, error) {
	var o model.OTCStockBuyOffer
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &o, err
}

// Save persists a modified offer. Optimistic-locked via the BeforeUpdate
// hook (Version field). Returns ErrOptimisticLock if RowsAffected == 0.
func (r *OTCStockBuyOfferRepository) Save(o *model.OTCStockBuyOffer) error {
	res := r.db.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// SaveTx variant for use inside an existing transaction.
func (r *OTCStockBuyOfferRepository) SaveTx(tx *gorm.DB, o *model.OTCStockBuyOffer) error {
	res := tx.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// OTCStockBuyOfferFilter drives both the marketplace listing and per-user
// listing queries. Page/PageSize default to 1/20 if zero.
type OTCStockBuyOfferFilter struct {
	Ticker   string
	Statuses []string // when empty, defaults to [Active]
	Page     int
	PageSize int
}

// ListActive returns buy offers currently fillable. Used by the unified
// marketplace cache (`/api/v3/otc/stocks` GET).
func (r *OTCStockBuyOfferRepository) ListActive(filter OTCStockBuyOfferFilter) ([]model.OTCStockBuyOffer, int64, error) {
	q := r.db.Model(&model.OTCStockBuyOffer{}).
		Where("status = ?", model.OTCStockBuyOfferStatusActive)
	if filter.Ticker != "" {
		q = q.Where("ticker = ?", filter.Ticker)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	var out []model.OTCStockBuyOffer
	if err := q.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListByOwner returns the caller's own buy offers, optionally filtered by
// statuses. Used by `GET /api/v3/me/otc/stocks` (buy-direction half).
func (r *OTCStockBuyOfferRepository) ListByOwner(
	ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int,
) ([]model.OTCStockBuyOffer, int64, error) {
	q := r.db.Model(&model.OTCStockBuyOffer{}).
		Where("buyer_owner_type = ?", ownerType)
	if ownerType == model.OwnerClient {
		q = q.Where("buyer_owner_id = ?", ownerID)
	} else {
		q = q.Where("buyer_owner_id IS NULL")
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	var out []model.OTCStockBuyOffer
	if err := q.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// AllocateReservationOrderID returns the next value of
// otc_stock_buy_offer_res_seq, used as the synthetic order-id passed to
// account-service ReserveFunds. The sequence is created in cmd/main.go on
// startup with START 1_000_000 so it cannot collide with real orders.id.
func (r *OTCStockBuyOfferRepository) AllocateReservationOrderID(tx *gorm.DB) (uint64, error) {
	var id uint64
	if err := tx.Raw("SELECT nextval('otc_stock_buy_offer_res_seq')").Row().Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

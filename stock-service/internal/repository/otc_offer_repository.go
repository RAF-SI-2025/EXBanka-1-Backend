package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCOfferRepository struct {
	db *gorm.DB
}

func NewOTCOfferRepository(db *gorm.DB) *OTCOfferRepository {
	return &OTCOfferRepository{db: db}
}

func (r *OTCOfferRepository) DB() *gorm.DB { return r.db }

func (r *OTCOfferRepository) Create(o *model.OTCOffer) error {
	return r.db.Create(o).Error
}

func (r *OTCOfferRepository) GetByID(id uint64) (*model.OTCOffer, error) {
	var o model.OTCOffer
	err := r.db.First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &o, err
}

// Save persists a modified offer. Optimistic-locked via the BeforeUpdate hook.
func (r *OTCOfferRepository) Save(o *model.OTCOffer) error {
	res := r.db.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByOwner returns offers where the owner appears as initiator,
// counterparty, or either, optionally filtered by status (variadic) and
// stock_id (zero = no filter). owner_id may be nil for OwnerType=bank.
func (r *OTCOfferRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	q := r.db.Model(&model.OTCOffer{})
	switch role {
	case "initiator":
		q = scopeOwner(q, "initiator_owner_type", "initiator_owner_id", ownerType, ownerID)
	case "counterparty":
		q = scopeOwner(q, "counterparty_owner_type", "counterparty_owner_id", ownerType, ownerID)
	default:
		// Match owner as either initiator OR counterparty. Inline the scopeOwner
		// expansion since we need an OR over two column-pair predicates.
		if ownerID == nil {
			q = q.Where("(initiator_owner_type = ? AND initiator_owner_id IS NULL) OR (counterparty_owner_type = ? AND counterparty_owner_id IS NULL)",
				ownerType, ownerType)
		} else {
			q = q.Where("(initiator_owner_type = ? AND initiator_owner_id = ?) OR (counterparty_owner_type = ? AND counterparty_owner_id = ?)",
				ownerType, *ownerID, ownerType, *ownerID)
		}
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	if stockID != 0 {
		q = q.Where("stock_id = ?", stockID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.OTCOffer
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// ListExpiringOffers returns up to limit pending/countered offers whose
// settlement_date is in the past. Used by the expiry cron.
func (r *OTCOfferRepository) ListExpiringOffers(today string, limit int) ([]model.OTCOffer, error) {
	var out []model.OTCOffer
	err := r.db.Where("status IN ? AND settlement_date < ?",
		[]string{model.OTCOfferStatusPending, model.OTCOfferStatusCountered}, today).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// SumActiveQuantityForSeller returns Σ over (a) active option contracts
// where the seller matches, plus (b) PENDING/COUNTERED sell-initiated
// offers where the initiator is the seller, plus (c) PENDING/COUNTERED
// buy-initiated offers where the counterparty is the seller. Used by the
// seller-invariant check (§4.6 of spec). owner_id may be nil for bank
// owners; predicates emit IS NULL in that case.
func (r *OTCOfferRepository) SumActiveQuantityForSeller(sellerOwnerType model.OwnerType, sellerOwnerID *uint64, stockID uint64) (decimal.Decimal, error) {
	var rows []struct{ Sum decimal.Decimal }
	if sellerOwnerID == nil {
		err := r.db.Raw(`
			SELECT COALESCE(SUM(q), 0) AS sum FROM (
				SELECT quantity AS q FROM option_contracts
				 WHERE seller_owner_type = ? AND seller_owner_id IS NULL
				   AND stock_id = ? AND status = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND initiator_owner_type = ? AND initiator_owner_id IS NULL
				   AND stock_id = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND counterparty_owner_type = ? AND counterparty_owner_id IS NULL
				   AND stock_id = ?
			) AS t`,
			sellerOwnerType, stockID, model.OptionContractStatusActive,
			model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
			model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
		).Scan(&rows).Error
		if err != nil || len(rows) == 0 {
			return decimal.Zero, err
		}
		return rows[0].Sum, nil
	}
	err := r.db.Raw(`
		SELECT COALESCE(SUM(q), 0) AS sum FROM (
			SELECT quantity AS q FROM option_contracts
			 WHERE seller_owner_type = ? AND seller_owner_id = ?
			   AND stock_id = ? AND status = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND initiator_owner_type = ? AND initiator_owner_id = ?
			   AND stock_id = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND counterparty_owner_type = ? AND counterparty_owner_id = ?
			   AND stock_id = ?
		) AS t`,
		sellerOwnerType, *sellerOwnerID, stockID, model.OptionContractStatusActive,
		model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerOwnerType, *sellerOwnerID, stockID,
		model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerOwnerType, *sellerOwnerID, stockID,
	).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return decimal.Zero, err
	}
	return rows[0].Sum, nil
}

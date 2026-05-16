// Package repository — OTCNegotiationRepository persists per-bidder
// negotiation chains against parent OTCOffer listings, plus the
// append-only OTCNegotiationRevision history.
package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCNegotiationRepository struct {
	db *gorm.DB
}

func NewOTCNegotiationRepository(db *gorm.DB) *OTCNegotiationRepository {
	return &OTCNegotiationRepository{db: db}
}

func (r *OTCNegotiationRepository) DB() *gorm.DB { return r.db }

// Create inserts a new negotiation row. Use inside a transaction when also
// inserting the matching revision so the chain is atomically created.
func (r *OTCNegotiationRepository) Create(n *model.OTCNegotiation) error {
	return r.db.Create(n).Error
}

func (r *OTCNegotiationRepository) CreateTx(tx *gorm.DB, n *model.OTCNegotiation) error {
	return tx.Create(n).Error
}

func (r *OTCNegotiationRepository) GetByID(id uint64) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := r.db.First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &n, err
}

// LockByID does SELECT FOR UPDATE inside an active transaction. Required
// before any state mutation (counter/accept/reject/cancel) so concurrent
// operations on the same chain serialize correctly.
func (r *OTCNegotiationRepository) LockByID(tx *gorm.DB, id uint64) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &n, err
}

// Save persists a modified negotiation. Optimistic-locked via the
// BeforeUpdate hook. Returns ErrOptimisticLock if RowsAffected == 0.
func (r *OTCNegotiationRepository) Save(n *model.OTCNegotiation) error {
	res := r.db.Save(n)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *OTCNegotiationRepository) SaveTx(tx *gorm.DB, n *model.OTCNegotiation) error {
	res := tx.Save(n)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByParentOffer returns all chains against a given parent listing.
// Used to surface "current bids" on an offer detail view, AND to drive
// the cascade-cancel step when one chain accepts.
func (r *OTCNegotiationRepository) ListByParentOffer(parentOfferID uint64) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	err := r.db.Where("parent_offer_id = ?", parentOfferID).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

// ListOpenByParentOfferForUpdate locks every still-open chain on the
// parent. Used by the accept transaction to cascade-cancel siblings
// after the winning chain transitions to "accepted".
func (r *OTCNegotiationRepository) ListOpenByParentOfferForUpdate(tx *gorm.DB, parentOfferID uint64) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	openStatuses := []string{
		model.OTCNegotiationStatusOpen,
		model.OTCNegotiationStatusCountered,
	}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("parent_offer_id = ? AND status IN ?", parentOfferID, openStatuses).
		Find(&out).Error
	return out, err
}

// ListByBidder returns chains where the caller is the bidder. Used by
// GET /api/v3/me/otc/options (the "bids I placed" half of the view).
func (r *OTCNegotiationRepository) ListByBidder(
	ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int,
) ([]model.OTCNegotiation, int64, error) {
	q := r.db.Model(&model.OTCNegotiation{}).
		Where("bidder_owner_type = ?", ownerType)
	if ownerType == model.OwnerClient {
		q = q.Where("bidder_owner_id = ?", ownerID)
	} else {
		q = q.Where("bidder_owner_id IS NULL")
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
	var out []model.OTCNegotiation
	if err := q.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// FindChainByBidder returns the (parent_offer_id, bidder) chain row if it
// exists, or ErrRecordNotFound. Used to enforce the "one chain per bidder
// per listing" invariant at chain-open time.
func (r *OTCNegotiationRepository) FindChainByBidder(
	parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	q := r.db.Where("parent_offer_id = ? AND bidder_owner_type = ?",
		parentOfferID, bidderOwnerType)
	if bidderOwnerType == model.OwnerClient {
		q = q.Where("bidder_owner_id = ?", bidderOwnerID)
	} else {
		q = q.Where("bidder_owner_id IS NULL")
	}
	err := q.First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &n, err
}

// ------- Revisions -------

func (r *OTCNegotiationRepository) AppendRevision(rev *model.OTCNegotiationRevision) error {
	return r.db.Create(rev).Error
}

func (r *OTCNegotiationRepository) AppendRevisionTx(tx *gorm.DB, rev *model.OTCNegotiationRevision) error {
	return tx.Create(rev).Error
}

// ListRevisions returns the full revision history for a chain, ordered by
// revision_number ascending.
func (r *OTCNegotiationRepository) ListRevisions(negotiationID uint64) ([]model.OTCNegotiationRevision, error) {
	var out []model.OTCNegotiationRevision
	err := r.db.Where("negotiation_id = ?", negotiationID).
		Order("revision_number ASC").Find(&out).Error
	return out, err
}

// NextRevisionNumber returns the next sequential revision number for the
// chain. Must be called inside the same TX that inserts the revision so
// the (negotiation_id, revision_number) unique index serializes.
func (r *OTCNegotiationRepository) NextRevisionNumber(tx *gorm.DB, negotiationID uint64) (int, error) {
	var current int
	err := tx.Model(&model.OTCNegotiationRevision{}).
		Where("negotiation_id = ?", negotiationID).
		Select("COALESCE(MAX(revision_number), 0)").Row().Scan(&current)
	if err != nil {
		return 0, err
	}
	return current + 1, nil
}

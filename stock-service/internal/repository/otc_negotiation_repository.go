// Package repository — OTCNegotiationRepository persists per-bidder
// negotiation chains against parent OTCOffer listings, plus the
// append-only OTCNegotiationRevision history.
package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// ChainAggregate is the per-parent active-chain summary that powers
// the marketplace listing's best-bid / best-ask surface. The caller
// picks BestBid for sell_initiated parents (buyers compete upward) or
// BestAsk for buy_initiated parents (sellers compete downward); both
// are computed in one query so the caller doesn't dispatch twice.
type ChainAggregate struct {
	BestBid     decimal.Decimal
	BestAsk     decimal.Decimal
	ActiveCount int32
}

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
//
// Defense-in-depth (Fix #3, 2026-05-16): also requires bidder_bank_code
// IS NULL — cross-bank bids live in peer_otc_negotiations and never
// populate this column, so the filter is a no-op today. If a future
// path ever writes bidder_bank_code, this query stays safe by excluding
// foreign-bank rows (it would NOT silently leak Bank B's client-1 to
// Bank A's client-1 with the same numeric id).
func (r *OTCNegotiationRepository) ListByBidder(
	ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int,
) ([]model.OTCNegotiation, int64, error) {
	q := r.db.Model(&model.OTCNegotiation{}).
		Where("bidder_owner_type = ?", ownerType).
		Where("bidder_bank_code IS NULL")
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
	return r.findChainByBidder(r.db, parentOfferID, bidderOwnerType, bidderOwnerID)
}

// FindChainByBidderTx variant for use inside an active transaction. The
// non-Tx version uses r.db which acquires a fresh connection and would
// deadlock under single-connection backends (sqlite :memory:) when the
// caller already holds the TX's connection.
func (r *OTCNegotiationRepository) FindChainByBidderTx(
	tx *gorm.DB, parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	return r.findChainByBidder(tx, parentOfferID, bidderOwnerType, bidderOwnerID)
}

func (r *OTCNegotiationRepository) findChainByBidder(
	db *gorm.DB, parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	q := db.Where("parent_offer_id = ? AND bidder_owner_type = ?",
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

// AggregateActiveBidsByOffer summarises every parent's active-chain
// pricing for the marketplace best-bid / best-ask surface. One query,
// GROUP BY parent_offer_id, filters status IN ('open','countered').
// Parents with zero active chains are absent from the result map (NOT
// keyed with zero values) so the caller can distinguish "no
// competition" from "competition at zero". Empty input ⇒ empty map.
func (r *OTCNegotiationRepository) AggregateActiveBidsByOffer(offerIDs []uint64) (map[uint64]ChainAggregate, error) {
	out := map[uint64]ChainAggregate{}
	if len(offerIDs) == 0 {
		return out, nil
	}
	type row struct {
		ParentOfferID uint64
		BestBid       decimal.Decimal
		BestAsk       decimal.Decimal
		ActiveCount   int32
	}
	var rows []row
	err := r.db.Model(&model.OTCNegotiation{}).
		Select("parent_offer_id, MAX(premium) AS best_bid, MIN(premium) AS best_ask, COUNT(*) AS active_count").
		Where("parent_offer_id IN ? AND status IN ?", offerIDs, []string{
			model.OTCNegotiationStatusOpen,
			model.OTCNegotiationStatusCountered,
		}).
		Group("parent_offer_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ParentOfferID] = ChainAggregate{
			BestBid:     r.BestBid,
			BestAsk:     r.BestAsk,
			ActiveCount: r.ActiveCount,
		}
	}
	return out, nil
}
